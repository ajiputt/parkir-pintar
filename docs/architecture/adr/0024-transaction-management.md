# ADR-0024: Transaction Management Strategy

| Status | Date | Decider | Supersedes |
|---|---|---|---|
| Accepted | 2026-05-21 | Aji P. | — |

## Context

Use case ParkirPintar punya **multi-step business operations** yang melibatkan
multiple DB writes:

1. **CreateReservation**: INSERT reservation + UPDATE spot.status='HELD'
2. **CheckOut**: UPDATE reservation.state='CHECKED_OUT' + UPDATE spot.status='AVAILABLE'
3. **Cancel**: UPDATE reservation.state='CANCELLED' + UPDATE spot.status='AVAILABLE'
4. **IssueInvoice**: INSERT invoice + INSERT invoice_item rows

Previous assessment feedback eksplisit:
> "Transaction Management Gaps — Missing `@Transactional` annotations on
> critical operations"
> "No distributed transaction handling (Saga pattern)"
> "Redis operations outside transaction boundaries"

Di Spring, `@Transactional` di service method otomatis wrap semua DB calls
dalam single transaction, rollback kalau exception. Equivalent di Go tidak
ada built-in (Go pakai eksplisit `db.Begin/Commit/Rollback`).

Pertanyaan: **bagaimana enforce atomicity untuk multi-step operations tanpa
boilerplate berlebihan?**

## Decision

**Adopt 4-tier transaction management strategy**, sesuai kompleksitas:

### Tier 1: Single-statement (default)

Operations dengan **satu** INSERT/UPDATE/DELETE — Postgres atomic by default.
Tidak perlu explicit `BEGIN/COMMIT`:

```go
// Atomic by default — single statement
_, err := pool.Exec(ctx, `UPDATE reservation SET state='CANCELLED' WHERE id=$1`)
```

Coverage: GetByID, FindBy*, UpdateState (single column), simple INSERT.

### Tier 2: Multi-statement DI REPOSITORY (current strength)

Operations yang touch multiple rows di **satu** aggregate — explicit
`pgx.BeginTx` dalam repo method:

```go
func (r *InvoiceRepo) Save(ctx context.Context, inv *Invoice) error {
    tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
    defer tx.Rollback(ctx)

    tx.Exec(ctx, "INSERT INTO invoice ...")
    tx.Exec(ctx, "DELETE FROM invoice_item WHERE invoice_id=$1")
    for _, item := range inv.Items {
        tx.Exec(ctx, "INSERT INTO invoice_item ...")
    }
    return tx.Commit(ctx)
}
```

Coverage: BillingRepo.Save (invoice + items), Reservation expiry worker
(SELECT FOR UPDATE SKIP LOCKED + UPDATE batch).

### Tier 3: Multi-aggregate USECASE-level (NEW — addresses gap)

Operations yang touch **multiple aggregates** (mis. reservation + spot)
diperlukan atomic — pakai `pkg/db.RunInTx` helper sebagai Spring-equivalent:

```go
// pkg/db/tx.go
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error

// Usage di usecase:
err := db.RunInTx(ctx, uc.Pool, func(tx pgx.Tx) error {
    // Both inserts/updates atomic
    if err := uc.Reservations.CreateTx(ctx, tx, r); err != nil { return err }
    if err := uc.Spots.MarkHeldTx(ctx, tx, spotID, version); err != nil { return err }
    return nil // commit
})
// fn return error → automatic rollback
// fn panic → automatic rollback + re-throw
```

Coverage: CreateReservation, CheckOut, Cancel (3 critical flows).

### Tier 4: Distributed (cross-service) — Choreography Saga

Operations yang touch **multiple services** (e.g., reservation + billing)
TIDAK pakai DB transaction (2PC anti-pattern). Pakai **Choreography Saga**
via NATS events dengan compensation (lihat [ADR-0021](0021-saga-pattern.md)).

Sample: reservation.confirmed → billing.invoice_issued → payment.charge.
Setiap step idempotent + compensation flow defined.

## Rationale

### Why NOT one-size-fits-all transaction wrapper

Tempting untuk wrap SEMUA usecase di `@Transactional`-style, tapi:

1. **Locking overhead**: Read-heavy queries tidak butuh tx, akan unnecessarily
   hold locks
2. **Connection pool pressure**: Long-running tx hold connection → connection
   pool saturation faster
3. **Cross-service ops** can't be wrapped di single DB tx (different DB schemas)
4. **Idempotency** needs separate concern (di event handler, di-handle by
   event_log dedup ADR-0007)

Tiered approach = right tool for right context.

### Why `pkg/db.RunInTx` callback pattern

Alternative considered:

**Option A — Pass `pgx.Tx` explicit through ports:**
```go
type ReservationRepo interface {
    Create(ctx context.Context, tx pgx.Tx, r *Reservation) error
}
```
Verbose. Caller harus selalu manage tx lifecycle.

**Option B — Context-based tx (Spring-like magic):**
```go
ctx = db.WithTx(ctx, tx)
repo.Create(ctx, r)  // auto-detect tx from ctx
```
Magic (good Spring DX, bad Go ergonomics). Hard to debug "wait kenapa ini commit duluan?".

