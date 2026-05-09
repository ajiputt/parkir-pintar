//go:build integration
// +build integration

// Integration test: reservation → billing flow.
//
// Pakai testcontainers untuk boot Postgres. Apply SEMUA migration (001..N)
// per schema supaya state schema match production:
//   - reservation: 001 init → 002 drop end_at + EXCLUDE → 003 one-active-per-driver → 004 payment_mode
//   - billing: 001 init → 002 overdue + payment_mode
//   - payment, notification: 001 init
//
// Run: `make test-integration` (butuh Docker daemon).
package integration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

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

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.ExecContext(ctx, `
		CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
		CREATE EXTENSION IF NOT EXISTS "btree_gist";
	`)
	require.NoError(t, err)

	// Apply SEMUA migration per schema, sorted by filename (numeric prefix
	// urut: 001 → 002 → 003 → 004). Sebelumnya cuma apply 001 yang bikin
	// schema state mismatch dengan production (end_at masih ada, EXCLUDE
	// constraint masih ada, payment_mode belum ada).
	migrationsRoot := findMigrationsRoot(t)
	for _, schema := range []string{"reservation", "billing", "payment", "notification"} {
		applyAllMigrations(t, db, filepath.Join(migrationsRoot, schema))
	}

	// Sanity: 1 area seed.
	// Note: schema-qualified table names (reservation.X) instead of
	// `SET search_path TO X; INSERT INTO Y` karena pgx stdlib via database/sql
	// pakai prepared statement protocol yang reject multi-statement query.
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
	//
	// Schema setelah migration 002 (drop end_at) + 004 (add payment_mode NOT NULL):
	// kolom yang relevan = id, driver_id, spot_id, plate_no, vehicle_type, state,
	// start_at, expires_at, assignment_mode, idempotency_key, payment_mode.
	// (end_at sudah TIDAK ADA, dropped di Path A migration.)
	driverID := "driver-" + uuid.NewString()
	resID := uuid.New()
	startAt := time.Now().Add(time.Minute)
	expiresAt := time.Now().Add(time.Hour)

	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.reservation
			(id, driver_id, spot_id, plate_no, vehicle_type, state,
			 start_at, expires_at,
			 assignment_mode, idempotency_key, payment_mode)
		VALUES ($1, $2, $3, 'B 1234 ABC', 'CAR', 'CONFIRMED',
				$4, $5,
				'SYSTEM', 'idem-1', 'AUTO')
	`, resID, driverID, spotID, startAt, expiresAt)
	require.NoError(t, err, "create reservation")

	// ----- Test 2: Double booking spot yang sama → violates partial unique index
	//
	// Anti-overlap mechanism setelah Path A migration (002): partial unique index
	// `one_active_reservation_per_spot` ON spot_id WHERE state IN ('CONFIRMED','CHECKED_IN'),
	// BUKAN EXCLUDE constraint lagi. Lihat ADR-0011.
	dupID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.reservation
			(id, driver_id, spot_id, plate_no, vehicle_type, state,
			 start_at, expires_at,
			 assignment_mode, idempotency_key, payment_mode)
		VALUES ($1, 'other-driver', $2, 'B 9999 XYZ', 'CAR', 'CONFIRMED',
				$3, $4,
				'SYSTEM', 'idem-2', 'AUTO')
	`, dupID, spotID, startAt.Add(30*time.Minute), expiresAt)
	require.Error(t, err, "partial unique index must reject 2nd active reservation di spot yang sama")
	// SQLSTATE 23505 = unique_violation. Error contains "violates unique constraint".
	// String stable lintas Postgres version dan constraint name.
	require.Contains(t, err.Error(), "violates unique constraint",
		"error harus dari partial unique index (Path A anti-overlap)")

	// ----- Test 3: Cancel reservation → spot bebas (keluar dari partial index)
	_, err = db.ExecContext(ctx, `
		UPDATE reservation.reservation SET state='CANCELLED' WHERE id=$1
	`, resID)
	require.NoError(t, err)

	// Reservation baru dengan driver lain di spot yang sama → harus berhasil
	// karena state lama CANCELLED tidak masuk partial index lagi.
	newID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.reservation
			(id, driver_id, spot_id, plate_no, vehicle_type, state,
			 start_at, expires_at,
			 assignment_mode, idempotency_key, payment_mode)
		VALUES ($1, 'driver-2', $2, 'B 8888 ZZZ', 'CAR', 'CONFIRMED',
				$3, $4,
				'SYSTEM', 'idem-3', 'MANUAL')
	`, newID, spotID, startAt, expiresAt)
	require.NoError(t, err, "after cancel, new reservation di spot yang sama harus berhasil")

	// ----- Test 4: Idempotency table (schema unchanged across migrations)
	_, err = db.ExecContext(ctx, `
		INSERT INTO reservation.idempotency_keys (key, request_hash, status, expires_at)
		VALUES ('test-key', 'hash-1', 'COMPLETED', NOW() + INTERVAL '24 hours')
	`)
	require.NoError(t, err)

	// Re-insert same key → ON CONFLICT DO NOTHING (production code uses this pattern).
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
		VALUES ('evt-1', 'reservation', $1, 'reservation.confirmed.v1', '{}', NOW());
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

// applyAllMigrations applies semua *.up.sql files dalam migrationsDir,
// sorted by filename. Naming convention `NNN_description.up.sql` dengan
// numeric prefix → lexicographic sort = numerical order.
func applyAllMigrations(t *testing.T, db *sql.DB, migrationsDir string) {
	t.Helper()
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err, "read migrations dir: %s", migrationsDir)

	var ups []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)

	for _, f := range ups {
		applyMigrationsFromFile(t, db, filepath.Join(migrationsDir, f))
	}
}

// applyMigrationsFromFile reads migration file dan execute via db.Exec.
// pgx stdlib gunakan simple query protocol untuk Exec tanpa parameters,
// jadi multi-statement SQL di migration file work fine.
func applyMigrationsFromFile(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	bs, err := os.ReadFile(path)
	require.NoError(t, err, "read migration: %s", path)
	_, err = db.Exec(string(bs))
	require.NoError(t, err, "apply migration: %s", path)
}

// findMigrationsRoot — locate migrations folder dari root project.
// Integration test ada di test/integration → ../.. = root.
func findMigrationsRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
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
