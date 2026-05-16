package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/notification/internal/usecase"
)

// ----- fakes -----

type fakeContacts struct {
	mu      sync.Mutex
	byID    map[string]*domain.Contact
	getErr  error
	getMiss bool
}

func newFakeContacts() *fakeContacts {
	return &fakeContacts{byID: map[string]*domain.Contact{}}
}

func (c *fakeContacts) GetByDriverID(_ context.Context, id string) (*domain.Contact, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.getErr != nil {
		return nil, c.getErr
	}
	if c.getMiss {
		return nil, domain.ErrContactNotFound
	}
	if v, ok := c.byID[id]; ok {
		return v, nil
	}
	return nil, domain.ErrContactNotFound
}

func (c *fakeContacts) Upsert(_ context.Context, ct *domain.Contact) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byID[ct.DriverID] = ct
	return nil
}

type fakeNotifications struct {
	mu        sync.Mutex
	saved     []*domain.Notification
	existsRet bool
	existsErr error
	saveErr   error
	updates   []notifUpdate
	updateErr error
	byID      map[uuid.UUID]*domain.Notification
}

type notifUpdate struct {
	id        uuid.UUID
	status    domain.Status
	lastErr   string
	hasSentAt bool
}

func newFakeNotifications() *fakeNotifications {
	return &fakeNotifications{byID: map[uuid.UUID]*domain.Notification{}}
}

func (n *fakeNotifications) SaveNew(_ context.Context, notif *domain.Notification) error {
	if n.saveErr != nil {
		return n.saveErr
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.saved = append(n.saved, notif)
	n.byID[notif.ID] = notif
	return nil
}

func (n *fakeNotifications) UpdateStatus(_ context.Context, id uuid.UUID, status domain.Status, sentAt *interface{}, lastErr string) error {
	if n.updateErr != nil {
		return n.updateErr
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.updates = append(n.updates, notifUpdate{
		id: id, status: status, lastErr: lastErr, hasSentAt: sentAt != nil,
	})
	return nil
}

func (n *fakeNotifications) GetByID(_ context.Context, id uuid.UUID) (*domain.Notification, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if v, ok := n.byID[id]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}

func (n *fakeNotifications) ExistsForEventID(_ context.Context, _ string) (bool, error) {
	if n.existsErr != nil {
		return false, n.existsErr
	}
	return n.existsRet, nil
}

type fakeSender struct {
	mu      sync.Mutex
	calls   []sendCall
	sendErr error
}

type sendCall struct{ toEmail, toName, subject, body string }

func (s *fakeSender) Send(_ context.Context, to, name, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, sendCall{to, name, subject, body})
	return s.sendErr
}

type fakeRenderer struct {
	subject string
	body    string
	err     error
}

func (r *fakeRenderer) Render(_ domain.Kind, _ map[string]any) (string, string, error) {
	if r.err != nil {
		return "", "", r.err
	}
	return r.subject, r.body, nil
}

type fakeDLQ struct {
	mu    sync.Mutex
	calls []dlqCall
	err   error
}

type dlqCall struct {
	subject string
	eventID string
	payload []byte
	reason  string
}

func (d *fakeDLQ) PublishDLQ(_ context.Context, subject, eventID string, payload []byte, reason string) error {
	if d.err != nil {
		return d.err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, dlqCall{subject, eventID, payload, reason})
	return nil
}

// ----- helpers -----

func newDispatcher(t *testing.T) (*usecase.Dispatcher, *fakeContacts, *fakeNotifications, *fakeSender, *fakeRenderer, *fakeDLQ) {
	t.Helper()
	c := newFakeContacts()
	n := newFakeNotifications()
	s := &fakeSender{}
	r := &fakeRenderer{subject: "Subject", body: "Body"}
	d := &fakeDLQ{}
	disp := &usecase.Dispatcher{
		Contacts:      c,
		Notifications: n,
		Sender:        s,
		Renderer:      r,
		DLQ:           d,
		Logger:        zap.NewNop(),
	}
	return disp, c, n, s, r, d
}

func sampleEnv(t *testing.T) eventbus.Envelope {
	t.Helper()
	return eventbus.Envelope{
		ID:         uuid.NewString(),
		Type:       "reservation.confirmed.v1",
		OccurredAt: time.Now().UTC(),
		Payload:    json.RawMessage(`{}`),
	}
}

func seedContact(c *fakeContacts, driverID string, optIn bool) {
	c.byID[driverID] = &domain.Contact{
		DriverID: driverID,
		Email:    driverID + "@example.com",
		Name:     "Driver " + driverID,
		OptIn:    optIn,
	}
}

// ----- Dispatch -----

func TestDispatch_HappyPath_SendsAndPersists(t *testing.T) {
	disp, contacts, notifs, sender, _, dlq := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	env := sampleEnv(t)

	err := disp.Dispatch(context.Background(), env,
		domain.KindReservationConfirmed, "drv-1", map[string]any{"X": "Y"},
		"reservation.confirmed.v1", []byte("{}"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if len(sender.calls) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sender.calls))
	}
	if sender.calls[0].toEmail != "drv-1@example.com" {
		t.Errorf("send to = %q", sender.calls[0].toEmail)
	}
	if len(notifs.saved) != 1 {
		t.Error("expected 1 notification persisted")
	}
	if len(notifs.updates) != 1 || notifs.updates[0].status != domain.StatusSent {
		t.Errorf("expected status SENT update; got %+v", notifs.updates)
	}
	if !notifs.updates[0].hasSentAt {
		t.Error("SENT update must include sentAt")
	}
	if len(dlq.calls) != 0 {
		t.Errorf("happy path should not DLQ; got %d", len(dlq.calls))
	}
}