**Option C — Callback higher-order function (CHOSEN):**
```go
db.RunInTx(ctx, pool, func(tx pgx.Tx) error { ... })
```
- Explicit lifecycle (vs option B)
- Less boilerplate per call (vs option A)
- Compose-able (caller decide what wraps)
- Standard Go pattern (mirror sql.Conn.WithinTransaction in stdlib variants)

### Why Tx-suffixed methods (CreateTx, MarkHeldTx)

Repo punya 2 variant per critical method:

| Method | When | Caller |
|---|---|---|
| `Create(ctx, ...)` | Single atomic call | Anywhere |
| `CreateTx(ctx, tx, ...)` | Dalam transaction (multi-step) | `RunInTx` callback |

Pros:
- ✅ Backward compatible (existing callers tidak break)
- ✅ Clear signature (no hidden context-based magic)
- ✅ Each method satu purpose, easy to test

Cons:
- ❌ Code duplication (single statement repeated)
- ❌ Mitigation: extract shared SQL ke private `createWith(querier Querier, ...)` helper

### Why NOT context-injected tx (Spring magic)

Spring's `@Transactional` works via runtime proxy + thread-local. Go ga punya
thread-local (goroutine-safe context.Context is alternative). Pattern would
work but:
- Implicit behavior harder to reason about
- Easy to forget tx propagation di goroutine spawn
- Debugging harder (which call started tx?)

Explicit > implicit. Go community lean toward explicit (Effective Go).

## Implementation Detail

### `pkg/db/tx.go` — RunInTx helper

```go
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) (err error) {
    tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil { return err }

    defer func() {
        if p := recover(); p != nil {
            _ = tx.Rollback(ctx)
            panic(p)
        }
        if err != nil {
            _ = tx.Rollback(ctx)
        }
    }()

    if err = fn(tx); err != nil { return err }
    return tx.Commit(ctx)
}
```

Plus `RunInTxWithOptions` variant untuk Serializable / ReadOnly tx.

### Tx-suffixed repo methods

Tiap critical repo method punya pair:

```go
// services/reservation/internal/adapter/postgres/reservation_repo.go

// Pool-based (single-statement context)
func (r *ReservationRepo) Create(ctx context.Context, res *Reservation) error {
    return r.create(ctx, r.pool, res)
}

// Tx-based (multi-step context)
func (r *ReservationRepo) CreateTx(ctx context.Context, tx pgx.Tx, res *Reservation) error {
    return r.create(ctx, tx, res)
}

// Shared implementation
func (r *ReservationRepo) create(ctx context.Context, q Querier, res *Reservation) error {
    _, err := q.Exec(ctx, "INSERT INTO reservation ...")
    return mapUniqueViolation(err)
}

// Querier — common interface (pgxpool.Pool dan pgx.Tx both implement this)
type Querier interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
```

### Usecase refactor — CreateReservation example

**Before**:
```go
// Step 5: INSERT reservation (auto-commit)
if err := uc.Reservations.Create(ctx, r); err != nil { return nil, err }

// Step 6: UPDATE spot (separate auto-commit — INCONSISTENT kalau gagal)
if err := uc.Spots.MarkHeld(ctx, spotID, version); err != nil { return nil, err }
```

**After**:
```go
// Step 5-6: atomic (single tx — rollback both kalau salah satu gagal)
err := db.RunInTx(ctx, uc.Pool, func(tx pgx.Tx) error {
    if err := uc.Reservations.CreateTx(ctx, tx, r); err != nil { return err }
    if err := uc.Spots.MarkHeldTx(ctx, tx, spotID, version); err != nil { return err }
    return nil
})
if err != nil { return nil, err }
```

Usecase struct gain `Pool *pgxpool.Pool` field, injected di `cmd/main.go`.

## Operations Coverage

Setelah refactor:

| Operation | Tier | Atomic? |
|---|---|---|
| CreateReservation (INSERT reservation + UPDATE spot) | Tier 3 | ✅ Yes |
| CheckOut (UPDATE reservation + UPDATE spot) | Tier 3 | ✅ Yes |
| Cancel (UPDATE reservation + UPDATE spot) | Tier 3 | ✅ Yes |
| Save Invoice (INSERT invoice + items) | Tier 2 | ✅ Yes |
| Expiry Worker (SELECT FOR UPDATE + UPDATE batch) | Tier 2 | ✅ Yes |
| Get* (read queries) | Tier 1 | n/a (read-only) |
| UpdateState single column | Tier 1 | ✅ Yes |
| Cross-service workflow (saga) | Tier 4 | ✅ Choreography + idempotent handlers |

## Consequences

### Positive

- ✅ **Address "Missing @Transactional" feedback** — multi-step ops atomic
- ✅ **Explicit lifecycle** — no implicit magic, easy to reason about
- ✅ **Tiered approach** — right cost for right complexity
- ✅ **Spring-equivalent ergonomics** — `RunInTx(func(){})` mirror Spring DX
- ✅ **Panic-safe** — defer Rollback catch panic propagation
- ✅ **Compose-able** — caller can mix tx ops with non-tx (e.g., Redis lock outside tx)

