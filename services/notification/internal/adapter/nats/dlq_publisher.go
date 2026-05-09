// Package nats — DLQ publisher adapter.
//
// Saat handler gagal (template error, send error, dst), original event di-wrap
// dengan error info dan publish ke `<original_subject>.dlq.v1`. Operator bisa
// pantau DLQ topic untuk replay manual setelah investigate.
package nats

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
)

type DLQPublisher struct {
	bus eventbus.Publisher
}

func NewDLQPublisher(bus eventbus.Publisher) *DLQPublisher {
	return &DLQPublisher{bus: bus}
}

// DLQPayload — wrapper untuk dead-lettered event.
type DLQPayload struct {
	OriginalSubject string    `json:"original_subject"`
	OriginalEventID string    `json:"original_event_id"`
	OriginalPayload []byte    `json:"original_payload"`
	ErrorReason     string    `json:"error_reason"`
	FailedAt        time.Time `json:"failed_at"`
}

// PublishDLQ — implements usecase.DLQPublisher port.
func (p *DLQPublisher) PublishDLQ(ctx context.Context, originalSubject string, originalEventID string, originalPayload []byte, errReason string) error {
	if p.bus == nil {
		return nil // graceful degradation kalau NATS down
	}
	pl := DLQPayload{
		OriginalSubject: originalSubject,
		OriginalEventID: originalEventID,
		OriginalPayload: originalPayload,
		ErrorReason:     errReason,
		FailedAt:        time.Now().UTC(),
	}
	env, err := eventbus.Encode(uuid.NewString(), "notification.dlq.v1", "dlq", originalEventID, pl)
	if err != nil {
		return err
	}
	dlqSubject := originalSubject + ".dlq"
	return p.bus.Publish(ctx, dlqSubject, env)
}

// NoopDLQ — fallback kalau NATS publisher tidak tersedia.
type NoopDLQ struct{}

func (NoopDLQ) PublishDLQ(_ context.Context, _ string, _ string, _ []byte, _ string) error {
	return nil
}
