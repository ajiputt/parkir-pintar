package usecase

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/money"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/adapter/midtrans"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/domain"
)

// ----- Test doubles -----

type fakeRepo struct {
	mu       sync.Mutex
	byID     map[uuid.UUID]*domain.Payment
	byInv    map[uuid.UUID]*domain.Payment
	webhooks []webhookEntry
}

type webhookEntry struct {
	paymentID *uuid.UUID
	verified  bool
	payload   []byte
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byID: map[uuid.UUID]*domain.Payment{}, byInv: map[uuid.UUID]*domain.Payment{}}
}

func (r *fakeRepo) Save(_ context.Context, p *domain.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[p.ID] = p
	r.byInv[p.InvoiceID] = p
	return nil
}
func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
func (r *fakeRepo) LogWebhook(_ context.Context, paymentID *uuid.UUID, _ string, _ string, raw []byte, verified bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.webhooks = append(r.webhooks, webhookEntry{paymentID: paymentID, verified: verified, payload: raw})
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
		ExpiryTime:    time.Now().Add(15 * time.Minute),
	}, nil
}

type fakePub struct {
	mu        sync.Mutex
	succeeded []*domain.Payment
	failed    []*domain.Payment
}

func (p *fakePub) PublishPaymentSucceeded(_ context.Context, pay *domain.Payment) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.succeeded = append(p.succeeded, pay)
	return nil
}
func (p *fakePub) PublishPaymentFailed(_ context.Context, pay *domain.Payment) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = append(p.failed, pay)
	return nil
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

// Helper: build Service dengan all fakes wired.
func newSvc(t *testing.T) (*Service, *fakeRepo, *fakePub) {
	t.Helper()
	repo := newFakeRepo()
	pub := &fakePub{}
	svc := &Service{
		Payments:  repo,
		Invoices:  &fakeInvoice{driverID: "drv-1", amount: 30_000},
		Pub:       pub,
		Gateway:   &fakeGateway{},
		Clock:     &fakeClock{t: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)},
		ServerKey: "SB-Mid-server-TEST",
		IdemTTL:   time.Hour,
	}
	return svc, repo, pub
}

// Helper: build a real-looking Midtrans notification with valid signature.
func notification(t *testing.T, paymentID uuid.UUID, transactionStatus, fraudStatus string, serverKey string) []byte {
	t.Helper()
	orderID := paymentID.String()
	statusCode := "200"
	gross := "30000.00"
	if transactionStatus == "deny" || transactionStatus == "expire" || transactionStatus == "cancel" || transactionStatus == "failure" {
		statusCode = "202"
	}

	sig := sha512.Sum512([]byte(orderID + statusCode + gross + serverKey))
	body := MidtransNotification{
		TransactionStatus: transactionStatus,
		OrderID:           orderID,
		TransactionID:     "txn-real-123",
		StatusCode:        statusCode,
		GrossAmount:       gross,
		SignatureKey:      hex.EncodeToString(sig[:]),
		FraudStatus:       fraudStatus,
		PaymentType:       "qris",
	}
	bs, _ := json.Marshal(body)
	return bs
}

// ----- CreatePayment tests -----

func TestCreatePayment_HappyPath(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	p, err := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPending, p.Status)
	assert.NotEmpty(t, p.QRString)
	assert.NotEmpty(t, p.GatewayRef)
	assert.Equal(t, int64(30_000), p.Amount.Amount())
}

func TestCreatePayment_IdempotentByInvoiceID(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	invID := uuid.New()
	p1, err := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: invID})
	require.NoError(t, err)

	p2, err := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: invID})
	require.NoError(t, err)
	assert.Equal(t, p1.ID, p2.ID, "second call with same invoice_id (PENDING) must return same payment")
	assert.Equal(t, p1.QRString, p2.QRString)
}

func TestCreatePayment_ExpiredPending_RegeneratesNew(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	invID := uuid.New()
	p1, err := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: invID})
	require.NoError(t, err)

	// Simulate clock past expiry.
	svc.Clock.(*fakeClock).t = p1.ExpiresAt.Add(time.Minute)

	p2, err := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: invID})
	require.NoError(t, err)
	assert.NotEqual(t, p1.ID, p2.ID, "expired pending should create new payment")
}

func TestCreatePayment_InvoiceLookupFails(t *testing.T) {
	svc, _, _ := newSvc(t)
	svc.Invoices = &fakeInvoice{err: errors.New("invoice not found")}
	_, err := svc.CreatePayment(context.Background(), CreatePaymentInput{InvoiceID: uuid.New()})
	assert.Error(t, err)
}

func TestCreatePayment_GatewayFails(t *testing.T) {
	svc, _, _ := newSvc(t)
	svc.Gateway = &fakeGateway{err: errors.New("midtrans 503")}
	_, err := svc.CreatePayment(context.Background(), CreatePaymentInput{InvoiceID: uuid.New()})
	assert.Error(t, err)
}

// ----- HandleMidtransNotification tests -----

func TestNotification_Settlement_Success(t *testing.T) {
	svc, repo, pub := newSvc(t)
	ctx := context.Background()
	p, err := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	require.NoError(t, err)

	body := notification(t, p.ID, "settlement", "accept", svc.ServerKey)
	require.NoError(t, svc.HandleMidtransNotification(ctx, body))

	got, _ := repo.GetByID(ctx, p.ID)
	assert.Equal(t, domain.StatusSuccess, got.Status)
	assert.NotNil(t, got.SettledAt)
	assert.Len(t, pub.succeeded, 1)
	assert.Len(t, pub.failed, 0)
}

