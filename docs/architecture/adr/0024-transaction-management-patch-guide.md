# ADR-0024 Implementation — Patch Guide

Step-by-step guide untuk apply transaction refactor secara lokal. Foundational
files (pkg/db/tx.go, pkg/db/querier.go, ADR-0024) sudah landed di repo. Patch
ini fokus ke refactor existing repo + usecase.

Total estimated effort: **2-3 jam** dengan testing.

⚠️ **Setelah selesai semua step, hapus file ini** — ini cuma transient guide.

---

## Step 1 — Update ReservationRepo (add Tx variants)

File: `services/reservation/internal/adapter/postgres/reservation_repo.go`

### Add `CreateTx` method (after existing `Create` method)

```go
// CreateTx — same as Create, but accept pgx.Tx untuk multi-step transaction.
// Caller responsibility: BeginTx + Commit/Rollback via pkg/db.RunInTx.
// Lihat ADR-0024.
func (r *ReservationRepo) CreateTx(ctx context.Context, tx pgx.Tx, res *domain.Reservation) error {
	_, err := tx.Exec(ctx, `
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
```

### Add `UpdateStateTx` method (after existing `UpdateState`)

```go
// UpdateStateTx — same as UpdateState, but accept pgx.Tx untuk transaction.
func (r *ReservationRepo) UpdateStateTx(ctx context.Context, tx pgx.Tx, res *domain.Reservation) error {
	tag, err := tx.Exec(ctx, `
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
```

---

## Step 2 — Update SpotRepo (add Tx variants)

File: `services/reservation/internal/adapter/postgres/spot_repo.go`

### Add `MarkHeldTx` (after `MarkHeld`)

```go
// MarkHeldTx — same as MarkHeld, but accept pgx.Tx untuk transaction.
func (r *SpotRepo) MarkHeldTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, version int) error {
	tag, err := tx.Exec(ctx, `
		UPDATE spot SET status='HELD', version=version+1
		WHERE id=$1 AND version=$2
	`, id, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrLockContention
	}
	return nil
}
```

### Add `MarkAvailableTx` (after `MarkAvailable`)

```go
// MarkAvailableTx — same as MarkAvailable, but accept pgx.Tx.
func (r *SpotRepo) MarkAvailableTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE spot SET status='AVAILABLE', version=version+1 WHERE id=$1`, id)
	return err
}
```

---

## Step 3 — Update usecase ports

File: `services/reservation/internal/usecase/ports.go`

Add new methods to existing interfaces (inside the existing struct):

```go
// ReservationRepo interface — ADD these methods:
type ReservationRepo interface {
	// ... existing methods ...

	// Transactional variants (lihat ADR-0024).
	CreateTx(ctx context.Context, tx pgx.Tx, r *domain.Reservation) error
	UpdateStateTx(ctx context.Context, tx pgx.Tx, r *domain.Reservation) error
}

// SpotRepo interface — ADD these methods:
type SpotRepo interface {
	// ... existing methods ...

	// Transactional variants.
	MarkHeldTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, version int) error
	MarkAvailableTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
}
```

Don't forget to add import:
```go
import "github.com/jackc/pgx/v5"
```

---

## Step 4 — Refactor usecases to use RunInTx

### File: `services/reservation/internal/usecase/create_reservation.go`

Add field to struct:
```go
type CreateReservation struct {
	Reservations  ReservationRepo
	Spots         SpotRepo
	Locker        Locker
	Events        EventPublisher
	Clock         Clock
	HoldDuration  time.Duration
	SpotLockTTL   time.Duration
	OverdueChecker OverdueChecker
	Pool          *pgxpool.Pool  // ← NEW: untuk RunInTx
}
```

Replace step 5-6 di `Execute()`:

**Find this block** (around line 65-76):
```go
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
```

**Replace dengan**:
```go
	// 5-6. Atomic: INSERT reservation + UPDATE spot dalam single tx (ADR-0024).
	// Kalau salah satu gagal, kedua-duanya rollback.
	err = db.RunInTx(ctx, uc.Pool, func(tx pgx.Tx) error {
		// INSERT reservation
		if err := uc.Reservations.CreateTx(ctx, tx, r); err != nil {
			// adapter map unique violation:
			//   - one_active_reservation_per_spot   → ErrSpotUnavailable
			//   - one_active_reservation_per_driver → ErrDriverHasActiveReservation
			return err
		}
		// Mark spot HELD (optimistic lock)
		if err := uc.Spots.MarkHeldTx(ctx, tx, spotFresh.ID, spotFresh.Version); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
```

Add imports:
```go
import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ajiperdana/parkir-pintar/pkg/db"
)
```

### File: `services/reservation/internal/usecase/checkin_checkout.go`

Add Pool field to `CheckOut` struct:
```go
type CheckOut struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
	Pool         *pgxpool.Pool  // ← NEW
}
```

Refactor `CheckOut.Execute`:

**Find this block**:
```go
	if err := uc.Reservations.UpdateState(ctx, r); err != nil {
		return nil, err
	}
	if err := uc.Spots.MarkAvailable(ctx, r.SpotID); err != nil {
		return nil, err
	}
