// Package eventbus — abstraksi publish/subscribe.
// Default impl: NATS JetStream (durable, at-least-once).
//
// Event di-encode JSON. Untuk schema yang strict, ganti ke proto/avro di production.
package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

//go:generate mockgen -package=mock_eventbus -source=eventbus.go -destination=../_mock/eventbus/eventbus_mock.go

// Envelope — event envelope dengan metadata yang konsisten.
type Envelope struct {
	ID            string            `json:"id"`             // UUID, untuk dedup di consumer
	Type          string            `json:"type"`           // mis. "reservation.confirmed.v1"
	AggregateType string            `json:"aggregate_type"` // mis. "reservation"
	AggregateID   string            `json:"aggregate_id"`
	OccurredAt    time.Time         `json:"occurred_at"`
	TraceID       string            `json:"trace_id,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Payload       json.RawMessage   `json:"payload"`
}

// Publisher — antarmuka untuk service yang publish event.
type Publisher interface {
	Publish(ctx context.Context, subject string, env Envelope) error
	Close() error
}

// Subscriber — antarmuka untuk service yang konsumsi event.
type Subscriber interface {
	// Subscribe — durable consumer dengan ack manual.
	// Handler harus return nil = ack, error = nack (redeliver).
	Subscribe(ctx context.Context, subject, durableName string, handler Handler) error
	Close() error
}

// Handler — signature consumer.
type Handler func(ctx context.Context, env Envelope) error

// Common errors.
var (
	ErrEmptySubject = errors.New("eventbus: empty subject")
)

// Encode — helper.
func Encode[T any](id, typ, aggregateType, aggregateID string, payload T) (Envelope, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		ID:            id,
		Type:          typ,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		OccurredAt:    time.Now().UTC(),
		Payload:       b,
	}, nil
}

// Decode — helper untuk unmarshal payload ke struct.
func Decode[T any](env Envelope) (T, error) {
	var out T
	err := json.Unmarshal(env.Payload, &out)
	return out, err
}
