// Package usecase — billing event handlers + queries.
package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/pkg/pricing"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
)

//go:generate mockgen -package=mock_usecase -source=handlers.go -destination=../../_mock/usecase/handlers_mock.go

// InvoiceRepo — persistence port.
type InvoiceRepo interface {
	GetByReservationID(ctx context.Context, reservationID uuid.UUID) (*domain.Invoice, error)
	Save(ctx context.Context, inv *domain.Invoice) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Invoice, error)
	// FindOverdueCandidates — overdue worker scan
	FindOverdueCandidates(ctx context.Context, issuedBefore time.Time, limit int) ([]*domain.Invoice, error)
	// CountOverdueByDriver — driver blocking pre-check
	CountOverdueByDriver(ctx context.Context, driverID string) (int, error)
}

// EventLog — append-only events log + dedup.
type EventLog interface {
	// Seen returns true kalau source_event_id sudah pernah diproses.
	Seen(ctx context.Context, sourceEventID string) (bool, error)
	// Append simpan event + mark seen (atomic).
	Append(ctx context.Context, env eventbus.Envelope) error
}

// EventPublisher — billing publisher (untuk InvoiceIssued/Paid/Overdue).
type EventPublisher interface {
	PublishInvoiceIssued(ctx context.Context, inv *domain.Invoice) error
	PublishInvoicePaid(ctx context.Context, inv *domain.Invoice) error
	PublishInvoiceOverdue(ctx context.Context, inv *domain.Invoice) error
}

// Reservation event payload (mirror dari reservation service publisher).
//
// EndAt dihapus per ADR-0011 — billing dihitung dari (CheckOutAt - CheckInAt).
// PaymentMode forward dari reservation per ADR-0014.
type ReservationPayload struct {
	ID          string     `json:"id"`
	DriverID    string     `json:"driver_id"`
	SpotID      string     `json:"spot_id"`
	State       string     `json:"state"`
	StartAt     time.Time  `json:"start_at"`
	CheckInAt   *time.Time `json:"checkin_at,omitempty"`
	CheckOutAt  *time.Time `json:"checkout_at,omitempty"`
	PaymentMode string     `json:"payment_mode"`
}

// Service — main billing application service.
type Service struct {
	Invoices InvoiceRepo
	Events   EventLog
	Engine   *pricing.Engine
	Pub      EventPublisher
}

// HandleReservationConfirmed — booking fee.
func (s *Service) HandleReservationConfirmed(ctx context.Context, env eventbus.Envelope) error {
	if seen, err := s.Events.Seen(ctx, env.ID); err != nil {
		return err
	} else if seen {
		return nil
	}
	pl, err := eventbus.Decode[ReservationPayload](env)
	if err != nil {
		return err
	}
	resID, err := uuid.Parse(pl.ID)
	if err != nil {
		return err
	}

	// Buat invoice draft + add booking fee, forward payment_mode dari reservation
	inv := domain.NewWithMode(resID, pl.DriverID, domain.PaymentMode(pl.PaymentMode))
	if err := inv.AddLine(s.Engine.BookingLine()); err != nil {
		return err
	}
	if err := s.Invoices.Save(ctx, inv); err != nil {
		return err
	}
	return s.Events.Append(ctx, env)
}

// HandleReservationCheckedOut — hitung billing dari sesi parkir + issue invoice.
func (s *Service) HandleReservationCheckedOut(ctx context.Context, env eventbus.Envelope) error {
	if seen, err := s.Events.Seen(ctx, env.ID); err != nil {
		return err
	} else if seen {
		return nil
	}
	pl, err := eventbus.Decode[ReservationPayload](env)
	if err != nil {
		return err
	}
	resID, err := uuid.Parse(pl.ID)
	if err != nil {
		return err
	}

	inv, err := s.Invoices.GetByReservationID(ctx, resID)
	if err != nil {
		// Tolerate: kalau handler ReservationConfirmed missed, kita create on the fly.
		inv = domain.NewWithMode(resID, pl.DriverID, domain.PaymentMode(pl.PaymentMode))
		if err := inv.AddLine(s.Engine.BookingLine()); err != nil {
			return err
		}
	}

	// Hitung session billing
	if pl.CheckInAt == nil || pl.CheckOutAt == nil {
		return nil // event belum lengkap
	}
	lines, err := s.Engine.CalculateSession(*pl.CheckInAt, *pl.CheckOutAt)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if err := inv.AddLine(l); err != nil {
			return err
		}
	}

	inv.Issue(time.Now().UTC())
	if err := s.Invoices.Save(ctx, inv); err != nil {
		return err
	}
	if err := s.Pub.PublishInvoiceIssued(ctx, inv); err != nil {
		// best-effort
		_ = err
	}
	return s.Events.Append(ctx, env)
}

