package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajiperdana/parkir-pintar/pkg/db"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/usecase"
)

type SpotRepo struct {
	pool *pgxpool.Pool
}

func NewSpotRepo(pool *pgxpool.Pool) *SpotRepo {
	return &SpotRepo{pool: pool}
}

func (r *SpotRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Spot, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT s.id, s.floor_id, f.level, s.code, s.vehicle_type, s.status, s.version
		FROM spot s
		JOIN floor f ON f.id = s.floor_id
		WHERE s.id = $1
	`, id)
	return scanSpot(row)
}

// PickAvailable — atomic: ambil 1 spot AVAILABLE & vehicle_type matching.
//
// Single-area assumption: tidak filter area_id. Kalau ekspansi multi-tenant,
// tambah parameter area_id atau fetch dari context (mis. tenant resolved dari
// JWT di middleware).
//
// Caller harus mark HELD dalam transaksi yang sama (di sini kita pakai dua call
// berturut-turut karena lock akan dilanjut Redis lock di usecase).
func (r *SpotRepo) PickAvailable(ctx context.Context, vt domain.VehicleType) (*domain.Spot, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT s.id, s.floor_id, f.level, s.code, s.vehicle_type, s.status, s.version
		FROM spot s
		JOIN floor f ON f.id = s.floor_id
		WHERE s.vehicle_type = $1
		  AND s.status = 'AVAILABLE'
		ORDER BY f.level ASC, s.code ASC
		LIMIT 1
	`, string(vt))
	return scanSpot(row)
}

// MarkHeld pakai pool (auto-commit). Untuk multi-step atomic, pakai MarkHeldTx.
func (r *SpotRepo) MarkHeld(ctx context.Context, id uuid.UUID, version int) error {
	return r.markHeld(ctx, r.pool, id, version)
}

// MarkHeldTx — Tx variant untuk db.RunInTx callback. Optimistic lock via version.
func (r *SpotRepo) MarkHeldTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, version int) error {
	return r.markHeld(ctx, tx, id, version)
}

func (r *SpotRepo) markHeld(ctx context.Context, q db.Querier, id uuid.UUID, version int) error {
	tag, err := q.Exec(ctx, `
		UPDATE spot SET status='HELD', version=version+1
		WHERE id=$1 AND version=$2
	`, id, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// version mismatch (someone else modified) — treat as contention
		return domain.ErrLockContention
	}
	return nil
}

// MarkAvailable pakai pool.
func (r *SpotRepo) MarkAvailable(ctx context.Context, id uuid.UUID) error {
	return r.markAvailable(ctx, r.pool, id)
}

// MarkAvailableTx — Tx variant.
func (r *SpotRepo) MarkAvailableTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.markAvailable(ctx, tx, id)
}

func (r *SpotRepo) markAvailable(ctx context.Context, q db.Querier, id uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE spot SET status='AVAILABLE', version=version+1 WHERE id=$1`, id)
	return err
}

// MarkOccupied pakai pool.
func (r *SpotRepo) MarkOccupied(ctx context.Context, id uuid.UUID) error {
	return r.markOccupied(ctx, r.pool, id)
}

// MarkOccupiedTx — Tx variant.
func (r *SpotRepo) MarkOccupiedTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.markOccupied(ctx, tx, id)
}

func (r *SpotRepo) markOccupied(ctx context.Context, q db.Querier, id uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE spot SET status='OCCUPIED', version=version+1 WHERE id=$1`, id)
	return err
}