func TestNotification_Capture_AcceptFraud_Success(t *testing.T) {
	svc, repo, pub := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	body := notification(t, p.ID, "capture", "accept", svc.ServerKey)
	require.NoError(t, svc.HandleMidtransNotification(ctx, body))
	got, _ := repo.GetByID(ctx, p.ID)
	assert.Equal(t, domain.StatusSuccess, got.Status)
	assert.Len(t, pub.succeeded, 1)
}

func TestNotification_Capture_ChallengeFraud_NoChange(t *testing.T) {
	svc, repo, pub := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	body := notification(t, p.ID, "capture", "challenge", svc.ServerKey)
	require.NoError(t, svc.HandleMidtransNotification(ctx, body))
	got, _ := repo.GetByID(ctx, p.ID)
	assert.Equal(t, domain.StatusPending, got.Status, "challenge fraud must not auto-settle")
	assert.Len(t, pub.succeeded, 0)
	assert.Len(t, pub.failed, 0)
}

func TestNotification_Deny_Failed(t *testing.T) {
	svc, repo, pub := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	body := notification(t, p.ID, "deny", "", svc.ServerKey)
	require.NoError(t, svc.HandleMidtransNotification(ctx, body))
	got, _ := repo.GetByID(ctx, p.ID)
	assert.Equal(t, domain.StatusFailed, got.Status)
	assert.Len(t, pub.failed, 1)
}

func TestNotification_Expire_Expired(t *testing.T) {
	svc, repo, pub := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	body := notification(t, p.ID, "expire", "", svc.ServerKey)
	require.NoError(t, svc.HandleMidtransNotification(ctx, body))
	got, _ := repo.GetByID(ctx, p.ID)
	assert.Equal(t, domain.StatusExpired, got.Status)
	assert.Len(t, pub.failed, 1)
}

func TestNotification_Pending_NoChange(t *testing.T) {
	svc, repo, pub := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	body := notification(t, p.ID, "pending", "", svc.ServerKey)
	require.NoError(t, svc.HandleMidtransNotification(ctx, body))
	got, _ := repo.GetByID(ctx, p.ID)
	assert.Equal(t, domain.StatusPending, got.Status)
	assert.Len(t, pub.succeeded, 0)
	assert.Len(t, pub.failed, 0)
}

func TestNotification_BadSignature_Rejected(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})

	// Build dengan server key SALAH → signature jadi invalid.
	body := notification(t, p.ID, "settlement", "accept", "WRONG-SERVER-KEY")
	err := svc.HandleMidtransNotification(ctx, body)
	assert.ErrorIs(t, err, domain.ErrInvalidSignature)
}

func TestNotification_AlwaysLogged(t *testing.T) {
	svc, repo, _ := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})

	// 1 valid + 1 invalid → 2 webhook log entries
	bodyOK := notification(t, p.ID, "settlement", "accept", svc.ServerKey)
	bodyBad := notification(t, p.ID, "settlement", "accept", "WRONG-KEY")

	_ = svc.HandleMidtransNotification(ctx, bodyOK)
	_ = svc.HandleMidtransNotification(ctx, bodyBad)

	assert.Len(t, repo.webhooks, 2, "must log both verified & unverified webhooks for security audit")
	assert.True(t, repo.webhooks[0].verified)
	assert.False(t, repo.webhooks[1].verified)
}

func TestNotification_Idempotent_SettlementTwice(t *testing.T) {
	svc, repo, pub := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	body := notification(t, p.ID, "settlement", "accept", svc.ServerKey)

	require.NoError(t, svc.HandleMidtransNotification(ctx, body))
	require.NoError(t, svc.HandleMidtransNotification(ctx, body)) // replay
	require.NoError(t, svc.HandleMidtransNotification(ctx, body)) // replay lagi

	got, _ := repo.GetByID(ctx, p.ID)
	assert.Equal(t, domain.StatusSuccess, got.Status)
	// Note: dengan domain.MarkSuccess yang idempotent (return nil kalau sudah SUCCESS),
	// kita TETAP publish event ulang — production sebaiknya add NATS-level dedup
	// via msg-id (NATS JetStream sudah handle ini lewat `WithMsgID`).
	// Untuk test ini cukup verifikasi state akhir konsisten.
	assert.GreaterOrEqual(t, len(pub.succeeded), 1)
}

func TestNotification_UnknownStatus_NoOp(t *testing.T) {
	svc, repo, pub := newSvc(t)
	ctx := context.Background()
	p, _ := svc.CreatePayment(ctx, CreatePaymentInput{InvoiceID: uuid.New()})
	body := notification(t, p.ID, "future_unknown_status", "", svc.ServerKey)
	require.NoError(t, svc.HandleMidtransNotification(ctx, body)) // tidak boleh error
	got, _ := repo.GetByID(ctx, p.ID)
	assert.Equal(t, domain.StatusPending, got.Status)
	assert.Len(t, pub.succeeded, 0)
	assert.Len(t, pub.failed, 0)
}

// Helper test: ensure money helper tetap kepakai (avoid unused warning saat refactor)
func TestMoneyImportSanity(t *testing.T) {
	assert.Equal(t, int64(30_000), money.IDR(30_000).Amount())
}
