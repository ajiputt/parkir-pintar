//go:build integration
// +build integration

// Integration test untuk pkg/db.RunInTx — verify atomic transaction semantics
// dengan real Postgres (testcontainers). Lihat ADR-0024.
//
// Run: `make test-integration` (butuh Docker daemon).
package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ajiperdana/parkir-pintar/pkg/db"
)

// setupTxTestPool — boot fresh Postgres container + return pool dengan test table.
func setupTxTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skip integration in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pgC, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("txtest"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(2*time.Minute),
		),
	)
	require.NoError(t, err)

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := db.Open(context.Background(), dsn, 5, 1)
	require.NoError(t, err)

	// Create test table — fresh per test.
	_, err = pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS test_tx (
			id   INT PRIMARY KEY,
			data TEXT NOT NULL
		)
	`)
	require.NoError(t, err)

	teardown := func() {
		pool.Close()
		_ = pgC.Terminate(context.Background())
	}
	return pool, teardown
}

// countRows — helper untuk verify state setelah tx.
func countRows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM test_tx`).Scan(&n)
	require.NoError(t, err)
	return n
}

// ---- Test cases ----

// TestRunInTx_CommitsOnSuccess — fn return nil → all writes persisted.
func TestRunInTx_CommitsOnSuccess(t *testing.T) {
	pool, teardown := setupTxTestPool(t)
	defer teardown()
	ctx := context.Background()

	err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (1, 'first')`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (2, 'second')`)
		return err
	})

	require.NoError(t, err)
	assert.Equal(t, 2, countRows(t, pool), "both INSERTs should be committed")
}

// TestRunInTx_RollsBackOnError — fn return error → no writes persisted.
func TestRunInTx_RollsBackOnError(t *testing.T) {
	pool, teardown := setupTxTestPool(t)
	defer teardown()
	ctx := context.Background()

	sentinelErr := errors.New("business logic failure")

	err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (10, 'persisted-before-fail')`)
		if err != nil {
			return err
		}
		// Simulate business logic error — semua write sebelumnya harus rollback.
		return sentinelErr
	})

	require.ErrorIs(t, err, sentinelErr, "error should propagate")
	assert.Equal(t, 0, countRows(t, pool), "INSERT before error should be rolled back")
}

// TestRunInTx_RollsBackOnPanic — fn panic → rollback + re-throw panic.
//
// Critical safety property: defer Rollback catch panic even kalau Commit
// belum dijalankan. Caller dapat panic propagated up untuk handle/log.
func TestRunInTx_RollsBackOnPanic(t *testing.T) {
	pool, teardown := setupTxTestPool(t)
	defer teardown()
	ctx := context.Background()

	require.Panics(t, func() {
		_ = db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
			_, _ = tx.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (20, 'will-be-rolled-back')`)
			panic("simulated panic in business logic")
		})
	})

	assert.Equal(t, 0, countRows(t, pool), "tx should rollback on panic")
}

// TestRunInTx_PropagatesContextCancel — ctx cancellation aborts tx.
func TestRunInTx_PropagatesContextCancel(t *testing.T) {
	pool, teardown := setupTxTestPool(t)
	defer teardown()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel — BeginTx should fail

	err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		t.Fatal("fn should not be called when ctx cancelled before BeginTx")
		return nil
	})

	require.Error(t, err, "BeginTx on cancelled ctx should fail")
	assert.Equal(t, 0, countRows(t, pool))
}

// TestRunInTx_NestedTxNotSupported — verify behavior kalau caller mencoba
// nest RunInTx (current implementation tidak support savepoint).
//
// Pgx allows BeginTx during active tx connection but creates separate tx,
// not nested. Inner commits independently. Outer rollback won't affect.
//
// Documentation: "Tidak support nested transaction (savepoint). Kalau
// dipanggil dari dalam tx, caller responsibility untuk pass tx existing instead."
func TestRunInTx_DoesNotProvideSavepoint(t *testing.T) {
	pool, teardown := setupTxTestPool(t)
	defer teardown()
	ctx := context.Background()

	// Outer tx insert row 1, then nested RunInTx insert row 2, then outer rollback.
	// Expected: row 2 inserted (separate tx, committed independently),
	// row 1 NOT inserted (outer rolled back).
	_ = db.RunInTx(ctx, pool, func(outerTx pgx.Tx) error {
		_, _ = outerTx.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (30, 'outer')`)

		// Nested call — gets fresh tx via pool, not nested savepoint.
		nestedErr := db.RunInTx(ctx, pool, func(innerTx pgx.Tx) error {
			_, _ = innerTx.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (31, 'inner')`)
			return nil
		})
		if nestedErr != nil {
			return nestedErr
		}

		// Outer fails → outer rollback.
		return errors.New("outer fails")
	})

	// Verify: row 31 committed (inner tx independent), row 30 rolled back.
	var id30Found, id31Found bool
	rows, _ := pool.Query(ctx, `SELECT id FROM test_tx ORDER BY id`)
	defer rows.Close()
	for rows.Next() {
		var id int
		_ = rows.Scan(&id)
		switch id {
		case 30:
			id30Found = true
		case 31:
			id31Found = true
		}
	}
	assert.False(t, id30Found, "outer tx should rollback row 30")
	assert.True(t, id31Found, "nested call uses separate tx, row 31 committed")
}

// TestRunInTxWithOptions_RespectsIsolation — verify Serializable isolation
// detected serialization failure (rare condition, harder to test deterministically).
//
// Smoke test: pass Serializable option, run simple INSERT, verify works.
func TestRunInTxWithOptions_Serializable(t *testing.T) {
	pool, teardown := setupTxTestPool(t)
	defer teardown()
	ctx := context.Background()

	err := db.RunInTxWithOptions(ctx, pool, pgx.TxOptions{
		IsoLevel:   pgx.Serializable,
		AccessMode: pgx.ReadWrite,
	}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (40, 'serializable')`)
		return err
	})

	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, pool))
}

// TestRunInTxWithOptions_ReadOnly — verify read-only tx reject writes.
func TestRunInTxWithOptions_ReadOnlyRejectsWrite(t *testing.T) {
	pool, teardown := setupTxTestPool(t)
	defer teardown()
	ctx := context.Background()

	// Pre-populate untuk read test
	_, err := pool.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (50, 'preexisting')`)
	require.NoError(t, err)

	err = db.RunInTxWithOptions(ctx, pool, pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		// Read should succeed
		var data string
		err := tx.QueryRow(ctx, `SELECT data FROM test_tx WHERE id=50`).Scan(&data)
		require.NoError(t, err)
		require.Equal(t, "preexisting", data)

		// Write should fail
		_, err = tx.Exec(ctx, `INSERT INTO test_tx (id, data) VALUES (51, 'should-fail')`)
		return err
	})

	require.Error(t, err, "write di read-only tx harus fail")
	assert.Contains(t, err.Error(), "read-only", "error message should mention read-only")
}
