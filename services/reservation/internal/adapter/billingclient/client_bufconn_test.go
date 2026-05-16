package billingclient

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	billingv1 "github.com/ajiperdana/parkir-pintar/proto/gen/billing/v1"
)

// fakeBillingServer — in-memory BillingServiceServer untuk uji happy & error path
// dari CountOverdueByDriverID tanpa butuh network real.
type fakeBillingServer struct {
	billingv1.UnimplementedBillingServiceServer
	countOverdue func(ctx context.Context, req *billingv1.CountOverdueByDriverRequest) (*billingv1.CountOverdueByDriverResponse, error)
}

func (f *fakeBillingServer) CountOverdueByDriver(ctx context.Context, req *billingv1.CountOverdueByDriverRequest) (*billingv1.CountOverdueByDriverResponse, error) {
	if f.countOverdue != nil {
		return f.countOverdue(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "no stub")
}

func newBufconnClient(t *testing.T, srv *fakeBillingServer) *Client {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	billingv1.RegisterBillingServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() { gs.Stop() })

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &Client{
		conn:   conn,
		client: billingv1.NewBillingServiceClient(conn),
	}
}

func TestCountOverdueByDriverID_HappyPath(t *testing.T) {
	gotDriver := ""
	c := newBufconnClient(t, &fakeBillingServer{
		countOverdue: func(_ context.Context, req *billingv1.CountOverdueByDriverRequest) (*billingv1.CountOverdueByDriverResponse, error) {
			gotDriver = req.GetDriverId()
			return &billingv1.CountOverdueByDriverResponse{Count: 4}, nil
		},
	})
	got, err := c.CountOverdueByDriverID(context.Background(), "drv-007")
	require.NoError(t, err)
	assert.Equal(t, 4, got)
	assert.Equal(t, "drv-007", gotDriver)
}

func TestCountOverdueByDriverID_Zero(t *testing.T) {
	c := newBufconnClient(t, &fakeBillingServer{
		countOverdue: func(_ context.Context, _ *billingv1.CountOverdueByDriverRequest) (*billingv1.CountOverdueByDriverResponse, error) {
			return &billingv1.CountOverdueByDriverResponse{Count: 0}, nil
		},
	})
	got, err := c.CountOverdueByDriverID(context.Background(), "drv-fresh")
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

func TestCountOverdueByDriverID_ServerError(t *testing.T) {
	c := newBufconnClient(t, &fakeBillingServer{
		countOverdue: func(_ context.Context, _ *billingv1.CountOverdueByDriverRequest) (*billingv1.CountOverdueByDriverResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	})
	got, err := c.CountOverdueByDriverID(context.Background(), "drv-x")
	assert.Error(t, err)
	assert.Equal(t, 0, got)
}
