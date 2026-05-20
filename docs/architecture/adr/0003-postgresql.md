# ADR-0003: PostgreSQL sebagai Primary Datastore

- **Status**: Accepted
- **Date**: 2026-04-29
- **Tags**: data, persistence

## Context

Setiap service butuh persistence. Domain reservation punya invariant kuat:
- No double-booking spot pada time-range yang overlap.
- Atomic state transitions (CONFIRMED → CHECKED_IN → CHECKED_OUT).

## Decision

PostgreSQL 16 sebagai datastore default untuk semua service yang butuh durable storage.

## Rationale

**Why PostgreSQL?**

1. **EXCLUDE constraint dengan `gist`** — built-in cara cegah overlap reservation, satu-satunya RDBMS mainstream yang punya ini secara native.
2. **Transactional integrity** — mutlak untuk reservation+billing.
3. **JSONB** — fleksibel untuk `meta` field di invoice_item, payment webhook payload.
4. **Logical replication** — siap untuk read replica & CDC kalau diperlukan future.
5. **Mature Go driver** (`pgx`) dengan connection pooling, prepared statement caching.
6. **Free, open source, well-supported di AWS RDS + Aurora**.

**Per-service isolation**:
- Demo: satu instance, schema terpisah (`reservation`, `billing`, `payment`).
- Production: instance terpisah per service (database-per-service pattern).

**Why bukan NoSQL?**
- MongoDB: overlap detection harus app-side (race risk), tidak ada constraint.
- DynamoDB: vendor lock, query inflexible untuk reporting, transactional limited.
- Redis: bukan durable, dipakai hanya untuk cache & lock.

## Library Stack

| Layer | Pilihan | Alasan |
|---|---|---|
| Driver | `jackc/pgx/v5` | Native postgres, fast, sttn driver-of-choice |
| SQL gen | `sqlc` | Type-safe, generate Go from SQL — no ORM magic |
| Migration | `golang-migrate/migrate` | versioned, rollback, multi-source |
| Pool | `pgxpool` | bawaan pgx, configurable |

**Why bukan ORM?** Spring Boot pakai JPA/Hibernate karena Java verbose. Go idiom: prefer explicit. `sqlc` memberi type safety **tanpa** runtime reflection.

## Consequences

**Positif**:
- Strong consistency untuk reservation.
- Query SQL eksplisit & cepat di-tune.
- Tooling backup/restore mature.

**Negatif**:
- Schema migration discipline penting (mitigasi: golang-migrate + CI check).
- Connection pool sizing harus dipantau (mitigasi: Prometheus metric).
