// Tests untuk Payment gRPC server. Konstruksi Server pakai *usecase.Service
// yang di-wire dengan fakes (Repo, InvoiceLookup, GatewayClient, EventPublisher).
package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ajiperdana/parkir-pintar/pkg/grpcutil"
	"github.com/ajiperdana/parkir-pintar/pkg/idempotency"
	paymentv1 "github.com/ajiperdana/parkir-pintar/proto/gen/payment/v1"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/adapter/midtrans"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/usecase"
)

// ----- fakes -----

type fakeRepo struct {
	mu        sync.Mutex
	byID      map[uuid.UUID]*domain.Payment
	byInv     map[uuid.UUID]*domain.Payment
	saved     []*domain.Payment
	saveErr   error
	getByIDEr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byID: map[uuid.UUID]*domain.Payment{}, byInv: map[uuid.UUID]*domain.Payment{}}
}

func (r *fakeRepo) Save(_ context.Context, p *domain.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[p.ID] = p
	r.byInv[p.InvoiceID] = p
	r.saved = append(r.saved, p)
	return nil
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getByIDEr != nil {
		return nil, r.getByIDEr
	}
	p, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrPaymentNotFound
	}
	return p, nil
}

func (r *fakeRepo) GetByGatewayRef(_ context.Context, _ string) (*domain.Payment, error) {
	return nil, domain.ErrPaymentNotFound
}

func (r *fakeRepo) GetByInvoiceID(_ context.Context, invID uuid.UUID) (*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byInv[invID]
	if !ok {
		return nil, domain.ErrPaymentNotFound
	}
	return p, nil
}

func (r *fakeRepo) LogWebhook(_ context.Context, _ *uuid.UUID, _, _ string, _ []byte, _ bool) error {
	return nil
}

type fakeInvoice struct {
	driverID string
	amount   int64
	err      error
}

func (f *fakeInvoice) Get(_ context.Context, _ uuid.UUID) (string, int64, error) {
	if f.err != nil {
		return "", 0, f.err
	}
	return f.driverID, f.amount, nil
}

type fakeGateway struct {
	resp *midtrans.ChargeResponse
	err  error
}

func (g *fakeGateway) Charge(_ context.Context, req midtrans.ChargeRequest) (*midtrans.ChargeResponse, error) {
	if g.err != nil {
		return nil, g.err
	}
	if g.resp != nil {
		return g.resp, nil
	}
	return &midtrans.ChargeResponse{
		TransactionID: "txn-" + req.OrderID,
		OrderID:       req.OrderID,
		StatusCode:    "201",
		QRString:      "QR-" + req.OrderID,
		QRURL:         "https://example.test/qr/" + req.OrderID,
		ExpiryTime:    time.Now().Add(15 * time.Minute),
	}, nil
}

type fakePub struct{}

func (p *fakePub) PublishPaymentSucceeded(_ context.Context, _ *domain.Payment) error { return nil }
func (p *fakePub) PublishPaymentFailed(_ context.Context, _ *domain.Payment) error    { return nil }

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

// ----- helper -----

type fixture struct {
	srv     *Server
	repo    *fakeRepo
	invoice *fakeInvoice
	gw      *fakeGateway
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	repo := newFakeRepo()
	inv := &fakeInvoice{driverID: "drv-1", amount: 25000}
	gw := &fakeGateway{}
	idem := idempotency.NewMemoryStore()
	svc := &usecase.Service{
		Payments:    repo,
		Invoices:    inv,
		Pub:         &fakePub{},
		Gateway:     gw,
		Idempotency: idem,
		Clock:       &fakeClock{t: time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)},
		IdemTTL:     time.Hour,
	}
	srv := &Server{
		Service:     svc,
		Idempotency: idem,
		IdemTTL:     time.Hour,
		Logger:      zap.NewNop(),
	}
	return &fixture{srv: srv, repo: repo, invoice: inv, gw: gw}
}

func ctxWithIdem(key string) context.Context {
	md := metadata.New(map[string]string{grpcutil.HeaderIdempotencyKey: key})
	return metadata.NewIncomingContext(context.Background(), md)
}

// ----- CreatePayment -----

func TestCreatePayment_HappyPath(t *testing.T) {
	f := newFixture(t)
	invoiceID := uuid.New()

	req := &paymentv1.CreatePaymentRequest{InvoiceId: invoiceID.String()}
	out, err := f.srv.CreatePayment(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, invoiceID.String(), out.InvoiceId)
	assert.Equal(t, paymentv1.PaymentMethod_QRIS, out.Method)
	assert.Equal(t, "MIDTRANS", out.Gateway)
	assert.Equal(t, paymentv1.PaymentStatus_PENDING, out.Status)
	assert.Equal(t, int64(25000), out.Amount.Amount)
	assert.NotEmpty(t, out.QrString)
	assert.NotEmpty(t, out.QrUrl)
}