// List — query spot dengan filter optional. Return total count untuk pagination.
//
// Strategy:
//   - Build WHERE clause dynamic dari filter
//   - Two queries: COUNT(*) untuk total, SELECT untuk page data
//   - ORDER BY floor_level, code (deterministic untuk consistent paging)
//
// Optimasi nanti: kalau total dataset besar, drop COUNT (mahal), pakai keyset pagination.
func (r *SpotRepo) List(ctx context.Context, filter usecase.SpotFilter) ([]*domain.Spot, int, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	conditions := []string{"1=1"}
	args := []any{}
	argIdx := 1

	if filter.VehicleType != nil {
		conditions = append(conditions, fmt.Sprintf("s.vehicle_type = $%d", argIdx))
		args = append(args, string(*filter.VehicleType))
		argIdx++
	}
	if filter.Status != nil {
		conditions = append(conditions, fmt.Sprintf("s.status = $%d", argIdx))
		args = append(args, string(*filter.Status))
		argIdx++
	}
	if filter.FloorLevel != nil {
		conditions = append(conditions, fmt.Sprintf("f.level = $%d", argIdx))
		args = append(args, *filter.FloorLevel)
		argIdx++
	}

	where := strings.Join(conditions, " AND ")

	// Total count untuk pagination metadata.
	var total int
	countSQL := `
		SELECT COUNT(*)
		FROM spot s
		JOIN floor f ON f.id = s.floor_id
		WHERE ` + where
	if err := r.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	// Page data.
	dataSQL := `
		SELECT s.id, s.floor_id, f.level, s.code, s.vehicle_type, s.status, s.version
		FROM spot s
		JOIN floor f ON f.id = s.floor_id
		WHERE ` + where + `
		ORDER BY f.level ASC, s.code ASC
		LIMIT $` + fmt.Sprintf("%d", argIdx) + ` OFFSET $` + fmt.Sprintf("%d", argIdx+1)
	args = append(args, limit, filter.Offset)

	rows, err := r.pool.Query(ctx, dataSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]*domain.Spot, 0, limit)
	for rows.Next() {
		s, err := scanSpot(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// GetAvailability — Floor-level rollup count per (floor_level, vehicle_type).
//
// Single-area assumption — return rollup dari semua spot di semua area.
// Untuk single-area deployment ini akan match parking_area "ParkirPintar Main".
func (r *SpotRepo) GetAvailability(ctx context.Context) (*usecase.Availability, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
		    pa.id, pa.name,
		    f.level,
		    f.car_capacity, f.motor_capacity,
		    SUM(CASE WHEN s.vehicle_type='CAR'   AND s.status='AVAILABLE' THEN 1 ELSE 0 END) AS car_av,
		    SUM(CASE WHEN s.vehicle_type='MOTOR' AND s.status='AVAILABLE' THEN 1 ELSE 0 END) AS motor_av
		FROM parking_area pa
		JOIN floor f ON f.area_id = pa.id
		JOIN spot  s ON s.floor_id = f.id
		GROUP BY pa.id, pa.name, f.level, f.car_capacity, f.motor_capacity
		ORDER BY f.level
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := &usecase.Availability{AsOf: time.Now().UTC()}
	for rows.Next() {
		var (
			fa     usecase.FloorAvailability
			areaID uuid.UUID
			name   string
			carAv  int
			motoAv int
		)
		if err := rows.Scan(&areaID, &name, &fa.Level, &fa.CarCapacity, &fa.MotorCapacity, &carAv, &motoAv); err != nil {
			return nil, err
		}
		fa.CarAvailable = carAv
		fa.MotorAvailable = motoAv
		out.ParkingAreaID = areaID
		out.ParkingAreaName = name
		out.Floors = append(out.Floors, fa)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanSpot(s scanner) (*domain.Spot, error) {
	var (
		sp     domain.Spot
		vt, st string
	)
	err := s.Scan(&sp.ID, &sp.FloorID, &sp.FloorLevel, &sp.Code, &vt, &st, &sp.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrSpotNotFound
		}
		return nil, err
	}
	sp.VehicleType = domain.VehicleType(vt)
	sp.Status = domain.SpotStatus(st)
	return &sp, nil
}

// (nullableUUID dihapus — tidak ada lagi area_id parameter setelah refactor.)
var _ = uuid.Nil // keep uuid import alive (dipakai di scanner untuk areaID lokal)
