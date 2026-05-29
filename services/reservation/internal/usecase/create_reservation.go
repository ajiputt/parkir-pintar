package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ajiperdana/parkir-pintar/pkg/lock"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// CreateReservation — orchestrate booking flow.
//
// Steps:
//  1. Pre-check 1-driver-1-active + overdue invoice.
//  2. Resolve spot (SYSTEM = pick available, USER = use given spot_id).
//  3. Acquire Redis lock pada spot (fast-fail untuk kontensi USER).
//  4. Re-read spot (anti stale).
//  5. Persist reservation + Mark spot HELD ATOMIC via db.RunInTx (ADR-0024).
//     → kalau MarkHeld fail (version mismatch), reservation INSERT auto-rollback
//     → consistency invariant terjaga
//  6. Publish ReservationConfirmed (best-effort, post-commit).
//  7. Release lock.
//
// Idempotency dipasang di layer adapter (gRPC handler) supaya seluruh response
// di-cache, bukan hanya intent — lihat adapter/grpcserver/server.go.
type CreateReservation struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Locker       Locker
	Events       EventPublisher
	Clock        Clock
	TxRunner     TxRunner // ADR-0024 — wrap multi-aggregate writes
	HoldDuration time.Duration
	SpotLockTTL  time.Duration
	// OverdueChecker — optional. Kalau di-set, pre-check overdue invoice via
	// billing gRPC. Kalau nil, skip check (untuk testing / billing service down).
	OverdueChecker OverdueChecker
}

//nolint:gocyclo // orchestrates booking flow; splitting hurts readability of the linear pipeline
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

	// 5. Persist + Mark spot HELD ATOMIC (ADR-0024).
	//    Either both commit, or both rollback. Tidak ada lagi state inkonsisten
	//    "reservation INSERT sukses tapi spot tidak HELD".
	//
	//    Inside TX:
	//      - CreateTx: partial unique index reject overlap — lihat ADR-0011
	//      - MarkHeldTx: optimistic version lock catch concurrent modify
	if err := uc.TxRunner.RunInTx(ctx, func(tx pgx.Tx) error {
		if err := uc.Reservations.CreateTx(ctx, tx, r); err != nil {
			return err
		}
		return uc.Spots.MarkHeldTx(ctx, tx, spotFresh.ID, spotFresh.Version)
	}); err != nil {
		return nil, err
	}

	// 6. Publish event (best-effort — log error, jangan rollback reservation)
	//
	// NOTE: event publish DI LUAR DB transaction (NATS bukan DB resource).
	// Inconsistency window: kalau commit sukses tapi publish fail, billing
	// tidak tau reservation confirmed → invoice tidak ter-create.
	//
	// Mitigasi current: NATS JetStream durable + ack mode kurangi risk window.
	// Future improvement (RES-OUTBOX): full transactional outbox pattern —
	// INSERT row ke outbox table DALAM tx yang sama (move publish out),
	// background dispatcher polling + publish + mark sent.
	// Pakai pattern: db.RunInTx → INSERT reservation + UPDATE spot + INSERT outbox,
	// dispatcher poll outbox → publish NATS → UPDATE outbox.published_at.
	if err := uc.Events.PublishReservationConfirmed(ctx, r); err != nil {
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
