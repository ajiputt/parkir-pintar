//go:build integration
// +build integration

// Integration test: reservation → billing flow.
//
// Pakai testcontainers untuk boot Postgres + Redis + NATS.
// Run: `make test-integration` (butuh Docker daemon).
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestMain bisa di-extend untuk shared setup, tapi kita pakai per-test container
// supaya isolasi maksimal.

func TestReservationToBillingFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pgC, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("parkirpintar"),
		postgres.WithUsername("parkir"),
		postgres.WithPassword("parkir_dev_only"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(2*time.Minute),
		),
	)
	require.NoError(t, err)
	defer func() { _ = pgC.Terminate(ctx) }()

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Bootstrap schema + extensions
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.ExecContext(ctx, `
		CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
		CREATE EXTENSION IF NOT EXISTS "btree_gist";
	`)
	require.NoError(t, err)

	// Apply migrations
	migrationsRoot := findMigrationsRoot(t)
	for _, schema := range []string{"reservation", "billing", "payment"} {
		applyMigrationsFromFile(t, db,
			filepath.Join(migrationsRoot, schema, "001_init.up.sql"))
	}

	// Sanity: 1 area seed.
	// Note: pakai schema-qualified table names (reservation.X) instead of
	// `SET search_path TO X; INSERT INTO Y` karena pgx stdlib via database/sql
	// pakai prepared statement protocol yang reject multi-statement query
	// (SQLSTATE 42601). Schema-qualified = single statement = OK.
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.parking_area (id, name, address, timezone)
		VALUES ('11111111-1111-1111-1111-111111111111','Test Area','-','Asia/Jakarta')
	`)
	require.NoError(t, err)

	floorID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.floor (id, area_id, level)
		VALUES ($1, '11111111-1111-1111-1111-111111111111', 1)
	`, floorID)
	require.NoError(t, err)

	spotID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.spot (id, floor_id, code, vehicle_type)
		VALUES ($1, $2, 'F1-C-001', 'CAR')
	`, spotID, floorID)
	require.NoError(t, err)

	// ----- Test 1: Create reservation
	driverID := "driver-" + uuid.NewString()
	resID := uuid.New()
	startAt := time.Now().Add(time.Minute)
	endAt := startAt.Add(2 * time.Hour)
	expiresAt := time.Now().Add(time.Hour)

	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.reservation (id, driver_id, spot_id, plate_no, vehicle_type,
		    state, start_at, end_at, expires_at, assignment_mode, idempotency_key)
		VALUES ($1,$2,$3,'B 1234 ABC','CAR','CONFIRMED',$4,$5,$6,'SYSTEM','idem-1')
	`, resID, driverID, spotID, startAt, endAt, expiresAt)
	require.NoError(t, err, "create reservation")

	// ----- Test 2: Try double booking — must violate EXCLUDE constraint
	dupID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.reservation (id, driver_id, spot_id, plate_no, vehicle_type,
		    state, start_at, end_at, expires_at, assignment_mode, idempotency_key)
		VALUES ($1,'other-driver',$2,'B 9999 XYZ','CAR','CONFIRMED',$3,$4,$5,'SYSTEM','idem-2')
	`, dupID, spotID, startAt.Add(30*time.Minute), endAt.Add(time.Hour), expiresAt)
	require.Error(t, err, "EXCLUDE constraint must reject overlap")
	require.Contains(t, err.Error(), "no_overlap", "error harus dari EXCLUDE constraint")

	// ----- Test 3: Cancel reservation → spot tersedia kembali
	_, err = db.ExecContext(ctx, `
		UPDATE reservation.reservation SET state='CANCELLED' WHERE id=$1
	`, resID)
	require.NoError(t, err)

	// Sekarang reservation baru di slot yang sama harus berhasil (state berbeda → tidak overlap)
	newID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.reservation (id, driver_id, spot_id, plate_no, vehicle_type,
		    state, start_at, end_at, expires_at, assignment_mode, idempotency_key)
		VALUES ($1,'driver-2',$2,'B 8888 ZZZ','CAR','CONFIRMED',$3,$4,$5,'SYSTEM','idem-3')
	`, newID, spotID, startAt, endAt, expiresAt)
	require.NoError(t, err, "after cancel, new reservation must succeed")

	// ----- Test 4: Idempotency table
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.idempotency_keys (key, request_hash, status, expires_at)
		VALUES ('test-key', 'hash-1', 'COMPLETED', NOW() + INTERVAL '24 hours')
	`)
	require.NoError(t, err)

	// Re-insert same key → ON CONFLICT DO NOTHING (in real code we use this).
	tag, err := db.ExecContext(ctx, `
		INSERT INTO reservation.idempotency_keys (key, request_hash, status, expires_at)
		VALUES ('test-key', 'hash-1', 'COMPLETED', NOW() + INTERVAL '24 hours')
		ON CONFLICT (key) DO NOTHING
	`)
	require.NoError(t, err)
	rows, _ := tag.RowsAffected()
	require.Equal(t, int64(0), rows, "duplicate idempotency key should not insert")

	// ----- Test 5: Billing event log (event sourcing dedup)
	_, err = db.ExecContext(ctx, `
		INSERT INTO billing.events_log (source_event_id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
		VALUES ('evt-1', 'reservation', $1, 'reservation.confirmed.v1', '{}', NOW())
	`, resID.String())
	require.NoError(t, err)

	tag2, err := db.ExecContext(ctx, `
		INSERT INTO billing.events_log (source_event_id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
		VALUES ('evt-1', 'reservation', $1, 'reservation.confirmed.v1', '{}', NOW())
		ON CONFLICT (source_event_id) DO NOTHING
	`, resID.String())
	require.NoError(t, err)
	rows2, _ := tag2.RowsAffected()
	require.Equal(t, int64(0), rows2, "duplicate event id must be skipped")
}

// Helper: apply migrations file
func applyMigrationsFromFile(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	bs, err := os.ReadFile(path)
	require.NoError(t, err, "read migration: %s", path)
	_, err = db.Exec(string(bs))
	require.NoError(t, err, "apply migration: %s", path)
}

// Helper: locate migrations folder dari root project.
func findMigrationsRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	// integration test ada di test/integration → ../..
	for i := 0; i < 5; i++ {
		candidate := filepath.Join(wd, "migrations")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		wd = filepath.Dir(wd)
	}
	t.Fatalf("migrations folder not found")
	return ""
}

// silence unused
var _ = fmt.Sprintf
