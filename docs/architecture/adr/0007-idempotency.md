# ADR-0007: Server-Side Idempotency Key

- **Status**: Accepted
- **Date**: 2026-04-29
- **Tags**: api, reliability

## Context

Operasi yang **tidak boleh dijalankan dua kali**:
- `CreateReservation` — bisa double-book / double-charge booking fee.
- `IssueInvoice` — duplicate line items.
- `CreatePayment` — double-charge user.
- `CheckOut` — double settle billing.

Sumber duplikasi:
- Client retry karena timeout.
- Network glitch (request sukses, response gagal balik).
- Webhook redelivery (Midtrans bisa kirim ulang).

## Decision

Server-side idempotency dengan pola **Stripe / IETF draft-ietf-httpapi-idempotency-key-header**:

1. Client sertakan header `Idempotency-Key: <uuid>` (REST) atau metadata `x-idempotency-key` (gRPC).
2. Server hash request body (`sha256`).
3. Cek tabel `idempotency_keys`:
   - Belum ada → INSERT dengan status `PROCESSING`.
   - Ada, hash sama, status `COMPLETED` → return cached response.
   - Ada, hash sama, status `PROCESSING` → return `409 Conflict` (request masih in-flight).
   - Ada, hash beda → return `422 IdempotencyConflict`.

```sql
CREATE TABLE idempotency_keys (
    key            TEXT PRIMARY KEY,           -- "reservation:create:<client-key>"
    request_hash   TEXT NOT NULL,
    response_status INT,
    response_body  JSONB,
    status         TEXT NOT NULL,              -- 'PROCESSING' | 'COMPLETED' | 'FAILED'
    created_at     TIMESTAMPTZ DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL        -- created_at + 24h
);
CREATE INDEX idempotency_expires_idx ON idempotency_keys(expires_at);
```

Library wrapper di `pkg/idempotency/`.

## Why Server-Side?

- **Client tidak bisa dipercaya** untuk retry-safe.
- Webhook retries dari third party (Midtrans) tidak punya cara lain untuk dedupe.

## Trade-offs

**Positif**:
- Strong duplicate prevention.
- Pattern industri standar (Stripe, AWS).

**Negatif**:
- Storage untuk key — mitigasi: TTL 24h + cleanup job.
- Latency tambahan: 1 SELECT + 1 INSERT — diabaikan vs benefit.

## Validation

Unit test:
- Same key + same body → return cached response, hanya 1 reservation insert.
- Same key + different body → 422 IdempotencyConflict.

E2E test:
- Curl 5x `POST /v1/reservations` dengan same `Idempotency-Key` → exactly 1 reservation di DB.

## API Documentation

Detailed HTTP behavior + client implementation guidance: [docs/api/idempotency.md](../../api/idempotency.md)

Topics covered:
- Request/response examples per scenario (created, replayed, conflict)
- Client best practices (generate key once, persist for crash resilience)
- Server concurrency safety (handling concurrent requests with same key)
- Why Postgres-backed store vs Redis cache

## Related

- [ADR-0011](0011-anti-overlap-strategy.md) — DB-enforced anti double-booking (defense in depth)
- [ADR-0021](0021-saga-pattern.md) — Saga pattern uses idempotency per-step
- `pkg/idempotency/` — package implementation
- `docs/api/idempotency.md` — full HTTP contract documentation
