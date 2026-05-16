package nats

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
)

// fakePublisher — implementasi eventbus.Publisher untuk test.
type fakePublisher struct {
	callCount   int
	gotSubject  string
	gotEnvelope eventbus.Envelope
	publishErr  error
}

func (f *fakePublisher) Publish(_ context.Context, subject string, env eventbus.Envelope) error {
	f.callCount++
	f.gotSubject = subject
	f.gotEnvelope = env
	return f.publishErr
}

func (f *fakePublisher) Close() error { return nil }

// --- Constructor --------------------------------------------------------

func TestNewDLQPublisher_StoresBus(t *testing.T) {
	t.Parallel()
	bus := &fakePublisher{}
	p := NewDLQPublisher(bus)
	if p == nil {
		t.Fatalf("NewDLQPublisher returned nil")
	}
	if p.bus == nil {
		t.Fatalf("internal bus not stored")
	}
}

func TestNewDLQPublisher_AcceptsNilBus(t *testing.T) {
	t.Parallel()
	p := NewDLQPublisher(nil)
	if p == nil {
		t.Fatalf("NewDLQPublisher(nil) returned nil instance")
	}
}

// --- PublishDLQ ---------------------------------------------------------

func TestPublishDLQ_HappyPath(t *testing.T) {
	t.Parallel()
	bus := &fakePublisher{}
	p := NewDLQPublisher(bus)

	payload := []byte(`{"foo":"bar"}`)
	err := p.PublishDLQ(context.Background(), "notification.invoice_issued.v1", "evt-1", payload, "template render error")
	if err != nil {
		t.Fatalf("PublishDLQ err = %v", err)
	}
	if bus.callCount != 1 {
		t.Fatalf("Publish call count = %d, want 1", bus.callCount)
	}

	wantSubject := "notification.invoice_issued.v1.dlq"
	if bus.gotSubject != wantSubject {
		t.Errorf("subject = %q, want %q", bus.gotSubject, wantSubject)
	}
	if bus.gotEnvelope.Type != "notification.dlq.v1" {
		t.Errorf("envelope.Type = %q, want notification.dlq.v1", bus.gotEnvelope.Type)
	}
	if bus.gotEnvelope.AggregateType != "dlq" {
		t.Errorf("envelope.AggregateType = %q, want dlq", bus.gotEnvelope.AggregateType)
	}
	if bus.gotEnvelope.AggregateID != "evt-1" {
		t.Errorf("envelope.AggregateID = %q, want evt-1", bus.gotEnvelope.AggregateID)
	}
	if bus.gotEnvelope.ID == "" {
		t.Errorf("envelope.ID should be auto-generated uuid")
	}

	// Verify payload struct content via JSON round-trip.
	var dlq DLQPayload
	if err := json.Unmarshal(bus.gotEnvelope.Payload, &dlq); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if dlq.OriginalSubject != "notification.invoice_issued.v1" {
		t.Errorf("OriginalSubject = %q", dlq.OriginalSubject)
	}
	if dlq.OriginalEventID != "evt-1" {
		t.Errorf("OriginalEventID = %q", dlq.OriginalEventID)
	}
	if string(dlq.OriginalPayload) != string(payload) {
		t.Errorf("OriginalPayload = %q, want %q", dlq.OriginalPayload, payload)
	}
	if dlq.ErrorReason != "template render error" {
		t.Errorf("ErrorReason = %q", dlq.ErrorReason)
	}
	if dlq.FailedAt.IsZero() {
		t.Errorf("FailedAt should be set, got zero")
	}
}

func TestPublishDLQ_NilBus_GracefulDegradation(t *testing.T) {
	t.Parallel()
	p := NewDLQPublisher(nil)
	err := p.PublishDLQ(context.Background(), "x.v1", "e1", []byte("{}"), "err")
	if err != nil {
		t.Fatalf("nil bus should be graceful, got err = %v", err)
	}
}

func TestPublishDLQ_PropagatesPublishError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("nats connection lost")
	bus := &fakePublisher{publishErr: wantErr}
	p := NewDLQPublisher(bus)

	err := p.PublishDLQ(context.Background(), "x.v1", "e1", []byte("{}"), "err")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if bus.callCount != 1 {
		t.Errorf("bus should still have been called once, got %d", bus.callCount)
	}
}

func TestPublishDLQ_SubjectSuffixAppended(t *testing.T) {
	t.Parallel()
	bus := &fakePublisher{}
	p := NewDLQPublisher(bus)

	_ = p.PublishDLQ(context.Background(), "any.subject", "id", nil, "reason")
	if !strings.HasSuffix(bus.gotSubject, ".dlq") {
		t.Errorf("subject %q should end with .dlq", bus.gotSubject)
	}
}

// --- NoopDLQ ------------------------------------------------------------

func TestNoopDLQ_AlwaysNil(t *testing.T) {
	t.Parallel()
	n := NoopDLQ{}
	err := n.PublishDLQ(context.Background(), "any.subject", "id", []byte("x"), "reason")
	if err != nil {
		t.Fatalf("NoopDLQ.PublishDLQ = %v, want nil", err)
	}
}
