// Package usecase — application services + port interfaces.
//
// Hex pattern: usecase doesn't know if email is via SES, SMTP, or stub. Kalau
// ekspansi multi-channel (SMS, push), tambah port baru, dispatcher branch via channel.
package usecase

import (
	"context"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

// ContactRepo — port lookup user contact by driver_id.
type ContactRepo interface {
	GetByDriverID(ctx context.Context, driverID string) (*domain.Contact, error)
	Upsert(ctx context.Context, c *domain.Contact) error
}

// NotificationRepo — port persistence audit log notification.
//
// SaveNew expected gagal dengan ErrAlreadyDispatched kalau event_id sudah ada
// (UNIQUE constraint). Caller pakai itu untuk dedup.
type NotificationRepo interface {
	SaveNew(ctx context.Context, n *domain.Notification) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.Status, sentAt *interface{}, lastErr string) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error)
	ExistsForEventID(ctx context.Context, eventID string) (bool, error)
}

// EmailSender — port channel adapter (SES, SMTP, mock log-only).
type EmailSender interface {
	Send(ctx context.Context, toEmail, toName, subject, body string) error
}

// TemplateRenderer — port: load + render template (kind → subject + body).
//
// Variables = arbitrary map dari handler. Template engine define variable yang
// expected di template file.
type TemplateRenderer interface {
	Render(kind domain.Kind, vars map[string]any) (subject string, body string, err error)
}

// DLQPublisher — port untuk publish failed notification ke DLQ subject untuk
// manual replay. Terpisah dari main publisher karena schema event berbeda
// (wrapper dengan original event + error info).
type DLQPublisher interface {
	PublishDLQ(ctx context.Context, originalSubject string, eventID string, originalPayload []byte, errReason string) error
}
