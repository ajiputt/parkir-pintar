// Package nats — adapter publisher untuk reservation events.
package nats

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// Publisher — wrap eventbus.Publisher dengan typed methods.
type Publisher struct {
	bus eventbus.Publisher
}

func NewPublisher(bus eventbus.Publisher) *Publisher {
	return &Publisher{bus: bus}
}

// ReservationPayload — struktur payload event (dipakai oleh billing).
//
// Lihat ADR-0011 — end_at sudah dihapus karena driver gak tau kapan keluar.
// Billing dihitung dari actual checkin_at sampai checkout_at.
type ReservationPayload struct {
	ID             string     `json:"id"`
	DriverID       string     `json:"driver_id"`
	SpotID         string     `json:"spot_id"`
	PlateNo        string     `json:"plate_no"`
	VehicleType    string     `json:"vehicle_type"`
	State          string     `json:"state"`
	StartAt        time.Time  `json:"start_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CheckInAt      *time.Time `json:"checkin_at,omitempty"`
	CheckOutAt     *time.Time `json:"checkout_at,omitempty"`
	AssignmentMode string     `json:"assignment_mode"`
	PaymentMode    string     `json:"payment_mode"` // AUTO atau MANUAL — ADR-0014
}

func toPayload(r *domain.Reservation) ReservationPayload {
	return ReservationPayload{
		ID:             r.ID.String(),
		DriverID:       r.DriverID,
		SpotID:         r.SpotID.String(),
		PlateNo:        r.PlateNo,
		VehicleType:    string(r.VehicleType),
		State:          string(r.State),
		StartAt:        r.StartAt,
		ExpiresAt:      r.ExpiresAt,
		CheckInAt:      r.CheckInAt,
		CheckOutAt:     r.CheckOutAt,
		AssignmentMode: string(r.AssignmentMode),
		PaymentMode:    string(r.PaymentMode),
	}
}

func (p *Publisher) PublishReservationConfirmed(ctx context.Context, r *domain.Reservation) error {
	return p.publish(ctx, eventbus.SubjReservationConfirmed, "reservation.confirmed.v1", r)
}

func (p *Publisher) PublishReservationCheckedIn(ctx context.Context, r *domain.Reservation) error {
	return p.publish(ctx, eventbus.SubjReservationCheckedIn, "reservation.checked_in.v1", r)
}

func (p *Publisher) PublishReservationCheckedOut(ctx context.Context, r *domain.Reservation) error {
	return p.publish(ctx, eventbus.SubjReservationCheckedOut, "reservation.checked_out.v1", r)
}

func (p *Publisher) PublishReservationCancelled(ctx context.Context, r *domain.Reservation) error {
	return p.publish(ctx, eventbus.SubjReservationCancelled, "reservation.cancelled.v1", r)
}

func (p *Publisher) PublishReservationExpired(ctx context.Context, r *domain.Reservation) error {
	return p.publish(ctx, eventbus.SubjReservationExpired, "reservation.expired.v1", r)
}

func (p *Publisher) publish(ctx context.Context, subject, eventType string, r *domain.Reservation) error {
	env, err := eventbus.Encode(uuid.NewString(), eventType, "reservation", r.ID.String(), toPayload(r))
	if err != nil {
		return err
	}
	return p.bus.Publish(ctx, subject, env)
}
