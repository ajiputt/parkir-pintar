# Testing Strategy — ParkirPintar

## Tujuan

Menjamin **correctness**, **resilience**, dan **performance** dari sistem reservation+billing+payment, dengan automated test pyramid yang terintegrasi di CI/CD.

## Pyramid

```
                ╱╲
               ╱E2E╲           ~5%   docker-compose, REST end-to-end
              ╱------╲
             ╱  INT   ╲        ~15%  testcontainers postgres+redis+nats
            ╱----------╲
           ╱   UNIT     ╲      ~80%  per-package, table-driven, parallel
          ╱--------------╲
```

## Layer 1 — Unit Test

**Ruang lingkup**: pure domain logic, pricing, overlap, idempotency hash, event projection.

**Tooling**:
- Go std `testing`
- `stretchr/testify` untuk assertion.
- `golang/mock` (mockgen) untuk port interface.
- `-race` flag wajib di CI.

**Coverage target**: `pkg/*` ≥ 90%, `internal/domain` ≥ 90%, `internal/usecase` ≥ 85%.

**Contoh skenario** (`pkg/pricing/engine_test.go`):
- 30 menit → 1 jam (5K)
- 1 jam 1 menit → 2 jam (10K)
- 8 jam → 8 jam (40K)
- 23:00–06:00 → 1 jam normal + overnight flat (5K + 20K)
- No-show → booking_fee + penalty (5K + 5K)

## Layer 2 — Integration Test

**Ruang lingkup**: adapter (postgres, redis, nats) + usecase orchestration.

**Tooling**: `testcontainers-go` — boot Postgres 16, Redis 7, NATS JetStream dalam test process. Migration jalan di `TestMain`.

**Skenario**:
- `TestReservationFlow_HappyPath` — create → checkin → checkout → invoice ter-issue dengan jumlah benar.
- `TestDoubleBooking_50Goroutines` — race 50 goroutine, exactly 1 sukses.
- `TestExpiryWorker` — set clock +1h, worker auto-expire.
- `TestIdempotency_RepeatRequest` — 5x request dengan key sama, exactly 1 row.
- `TestEventReplay` — drop projection, replay events, projection identik.

## Layer 3 — End-to-End Test

**Ruang lingkup**: full stack (semua container) via REST.

**Tooling**: Go test + `net/http` client + `make demo-up`.

**Skenario** (sesuai use case requirement):

| # | Skenario | Verifikasi |
|---|---|---|
| 1 | Happy path reservation | 201 + invoice = booking_fee + hourly |
| 2 | Double-book prevention | 2 client paralel, 1 sukses 1 fail dengan 409 |
| 3 | User-selected contention/queue | Redis lock contention, retry success |
| 4 | No-show expiry & spot release | wait expiry → spot kembali AVAILABLE, penalty masuk |
| 5 | Cancellation | state CANCELLED, no billing additional |
| 6 | Extended stay | checkout > end_at, billing pakai actual hours |
| 7 | Overnight fee | reservation 23:00-03:00, invoice = booking + overnight flat + 3 jam |
| 8 | Payment success (QRIS) | webhook dipanggil → invoice PAID |
| 9 | Payment failure | webhook fail → invoice tetap ISSUED, retry possible |

## Layer 4 — Non-Functional

### Performance / Load
- Tool: `k6` di [`test/load/`](../../test/load/)
- Target SLA:
  - P95 `GET /v1/availability` < 100 ms
  - P95 `POST /v1/reservations` < 250 ms
  - 500 RPS sustained selama 5 menit dengan error < 1%

### Security
- `gosec` di CI — gagal kalau finding HIGH.
- `govulncheck` — gagal kalau ada known CVE.
- `trivy` image scan — gagal kalau ada CRITICAL.
- OWASP API Top 10 manual checklist — lihat `docs/security/owasp-checklist.md`.

### Chaos
- `toxiproxy` integration test: simulate 500ms DB latency, packet loss, NATS down.
- Goal: graceful degradation, circuit breaker open, no cascading failure.

## CI Integration

`.github/workflows/ci.yml`:
1. lint (golangci-lint, buf lint)
2. unit test + coverage upload
3. integration test (testcontainers)
4. e2e test (docker-compose up + scripts/e2e)
5. security scan (gosec, govulncheck, trivy)
6. build & push image (kalau merged ke main)

Failure di step manapun → block merge.