func TestCreatePayment_InvalidInvoiceID(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.CreatePayment(context.Background(), &paymentv1.CreatePaymentRequest{InvoiceId: "not-uuid"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreatePayment_InvoiceLookupFails(t *testing.T) {
	f := newFixture(t)
	f.invoice.err = errors.New("billing down")
	_, err := f.srv.CreatePayment(context.Background(), &paymentv1.CreatePaymentRequest{InvoiceId: uuid.NewString()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	// Lookup error has no errs.E typing -> default Internal.
	assert.Equal(t, codes.Internal, st.Code())
}

func TestCreatePayment_GatewayFails(t *testing.T) {
	f := newFixture(t)
	f.gw.err = errors.New("midtrans 503")
	_, err := f.srv.CreatePayment(context.Background(), &paymentv1.CreatePaymentRequest{InvoiceId: uuid.NewString()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestCreatePayment_IdempotentReplay(t *testing.T) {
	f := newFixture(t)
	invID := uuid.New()
	req := &paymentv1.CreatePaymentRequest{InvoiceId: invID.String()}
	ctx := ctxWithIdem("k-1")

	r1, err := f.srv.CreatePayment(ctx, req)
	require.NoError(t, err)

	// Force a new gateway response on the underlying service; replay should
	// still surface r1.
	f.gw.resp = &midtrans.ChargeResponse{TransactionID: "should-not-be-used", QRString: "X"}
	r2, err := f.srv.CreatePayment(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, r1.Id, r2.Id, "replay returns cached payment id")
	assert.Equal(t, r1.QrString, r2.QrString)
}

// ----- GetPayment -----

func TestGetPayment_HappyPath(t *testing.T) {
	f := newFixture(t)
	// Create a payment via CreatePayment so the repo has it.
	invID := uuid.New()
	created, err := f.srv.CreatePayment(context.Background(), &paymentv1.CreatePaymentRequest{InvoiceId: invID.String()})
	require.NoError(t, err)

	got, err := f.srv.GetPayment(context.Background(), &paymentv1.GetPaymentRequest{Id: created.Id})
	require.NoError(t, err)
	assert.Equal(t, created.Id, got.Id)
	assert.Equal(t, paymentv1.PaymentStatus_PENDING, got.Status)
	assert.Equal(t, paymentv1.PaymentMethod_QRIS, got.Method)
}

func TestGetPayment_BadUUID(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.GetPayment(context.Background(), &paymentv1.GetPaymentRequest{Id: "not-uuid"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetPayment_NotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.GetPayment(context.Background(), &paymentv1.GetPaymentRequest{Id: uuid.NewString()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ----- HandleWebhook -----

func TestHandleWebhook_BadPayload_ReturnsError(t *testing.T) {
	f := newFixture(t)
	out, err := f.srv.HandleWebhook(context.Background(), &paymentv1.WebhookPayload{RawPayload: []byte("{ not json")})
	require.Error(t, err)
	require.NotNil(t, out)
	assert.False(t, out.Accepted)
}

func TestHandleWebhook_InvalidSignature_ReturnsError(t *testing.T) {
	f := newFixture(t)
	// Empty server key + arbitrary signature_key in payload → signature check
	// fails → service returns ErrInvalidSignature → handler returns Accepted=false.
	payload := map[string]any{
		"transaction_status": "pending",
		"order_id":           uuid.NewString(),
		"transaction_id":     "txn-xyz",
		"status_code":        "201",
		"gross_amount":       "25000",
		"payment_type":       "qris",
		"signature_key":      "deadbeef",
	}
	raw, _ := json.Marshal(payload)
	out, err := f.srv.HandleWebhook(context.Background(), &paymentv1.WebhookPayload{RawPayload: raw})
	require.Error(t, err)
	require.NotNil(t, out)
	assert.False(t, out.Accepted)
}

// ----- converter helpers -----

func TestMethodToProto(t *testing.T) {
	assert.Equal(t, paymentv1.PaymentMethod_QRIS, methodToProto("QRIS"))
	assert.Equal(t, paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED, methodToProto("WEIRD"))
}

func TestStatusToProto_AllBranches(t *testing.T) {
	cases := []struct {
		in   string
		want paymentv1.PaymentStatus
	}{
		{"PENDING", paymentv1.PaymentStatus_PENDING},
		{"SUCCESS", paymentv1.PaymentStatus_SUCCESS},
		{"FAILED", paymentv1.PaymentStatus_FAILED},
		{"EXPIRED", paymentv1.PaymentStatus_EXPIRED},
		{"GARBAGE", paymentv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := statusToProto(c.in); got != c.want {
			t.Errorf("statusToProto(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPaymentToProto_NilSafe(t *testing.T) {
	if got := paymentToProto(nil); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
}

// ----- Register smoke -----

func TestRegister_DoesNotPanic(t *testing.T) {
	f := newFixture(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Register panicked: %v", r)
		}
	}()
	gs := grpc.NewServer()
	f.srv.Register(gs)
	gs.Stop()
}