```

**Replace dengan**:
```go
	err = db.RunInTx(ctx, uc.Pool, func(tx pgx.Tx) error {
		if err := uc.Reservations.UpdateStateTx(ctx, tx, r); err != nil {
			return err
		}
		return uc.Spots.MarkAvailableTx(ctx, tx, r.SpotID)
	})
	if err != nil {
		return nil, err
	}
```

Same imports as create_reservation.go.

### File: `services/reservation/internal/usecase/cancel.go` (atau wherever Cancel is defined)

Same pattern. Add Pool field, wrap `UpdateState + MarkAvailable` in `RunInTx`.

---

## Step 5 — Update cmd/main.go to inject Pool

File: `services/reservation/cmd/main.go`

Find usecase wiring (around line 140-150):

**Find**:
```go
	createUC := &usecase.CreateReservation{
		Reservations:   resRepo,
		Spots:          spotRepo,
		Locker:         redisadapter.NewLocker(locker),
		Events:         publisher,
		Clock:          clk,
		HoldDuration:   holdDur,
		SpotLockTTL:    lockTTL,
		OverdueChecker: overdueChecker,
	}
	checkInUC := &usecase.CheckIn{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk}
	checkOutUC := &usecase.CheckOut{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk}
	cancelUC := &usecase.Cancel{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk}
```

**Replace dengan** (just add `Pool: pool,` field):
```go
	createUC := &usecase.CreateReservation{
		Reservations:   resRepo,
		Spots:          spotRepo,
		Locker:         redisadapter.NewLocker(locker),
		Events:         publisher,
		Clock:          clk,
		HoldDuration:   holdDur,
		SpotLockTTL:    lockTTL,
		OverdueChecker: overdueChecker,
		Pool:           pool,  // ← NEW
	}
	checkInUC := &usecase.CheckIn{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk}
	checkOutUC := &usecase.CheckOut{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk, Pool: pool}
	cancelUC := &usecase.Cancel{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk, Pool: pool}
```

---

## Step 6 — Update test fakes

File: `services/reservation/internal/usecase/helpers_test.go`

Add Tx variants to `fakeReservationRepo`:

```go
// Existing Create — keep as is. Add CreateTx that delegates:
func (r *fakeReservationRepo) CreateTx(ctx context.Context, _ pgx.Tx, res *domain.Reservation) error {
	return r.Create(ctx, res)  // ignore tx, delegate to in-memory
}

func (r *fakeReservationRepo) UpdateStateTx(ctx context.Context, _ pgx.Tx, res *domain.Reservation) error {
	return r.UpdateState(ctx, res)
}
```

Same for `fakeSpotRepo`:
```go
func (s *fakeSpotRepo) MarkHeldTx(ctx context.Context, _ pgx.Tx, id uuid.UUID, version int) error {
	return s.MarkHeld(ctx, id, version)
}

func (s *fakeSpotRepo) MarkAvailableTx(ctx context.Context, _ pgx.Tx, id uuid.UUID) error {
	return s.MarkAvailable(ctx, id)
}
```

Add import:
```go
import "github.com/jackc/pgx/v5"
```

### Test setup — Pool can be nil di test

Karena fakes ignore tx parameter, usecase test bisa pass `nil` untuk Pool.
Tapi `db.RunInTx(ctx, nil, fn)` akan panic. Solution: wrap di test setup.

Option A — extract tx-wrapper jadi interface:
```go
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

// Production: pool-based
// Test: pass-through (just call fn(nil))
```

Option B — set `uc.Pool = pool` di integration test, skip RunInTx logic in unit test:
```go
// Add helper field
type CreateReservation struct {
	// ...
	Pool *pgxpool.Pool  // nil di unit test → use noop
}

func (uc *CreateReservation) runInTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if uc.Pool == nil {
		return fn(nil)  // unit test: skip tx
	}
	return db.RunInTx(ctx, uc.Pool, fn)
}
```

**Recommended**: Option B (simpler). Add `runInTx` helper to each usecase that has Pool. Production passes pool, test passes nil.

---

## Step 7 — Add unit tests for RunInTx

File: `pkg/db/tx_test.go` (NEW)

```go
package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/db"
)

