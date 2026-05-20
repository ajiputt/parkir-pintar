// Package billingclient — gRPC client untuk panggil billing service dari
// reservation service (driver blocking pre-check).
//
// Lihat ADR-0014.
package billingclient

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "github.com/ajiperdana/parkir-pintar/proto/gen/billing/v1"
)

// Client — billing gRPC client.
type Client struct {
	conn   *grpc.ClientConn
	client billingv1.BillingServiceClient
}

// New — buka gRPC connection ke billing service.
func New(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Propagate trace context across the wire (W3C TraceContext via metadata).
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial billing %s: %w", addr, err)
	}
	return &Client{
		conn:   conn,
		client: billingv1.NewBillingServiceClient(conn),
	}, nil
}

// Close — release connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// CountOverdueByDriverID — implements usecase.OverdueChecker.
func (c *Client) CountOverdueByDriverID(ctx context.Context, driverID string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	resp, err := c.client.CountOverdueByDriver(ctx, &billingv1.CountOverdueByDriverRequest{DriverId: driverID})
	if err != nil {
		return 0, err
	}
	return int(resp.GetCount()), nil
}

// NoopChecker — fallback kalau billing service unreachable atau di-disable.
type NoopChecker struct{}

func (NoopChecker) CountOverdueByDriverID(_ context.Context, _ string) (int, error) {
	return 0, nil
}
