// Package billingclient — gRPC client untuk lookup invoice ke Billing Service.
//
// Implementasi InvoiceLookup port (services/payment/internal/usecase.InvoiceLookup).
// Service-to-service via gRPC over HTTP/2 sesuai use-case requirement.
//
// Connection: lazy single connection. Reuse antar request (gRPC HTTP/2 multiplex).
//
// Untuk demo / offline testing, pakai NewStub() di stub.go.
package billingclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	billingv1 "github.com/ajiperdana/parkir-pintar/proto/gen/billing/v1"
)

// Sentinel errors — wrapper agar caller bisa pakai errors.Is.
var (
	ErrInvoiceNotFound     = errors.New("billingclient: invoice not found")
	ErrInvoiceNotPayable   = errors.New("billingclient: invoice not in ISSUED state")
	ErrUpstreamUnavailable = errors.New("billingclient: upstream unavailable")
)

// GRPCClient — gRPC client.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client billingv1.BillingServiceClient
}

// NewGRPC — buka koneksi ke billing gRPC server.
//
// addr format: "host:port", mis. "localhost:9092" atau "billing:9092" (Docker).
//
// Production tweak:
//   - WithChainUnaryInterceptor(timeout, retry, breaker, otel) — lihat pkg/grpcutil.
//   - TLS via WithTransportCredentials(credentials.NewTLS(...)) — sekarang insecure
//     karena internal mesh/VPC.
func NewGRPC(addr string) (*GRPCClient, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial billing %s: %w", addr, err)
	}
	return &GRPCClient{
		conn:   conn,
		client: billingv1.NewBillingServiceClient(conn),
	}, nil
}

// Close — tutup koneksi.
func (c *GRPCClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Get — implementasi InvoiceLookup.Get. Ambil invoice by id.
func (c *GRPCClient) Get(ctx context.Context, invoiceID uuid.UUID) (string, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	resp, err := c.client.GetInvoice(ctx, &billingv1.GetInvoiceRequest{Id: invoiceID.String()})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			return "", 0, err
		}
		switch st.Code() {
		case codes.NotFound:
			return "", 0, ErrInvoiceNotFound
		case codes.Unavailable, codes.DeadlineExceeded:
			return "", 0, ErrUpstreamUnavailable
		default:
			return "", 0, fmt.Errorf("billing grpc: %w", err)
		}
	}
	if resp == nil {
		return "", 0, ErrInvoiceNotFound
	}

	// Business rule: invoice harus dalam state ISSUED untuk bisa di-bayar.
	// (DRAFT diizinkan untuk menanggung skenario early-payment booking fee — sesuai logic lama.)
	switch resp.GetStatus() {
	case billingv1.InvoiceStatus_ISSUED, billingv1.InvoiceStatus_DRAFT:
		// ok
	default:
		return "", 0, fmt.Errorf("%w: status=%s", ErrInvoiceNotPayable, resp.GetStatus().String())
	}

	total := resp.GetTotal()
	if total == nil || total.GetAmount() <= 0 {
		return "", 0, fmt.Errorf("invoice total invalid")
	}
	return resp.GetDriverId(), total.GetAmount(), nil
}