func TestDispatch_AlreadyDispatched_DedupSkip(t *testing.T) {
	disp, contacts, notifs, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	notifs.existsRet = true

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindReservationConfirmed, "drv-1", map[string]any{},
		"subj", []byte("{}"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("dedup-existing notification must skip send")
	}
}

func TestDispatch_ExistsCheckError_Skips(t *testing.T) {
	disp, contacts, notifs, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	notifs.existsErr = errors.New("db down")

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindReservationConfirmed, "drv-1", map[string]any{},
		"subj", []byte("{}"))
	if err != nil {
		t.Errorf("must not return err to NATS; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("on exists error, must skip downstream")
	}
}

func TestDispatch_ContactNotFound_SkipSilently(t *testing.T) {
	disp, contacts, _, sender, _, dlq := newDispatcher(t)
	contacts.getMiss = true

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindReservationConfirmed, "drv-1", map[string]any{},
		"subj", []byte("{}"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("contact not found must not send")
	}
	if len(dlq.calls) != 0 {
		t.Error("contact-not-found is silent skip — no DLQ")
	}
}

func TestDispatch_ContactLookupError_DLQAndContinue(t *testing.T) {
	disp, contacts, _, sender, _, dlq := newDispatcher(t)
	contacts.getErr = errors.New("conn refused")

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindReservationConfirmed, "drv-1", map[string]any{},
		"reservation.confirmed.v1", []byte("{}"))
	if err != nil {
		t.Errorf("must not return err to NATS; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("must not send on lookup error")
	}
	if len(dlq.calls) != 1 {
		t.Errorf("expected 1 DLQ publish; got %d", len(dlq.calls))
	}
}

func TestDispatch_OptOut_Skip(t *testing.T) {
	disp, contacts, _, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", false) // opted-out

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindInvoiceIssued, "drv-1", map[string]any{},
		"subj", []byte("{}"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("opted-out driver must not be sent")
	}
}

func TestDispatch_TemplateRenderError_DLQ(t *testing.T) {
	disp, contacts, _, sender, renderer, dlq := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	renderer.err = errors.New("template error")

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindInvoiceIssued, "drv-1", map[string]any{},
		"subj", []byte("{}"))
	if err != nil {
		t.Errorf("must not return err; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("render error → no send")
	}
	if len(dlq.calls) != 1 {
		t.Errorf("expected DLQ publish; got %d", len(dlq.calls))
	}
}

func TestDispatch_PersistError_DLQ(t *testing.T) {
	disp, contacts, notifs, sender, _, dlq := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	notifs.saveErr = errors.New("db write fail")

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindReservationConfirmed, "drv-1", map[string]any{},
		"subj", []byte("{}"))
	if err != nil {
		t.Errorf("must not return err; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("persist error → no send")
	}
	if len(dlq.calls) != 1 {
		t.Errorf("expected DLQ; got %d", len(dlq.calls))
	}
}

func TestDispatch_PersistRaceLost_AlreadyDispatched_NoDLQ(t *testing.T) {
	disp, contacts, notifs, sender, _, dlq := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	notifs.saveErr = domain.ErrAlreadyDispatched

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindReservationConfirmed, "drv-1", map[string]any{},
		"subj", []byte("{}"))
	if err != nil {
		t.Errorf("must not return err; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("race lost → no send")
	}
	if len(dlq.calls) != 0 {
		t.Error("race-lost path must NOT DLQ (other consumer handled it)")
	}
}

