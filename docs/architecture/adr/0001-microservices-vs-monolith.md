# ADR-0001: Microservices vs Modular Monolith

- **Status**: Accepted (di-superseded sebagian oleh [ADR-0009](0009-defer-search-presence-services.md))
- **Date**: 2026-04-29
- **Decision-makers**: Backend team (Aji)
- **Tags**: architecture, deployment

> **Catatan revisi**: Decision awal di ADR ini ("Option D — full split + skeleton"). Setelah review post-implementation, `search` & `presence` di-defer (proto-only) per [ADR-0009](0009-defer-search-presence-services.md). Lihat *Decision* section di bawah.

## Context

Use case ParkirPintar **mensyaratkan** arsitektur microservices (gateway, search, reservation, billing, payment, presence, notification) dengan komunikasi gRPC over HTTP/2. Namun use case juga membolehkan *merge some if you justify it*.

Sebagai engineer kita harus eksplisit memilih: **strict 7-service split**, **modular monolith**, atau **hybrid (some merged)**?

## Forces

| Force | Direction |
|---|---|
| Skala traffic real | rendah-medium (single area, demo budget) |
| Tim ukuran | 1 (assessment) → real-world: 5-10 |
| Kompleksitas domain | medium (reservation+billing punya invariant kuat) |
| Deployment fleksibilitas | harus bisa Docker Compose, K8s/OCP, AWS |
| Demo ops cost | budget terbatas |
| Time to ship | 1 sprint untuk demo |
| Asesmen | menilai design distributed systems |

## Options

### Option A — Strict 7 microservices

✅ Maksimal demo kompetensi distributed systems, sesuai use case literal.
✅ Independent deployment, scale, dan fault isolation.
❌ Operational overhead tinggi: 7 container × (logs, metrics, traces, secrets).
❌ Distributed tracing complexity untuk demo cycle pendek.
❌ Cross-service transaction (mis. presence → reservation) butuh lebih banyak boilerplate.

### Option B — Modular monolith (1 binary, 7 modules)

✅ Hemat resource, deploy mudah.
✅ Refactor antar module trivial (no network).
✅ Cocok untuk skala traffic awal.
❌ **TIDAK** memenuhi syarat use case (gRPC antar service).
❌ Tidak mendemonstrasikan kompetensi *Mampu merancang arsitektur microservices* (kompetensi #1.4).
❌ Coupling cepat naik kalau tidak disiplin.

### Option C — Hybrid (5 services, dengan beberapa merge)

Merge yang masuk akal:
- `search` + `reservation` → `reservation` expose `GetAvailability` (search adalah read-side dari domain reservation).
- `presence` tetap terpisah (real domain berbeda — location streaming).
- Sisanya tetap.

Hasil: `gateway`, `reservation` (incl search), `billing`, `payment`, `presence`, `notification` = **6 service**.

✅ Tetap microservices (memenuhi requirement).
✅ Mengurangi operational footprint ~14%.
✅ Justifikasi domain-driven (search read model belum kompleks).
❌ Risiko: kalau search nanti butuh denormalisasi besar (Elastic), harus split out.

### Option D — Full split + skeleton untuk yang tidak diminta

Implementasi penuh hanya untuk `reservation`, `billing`, `payment`, `gateway`. Yang lain skeleton + interface lengkap untuk demo arsitektur.

✅ Sesuai instruksi use case ("backend service codes that you provide to make sure the reservation and billing backend can be running smoothly for booking and charging only").
✅ Tetap mendemonstrasikan **desain** 7 service.
✅ Hemat effort tanpa mengurangi kualitas demo arsitektur.
❌ Implementor harus disiplin tidak meninggalkan TODO yang merusak demo.

## Decision

**Pilih Option D.**

7 service didesain dan kontrak proto-nya lengkap. Implementasi full ada di **gateway, reservation, billing, payment** (sesuai mandat use case). `search`, `presence`, `notification` di-implement sebagai **runnable skeleton** dengan stub yang cukup untuk demo gRPC handshake & subscribe NATS.

## Consequences

**Positif**:
- Memenuhi semua syarat use case (gRPC, microservices, focus implementasi reservation+billing).
- Kompetensi *merancang microservices* terpenuhi via dokumen LLD & proto contract.
- Operational manageable untuk demo (6-7 container kecil di docker-compose).

**Negatif**:
- Skeleton service (`search`, `presence`, `notification`) bisa terlihat thin dalam code review — mitigasi: ada README per service yang jelaskan ekspektasi production.
- Saat scale ke real production, perlu split `search` dari `reservation` kalau read-model jadi besar.

## Validation

- Run `make demo-up` → 7 container berjalan, `make test-e2e` lulus end-to-end.
- Code review checklist: setiap skeleton service punya README + Dockerfile + proto handler stub.
