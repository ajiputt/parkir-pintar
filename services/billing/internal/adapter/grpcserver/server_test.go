// Tests untuk Billing gRPC server. Konstruksi Server pakai *usecase.Service yang
// di-wire dengan fake InvoiceRepo. No bufconn — call handlers directly.
package grpcserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ajiperdana/parkir-pintar/pkg/money"
	"github.com/ajiperdana/parkir-pintar/pkg/pricing"
	billingv1 "github.com/ajiperdana/parkir-pintar/proto/gen/billing/v1"
	commonv1 "github.com/ajiperdana/parkir-pintar/proto/gen/common/v1"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/usecase"
)

// ----- fake InvoiceRepo -----

type fakeRepo struct {
	mu              sync.Mutex
	byID            map[uuid.UUID]*domain.Invoice
	byReservation   map[uuid.UUID]*domain.Invoice
	getByIDErr      error
	getByResErr     error
	overdueCount    int
	overdueCountErr error
	findOverdueRes  []*domain.Invoice
	findOverdueErr  error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID:          map[uuid.UUID]*domain.Invoice{},
		byReservation: map[uuid.UUID]*domain.Invoice{},
	}
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Invoice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	inv, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrInvoiceNotFound
	}
	return inv, nil
}

func (r *fakeRepo) GetByReservationID(_ context.Context, id uuid.UUID) (*domain.Invoice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getByResErr != nil {
		return nil, r.getByResErr
	}
	inv, ok := r.byReservation[id]
	if !ok {
		return nil, domain.ErrInvoiceNotFound
	}
	return inv, nil
}

func (r *fakeRepo) Save(_ context.Context, inv *domain.Invoice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[inv.ID] = inv
	r.byReservation[inv.ReservationID] = inv
	return nil
}

func (r *fakeRepo) FindOverdueCandidates(_ context.Context, _ time.Time, _ int) ([]*domain.Invoice, error) {
	return r.findOverdueRes, r.findOverdueErr
}

func (r *fakeRepo) CountOverdueByDriver(_ context.Context, _ string) (int, error) {
	if r.overdueCountErr != nil {
		return 0, r.overdueCountErr
	}
	return r.overdueCount, nil
}

// ----- helper -----

func newTestServer() (*Server, *fakeRepo) {
	repo := newFakeRepo()
	svc := &usecase.Service{
		Invoices: repo,
		Engine:   pricing.Default(),
		// Events + Pub not used for the read paths exercised by these tests.
	}
	return &Server{Service: svc, Logger: zap.NewNop()}, repo
}

func makeInvoice(t *testing.T, repo *fakeRepo) *domain.Invoice {
	t.Helper()
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentManual)
	inv.Status = domain.InvoiceIssued
	now := time.Now().UTC()
	inv.IssuedAt = &now
	inv.Total = money.IDR(15000)
	inv.Items = []domain.Item{
		{
			ID:          uuid.New(),
			Type:        pricing.LineKind("HOURLY"),
			Description: "Parking 1h",
			Amount:      money.IDR(10000),
		},
	}
	_ = repo.Save(context.Background(), inv)
	return inv
}

// ----- GetInvoice -----

func TestGetInvoice_HappyPath(t *testing.T) {
	srv, repo := newTestServer()
	inv := makeInvoice(t, repo)

	out, err := srv.GetInvoice(context.Background(), &billingv1.GetInvoiceRequest{Id: inv.ID.String()})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Id != inv.ID.String() {
		t.Errorf("want id %s, got %s", inv.ID.String(), out.Id)
	}
	if out.Status != billingv1.InvoiceStatus_ISSUED {
		t.Errorf("want status ISSUED, got %v", out.Status)
	}
	if out.PaymentMode != commonv1.BillingMode_MANUAL {
		t.Errorf("want MANUAL, got %v", out.PaymentMode)
	}
	if out.Total.Amount != 15000 {
		t.Errorf("want total 15000, got %d", out.Total.Amount)
	}
	if len(out.Items) != 1 {
		t.Errorf("want 1 item, got %d", len(out.Items))
	}
}

func TestGetInvoice_BadUUID(t *testing.T) {
	srv, _ := newTestServer()
	_, err := srv.GetInvoice(context.Background(), &billingv1.GetInvoiceRequest{Id: "not-a-uuid"})
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status, got %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("want NotFound, got %v", st.Code())
	}
}

func TestGetInvoice_NotFound(t *testing.T) {
	srv, _ := newTestServer()
	_, err := srv.GetInvoice(context.Background(), &billingv1.GetInvoiceRequest{Id: uuid.NewString()})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("want NotFound, got %v", st.Code())
	}
}

func TestGetInvoice_RepoError(t *testing.T) {
	srv, repo := newTestServer()
	repo.getByIDErr = errors.New("db down")
	_, err := srv.GetInvoice(context.Background(), &billingv1.GetInvoiceRequest{Id: uuid.NewString()})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("want Internal, got %v", st.Code())
	}
}

// ----- GetInvoiceByReservation -----

