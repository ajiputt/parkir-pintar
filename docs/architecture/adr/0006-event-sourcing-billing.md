# ADR-0006: Event Sourcing untuk Billing Service

- **Status**: Accepted
- **Date**: 2026-04-29
- **Tags**: event-sourcing, audit, billing

## Context

Billing punya kompleksitas:
- Banyak input event (`ReservationConfirmed`, `CheckedIn`, `CheckedOut`, `Expired`, `PaymentSucceeded`).
- Dispute & audit trail penting (uang!).
- Pricing rule bisa berubah → perlu rebuild invoice dari history.

## Decision

Pakai **Event Sourcing (lite)** untuk billing service:
- Append-only `events_log` table per aggregate (invoice).
- Projection: `invoices` + `invoice_items` di-derive dari events.
- Snapshot: tidak perlu (volume rendah, < 1000 invoice/jam).

> Catatan: ini "event sourcing lite" — kita pakai NATS sebagai *transport* event, dan `events_log` sebagai *local store* di service billing. Bukan event sourcing fundamentalist (yang menyimpan SEMUA event di store sebelum publish).

## Schema

```sql
CREATE TABLE billing.events_log (
    seq         BIGSERIAL PRIMARY KEY,
    aggregate_type TEXT NOT NULL,             -- 'invoice'
    aggregate_id TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    payload     JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX events_aggregate_idx ON billing.events_log(aggregate_type, aggregate_id, seq);
```

Event handler pattern:

```go
func (s *Service) Handle(ctx, env eventbus.Envelope) error {
    // 1. dedupe by (aggregate_id, source_event_id) — idempotent consumer
    if seen, _ := s.dedup.Seen(ctx, env.ID); seen {
        return nil
    }

    // 2. begin tx
    tx, _ := s.db.Begin(ctx)
    defer tx.Rollback(ctx)

    // 3. append event
    appendEvent(tx, env)

    // 4. apply to projection
    project(tx, env)

    // 5. commit + ack
    tx.Commit(ctx)
    s.dedup.Mark(ctx, env.ID)
    return nil
}
```

## Rationale

1. **Audit & dispute**: setiap perubahan invoice traceable ke event sumber.
2. **Replay**: kalau pricing rule ada bug, kita bisa rebuild semua invoice dari `events_log`.
3. **Decoupling**: Billing tidak perlu polling Reservation — event-driven.
4. **Kompetensi**: #1.5 (event sourcing), #2.3 (event-based data flow), #2.5 (audit & integritas).

## Trade-offs

**Positif**:
- Audit complete.
- Rebuild flexibility.
- Decouple dari source service.

**Negatif**:
- Storage growth — mitigasi: archive event > 90 hari ke S3 (Glacier).
- Eventual consistency: invoice mungkin lag 1-2 detik dari reservation. Dapat ditoleransi.
- Replay rule butuh versioning event — mitigasi: `event_type` diakhiri `.v1`, `.v2`.

## Validation

- Test: `TestRebuildInvoiceFromEvents` — drop projection, replay all events, expect identik.
- Metric: `billing_event_lag_seconds` (received_at - occurred_at).
