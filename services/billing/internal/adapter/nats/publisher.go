package nats

import (
	"context"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
)

type Publisher struct {
	bus eventbus.Publisher
}

func NewPublisher(bus eventbus.Publisher) *Publisher { return &Publisher{bus: bus} }

type InvoicePayload struct {
	InvoiceID     string `json:"invoice_id"`
	ReservationID string `json:"reservation_id"`
	DriverID      string `json:"driver_id"`
	Status        string `json:"status"`
	TotalIDR      int64  `json:"total_idr"`
	PaymentMode   string `json:"payment_mode"` // AUTO atau MANUAL — ADR-0014
}

func (p *Publisher) PublishInvoiceIssued(ctx context.Context, inv *domain.Invoice) error {
	return p.publish(ctx, eventbus.SubjBillingInvoiceIssued, "billing.invoice.issued.v1", inv)
}

func (p *Publisher) PublishInvoicePaid(ctx context.Context, inv *domain.Invoice) error {
	return p.publish(ctx, eventbus.SubjBillingInvoicePaid, "billing.invoice.paid.v1", inv)
}

// PublishInvoiceOverdue — Tier 2 ADR-0014: dipanggil oleh OverdueWorker.
func (p *Publisher) PublishInvoiceOverdue(ctx context.Context, inv *domain.Invoice) error {
	return p.publish(ctx, eventbus.SubjBillingInvoiceOverdue, "billing.invoice.overdue.v1", inv)
}

func (p *Publisher) publish(ctx context.Context, subject, eventType string, inv *domain.Invoice) error {
	pl := InvoicePayload{
		InvoiceID:     inv.ID.String(),
		ReservationID: inv.ReservationID.String(),
		DriverID:      inv.DriverID,
		Status:        string(inv.Status),
		TotalIDR:      inv.Total.Amount(),
		PaymentMode:   string(inv.PaymentMode),
	}
	env, err := eventbus.Encode(uuid.NewString(), eventType, "invoice", inv.ID.String(), pl)
	if err != nil {
		return err
	}
	return p.bus.Publish(ctx, subject, env)
}

// NoopPublisher — graceful degradation kalau NATS down.
type NoopPublisher struct{}

func (NoopPublisher) PublishInvoiceIssued(_ context.Context, _ *domain.Invoice) error  { return nil }
func (NoopPublisher) PublishInvoicePaid(_ context.Context, _ *domain.Invoice) error    { return nil }
func (NoopPublisher) PublishInvoiceOverdue(_ context.Context, _ *domain.Invoice) error { return nil }
