// Package domain — model billing.
package domain

import (
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/money"
	"github.com/ajiperdana/parkir-pintar/pkg/pricing"
)

type InvoiceStatus string

const (
	InvoiceDraft   InvoiceStatus = "DRAFT"
	InvoiceIssued  InvoiceStatus = "ISSUED"
	InvoicePaid    InvoiceStatus = "PAID"
	InvoiceVoid    InvoiceStatus = "VOID"
	InvoiceOverdue InvoiceStatus = "OVERDUE" // ADR-0014: MANUAL mode + age > grace period
)

// PaymentMode — forwarded dari reservation. Lihat ADR-0014.
type PaymentMode string

const (
	PaymentAuto   PaymentMode = "AUTO"
	PaymentManual PaymentMode = "MANUAL"
)

// Invoice — aggregate root.
type Invoice struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	DriverID      string
	Status        InvoiceStatus
	PaymentMode   PaymentMode // forwarded dari reservation, AUTO/MANUAL
	Total         money.Money
	Items         []Item
	CreatedAt     time.Time
	IssuedAt      *time.Time
	PaidAt        *time.Time
	OverdueAt     *time.Time // di-set saat MarkOverdue (ADR-0014)
	UpdatedAt     time.Time
}

// Item — line item.
type Item struct {
	ID          uuid.UUID
	Type        pricing.LineKind
	Description string
	Amount      money.Money
	PeriodStart *time.Time
	PeriodEnd   *time.Time
}

// New — factory invoice draft. payMode default PaymentAuto kalau callee tidak peduli.
// Untuk full constructor pakai NewWithMode.
func New(reservationID uuid.UUID, driverID string) *Invoice {
	return NewWithMode(reservationID, driverID, PaymentAuto)
}

// NewWithMode — factory dengan payment mode eksplisit.
func NewWithMode(reservationID uuid.UUID, driverID string, payMode PaymentMode) *Invoice {
	if payMode == "" {
		payMode = PaymentAuto
	}
	now := time.Now().UTC()
	return &Invoice{
		ID:            uuid.New(),
		ReservationID: reservationID,
		DriverID:      driverID,
		Status:        InvoiceDraft,
		PaymentMode:   payMode,
		Total:         money.Zero(),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// MarkOverdue — transisi ISSUED → OVERDUE. Hanya valid untuk MANUAL mode +
// invoice yang belum PAID. Idempotent.
func (inv *Invoice) MarkOverdue(now time.Time) error {
	if inv.Status == InvoiceOverdue {
		return nil // idempotent
	}
	if inv.Status != InvoiceIssued {
		return ErrInvalidStateTransition
	}
	if inv.PaymentMode != PaymentManual {
		return ErrInvalidStateTransition // AUTO mode shouldn't go OVERDUE
	}
	inv.Status = InvoiceOverdue
	inv.OverdueAt = &now
	inv.UpdatedAt = now
	return nil
}

// AddLine — append + recompute total.
func (inv *Invoice) AddLine(line pricing.Line) error {
	item := Item{
		ID:          uuid.New(),
		Type:        line.Kind,
		Description: line.Description,
		Amount:      line.Amount,
	}
	if !line.PeriodStart.IsZero() {
		t := line.PeriodStart
		item.PeriodStart = &t
	}
	if !line.PeriodEnd.IsZero() {
		t := line.PeriodEnd
		item.PeriodEnd = &t
	}
	inv.Items = append(inv.Items, item)
	t, err := inv.Total.Add(line.Amount)
	if err != nil {
		return err
	}
	inv.Total = t
	inv.UpdatedAt = time.Now().UTC()
	return nil
}

// Issue — pindah ke ISSUED (after CheckOut event diterima).
func (inv *Invoice) Issue(now time.Time) {
	inv.Status = InvoiceIssued
	inv.IssuedAt = &now
	inv.UpdatedAt = now
}

// MarkPaid — settle invoice. Idempotent. ISSUED dan OVERDUE bisa di-PAID
// (driver bayar setelah kena overdue → unblock).
func (inv *Invoice) MarkPaid(now time.Time) error {
	if inv.Status == InvoicePaid {
		return nil // idempotent
	}
	if inv.Status != InvoiceIssued && inv.Status != InvoiceOverdue {
		return ErrInvalidStateTransition
	}
	inv.Status = InvoicePaid
	inv.PaidAt = &now
	inv.UpdatedAt = now
	return nil
}
