// Package postgres — adapter implementing usecase ports terhadap PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajiperdana/parkir-pintar/pkg/db"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// pgErrCodeUniqueViolation — Postgres SQLSTATE untuk unique constraint violation
// (partial unique index — lihat ADR-0011).
const pgErrCodeUniqueViolation = "23505"

// Constraint names — sinkron dengan migration 002 & 003.
const (
	idxOneActiveReservationPerSpot   = "one_active_reservation_per_spot"
	idxOneActiveReservationPerDriver = "one_active_reservation_per_driver"
)

// ReservationRepo — implementasi usecase.ReservationRepo.
type ReservationRepo struct {
	pool *pgxpool.Pool
}

func NewReservationRepo(pool *pgxpool.Pool) *ReservationRepo {
	return &ReservationRepo{pool: pool}
}

// Create simpan reservation baru pakai pool (auto-commit single statement).
//
// Pattern: pool-based untuk single-statement context. Untuk multi-step
// operations dalam db.RunInTx, pakai CreateTx variant.
func (r *ReservationRepo) Create(ctx context.Context, res *domain.Reservation) error {
	return r.create(ctx, r.pool, res)
}

// CreateTx — Tx variant. Caller manage tx lifecycle via db.RunInTx.
// Lihat ADR-0024 untuk pattern guidance.
func (r *ReservationRepo) CreateTx(ctx context.Context, tx pgx.Tx, res *domain.Reservation) error {
	return r.create(ctx, tx, res)
}

// create — shared impl, accept db.Querier (Pool atau Tx).
func (r *ReservationRepo) create(ctx context.Context, q db.Querier, res *domain.Reservation) error {
	_, err := q.Exec(ctx, `
		INSERT INTO reservation (
			id, driver_id, spot_id, plate_no, vehicle_type,
			state, start_at, expires_at,
			assignment_mode, payment_mode, idempotency_key, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`,
		res.ID, res.DriverID, res.SpotID, res.PlateNo, string(res.VehicleType),
		string(res.State), res.StartAt, res.ExpiresAt,
		string(res.AssignmentMode), string(res.PaymentMode), res.IdempotencyKey, res.CreatedAt, res.UpdatedAt,
	)
	if err != nil {
		// Map unique violation berdasarkan constraint name yang violated:
		//   - one_active_reservation_per_spot   → ErrSpotUnavailable
		//   - one_active_reservation_per_driver → ErrDriverHasActiveReservation
		// Lihat ADR-0011.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeUniqueViolation {
			switch pgErr.ConstraintName {
			case idxOneActiveReservationPerSpot:
				return domain.ErrSpotUnavailable
			case idxOneActiveReservationPerDriver:
				return domain.ErrDriverHasActiveReservation
			default:
				return domain.ErrSpotUnavailable
			}
		}
		return err
	}
	return nil
}

// FindActiveByDriverID — return reservation aktif (CONFIRMED atau CHECKED_IN) untuk driver.
// Kalau tidak ada, return ErrReservationNotFound (caller bisa errors.Is check).
func (r *ReservationRepo) FindActiveByDriverID(ctx context.Context, driverID string) (*domain.Reservation, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, driver_id, spot_id, plate_no, vehicle_type, state,
		       start_at, expires_at, checkin_at, checkout_at,
		       assignment_mode, COALESCE(payment_mode, 'AUTO'), COALESCE(idempotency_key, ''), created_at, updated_at
		FROM reservation
		WHERE driver_id = $1 AND state IN ('CONFIRMED', 'CHECKED_IN')
		LIMIT 1
	`, driverID)
	return scanReservation(row)
}

func (r *ReservationRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Reservation, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, driver_id, spot_id, plate_no, vehicle_type, state,
		       start_at, expires_at, checkin_at, checkout_at,
		       assignment_mode, COALESCE(payment_mode, 'AUTO'), COALESCE(idempotency_key, ''), created_at, updated_at
		FROM reservation WHERE id = $1
	`, id)
	return scanReservation(row)
}

// UpdateState pakai pool (auto-commit single statement).
func (r *ReservationRepo) UpdateState(ctx context.Context, res *domain.Reservation) error {
	return r.updateState(ctx, r.pool, res)
}

// UpdateStateTx — Tx variant untuk db.RunInTx callback.
func (r *ReservationRepo) UpdateStateTx(ctx context.Context, tx pgx.Tx, res *domain.Reservation) error {
	return r.updateState(ctx, tx, res)
}

// updateState — shared impl, accept db.Querier.
func (r *ReservationRepo) updateState(ctx context.Context, q db.Querier, res *domain.Reservation) error {
	tag, err := q.Exec(ctx, `
		UPDATE reservation
		SET state = $2, checkin_at = $3, checkout_at = $4, updated_at = $5
		WHERE id = $1
	`, res.ID, string(res.State), res.CheckInAt, res.CheckOutAt, res.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrReservationNotFound
	}
	return nil
}

func (r *ReservationRepo) FindExpiredConfirmed(ctx context.Context, now time.Time, limit int) ([]*domain.Reservation, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, driver_id, spot_id, plate_no, vehicle_type, state,
		       start_at, expires_at, checkin_at, checkout_at,
		       assignment_mode, COALESCE(payment_mode, 'AUTO'), COALESCE(idempotency_key, ''), created_at, updated_at
		FROM reservation
		WHERE state = 'CONFIRMED' AND expires_at < $1
		ORDER BY expires_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Reservation
	for rows.Next() {
		res, err := scanReservation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// scanReservation — works for both Row and Rows (both implement Scan).
type scanner interface {
	Scan(dest ...any) error
}

func scanReservation(s scanner) (*domain.Reservation, error) {
	var (
		r              domain.Reservation
		vt, st, am, pm string
		checkIn        *time.Time
		checkOut       *time.Time
	)
	err := s.Scan(
		&r.ID, &r.DriverID, &r.SpotID, &r.PlateNo, &vt, &st,
		&r.StartAt, &r.ExpiresAt, &checkIn, &checkOut,
		&am, &pm, &r.IdempotencyKey, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrReservationNotFound
		}
		return nil, err
	}
	r.VehicleType = domain.VehicleType(vt)
	r.State = domain.State(st)
	r.AssignmentMode = domain.AssignmentMode(am)
	r.PaymentMode = domain.PaymentMode(pm)
	r.CheckInAt = checkIn
	r.CheckOutAt = checkOut
	return &r, nil
}
