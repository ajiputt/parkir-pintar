// Package domain — entity + business rules notification service.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Status — siklus hidup notification (terminal: SENT atau FAILED, no retry).
type Status string

const (
	StatusPending Status = "PENDING"
	StatusSent    Status = "SENT"
	StatusFailed  Status = "FAILED"
)

// Channel — delivery channel.
type Channel string

const (
	ChannelEmail Channel = "EMAIL"
	ChannelSMS   Channel = "SMS"  // future
	ChannelPush  Channel = "PUSH" // future
)

// Kind — event yang trigger notification (mirror NATS topic).
type Kind string

const (
	KindReservationConfirmed Kind = "RESERVATION_CONFIRMED"
	KindReservationExpired   Kind = "RESERVATION_EXPIRED"
	KindInvoiceIssued        Kind = "INVOICE_ISSUED"
	KindInvoiceOverdue       Kind = "INVOICE_OVERDUE"
	KindPaymentSucceeded     Kind = "PAYMENT_SUCCEEDED"
	KindPaymentFailed        Kind = "PAYMENT_FAILED"
)

// Notification — aggregate root. Single notification = single dispatch attempt.
//
// EventID = source NATS envelope ID, dipakai untuk idempotency (UNIQUE constraint
// di DB). Kalau NATS redeliver event yang sama, INSERT akan reject + handler skip.
type Notification struct {
	ID        uuid.UUID
	DriverID  string
	Kind      Kind
	Channel   Channel
	Subject   string
	Body      string
	Status    Status
	SentAt    *time.Time
	LastError string
	EventID   string
	CreatedAt time.Time
}

// New — construct PENDING notification.
func New(driverID string, kind Kind, channel Channel, eventID, subject, body string) *Notification {
	return &Notification{
		ID:        uuid.New(),
		DriverID:  driverID,
		Kind:      kind,
		Channel:   channel,
		Subject:   subject,
		Body:      body,
		Status:    StatusPending,
		EventID:   eventID,
		CreatedAt: time.Now().UTC(),
	}
}

// MarkSent — transisi PENDING → SENT.
func (n *Notification) MarkSent(now time.Time) {
	n.Status = StatusSent
	n.SentAt = &now
}

// MarkFailed — transisi PENDING → FAILED dengan error reason.
// Terminal — no retry. Caller harus publish ke DLQ subject untuk replay manual.
func (n *Notification) MarkFailed(reason string) {
	n.Status = StatusFailed
	n.LastError = truncate(reason, 1000)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