func TestDispatch_SendFails_MarksFailedAndDLQ(t *testing.T) {
	disp, contacts, notifs, sender, _, dlq := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	sender.sendErr = errors.New("SES timeout")

	err := disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindInvoiceIssued, "drv-1", map[string]any{},
		"billing.invoice.issued.v1", []byte("{}"))
	if err != nil {
		t.Errorf("send failure must not error to NATS; got %v", err)
	}
	if len(notifs.updates) != 1 || notifs.updates[0].status != domain.StatusFailed {
		t.Errorf("expected status FAILED update; got %+v", notifs.updates)
	}
	if len(dlq.calls) != 1 {
		t.Errorf("expected DLQ publish; got %d", len(dlq.calls))
	}
}

func TestDispatch_InjectsContactDataIntoVars(t *testing.T) {
	disp, contacts, _, _, renderer, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)

	// Capture vars passed to renderer by wrapping it.
	var capturedVars map[string]any
	disp.Renderer = &captureRenderer{base: renderer, captured: &capturedVars}

	_ = disp.Dispatch(context.Background(), sampleEnv(t),
		domain.KindReservationConfirmed, "drv-1", map[string]any{"X": 1},
		"subj", []byte("{}"))

	if capturedVars["DriverName"] != "Driver drv-1" {
		t.Errorf("DriverName var = %v, want 'Driver drv-1'", capturedVars["DriverName"])
	}
	if capturedVars["DriverEmail"] != "drv-1@example.com" {
		t.Errorf("DriverEmail var = %v", capturedVars["DriverEmail"])
	}
	if capturedVars["X"] != 1 {
		t.Errorf("user-provided vars must be preserved; got X=%v", capturedVars["X"])
	}
}

type captureRenderer struct {
	base     *fakeRenderer
	captured *map[string]any
}

func (c *captureRenderer) Render(k domain.Kind, vars map[string]any) (string, string, error) {
	*c.captured = vars
	return c.base.Render(k, vars)
}

// ----- HandleXxx wrappers -----

func mustEnv(t *testing.T, typ, aggregateID string, pl any) eventbus.Envelope {
	t.Helper()
	b, _ := json.Marshal(pl)
	return eventbus.Envelope{
		ID:          uuid.NewString(),
		Type:        typ,
		AggregateID: aggregateID,
		OccurredAt:  time.Now().UTC(),
		Payload:     b,
	}
}

func TestHandleReservationConfirmed_DispatchesWithVars(t *testing.T) {
	disp, contacts, _, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)

	pl := map[string]any{
		"id":         "res-1",
		"driver_id":  "drv-1",
		"spot_id":    "spot-9",
		"plate_no":   "B 1 ABC",
		"state":      "CONFIRMED",
		"start_at":   time.Now().UTC().Format(time.RFC3339),
		"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	env := mustEnv(t, "reservation.confirmed.v1", "res-1", pl)

	if err := disp.HandleReservationConfirmed(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sender.calls) != 1 {
		t.Errorf("expected 1 send; got %d", len(sender.calls))
	}
}

