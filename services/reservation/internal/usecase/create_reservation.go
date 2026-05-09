package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/lock"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// CreateReservation — orchestrate booking flow.
//
// Steps:
//  1. Resolve spot (SYSTEM = pick available, USER = use given spot_id).
//  2. Acquire Redis lock pada spot (fast-fail untuk kontensi USER).
//  3. Persist reservation (DB EXCLUDE constraint akan menjamin no overlap).
//  4. Mark spot HELD.
//  5. Publish ReservationConfirmed.
//  6. Release lock.
//
// Idempotency dipasang di layer adapter (gRPC handler) supaya seluruh response
// di-cache, bukan hanya intent — lihat adapter/grpcserver/server.go.
type CreateReservation struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Locker       Locker
	Events       EventPublisher
	Clock        Clock
	HoldDuration time.Duration
	SpotLockTTL  time.Duration
	// OverdueChecker — optional. Kalau di-set, pre-check overdue invoice via
	// billing gRPC. Kalau nil, skip check (untuk testing / billing service down).
	OverdueChecker OverdueChecker
}

func (uc *CreateReservation) Execute(ctx context.Context, in CreateReservationInput) (*CreateReservationOutput, error) {
	now := uc.Clock.Now()

	// 0a. Pre-check: 1 driver = 1 active reservation.
	// Friendly error message di app layer. Kalau race lolos, DB partial unique
	// index (one_active_reservation_per_driver) akan reject di Layer 3 — adapter
	// map ke ErrDriverHasActiveReservation juga.
	existing, err := uc.Reservations.FindActiveByDriverID(ctx, in.DriverID)
	if err != nil && !errors.Is(err, domain.ErrReservationNotFound) {
		return nil, err
	}
	if existing != nil {
		return nil, domain.ErrDriverHasActiveReservation
	}

	// 0b. Pre-check: driver punya invoice OVERDUE? Block (lihat ADR-0014).
	// Best-effort: kalau billing service down, skip check (graceful degradation).
	if uc.OverdueChecker != nil {
		count, oerr := uc.OverdueChecker.CountOverdueByDriverID(ctx, in.DriverID)
		if oerr == nil && count > 0 {
			return nil, domain.ErrDriverHasOverdueInvoice
		}
		// oerr ≠ nil → log warning di adapter layer, biarkan booking proceed
		// (assumption: ke-block sementara billing down lebih buruk dari miss check)
	}

	// 1. Resolve spot
	spot, err := uc.resolveSpot(ctx, in)
	if err != nil {
		return nil, err
	}

	// 2. Acquire lock pada spot ID
	lockKey := "lock:spot:" + spot.ID.String()
	release, ok, err := uc.Locker.Acquire(ctx, lockKey, uc.SpotLockTTL)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	if !ok {
		return nil, domain.ErrLockContention
	}
	defer func() { _ = release(ctx) }()

	// 3. Re-check spot status setelah dapat lock (anti-stale read)
	spotFresh, err := uc.Spots.GetByID(ctx, spot.ID)
	if err != nil {
		return nil, err
	}
	if !spotFresh.IsAvailableFor(in.VehicleType) {
		return nil, domain.ErrSpotUnavailable
	}

	// 4. Build reservation domain
	startAt := in.StartAt
	if startAt.IsZero() {
		startAt = now
	}
	r, err := domain.New(
		in.DriverID, in.PlateNo, in.VehicleType, in.Mode, in.PaymentMode,
		spotFresh.ID, startAt,
		startAt.Add(uc.HoldDuration), in.IdempotencyKey,
	)
	if err != nil {
		return nil, err
	}

	// 5. Persist (partial unique index akan reject overlap — lihat ADR-0011).
	if err := uc.Reservations.Create(ctx, r); err != nil {
		// adapter map unique violation:
		//   - one_active_reservation_per_spot   → ErrSpotUnavailable
		//   - one_active_reservation_per_driver → ErrDriverHasActiveReservation
		return nil, err
	}

	// 6. Mark spot HELD (optimistic lock)
	if err := uc.Spots.MarkHeld(ctx, spotFresh.ID, spotFresh.Version); err != nil {
		return nil, err
	}

	// 7. Publish event (best-effort — log error, jangan rollback reservation)
	if err := uc.Events.PublishReservationConfirmed(ctx, r); err != nil {
		// Tidak block flow — billing akan rebuild via outbox/replay nanti
		// (lihat docs/architecture/adr/0006).
		//
		// Known limitation: kalau NATS down saat publish, event hilang.
		// Mitigasi current: NATS JetStream durable + ack mode kurangi risk window.
		// Roadmap M2: full transactional outbox pattern — insert event row ke
		// outbox table dalam tx yang sama dengan reservation, worker terpisah
		// polling + publish + mark sent. Lihat ROADMAP.md item RES-OUTBOX.
		_ = err
	}

	return &CreateReservationOutput{Reservation: r, Spot: spotFresh}, nil
}

func (uc *CreateReservation) resolveSpot(ctx context.Context, in CreateReservationInput) (*domain.Spot, error) {
	if in.Mode == domain.AssignmentUser {
		if in.SpotID == nil {
			return nil, fmt.Errorf("%w: spot_id required for USER mode", domain.ErrSpotNotFound)
		}
		spot, err := uc.Spots.GetByID(ctx, *in.SpotID)
		if err != nil {
			return nil, err
		}
		if spot.VehicleType != in.VehicleType {
			return nil, domain.ErrSpotMismatchedType
		}
		if !spot.IsAvailableFor(in.VehicleType) {
			return nil, domain.ErrSpotUnavailable
		}
		return spot, nil
	}

	// SYSTEM mode: pick first available (adapter pilih area default)
	spot, err := uc.Spots.PickAvailable(ctx, in.VehicleType)
	if err != nil {
		if errors.Is(err, domain.ErrSpotNotFound) {
			return nil, domain.ErrNoSpotAvailable
		}
		return nil, err
	}
	return spot, nil
}

// helper: ensure unused import
var _ = uuid.Nil
var _ lock.Locker