// HandleReservationExpired — no-show penalty.
func (s *Service) HandleReservationExpired(ctx context.Context, env eventbus.Envelope) error {
	if seen, err := s.Events.Seen(ctx, env.ID); err != nil {
		return err
	} else if seen {
		return nil
	}
	pl, err := eventbus.Decode[ReservationPayload](env)
	if err != nil {
		return err
	}
	resID, err := uuid.Parse(pl.ID)
	if err != nil {
		return err
	}
	inv, err := s.Invoices.GetByReservationID(ctx, resID)
	if err != nil {
		// Kalau invoice belum ada, init dengan booking fee.
		inv = domain.NewWithMode(resID, pl.DriverID, domain.PaymentMode(pl.PaymentMode))
		if err := inv.AddLine(s.Engine.BookingLine()); err != nil {
			return err
		}
	}
	if err := inv.AddLine(s.Engine.NoShowLine()); err != nil {
		return err
	}
	inv.Issue(time.Now().UTC())
	if err := s.Invoices.Save(ctx, inv); err != nil {
		return err
	}
	if err := s.Pub.PublishInvoiceIssued(ctx, inv); err != nil {
		_ = err
	}
	return s.Events.Append(ctx, env)
}

// HandleReservationCancelled — booking fee charge.
//
// Policy (lihat ADR-0012): SEKALI driver berhasil booking (CONFIRMED), booking
// fee 5000 IDR ditagih regardless of terminal state. Cancel = booking_fee saja
// (tidak ditambah cancellation_fee terpisah).
//
// Race-safe: kalau invoice belum sempat dibuat oleh ConfirmedHandler (mis.
// driver cancel sangat cepat setelah CONFIRMED, atau billing service down saat
// confirmed event), create on the fly.
func (s *Service) HandleReservationCancelled(ctx context.Context, env eventbus.Envelope) error {
	if seen, err := s.Events.Seen(ctx, env.ID); err != nil {
		return err
	} else if seen {
		return nil
	}
	pl, err := eventbus.Decode[ReservationPayload](env)
	if err != nil {
		return err
	}
	resID, err := uuid.Parse(pl.ID)
	if err != nil {
		return err
	}

	inv, err := s.Invoices.GetByReservationID(ctx, resID)
	if err != nil {
		// Invoice belum ada → create dengan booking fee.
		inv = domain.NewWithMode(resID, pl.DriverID, domain.PaymentMode(pl.PaymentMode))
		if err := inv.AddLine(s.Engine.BookingLine()); err != nil {
			return err
		}
	}

	// ISSUE invoice (chargeable). Auto-pay subscriber di payment service akan
	// trigger PaymentSucceeded → mark PAID.
	inv.Issue(time.Now().UTC())
	if err := s.Invoices.Save(ctx, inv); err != nil {
		return err
	}
	if err := s.Pub.PublishInvoiceIssued(ctx, inv); err != nil {
		// best-effort
		_ = err
	}
	return s.Events.Append(ctx, env)
}

// HandlePaymentSucceeded — mark invoice PAID.
func (s *Service) HandlePaymentSucceeded(ctx context.Context, env eventbus.Envelope) error {
	if seen, err := s.Events.Seen(ctx, env.ID); err != nil {
		return err
	} else if seen {
		return nil
	}
	type payload struct {
		PaymentID string `json:"payment_id"`
		InvoiceID string `json:"invoice_id"`
	}
	pl, err := eventbus.Decode[payload](env)
	if err != nil {
		return err
	}
	invID, err := uuid.Parse(pl.InvoiceID)
	if err != nil {
		return err
	}
	inv, err := s.Invoices.GetByID(ctx, invID)
	if err != nil {
		return err
	}
	if err := inv.MarkPaid(time.Now().UTC()); err != nil {
		return err
	}
	if err := s.Invoices.Save(ctx, inv); err != nil {
		return err
	}
	if err := s.Pub.PublishInvoicePaid(ctx, inv); err != nil {
		_ = err
	}
	return s.Events.Append(ctx, env)
}

// GetInvoice — query handler.
func (s *Service) GetInvoice(ctx context.Context, id uuid.UUID) (*domain.Invoice, error) {
	return s.Invoices.GetByID(ctx, id)
}

// GetInvoiceByReservation — query handler.
func (s *Service) GetInvoiceByReservation(ctx context.Context, resID uuid.UUID) (*domain.Invoice, error) {
	return s.Invoices.GetByReservationID(ctx, resID)
}

// CountOverdueByDriver — query handler. Tier 2 ADR-0014.
func (s *Service) CountOverdueByDriver(ctx context.Context, driverID string) (int, error) {
	return s.Invoices.CountOverdueByDriver(ctx, driverID)
}
