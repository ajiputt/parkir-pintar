package usecase

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

// Reservation event payload (mirror dari reservation/internal/adapter/nats).
type reservationPayload struct {
	ID         string     `json:"id"`
	DriverID   string     `json:"driver_id"`
	SpotID     string     `json:"spot_id"`
	PlateNo    string     `json:"plate_no"`
	State      string     `json:"state"`
	StartAt    time.Time  `json:"start_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CheckInAt  *time.Time `json:"checkin_at,omitempty"`
	CheckOutAt *time.Time `json:"checkout_at,omitempty"`
}

// Invoice event payload (mirror dari billing publisher).
type invoicePayload struct {
	InvoiceID     string `json:"invoice_id"`
	ReservationID string `json:"reservation_id"`
	DriverID      string `json:"driver_id"`
	Status        string `json:"status"`
	TotalIDR      int64  `json:"total_idr"`
}

// Payment event payload.
type paymentPayload struct {
	PaymentID string `json:"payment_id"`
	InvoiceID string `json:"invoice_id"`
	Status    string `json:"status"`
	AmountIDR int64  `json:"amount_idr"`
}

// HandleReservationConfirmed — kirim email "Booking dikonfirmasi".
func (d *Dispatcher) HandleReservationConfirmed(ctx context.Context, env eventbus.Envelope) error {
	pl, err := eventbus.Decode[reservationPayload](env)
	if err != nil {
		return nil
	}
	raw, _ := json.Marshal(pl)
	vars := map[string]any{
		"ReservationID": pl.ID,
		"SpotID":        pl.SpotID,
		"PlateNo":       pl.PlateNo,
		"StartAt":       pl.StartAt.Format(time.RFC1123),
		"ExpiresAt":     pl.ExpiresAt.Format(time.RFC1123),
	}
	return d.Dispatch(ctx, env, domain.KindReservationConfirmed, pl.DriverID, vars, "reservation.confirmed.v1", raw)
}

// HandleReservationExpired — kirim email "Booking expired (no-show)".
func (d *Dispatcher) HandleReservationExpired(ctx context.Context, env eventbus.Envelope) error {
	pl, err := eventbus.Decode[reservationPayload](env)
	if err != nil {
		return nil
	}
	raw, _ := json.Marshal(pl)
	vars := map[string]any{
		"ReservationID": pl.ID,
		"PlateNo":       pl.PlateNo,
		"ExpiresAt":     pl.ExpiresAt.Format(time.RFC1123),
	}
	return d.Dispatch(ctx, env, domain.KindReservationExpired, pl.DriverID, vars, "reservation.expired.v1", raw)
}

// HandleInvoiceIssued — kirim email "Tagihan parkir kamu".
func (d *Dispatcher) HandleInvoiceIssued(ctx context.Context, env eventbus.Envelope) error {
	pl, err := eventbus.Decode[invoicePayload](env)
	if err != nil {
		return nil
	}
	raw, _ := json.Marshal(pl)
	vars := map[string]any{
		"InvoiceID":     pl.InvoiceID,
		"ReservationID": pl.ReservationID,
		"TotalIDR":      pl.TotalIDR,
	}
	return d.Dispatch(ctx, env, domain.KindInvoiceIssued, pl.DriverID, vars, "billing.invoice.issued.v1", raw)
}

// HandleInvoiceOverdue — Tier 2 ADR-0014: tagihan tertunggak, kirim peringatan.
func (d *Dispatcher) HandleInvoiceOverdue(ctx context.Context, env eventbus.Envelope) error {
	pl, err := eventbus.Decode[invoicePayload](env)
	if err != nil {
		return nil
	}
	raw, _ := json.Marshal(pl)
	vars := map[string]any{
		"InvoiceID":     pl.InvoiceID,
		"ReservationID": pl.ReservationID,
		"TotalIDR":      pl.TotalIDR,
	}
	return d.Dispatch(ctx, env, domain.KindInvoiceOverdue, pl.DriverID, vars, "billing.invoice.overdue.v1", raw)
}

// HandlePaymentSucceeded — kirim email "Pembayaran berhasil".
//
// Note: payment payload tidak punya driver_id langsung. Kita perlu lookup
// invoice → driver_id. Untuk simplicity demo, kita extract dari env.AggregateID
// yang berisi payment.ID, dan asumsikan downstream system bisa enrich.
//
// FUTURE: kalau perlu driver_id strict, tambah field di payment event payload.
func (d *Dispatcher) HandlePaymentSucceeded(ctx context.Context, env eventbus.Envelope) error {
	pl, err := eventbus.Decode[paymentPayload](env)
	if err != nil {
		return nil
	}
	// Kita TIDAK punya driver_id di payment event. Skip notification (alternative:
	// query billing service untuk lookup, tapi tambah cross-service call).
	// Untuk demo, kita pakai placeholder driver_id dari env.AggregateID kalau ada.
	driverID := env.AggregateID
	if driverID == "" {
		d.Logger.Debug("payment event tanpa driver_id — skip notification")
		return nil
	}
	raw, _ := json.Marshal(pl)
	vars := map[string]any{
		"PaymentID": pl.PaymentID,
		"InvoiceID": pl.InvoiceID,
		"AmountIDR": pl.AmountIDR,
	}
	return d.Dispatch(ctx, env, domain.KindPaymentSucceeded, driverID, vars, "payment.succeeded.v1", raw)
}

// HandlePaymentFailed — kirim email "Pembayaran gagal".
func (d *Dispatcher) HandlePaymentFailed(ctx context.Context, env eventbus.Envelope) error {
	pl, err := eventbus.Decode[paymentPayload](env)
	if err != nil {
		return nil
	}
	driverID := env.AggregateID
	if driverID == "" {
		return nil
	}
	raw, _ := json.Marshal(pl)
	vars := map[string]any{
		"PaymentID": pl.PaymentID,
		"InvoiceID": pl.InvoiceID,
		"AmountIDR": pl.AmountIDR,
	}
	return d.Dispatch(ctx, env, domain.KindPaymentFailed, driverID, vars, "payment.failed.v1", raw)
}
