package usecase

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// CheckIn use case — driver tiba di parkir.
//
// Multi-aggregate write (reservation state + spot status) di-wrap dalam
// single TX via TxRunner (ADR-0024). Either both commit, or both rollback.
type CheckIn struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
	TxRunner     TxRunner
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

	// Atomic: UpdateState (CONFIRMED → CHECKED_IN) + MarkOccupied (HELD → OCCUPIED).
	// Kalau salah satu fail, rollback semua → state machine tetap konsisten.
	if err := uc.TxRunner.RunInTx(ctx, func(tx pgx.Tx) error {
		if err := uc.Reservations.UpdateStateTx(ctx, tx, r); err != nil {
			return err
		}
		return uc.Spots.MarkOccupiedTx(ctx, tx, r.SpotID)
	}); err != nil {
		return nil, err
	}

	// Best-effort post-commit publish. Future: outbox pattern dalam TX.
	_ = uc.Events.PublishReservationCheckedIn(ctx, r)
	return r, nil
}

// CheckOut use case — driver pulang.
//
// Multi-aggregate write di-wrap dalam single TX (ADR-0024).
type CheckOut struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
	TxRunner     TxRunner
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

	// Atomic: UpdateState (CHECKED_IN → CHECKED_OUT) + MarkAvailable (OCCUPIED → AVAILABLE).
	if err := uc.TxRunner.RunInTx(ctx, func(tx pgx.Tx) error {
		if err := uc.Reservations.UpdateStateTx(ctx, tx, r); err != nil {
			return err
		}
		return uc.Spots.MarkAvailableTx(ctx, tx, r.SpotID)
	}); err != nil {
		return nil, err
	}

	_ = uc.Events.PublishReservationCheckedOut(ctx, r)
	return r, nil
}

// Cancel use case.
//
// Multi-aggregate write di-wrap dalam single TX (ADR-0024).
type Cancel struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
	TxRunner     TxRunner
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

	// Atomic: UpdateState (CONFIRMED → CANCELLED) + MarkAvailable (HELD → AVAILABLE).
	if err := uc.TxRunner.RunInTx(ctx, func(tx pgx.Tx) error {
		if err := uc.Reservations.UpdateStateTx(ctx, tx, r); err != nil {
			return err
		}
		return uc.Spots.MarkAvailableTx(ctx, tx, r.SpotID)
	}); err != nil {
		return nil, err
	}

	_ = uc.Events.PublishReservationCancelled(ctx, r)
	return r, nil
}