func TestHandleReservationConfirmed_BadPayload_AckAndSkip(t *testing.T) {
	disp, _, _, sender, _, _ := newDispatcher(t)
	env := eventbus.Envelope{ID: uuid.NewString(), Payload: []byte("not-json")}

	if err := disp.HandleReservationConfirmed(context.Background(), env); err != nil {
		t.Errorf("poison message must be ack'd (nil err); got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("poison payload must not trigger send")
	}
}

func TestHandleReservationExpired_DispatchesWithVars(t *testing.T) {
	disp, contacts, _, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	pl := map[string]any{
		"id": "res-1", "driver_id": "drv-1", "plate_no": "B 1",
		"expires_at": time.Now().UTC().Format(time.RFC3339),
	}
	env := mustEnv(t, "reservation.expired.v1", "res-1", pl)

	if err := disp.HandleReservationExpired(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sender.calls) != 1 {
		t.Error("expected send")
	}
}

func TestHandleInvoiceIssued_DispatchesWithVars(t *testing.T) {
	disp, contacts, _, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	pl := map[string]any{
		"invoice_id":     "inv-1",
		"reservation_id": "res-1",
		"driver_id":      "drv-1",
		"status":         "ISSUED",
		"total_idr":      15000,
	}
	env := mustEnv(t, "billing.invoice.issued.v1", "inv-1", pl)

	if err := disp.HandleInvoiceIssued(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sender.calls) != 1 {
		t.Error("expected send")
	}
}

func TestHandleInvoiceOverdue_DispatchesWithVars(t *testing.T) {
	disp, contacts, _, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	pl := map[string]any{
		"invoice_id": "inv-1", "reservation_id": "res-1",
		"driver_id": "drv-1", "status": "OVERDUE", "total_idr": 15000,
	}
	env := mustEnv(t, "billing.invoice.overdue.v1", "inv-1", pl)

	if err := disp.HandleInvoiceOverdue(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sender.calls) != 1 {
		t.Error("expected send")
	}
}

func TestHandlePaymentSucceeded_MissingAggregateID_SkipSilent(t *testing.T) {
	disp, _, _, sender, _, _ := newDispatcher(t)
	pl := map[string]any{"payment_id": "pay-1", "invoice_id": "inv-1", "status": "SUCCESS", "amount_idr": 5000}
	b, _ := json.Marshal(pl)
	env := eventbus.Envelope{ID: uuid.NewString(), Type: "payment.succeeded.v1", Payload: b}

	if err := disp.HandlePaymentSucceeded(context.Background(), env); err != nil {
		t.Errorf("must not error; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("missing driver_id (AggregateID) must skip send")
	}
}

func TestHandlePaymentSucceeded_WithAggregateID_Dispatches(t *testing.T) {
	disp, contacts, _, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	pl := map[string]any{"payment_id": "pay-1", "invoice_id": "inv-1", "status": "SUCCESS", "amount_idr": 5000}
	env := mustEnv(t, "payment.succeeded.v1", "drv-1", pl)

	if err := disp.HandlePaymentSucceeded(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sender.calls) != 1 {
		t.Error("expected send")
	}
}

func TestHandlePaymentSucceeded_PoisonPayload_Skip(t *testing.T) {
	disp, _, _, sender, _, _ := newDispatcher(t)
	env := eventbus.Envelope{ID: uuid.NewString(), Type: "payment.succeeded.v1", Payload: []byte("xxx")}
	if err := disp.HandlePaymentSucceeded(context.Background(), env); err != nil {
		t.Errorf("poison must be ack; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("no send on poison")
	}
}

func TestHandlePaymentFailed_MissingAggregateID_Skip(t *testing.T) {
	disp, _, _, sender, _, _ := newDispatcher(t)
	pl := map[string]any{"payment_id": "p", "invoice_id": "i", "status": "FAILED", "amount_idr": 1}
	b, _ := json.Marshal(pl)
	env := eventbus.Envelope{ID: uuid.NewString(), Type: "payment.failed.v1", Payload: b}
	if err := disp.HandlePaymentFailed(context.Background(), env); err != nil {
		t.Errorf("err: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("no driver_id → no send")
	}
}

func TestHandlePaymentFailed_WithAggregateID_Dispatches(t *testing.T) {
	disp, contacts, _, sender, _, _ := newDispatcher(t)
	seedContact(contacts, "drv-1", true)
	pl := map[string]any{"payment_id": "p", "invoice_id": "i", "status": "FAILED", "amount_idr": 1}
	env := mustEnv(t, "payment.failed.v1", "drv-1", pl)
	if err := disp.HandlePaymentFailed(context.Background(), env); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sender.calls) != 1 {
		t.Error("expected send")
	}
}

func TestHandlePaymentFailed_PoisonPayload_Skip(t *testing.T) {
	disp, _, _, sender, _, _ := newDispatcher(t)
	env := eventbus.Envelope{ID: uuid.NewString(), Type: "payment.failed.v1", Payload: []byte("x")}
	if err := disp.HandlePaymentFailed(context.Background(), env); err != nil {
		t.Errorf("poison must ack; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("no send on poison")
	}
}

func TestHandleInvoiceIssued_PoisonPayload_Skip(t *testing.T) {
	disp, _, _, sender, _, _ := newDispatcher(t)
	env := eventbus.Envelope{ID: uuid.NewString(), Type: "billing.invoice.issued.v1", Payload: []byte("x")}
	if err := disp.HandleInvoiceIssued(context.Background(), env); err != nil {
		t.Errorf("poison must ack; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("no send on poison")
	}
}

func TestHandleInvoiceOverdue_PoisonPayload_Skip(t *testing.T) {
	disp, _, _, sender, _, _ := newDispatcher(t)
	env := eventbus.Envelope{ID: uuid.NewString(), Type: "billing.invoice.overdue.v1", Payload: []byte("x")}
	if err := disp.HandleInvoiceOverdue(context.Background(), env); err != nil {
		t.Errorf("poison must ack; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("no send on poison")
	}
}

func TestHandleReservationExpired_PoisonPayload_Skip(t *testing.T) {
	disp, _, _, sender, _, _ := newDispatcher(t)
	env := eventbus.Envelope{ID: uuid.NewString(), Type: "reservation.expired.v1", Payload: []byte("x")}
	if err := disp.HandleReservationExpired(context.Background(), env); err != nil {
		t.Errorf("poison must ack; got %v", err)
	}
	if len(sender.calls) != 0 {
		t.Error("no send on poison")
	}
}