func TestGetInvoiceByReservation_HappyPath(t *testing.T) {
	srv, repo := newTestServer()
	inv := makeInvoice(t, repo)
	out, err := srv.GetInvoiceByReservation(context.Background(), &billingv1.GetInvoiceByReservationRequest{
		ReservationId: inv.ReservationID.String(),
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Id != inv.ID.String() {
		t.Errorf("id mismatch")
	}
	if out.ReservationId != inv.ReservationID.String() {
		t.Errorf("reservation_id mismatch")
	}
}

func TestGetInvoiceByReservation_BadUUID(t *testing.T) {
	srv, _ := newTestServer()
	_, err := srv.GetInvoiceByReservation(context.Background(), &billingv1.GetInvoiceByReservationRequest{ReservationId: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("want NotFound, got %v", st.Code())
	}
}

func TestGetInvoiceByReservation_NotFound(t *testing.T) {
	srv, _ := newTestServer()
	_, err := srv.GetInvoiceByReservation(context.Background(), &billingv1.GetInvoiceByReservationRequest{ReservationId: uuid.NewString()})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("want NotFound, got %v", st.Code())
	}
}

// ----- IssueInvoice (always FailedPrecondition stub) -----

func TestIssueInvoice_NotImplemented(t *testing.T) {
	srv, _ := newTestServer()
	_, err := srv.IssueInvoice(context.Background(), &billingv1.IssueInvoiceRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("want FailedPrecondition, got %v", st.Code())
	}
}

// ----- MarkPaid (always FailedPrecondition stub) -----

func TestMarkPaid_NotImplemented(t *testing.T) {
	srv, _ := newTestServer()
	_, err := srv.MarkPaid(context.Background(), &billingv1.MarkPaidRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("want FailedPrecondition, got %v", st.Code())
	}
}

// ----- CountOverdueByDriver -----

func TestCountOverdueByDriver_HappyPath(t *testing.T) {
	srv, repo := newTestServer()
	repo.overdueCount = 3
	out, err := srv.CountOverdueByDriver(context.Background(), &billingv1.CountOverdueByDriverRequest{DriverId: "drv-1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Count != 3 {
		t.Errorf("want count 3, got %d", out.Count)
	}
}

func TestCountOverdueByDriver_Error(t *testing.T) {
	srv, repo := newTestServer()
	repo.overdueCountErr = errors.New("boom")
	_, err := srv.CountOverdueByDriver(context.Background(), &billingv1.CountOverdueByDriverRequest{DriverId: "drv-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("want Internal, got %v", st.Code())
	}
}

// ----- converter helpers (exported via server invocation indirectly, but a
// few branches need direct coverage that GetInvoice can't exercise). -----

func TestInvoiceToProto_NilInput_ReturnsNil(t *testing.T) {
	if got := invoiceToProto(nil); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestInvoiceStatusToProto_AllBranches(t *testing.T) {
	cases := []struct {
		in   domain.InvoiceStatus
		want billingv1.InvoiceStatus
	}{
		{domain.InvoiceDraft, billingv1.InvoiceStatus_DRAFT},
		{domain.InvoiceIssued, billingv1.InvoiceStatus_ISSUED},
		{domain.InvoicePaid, billingv1.InvoiceStatus_PAID},
		{domain.InvoiceVoid, billingv1.InvoiceStatus_VOID},
		{domain.InvoiceOverdue, billingv1.InvoiceStatus_OVERDUE},
		{domain.InvoiceStatus("BOGUS"), billingv1.InvoiceStatus_INVOICE_STATUS_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := invoiceStatusToProto(c.in); got != c.want {
			t.Errorf("invoiceStatusToProto(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBillingPaymentModeToProto_AllBranches(t *testing.T) {
	if got := billingPaymentModeToProto(domain.PaymentAuto); got != commonv1.BillingMode_AUTO {
		t.Errorf("want AUTO, got %v", got)
	}
	if got := billingPaymentModeToProto(domain.PaymentManual); got != commonv1.BillingMode_MANUAL {
		t.Errorf("want MANUAL, got %v", got)
	}
	if got := billingPaymentModeToProto(domain.PaymentMode("X")); got != commonv1.BillingMode_BILLING_MODE_UNSPECIFIED {
		t.Errorf("want UNSPECIFIED, got %v", got)
	}
}

func TestLineKindToProto_AllBranches(t *testing.T) {
	cases := []struct {
		in   string
		want billingv1.LineItemType
	}{
		{"BOOKING_FEE", billingv1.LineItemType_BOOKING_FEE},
		{"HOURLY", billingv1.LineItemType_HOURLY},
		{"OVERNIGHT", billingv1.LineItemType_OVERNIGHT},
		{"NO_SHOW_PENALTY", billingv1.LineItemType_NO_SHOW_PENALTY},
		{"GARBAGE", billingv1.LineItemType_LINE_ITEM_TYPE_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := lineKindToProto(c.in); got != c.want {
			t.Errorf("lineKindToProto(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTimeToTs_ZeroReturnsNil(t *testing.T) {
	if got := timeToTs(time.Time{}); got != nil {
		t.Errorf("want nil for zero time, got %v", got)
	}
	now := time.Now()
	if got := timeToTs(now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("round trip failed: %v vs %v", got, now)
	}
}

func TestTimePtrToTs(t *testing.T) {
	if got := timePtrToTs(nil); got != nil {
		t.Errorf("want nil for nil pointer, got %v", got)
	}
	zero := time.Time{}
	if got := timePtrToTs(&zero); got != nil {
		t.Errorf("want nil for zero time pointer, got %v", got)
	}
	now := time.Now()
	if got := timePtrToTs(&now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("round trip failed")
	}
}

// ----- Register smoke -----

func TestRegister_DoesNotPanic(t *testing.T) {
	srv, _ := newTestServer()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Register panicked: %v", r)
		}
	}()
	gs := grpc.NewServer()
	srv.Register(gs)
	gs.Stop()
}
