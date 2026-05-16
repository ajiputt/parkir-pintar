package billingclient

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	billingv1 "github.com/ajiperdana/parkir-pintar/proto/gen/billing/v1"
	commonv1 "github.com/ajiperdana/parkir-pintar/proto/gen/common/v1"
)

// fakeBillingServer — implementasi BillingServiceServer untuk test via bufconn.
type fakeBillingServer struct {
	billingv1.UnimplementedBillingServiceServer
	getInvoice func(ctx context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error)
}

func (f *fakeBillingServer) GetInvoice(ctx context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
	if f.getInvoice != nil {
		return f.getInvoice(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "no stub")
}

// newTestClient — bootstrap GRPCClient yang ngomong ke fakeBillingServer
// via in-memory bufconn (tanpa port real).
func newTestClient(t *testing.T, srv *fakeBillingServer) *GRPCClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	billingv1.RegisterBillingServiceServer(gs, srv)
	go func() {
		// Error di-suppress untuk listener yang di-close di t.Cleanup.
		_ = gs.Serve(lis)
	}()
	t.Cleanup(func() {
		gs.Stop()
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &GRPCClient{
		conn:   conn,
		client: billingv1.NewBillingServiceClient(conn),
	}
}

// --- NewGRPC / constructor ----------------------------------------------

func TestNewGRPC_ReturnsClient(t *testing.T) {
	t.Parallel()
	// grpc.NewClient lazy — tidak panggil dial sampai RPC dipanggil. Address
	// invalid TIDAK error di constructor; baru error pas RPC.
	c, err := NewGRPC("localhost:0")
	if err != nil {
		t.Fatalf("NewGRPC error: %v", err)
	}
	if c == nil || c.conn == nil || c.client == nil {
		t.Fatalf("NewGRPC returned incomplete client: %+v", c)
	}
	_ = c.Close()
}

func TestNewGRPC_EmptyAddr_LazyOK(t *testing.T) {
	t.Parallel()
	// grpc.NewClient is lazy: empty addr accepted at construction.
	// Connection errors only surface on first RPC call.
	c, err := NewGRPC("")
	if err != nil {
		t.Fatalf("NewGRPC('') unexpected error: %v", err)
	}
	if c == nil {
		t.Fatalf("NewGRPC('') returned nil client")
	}
	_ = c.Close()
}

// --- Close --------------------------------------------------------------

func TestClose_NilConn(t *testing.T) {
	t.Parallel()
	c := &GRPCClient{}
	if err := c.Close(); err != nil {
		t.Fatalf("Close on nil conn = %v, want nil", err)
	}
}

func TestClose_RealConn(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{})
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
}

// --- Get happy path -----------------------------------------------------

func TestGet_HappyPath_Issued(t *testing.T) {
	t.Parallel()
	invID := uuid.New()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			if req.GetId() != invID.String() {
				t.Errorf("server received id = %q, want %q", req.GetId(), invID.String())
			}
			return &billingv1.Invoice{
				DriverId: "driver-abc",
				Status:   billingv1.InvoiceStatus_ISSUED,
				Total:    &commonv1.Money{Amount: 50000, Currency: "IDR"},
			}, nil
		},
	})

	driverID, amount, err := c.Get(context.Background(), invID)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if driverID != "driver-abc" {
		t.Errorf("driverID = %q, want driver-abc", driverID)
	}
	if amount != 50000 {
		t.Errorf("amount = %d, want 50000", amount)
	}
}

func TestGet_HappyPath_Draft(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, _ *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			return &billingv1.Invoice{
				DriverId: "drv-1",
				Status:   billingv1.InvoiceStatus_DRAFT,
				Total:    &commonv1.Money{Amount: 1000, Currency: "IDR"},
			}, nil
		},
	})
	_, amt, err := c.Get(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("DRAFT should be acceptable: %v", err)
	}
	if amt != 1000 {
		t.Errorf("amount = %d, want 1000", amt)
	}
}

// --- Get error paths ----------------------------------------------------

func TestGet_NotFound(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, _ *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			return nil, status.Error(codes.NotFound, "no such invoice")
		},
	})
	_, _, err := c.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrInvoiceNotFound) {
		t.Fatalf("err = %v, want ErrInvoiceNotFound", err)
	}
}

func TestGet_Unavailable(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, _ *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			return nil, status.Error(codes.Unavailable, "service down")
		},
	})
	_, _, err := c.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrUpstreamUnavailable", err)
	}
}

func TestGet_DeadlineExceeded(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, _ *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			return nil, status.Error(codes.DeadlineExceeded, "timeout")
		},
	})
	_, _, err := c.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrUpstreamUnavailable", err)
	}
}

func TestGet_OtherGRPCError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, _ *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	})
	_, _, err := c.Get(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("expected error for Internal code")
	}
	// Tidak match sentinel di atas, harus wrapped sebagai "billing grpc:"
	if errors.Is(err, ErrInvoiceNotFound) || errors.Is(err, ErrUpstreamUnavailable) {
		t.Errorf("err = %v, should not match known sentinels", err)
	}
}

func TestGet_NotPayable(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, _ *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			return &billingv1.Invoice{
				DriverId: "drv-1",
				Status:   billingv1.InvoiceStatus_PAID,
				Total:    &commonv1.Money{Amount: 1000, Currency: "IDR"},
			}, nil
		},
	})
	_, _, err := c.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrInvoiceNotPayable) {
		t.Fatalf("err = %v, want ErrInvoiceNotPayable", err)
	}
}

func TestGet_InvalidTotal_Nil(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, _ *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			return &billingv1.Invoice{
				DriverId: "drv",
				Status:   billingv1.InvoiceStatus_ISSUED,
				Total:    nil,
			}, nil
		},
	})
	_, _, err := c.Get(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("expected error for nil total")
	}
}

func TestGet_InvalidTotal_Zero(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, &fakeBillingServer{
		getInvoice: func(_ context.Context, _ *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
			return &billingv1.Invoice{
				DriverId: "drv",
				Status:   billingv1.InvoiceStatus_ISSUED,
				Total:    &commonv1.Money{Amount: 0, Currency: "IDR"},
			}, nil
		},
	})
	_, _, err := c.Get(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("expected error for zero total")
	}
}
