package usecase

import (
	"context"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// CheckIn use case — driver tiba di parkir.
type CheckIn struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
}

func (uc *CheckIn) Execute(ctx context.Context, id uuid.UUID) (*domain.Reservation, error) {
	r, err := uc.Reservations.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	now := uc.Clock.Now()
	if err := r.CheckIn(now); err != nil {
		return nil, err
	}
	if err := uc.Reservations.UpdateState(ctx, r); err != nil {
		return nil, err
	}
	if err := uc.Spots.MarkOccupied(ctx, r.SpotID); err != nil {
		return nil, err
	}
	_ = uc.Events.PublishReservationCheckedIn(ctx, r)
	return r, nil
}

// CheckOut use case — driver pulang.
type CheckOut struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
}

func (uc *CheckOut) Execute(ctx context.Context, id uuid.UUID) (*domain.Reservation, error) {
	r, err := uc.Reservations.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	now := uc.Clock.Now()
	if err := r.CheckOut(now); err != nil {
		return nil, err
	}
	if err := uc.Reservations.UpdateState(ctx, r); err != nil {
		return nil, err
	}
	if err := uc.Spots.MarkAvailable(ctx, r.SpotID); err != nil {
		return nil, err
	}
	_ = uc.Events.PublishReservationCheckedOut(ctx, r)
	return r, nil
}

// Cancel use case.
type Cancel struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
}

func (uc *Cancel) Execute(ctx context.Context, id uuid.UUID, reason string) (*domain.Reservation, error) {
	_ = reason // could be persisted in future
	r, err := uc.Reservations.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := r.Cancel(uc.Clock.Now()); err != nil {
		return nil, err
	}
	if err := uc.Reservations.UpdateState(ctx, r); err != nil {
		return nil, err
	}
	if err := uc.Spots.MarkAvailable(ctx, r.SpotID); err != nil {
		return nil, err
	}
	_ = uc.Events.PublishReservationCancelled(ctx, r)
	return r, nil
}