// Note: RunInTx needs real Postgres connection. Use testcontainers
// atau skip kalau tidak ada DB_URL env.
func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := getenv("TEST_DB_URL", "postgres://gopark:gopark@localhost:5432/parkirpintar_test?sslmode=disable")
	pool, err := db.Open(context.Background(), dsn, 5, 1)
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func TestRunInTx_CommitsOnSuccess(t *testing.T) {
	pool := setupTestPool(t)
	ctx := context.Background()

	// Setup
	_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS test_tx; CREATE TABLE test_tx (id INT)`)

	err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO test_tx (id) VALUES (1)`)
		return err
	})
	require.NoError(t, err)

	// Verify row exists
	var count int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM test_tx`).Scan(&count)
	assert.Equal(t, 1, count)
}

func TestRunInTx_RollsBackOnError(t *testing.T) {
	pool := setupTestPool(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS test_tx; CREATE TABLE test_tx (id INT)`)

	err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `INSERT INTO test_tx (id) VALUES (1)`)
		return errors.New("simulated failure")
	})
	require.Error(t, err)

	// Verify row NOT inserted
	var count int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM test_tx`).Scan(&count)
	assert.Equal(t, 0, count, "tx should have rolled back")
}

func TestRunInTx_RollsBackOnPanic(t *testing.T) {
	pool := setupTestPool(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS test_tx; CREATE TABLE test_tx (id INT)`)

	require.Panics(t, func() {
		_ = db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
			_, _ = tx.Exec(ctx, `INSERT INTO test_tx (id) VALUES (1)`)
			panic("boom")
		})
	})

	var count int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM test_tx`).Scan(&count)
	assert.Equal(t, 0, count, "tx should rollback on panic")
}

func getenv(k, def string) string {
	// implement or pull from existing test helper
	return def
}
```

---

## Step 8 — Verify

```bash
# Run unit tests
cd services/reservation && go test ./...
cd ../../pkg/db && go test ./...

# Run E2E
make test-e2e

# Verify atomicity (manual integration test)
# Scenario: simulate spot UPDATE fail → reservation NOT inserted
```

---

## Step 9 — Commit

```bash
git add pkg/db/tx.go pkg/db/querier.go pkg/db/tx_test.go \
  docs/architecture/adr/0024-transaction-management.md \
  services/reservation/internal/adapter/postgres/reservation_repo.go \
  services/reservation/internal/adapter/postgres/spot_repo.go \
  services/reservation/internal/usecase/ports.go \
  services/reservation/internal/usecase/create_reservation.go \
  services/reservation/internal/usecase/checkin_checkout.go \
  services/reservation/internal/usecase/cancel.go \
  services/reservation/internal/usecase/helpers_test.go \
  services/reservation/cmd/main.go

git commit -m "feat(reservation): atomic transaction for multi-aggregate ops (ADR-0024)

Address previous assessment feedback: 'Missing @Transactional annotations
on critical operations'.

- Add pkg/db/RunInTx helper — Spring @Transactional equivalent via
  callback pattern. Auto-commit on nil return, rollback on error/panic.
- Add pkg/db/Querier interface — common type for *pgxpool.Pool dan pgx.Tx
- Add Tx-suffixed repo methods: ReservationRepo.CreateTx, UpdateStateTx;
  SpotRepo.MarkHeldTx, MarkAvailableTx
- Refactor 3 critical usecases (CreateReservation, CheckOut, Cancel)
  untuk wrap multi-step DB ops dalam single atomic transaction
- Update test fakes to delegate Tx variants to in-memory store
- Add unit tests untuk RunInTx (commit, rollback, panic recovery)
- ADR-0024 documenting 4-tier transaction strategy + defense-in-depth

Before: INSERT reservation + UPDATE spot di separate auto-commit tx.
Kalau UPDATE gagal, reservation tetap exist tapi spot status stale.

After: Both ops dalam single tx. Rollback automatic kalau salah satu fail."
```

---

## Step 10 — Hapus file ini

```bash
rm docs/architecture/adr/0024-transaction-management-patch-guide.md
git add -A
git commit -m "chore(docs): remove transient patch guide (refactor landed)"
```

---

## Roadmap (Future Work)

Setelah refactor ini landed:

1. **Apply same pattern ke billing** — `IssueInvoice` operation kalau touch
   multiple tables (e.g., invoice + outbox)
2. **Transactional Outbox** — INSERT outbox event dalam tx yang sama dengan
   business state (ADR-0021 roadmap)
3. **Integration test untuk atomicity** — explicit test case yang simulate
   middle-step failure dan verify rollback
4. **Lint rule custom** — golangci-lint custom analyzer untuk detect
   "multi-step write outside RunInTx" (advanced)

---

## Why I provided guide instead of doing edits directly

OneDrive sync delay antara Windows file system dan Linux bash mount kadang
bikin Edit tool baca state outdated. Untuk refactor seukuran ini (8+ files
+ test updates), risk regression > value. Lebih reliable kamu apply manual di
local editor, run `go test ./...` per-step untuk verify, lalu commit confidently.

Foundational files (tx.go, querier.go, ADR-0024) sudah landed safe karena
new files, no merge risk.
