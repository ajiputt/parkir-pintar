# ADR-0005: NATS JetStream sebagai Event Bus

- **Status**: Accepted
- **Date**: 2026-04-29
- **Tags**: messaging, event-driven

## Context

Service-to-service async communication: Reservation → Billing → Payment → Notification. Butuh broker yang:
- At-least-once delivery dengan ack & redelivery.
- Persistence (replay event untuk projection rebuild).
- Subject hierarchy (mudah filter `reservation.>`, `billing.>`).
- Hemat resource untuk demo budget.

## Options

| Option | Pros | Cons |
|---|---|---|
| **NATS JetStream** | lite (single binary), embed-able, persistent, KV store, fast, low memory | komunitas lebih kecil dari Kafka |
| **Apache Kafka** | industry standard, infinite retention, strong ordering | berat resource (ZK/KRaft + brokers), overkill untuk demo |
| **AWS SNS+SQS** | managed, hemat ops | vendor lock, susah local dev tanpa LocalStack |
| **RabbitMQ** | mature, AMQP, routing flexible | replay sulit, kurang cocok untuk event sourcing |
| **Redis Streams** | sudah ada Redis, simple | retention manual, kurang production-grade |

## Decision

**NATS JetStream** sebagai event bus utama.

## Rationale

1. **Resource efficient**: single binary < 20 MB, < 50 MB RAM untuk demo. Pas untuk Docker Compose + AWS demo budget.
2. **Persistence built-in**: file-based stream, replay-friendly.
3. **Subject-based**: mudah desain hierarchy `reservation.confirmed`, `billing.invoice.issued`, `payment.succeeded`.
4. **Consumer groups (durable)**: setiap subscriber service punya durable consumer dengan ack & redelivery + DLQ.
5. **Kompatibel cloud-agnostic**: deploy di mana saja (AWS, OCP, on-prem).
6. **Operability**: NATS CLI cepat untuk debugging.

## Topology

```
Stream: PARKIRPINTAR
Subjects:
  reservation.confirmed.v1
  reservation.checked_in.v1
  reservation.checked_out.v1
  reservation.cancelled.v1
  reservation.expired.v1
  billing.invoice.issued.v1
  billing.invoice.paid.v1
  payment.succeeded.v1
  payment.failed.v1
  spot.status_changed.v1
  presence.location_updated.v1   # reserved untuk future (lihat ADR-0009)

Storage: file (10 GB max)
Retention: 7 days (untuk debug & replay)
Discard: old
Max msg size: 1 MB
```

Consumer naming (active):
- `billing-reservation-events` — billing svc subscribe ke `reservation.*`
- `payment-billing-events` — payment svc subscribe ke `billing.invoice.issued`
- `notification-all` — notification svc subscribe ke wildcard

Consumer naming (reserved untuk future):
- `search-spot-events` — search svc subscribe untuk update snapshot (deferred — ADR-0009)

## Consequences

**Positif**:
- Decoupled services, async, scalable.
- Event sourcing-friendly (kompetensi #1.5).
- Replay untuk rebuild projection.

**Negatif**:
- At-least-once = consumer harus idempotent (lihat `pkg/idempotency`).
- Komunitas lebih kecil — mitigasi: dokumentasi resmi NATS sangat baik.