### Negative

- ❌ **Method duplication** — `Create` + `CreateTx` per critical repo method.
  Mitigation: shared private `createWith` helper using `Querier` interface
- ❌ **Usecase struct gain `Pool` dependency** — slight port leak (hexagonal violation).
  Mitigation: justified untuk transaction concern, accepted trade-off
- ❌ **Test fakes need update** — new `*Tx` methods must be implemented.
  Mitigation: fakes simple (ignore tx param, use in-memory map directly)
- ❌ **No automatic nested transaction support** — caller responsibility kalau
  perlu savepoint. Mitigation: rare case di codebase

### Risks

| Risk | Mitigation |
|---|---|
| Forgetting `Tx` suffix → silent non-atomic | Code review checklist + lint rule (future) |
| Long-running tx hold connection | Set `cfg.MaxConnLifetime` + tx timeout via context |
| Redis lock outside tx → race | Acquire Redis lock BEFORE `RunInTx`, release AFTER |
| Saga compensation logic incomplete | E2E test untuk every cancel path (existing) |

## Defense-in-Depth Layers

ParkirPintar's data consistency strategy is **multi-layered**, not relying
solely on transaction:

```
Layer 1: Application validation (input check)
Layer 2: Optimistic locking (version column di Spots)
Layer 3: Redis distributed lock (cross-process, short TTL)
Layer 4: DB transaction (RunInTx — THIS ADR)
Layer 5: Postgres unique constraint (partial index — ADR-0011)
Layer 6: Saga compensation (rollback via event — ADR-0021)
```

Setiap layer = independent defense. Bug di layer N caught by layer N+1.
Example untuk anti double-booking:
- L2 fails (version mismatch) → retry with fresh data
- L3 fails (lock contention) → return 409
- L4 fails (tx rollback) → no partial state
- L5 fails (constraint violation) → DB reject
- L6 fails (event loss) → reconciliation worker eventually consistent

## Validation

### Unit tests

`pkg/db/tx_test.go` (TO BE ADDED):
- RunInTx commits on nil return
- RunInTx rolls back on error return
- RunInTx rolls back on panic + re-throws
- RunInTxWithOptions respects isolation level

### Integration tests

`test/integration/transaction_test.go` (TO BE ADDED):
- CreateReservation: simulate spot UPDATE fail → reservation NOT inserted
- CheckOut: simulate spot UPDATE fail → reservation state UNCHANGED
- Concurrent CreateReservation same spot: 1 succeeds, others get 409

### E2E tests

Existing `test/e2e/double-book.spec.ts` already covers atomicity at user level.

## Migration Path (Existing → New)

For backward compatibility:
1. Keep existing `Create()`, `MarkHeld()`, `MarkAvailable()`, `UpdateState()` (single-statement context)
2. Add new `CreateTx()`, `MarkHeldTx()`, `MarkAvailableTx()`, `UpdateStateTx()`
3. Refactor 3 critical usecases (CreateReservation, CheckOut, Cancel) untuk pakai `RunInTx`
4. Update test fakes untuk implement new methods (simple in-memory delegate)

Other usecases tetap pakai existing methods (no change).

## Future Improvements

### Transactional outbox (ADR-0021 roadmap)

Untuk Tier 4 saga, ideal pattern: INSERT outbox event di tx yang sama dengan
business state. Worker poll outbox → publish NATS → mark sent. Ensures
**event publishing + DB write atomic**.

Status: planned for M2 milestone, not in current scope.

### Read-only tx untuk reports

Long-running read queries (e.g., admin dashboard aggregation) bisa pakai
`RunInTxWithOptions(ctx, pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, ...)`
untuk Postgres optimization (skip MVCC overhead).

### Distributed tracing per tx

OTel span wrap `RunInTx` invocation:
```go
ctx, span := tracer.Start(ctx, "db.transaction")
defer span.End()
return db.RunInTx(ctx, pool, fn)
```

Future improvement untuk better trace correlation.

## References

- [Effective Go — Errors](https://go.dev/doc/effective_go#errors)
- [pgx documentation — Transaction](https://pkg.go.dev/github.com/jackc/pgx/v5#Tx)
- [Spring @Transactional](https://docs.spring.io/spring-framework/reference/data-access/transaction/declarative.html) (reference equivalent)
- Newman, Sam (2021). *Building Microservices*, 2nd ed. — Chapter 6
- [Transactional Outbox Pattern](https://microservices.io/patterns/data/transactional-outbox.html)

## Related ADRs

- [ADR-0004](0004-locking-strategy.md) — Three-layer locking
- [ADR-0007](0007-idempotency.md) — Idempotency key
- [ADR-0011](0011-anti-overlap-strategy.md) — Partial unique index (Layer 5)
- [ADR-0021](0021-saga-pattern.md) — Cross-service saga (Tier 4)
- [pkg/db/tx.go](../../../pkg/db/tx.go) — Implementation
