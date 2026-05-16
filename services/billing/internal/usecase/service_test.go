package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/pkg/pricing"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/usecase"
)

// ----- test doubles -----

type fakeRepo struct {
	mu              sync.Mutex
	byID            map[uuid.UUID]*domain.Invoice
	byReservation   map[uuid.UUID]*domain.Invoice
	saved           []*domain.Invoice
	saveErr         error
	getByIDErr      error
	getByResErr     error // returned when invoice not in byReservation
	findOverdueRes  []*domain.Invoice
	findOverdueErr  error
	countByDriver   int
	countByDriverEr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID:          map[uuid.UUID]*domain.Invoice{},
		byReservation: map[uuid.UUID]*domain.Invoice{},
	}
}

func (r *fakeRepo) GetByReservationID(_ context.Context, id uuid.UUID) (*domain.Invoice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inv, ok := r.byReservation[id]; ok {
		return inv, nil
	}
	if r.getByResErr != nil {
		return nil, r.getByResErr
	}
	return nil, domain.ErrInvoiceNotFound
}

func (r *fakeRepo) Save(_ context.Context, inv *domain.Invoice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[inv.ID] = inv
	r.byReservation[inv.ReservationID] = inv
	r.saved = append(r.saved, inv)
	return nil
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

func (r *fakeRepo) FindOverdueCandidates(_ context.Context, _ time.Time, _ int) ([]*domain.Invoice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.findOverdueErr != nil {
		return nil, r.findOverdueErr
	}
	return r.findOverdueRes, nil
}

func (r *fakeRepo) CountOverdueByDriver(_ context.Context, _ string) (int, error) {
	if r.countByDriverEr != nil {
		return 0, r.countByDriverEr
	}
	return r.countByDriver, nil
}

// ----- event log fake -----

type fakeEvents struct {
	mu       sync.Mutex
	seen     map[string]bool
	appended []eventbus.Envelope
	seenErr  error
	appErr   error
}

func newFakeEvents() *fakeEvents { return &fakeEvents{seen: map[string]bool{}} }

func (e *fakeEvents) Seen(_ context.Context, id string) (bool, error) {
	if e.seenErr != nil {
		return false, e.seenErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.seen[id], nil
}

func (e *fakeEvents) Append(_ context.Context, env eventbus.Envelope) error {
	if e.appErr != nil {
		return e.appErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen[env.ID] = true
	e.appended = append(e.appended, env)
	return nil
}

// ----- publisher fake -----

type fakePub struct {
	mu      sync.Mutex
	issued  []*domain.Invoice
	paid    []*domain.Invoice
	overdue []*domain.Invoice
	issErr  error
	paidErr error
	ovErr   error
}

func (p *fakePub) PublishInvoiceIssued(_ context.Context, inv *domain.Invoice) error {
	if p.issErr != nil {
		return p.issErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.issued = append(p.issued, inv)
	return nil
}

func (p *fakePub) PublishInvoicePaid(_ context.Context, inv *domain.Invoice) error {
	if p.paidErr != nil {
		return p.paidErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paid = append(p.paid, inv)
	return nil
}

func (p *fakePub) PublishInvoiceOverdue(_ context.Context, inv *domain.Invoice) error {
	if p.ovErr != nil {
		return p.ovErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.overdue = append(p.overdue, inv)
	return nil
}

// ----- helpers -----

func newSvc(t *testing.T) (*usecase.Service, *fakeRepo, *fakeEvents, *fakePub) {
	t.Helper()
	repo := newFakeRepo()
	events := newFakeEvents()
	pub := &fakePub{}
	svc := &usecase.Service{
		Invoices: repo,
		Events:   events,
		Engine:   pricing.Default(),
		Pub:      pub,
	}
	return svc, repo, events, pub
}

func resEnv(t *testing.T, pl usecase.ReservationPayload) eventbus.Envelope {
	t.Helper()
	b, err := json.Marshal(pl)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return eventbus.Envelope{
		ID:            uuid.NewString(),
		Type:          "reservation.confirmed.v1",
		AggregateType: "reservation",
		AggregateID:   pl.ID,
		OccurredAt:    time.Now().UTC(),
		Payload:       b,
	}
}

func paymentEnv(t *testing.T, invID uuid.UUID) eventbus.Envelope {
	t.Helper()
	pl := map[string]string{
		"payment_id": uuid.NewString(),
		"invoice_id": invID.String(),
	}
	b, _ := json.Marshal(pl)
	return eventbus.Envelope{
		ID:          uuid.NewString(),
		Type:        "payment.succeeded.v1",
		AggregateID: invID.String(),
		OccurredAt:  time.Now().UTC(),
		Payload:     b,
	}
}

// ----- HandleReservationConfirmed -----

func TestHandleReservationConfirmed_CreatesDraftWithBookingFee(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	resID := uuid.New()
	env := resEnv(t, usecase.ReservationPayload{
		ID:          resID.String(),
		DriverID:    "drv-1",
		PaymentMode: "AUTO",
	})

	if err := svc.HandleReservationConfirmed(context.Background(), env); err != nil {
		t.Fatalf("HandleReservationConfirmed: %v", err)
	}

	if len(repo.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(repo.saved))
	}
	inv := repo.saved[0]
	if inv.Status != domain.InvoiceDraft {
		t.Errorf("status = %q, want DRAFT", inv.Status)
	}
	if inv.PaymentMode != domain.PaymentAuto {
		t.Errorf("PaymentMode = %q, want AUTO", inv.PaymentMode)
	}
	if len(inv.Items) != 1 || inv.Items[0].Type != pricing.LineBookingFee {
		t.Errorf("expected exactly 1 booking-fee item, got %+v", inv.Items)
	}
	if inv.Total.Amount() != 5_000 {
		t.Errorf("Total = %d, want 5000", inv.Total.Amount())
	}
}

func TestHandleReservationConfirmed_DuplicateEvent_NoOp(t *testing.T) {
	svc, repo, events, _ := newSvc(t)
	env := resEnv(t, usecase.ReservationPayload{
		ID:          uuid.NewString(),
		DriverID:    "drv-1",
		PaymentMode: "AUTO",
	})
	events.seen[env.ID] = true

	if err := svc.HandleReservationConfirmed(context.Background(), env); err != nil {
		t.Fatalf("HandleReservationConfirmed: %v", err)
	}
	if len(repo.saved) != 0 {
		t.Errorf("duplicate event must not create invoice; saved=%d", len(repo.saved))
	}
}

func TestHandleReservationConfirmed_InvalidPayloadID_Errors(t *testing.T) {
	svc, _, _, _ := newSvc(t)
	pl := usecase.ReservationPayload{ID: "not-a-uuid", DriverID: "drv-1", PaymentMode: "AUTO"}
	b, _ := json.Marshal(pl)
	env := eventbus.Envelope{ID: uuid.NewString(), Payload: b}

	err := svc.HandleReservationConfirmed(context.Background(), env)
	if err == nil {
		t.Error("expected uuid parse error")
	}
}

func TestHandleReservationConfirmed_RepoSaveError_Propagates(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	repo.saveErr = errors.New("db down")
	env := resEnv(t, usecase.ReservationPayload{
		ID: uuid.NewString(), DriverID: "drv-1", PaymentMode: "AUTO",
	})
	if err := svc.HandleReservationConfirmed(context.Background(), env); err == nil {
		t.Error("expected save error to propagate")
	}
}

func TestHandleReservationConfirmed_ForwardsManualMode(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	env := resEnv(t, usecase.ReservationPayload{
		ID: uuid.NewString(), DriverID: "drv-1", PaymentMode: "MANUAL",
	})
	if err := svc.HandleReservationConfirmed(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if repo.saved[0].PaymentMode != domain.PaymentManual {
		t.Errorf("PaymentMode = %q, want MANUAL", repo.saved[0].PaymentMode)
	}
}

// ----- HandleReservationCheckedOut -----

func TestHandleReservationCheckedOut_HappyPath_IssuesInvoice(t *testing.T) {
	svc, repo, _, pub := newSvc(t)
	resID := uuid.New()
	// Seed pre-existing draft (from earlier ReservationConfirmed).
	inv := domain.NewWithMode(resID, "drv-1", domain.PaymentAuto)
	_ = inv.AddLine(pricing.Default().BookingLine())
	_ = repo.Save(context.Background(), inv)

	in := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	out := in.Add(2 * time.Hour)
	env := resEnv(t, usecase.ReservationPayload{
		ID:          resID.String(),
		DriverID:    "drv-1",
		PaymentMode: "AUTO",
		CheckInAt:   &in,
		CheckOutAt:  &out,
	})

	if err := svc.HandleReservationCheckedOut(context.Background(), env); err != nil {
		t.Fatalf("HandleReservationCheckedOut: %v", err)
	}

	// Reload from repo.
	got, err := repo.GetByID(context.Background(), inv.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != domain.InvoiceIssued {
		t.Errorf("Status = %q, want ISSUED", got.Status)
	}
	// 1 booking fee + 1 hourly (2 jam @5000)
	if got.Total.Amount() != 5_000+10_000 {
		t.Errorf("Total = %d, want 15000", got.Total.Amount())
	}
	if len(pub.issued) != 1 {
		t.Errorf("expected 1 invoice-issued publish; got %d", len(pub.issued))
	}
}

func TestHandleReservationCheckedOut_MissingDraft_CreatesOnTheFly(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	resID := uuid.New()
	in := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	out := in.Add(time.Hour)
	env := resEnv(t, usecase.ReservationPayload{
		ID:          resID.String(),
		DriverID:    "drv-1",
		PaymentMode: "AUTO",
		CheckInAt:   &in,
		CheckOutAt:  &out,
	})

	if err := svc.HandleReservationCheckedOut(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("saved = %d, want 1 (created on the fly)", len(repo.saved))
	}
	if repo.saved[0].Status != domain.InvoiceIssued {
		t.Error("freshly-created invoice should be ISSUED")
	}
}

func TestHandleReservationCheckedOut_MissingTimestamps_NoOp(t *testing.T) {
	svc, repo, events, _ := newSvc(t)
	resID := uuid.New()
	env := resEnv(t, usecase.ReservationPayload{
		ID:          resID.String(),
		DriverID:    "drv-1",
		PaymentMode: "AUTO",
		// CheckInAt / CheckOutAt left nil — incomplete event
	})

	if err := svc.HandleReservationCheckedOut(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(repo.saved) != 0 {
		t.Error("incomplete event must not save invoice")
	}
	if len(events.appended) != 0 {
		t.Error("incomplete event must not be marked as processed")
	}
}

func TestHandleReservationCheckedOut_Idempotent_DuplicateEvent(t *testing.T) {
	svc, repo, events, _ := newSvc(t)
	resID := uuid.New()
	in := time.Now().UTC()
	out := in.Add(time.Hour)
	env := resEnv(t, usecase.ReservationPayload{
		ID:          resID.String(),
		DriverID:    "drv-1",
		PaymentMode: "AUTO",
		CheckInAt:   &in,
		CheckOutAt:  &out,
	})
	events.seen[env.ID] = true

	if err := svc.HandleReservationCheckedOut(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(repo.saved) != 0 {
		t.Error("duplicate event must not save invoice")
	}
}

// ----- HandleReservationExpired -----

func TestHandleReservationExpired_AddsNoShowPenalty_AndIssues(t *testing.T) {
	svc, repo, _, pub := newSvc(t)
	resID := uuid.New()
	env := resEnv(t, usecase.ReservationPayload{
		ID: resID.String(), DriverID: "drv-1", PaymentMode: "AUTO",
	})

	if err := svc.HandleReservationExpired(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}

	if len(repo.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(repo.saved))
	}
	inv := repo.saved[0]
	if inv.Status != domain.InvoiceIssued {
		t.Errorf("expected ISSUED, got %q", inv.Status)
	}
	// booking fee 5000 + no-show penalty 5000
	if inv.Total.Amount() != 10_000 {
		t.Errorf("Total = %d, want 10000 (booking+penalty)", inv.Total.Amount())
	}
	if len(pub.issued) != 1 {
		t.Error("expected publish InvoiceIssued")
	}
}

func TestHandleReservationExpired_PreExistingInvoice_AppendsPenalty(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	resID := uuid.New()
	inv := domain.NewWithMode(resID, "drv-1", domain.PaymentAuto)
	_ = inv.AddLine(pricing.Default().BookingLine())
	_ = repo.Save(context.Background(), inv)

	env := resEnv(t, usecase.ReservationPayload{
		ID: resID.String(), DriverID: "drv-1", PaymentMode: "AUTO",
	})
	if err := svc.HandleReservationExpired(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), inv.ID)
	if got.Total.Amount() != 10_000 {
		t.Errorf("Total = %d, want 10000 after penalty", got.Total.Amount())
	}
}

// ----- HandleReservationCancelled -----

func TestHandleReservationCancelled_NoExistingInvoice_CreatesAndIssues(t *testing.T) {
	svc, repo, _, pub := newSvc(t)
	resID := uuid.New()
	env := resEnv(t, usecase.ReservationPayload{
		ID: resID.String(), DriverID: "drv-1", PaymentMode: "AUTO",
	})
	if err := svc.HandleReservationCancelled(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(repo.saved))
	}
	inv := repo.saved[0]
	if inv.Status != domain.InvoiceIssued {
		t.Errorf("Status = %q, want ISSUED", inv.Status)
	}
	if inv.Total.Amount() != 5_000 {
		t.Errorf("Total = %d, want 5000 (booking fee only per ADR-0012)", inv.Total.Amount())
	}
	if len(pub.issued) != 1 {
		t.Error("expected publish InvoiceIssued")
	}
}

func TestHandleReservationCancelled_ExistingDraft_IssuesIt(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	resID := uuid.New()
	inv := domain.NewWithMode(resID, "drv-1", domain.PaymentAuto)
	_ = inv.AddLine(pricing.Default().BookingLine())
	_ = repo.Save(context.Background(), inv)

	env := resEnv(t, usecase.ReservationPayload{
		ID: resID.String(), DriverID: "drv-1", PaymentMode: "AUTO",
	})
	if err := svc.HandleReservationCancelled(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), inv.ID)
	if got.Status != domain.InvoiceIssued {
		t.Errorf("existing draft should now be ISSUED, got %q", got.Status)
	}
	if got.Total.Amount() != 5_000 {
		t.Errorf("Total should remain 5000 (no additional fee), got %d", got.Total.Amount())
	}
}

// ----- HandlePaymentSucceeded -----

func TestHandlePaymentSucceeded_MarksInvoicePaid(t *testing.T) {
	svc, repo, _, pub := newSvc(t)
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentAuto)
	inv.Issue(time.Now().UTC())
	_ = repo.Save(context.Background(), inv)

	env := paymentEnv(t, inv.ID)
	if err := svc.HandlePaymentSucceeded(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}

	got, _ := repo.GetByID(context.Background(), inv.ID)
	if got.Status != domain.InvoicePaid {
		t.Errorf("Status = %q, want PAID", got.Status)
	}
	if got.PaidAt == nil {
		t.Error("PaidAt must be set")
	}
	if len(pub.paid) != 1 {
		t.Error("expected publish InvoicePaid")
	}
}

func TestHandlePaymentSucceeded_InvoiceNotFound_Errors(t *testing.T) {
	svc, _, _, _ := newSvc(t)
	env := paymentEnv(t, uuid.New()) // no invoice seeded
	err := svc.HandlePaymentSucceeded(context.Background(), env)
	if err == nil {
		t.Error("expected NotFound error")
	}
}

func TestHandlePaymentSucceeded_AlreadyPaid_Idempotent(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentAuto)
	inv.Issue(time.Now().UTC())
	_ = inv.MarkPaid(time.Now().UTC())
	_ = repo.Save(context.Background(), inv)

	env := paymentEnv(t, inv.ID)
	if err := svc.HandlePaymentSucceeded(context.Background(), env); err != nil {
		t.Errorf("idempotent replay should not error: %v", err)
	}
}

func TestHandlePaymentSucceeded_DuplicateEvent_NoOp(t *testing.T) {
	svc, repo, events, pub := newSvc(t)
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentAuto)
	inv.Issue(time.Now().UTC())
	_ = repo.Save(context.Background(), inv)

	env := paymentEnv(t, inv.ID)
	events.seen[env.ID] = true

	if err := svc.HandlePaymentSucceeded(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(pub.paid) != 0 {
		t.Error("duplicate event must not re-publish")
	}
}

// ----- Queries -----

func TestGetInvoice_ReturnsRepoResult(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentAuto)
	_ = repo.Save(context.Background(), inv)

	got, err := svc.GetInvoice(context.Background(), inv.ID)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.ID != inv.ID {
		t.Error("returned invoice mismatched")
	}
}

func TestGetInvoiceByReservation_ReturnsRepoResult(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	resID := uuid.New()
	inv := domain.NewWithMode(resID, "drv-1", domain.PaymentAuto)
	_ = repo.Save(context.Background(), inv)

	got, err := svc.GetInvoiceByReservation(context.Background(), resID)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.ID != inv.ID {
		t.Error("returned invoice mismatched")
	}
}

func TestCountOverdueByDriver_Delegates(t *testing.T) {
	svc, repo, _, _ := newSvc(t)
	repo.countByDriver = 3
	got, err := svc.CountOverdueByDriver(context.Background(), "drv-1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

// ----- Common error paths -----

func TestHandlers_EventLogSeenError_Propagates(t *testing.T) {
	svc, _, events, _ := newSvc(t)
	events.seenErr = errors.New("redis down")
	env := resEnv(t, usecase.ReservationPayload{ID: uuid.NewString(), DriverID: "drv-1"})

	if err := svc.HandleReservationConfirmed(context.Background(), env); err == nil {
		t.Error("expected Seen() error to propagate")
	}
}
