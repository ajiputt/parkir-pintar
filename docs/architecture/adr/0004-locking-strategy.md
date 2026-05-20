# ADR-0004: Three-Layer Locking untuk Cegah Double-Booking

- **Status**: Accepted
- **Date**: 2026-04-29
- **Tags**: concurrency, consistency, defense-in-depth

## Context

Skenario use case yang harus dijamin:
- **Concurrent system-assigned**: dua driver request hampir bersamaan, sistem assign spot — harus tidak boleh sama.
- **User-selected contention**: dua driver klik spot yang sama dalam window 100ms.
- **Defense in depth**: jika ada bug app-level, DB harus tetap reject.

## Decision

Tiga lapis lock yang **complementary**, tidak duplicate:

### Layer 1 — Application Lock (Redis Redlock)

```go
ok, err := locker.Acquire(ctx, "lock:spot:"+spotID, 10*time.Second)
defer locker.Release(ctx, "lock:spot:"+spotID)
```

- **Tujuan**: fast-fail saat user-selected contention (TTL pendek).
- **Algoritma**: Redlock — single-instance Redis cukup untuk demo, multi-instance Redis untuk production HA.
- **Library**: `github.com/go-redsync/redsync/v4`.
- **Failure mode**: Redis down → fallback ke DB lock (degraded mode, ada metric `lock_fallback_total`).

### Layer 2 — Database Row Lock (`SELECT … FOR UPDATE`)

```sql
SELECT id, status, version
FROM spot
WHERE id = $1
FOR UPDATE NOWAIT;   -- fail fast jika sudah dipegang
```

- **Tujuan**: serialize akses ke row spot dalam transaksi.
- **NOWAIT**: cegah deadlock; client retry dengan exponential backoff jika gagal.

### Layer 3 — PostgreSQL EXCLUDE Constraint (Defense)

```sql
ALTER TABLE reservations
ADD CONSTRAINT no_overlap
EXCLUDE USING gist (
    spot_id WITH =,
    tstzrange(start_at, end_at, '[)') WITH &&
)
WHERE (state IN ('CONFIRMED','CHECKED_IN'));
```

- **Tujuan**: data-level guarantee — **mustahil** menyimpan dua reservation overlap untuk spot yang sama.
- **Kekuatan**: bekerja **walaupun** Layer 1 & 2 gagal/dilewati (mis. INSERT manual oleh DBA).

## Rationale

Mengapa tidak cukup satu layer?

| Layer saja | Risiko |
|---|---|
| Hanya Redis | Redis fail = double-book; kompleksitas Redlock dispute. |
| Hanya FOR UPDATE | Lock acquired = block lain; kalau crash sebelum commit, butuh tx timeout. |
| Hanya EXCLUDE | Kena di insert (late) — UX buruk: validation muncul setelah long pipeline. Juga tidak proteksi *update* race. |

**Three-layer**:
- Redis = UX-fast fail (50ms) untuk kontensi user-selected.
- Row lock = transaksi safety dalam DB.
- EXCLUDE = ultimate guarantee.

## Consequences

**Positif**:
- Mathematical guarantee no double-booking.
- Performance: kontensi rendah → Redis fast path.
- Demonstrate kompetensi distributed programming + integritas data (#2.2).

**Negatif**:
- Operational complexity: 3 mekanisme harus monitored.
- Dev harus tahu kapan pakai yang mana — mitigasi: encapsulate dalam `pkg/lock` + ReservationRepo, dev tinggal call `repo.CreateReservation(ctx, ...)`.

## Validation

Integration test `TestDoubleBooking_ConcurrentReserve`:
- Spawn 50 goroutine race ke `CreateReservation` spot yang sama.
- Assert: tepat 1 sukses, 49 gagal dengan error `ErrSpotUnavailable` atau `ErrConflict`.
- Run dengan `-race` flag.
