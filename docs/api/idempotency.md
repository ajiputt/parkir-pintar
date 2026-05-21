# Idempotency Strategy

ParkirPintar menggunakan **Idempotency-Key header pattern** untuk mencegah
duplicate execution pada operasi mutasi (create reservation, create payment, mark invoice paid).
Implementasi mengikuti [Stripe's idempotency model](https://stripe.com/docs/api/idempotent_requests).

## Overview

Setiap endpoint POST yang melakukan state mutation memerlukan client kirim
header `Idempotency-Key` dengan UUID v4 unique. Server akan:

1. **Lookup key** di `pkg/idempotency` Postgres-backed store
2. **First request**: process normally, store response + status
3. **Retry dengan key sama**: return cached response (no re-execution)
4. **Retry dengan key sama + payload berbeda**: return `409 Conflict`
   (request hash mismatch)

## Endpoints yang require Idempotency-Key

| HTTP Method | Endpoint | Why idempotent |
|---|---|---|
| `POST` | `/v1/reservations` | Avoid double-booking from network retry |
| `POST` | `/v1/payments` | Avoid double-charge dari user double-click |
| `POST` | `/v1/billing/invoices/{id}/pay` | Avoid mark paid duplicate trigger |

GET requests (read-only) tidak memerlukan key — sudah idempotent by nature.

## Request Format

```http
POST /v1/reservations HTTP/1.1
Host: api.parkirpintar.id
Authorization: Bearer <JWT_TOKEN>
Content-Type: application/json
Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000

{
  "driver_id": "driver-xyz",
  "spot_id": "spot-abc",
  "vehicle_type": "CAR"
}
```

**Constraints**:
- Format: UUID v4 (string, 36 chars including hyphens)
- Lifetime: 24 jam (TTL configured via `RESERVATION_IDEMPOTENCY_TTL` env)
- Scope: per-endpoint (key reuse across different endpoints OK)

## Response Behavior

### First request (200 OK)

```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Idempotency-Status: created

{
  "reservation_id": "res-123",
  "status": "PENDING",
  "expires_at": "2026-05-20T15:30:00Z"
}
```

### Retry dengan key + payload sama (200 OK, cached)

```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Idempotency-Status: replayed

{
  "reservation_id": "res-123",
  "status": "PENDING",
  "expires_at": "2026-05-20T15:30:00Z"
}
```

Response **identik** dengan first request. Server tidak re-execute logic.

### Retry dengan key sama tapi payload berbeda (409 Conflict)

```http
HTTP/1.1 409 Conflict
Content-Type: application/json
X-Idempotency-Status: conflict

{
  "code": "IDEMPOTENCY_CONFLICT",
  "message": "Idempotency-Key reused with different request payload",
  "details": {
    "key": "550e8400-e29b-41d4-a716-446655440000",
    "first_request_hash": "sha256:abc...",
    "current_request_hash": "sha256:xyz..."
  }
}
```

**Why 409 not 200**: melindungi client dari subtle bug (e.g., generate key
once, send 2 different requests with same key). Force client untuk generate
key baru kalau payload berubah.

### Missing Idempotency-Key (400 Bad Request)

```http
HTTP/1.1 400 Bad Request

{
  "code": "MISSING_IDEMPOTENCY_KEY",
  "message": "Idempotency-Key header required for this endpoint"
}
```

## Client Implementation Best Practices

### 1. Generate key once per logical operation

```javascript
// BAD: regenerate key on retry
function createReservation(payload) {
  return fetch('/v1/reservations', {
    headers: { 'Idempotency-Key': crypto.randomUUID() }, // ← generates new key each call
    body: JSON.stringify(payload)
  });
}

// GOOD: generate once, retry with same key
function createReservation(payload) {
  const key = crypto.randomUUID();
  return retry(() => fetch('/v1/reservations', {
    headers: { 'Idempotency-Key': key }, // ← stable across retries
    body: JSON.stringify(payload)
  }));
}
```

### 2. Persist key kalau client crash-resilient

Untuk mobile/web client yang mungkin crash mid-request, simpan key di
local storage sebelum kirim request. Saat app re-open, retry dengan key sama.

### 3. Don't reuse key across different operations

Key per-logical-operation, bukan per-user-session:

```javascript
// BAD: reuse key for different reservations
const userSessionKey = crypto.randomUUID();
createReservation(payload1, userSessionKey); // OK
createReservation(payload2, userSessionKey); // 409 Conflict (payload differs)

// GOOD: new key per reservation attempt
createReservation(payload1, crypto.randomUUID()); // OK
createReservation(payload2, crypto.randomUUID()); // OK
```

## Server Implementation Detail

Implementation di `pkg/idempotency/store.go` interface:

```go
type Store interface {
    // Begin: try register key. existing != nil = sudah ada, found = bool
    Begin(ctx context.Context, key, requestHash string, ttl time.Duration) (existing *Record, found bool, err error)

    // Complete: simpan response untuk key yang in-progress
    Complete(ctx context.Context, key string, status int, body []byte) error

    // Fail: mark key sebagai failed (caller decide cleanup)
    Fail(ctx context.Context, key string) error

    // Get: read record (untuk inspeksi)
    Get(ctx context.Context, key string) (*Record, error)
}
```

Postgres-backed implementation: `pkg/idempotency/postgres.go`. Tabel
`idempotency_records` punya unique constraint pada `(scope, key)` + TTL
column. Background cleanup worker prune expired records tiap 1 jam.

### Concurrency safety

Multiple concurrent requests dengan same key:
- **First request**: insert record dengan status `IN_PROGRESS`
- **Concurrent requests**: detect via `ON CONFLICT DO NOTHING` → block 100ms
  → re-read → return cached response kalau sudah `COMPLETED`
- **Stale `IN_PROGRESS` (> 30s)**: assume original request failed, allow
  re-execution (caller responsibility)

## Testing

Idempotency tested via:
- Unit tests: `pkg/idempotency/store_test.go` — store contract verification
- Integration test: `/test/integration/idempotency_test.go` — concurrent key reuse
- E2E test: `/test/e2e/idempotency.spec.ts` — full HTTP flow dengan retry

## Trade-offs

### Pro

- ✅ Defense vs network retry (mobile flaky connection)
- ✅ Defense vs accidental double-click di UI
- ✅ Audit trail per logical operation
- ✅ Stripe-compatible pattern (familiar untuk integration partners)

### Con

- ❌ Adds Postgres write per request (latency + storage cost)
- ❌ Client must generate UUID (extra dependency)
- ❌ TTL coordination (must outlast longest possible client retry)

### Why not in-memory cache (Redis)?

- **Postgres** more durable: survives Redis flush/restart
- **TTL** managed via DB cleanup worker (single source of truth)
- **Audit trail** preserved across deploys
- **Cost** acceptable untuk operasi mutation (low-frequency vs reads)

## References

- [Stripe Idempotency Docs](https://stripe.com/docs/api/idempotent_requests)
- [RFC 9457 — Problem Details for HTTP APIs](https://www.rfc-editor.org/rfc/rfc9457)
- [ADR-0011](../architecture/adr/0011-postgres-unique-constraints.md) — reservation invariants
- `pkg/idempotency/` — package documentation + source
