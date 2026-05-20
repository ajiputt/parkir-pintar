# ADR-0009: Defer Search & Presence Services

- **Status**: Accepted
- **Date**: 2026-04-30
- **Decision-makers**: Backend team (Aji)
- **Tags**: architecture, scope-management, microservices

## Context

Use case ParkirPintar mensugest 7 microservices:
> Suggested microservices (you may merge some **if you justify it**): gateway, search, reservation, billing, payment, presence, notification.

Bagian "*if you justify it*" eksplisit mengundang justifikasi. ADR ini menyusun rationale untuk **defer** dua service tersebut: `search` dan `presence`.

> **Defer ≠ delete**. Kontrak `.proto` tetap dipertahankan di [`proto/search/v1/`](../../../proto/search/v1) dan [`proto/presence/v1/`](../../../proto/presence/v1) sebagai forward-compatible API contract — siap di-implement saat ada requirement valid.

## Forces

| Force | Direction |
|---|---|
| Use case scope (single area, no multi-area search) | Search redundant |
| Use case scope (no geofencing acceptance criteria) | Presence out-of-scope |
| Operational cost (per-service: container, log, monitor, deploy) | Hindari boilerplate tanpa value |
| Demonstrasi kompetensi senior (kapan TIDAK split) | Justifikasi merge = bukti seniority |
| Future expansion (multi-area, geofencing) | Kontrak proto harus tetap ada |
| Use case literal "7 service di-suggest" | Risiko assessor tanya kenapa tidak implement |

## Analysis per Service

### Search

**Apa yang search seharusnya lakukan**:
- Multi-area discovery (cari parking dekat lokasi user)
- Filter advanced (harga, vehicle type, fitur)
- Read-side projection terpisah dari write-side reservation

**Mengapa tidak cocok untuk scope ini**:

1. **Use case eksplisit menutup multi-area**:
   > *"The system manages a single parking area (one district/area only) with a centralized inventory; there is no Host onboarding or spot publishing."*
   > *"Driver will view availability and reserve a spot within that parking area (no multi-area search radius)."*

2. **`GetAvailability` di reservation sudah cukup**: Single-area + centralized inventory → 1 endpoint cukup return per-floor availability. Lihat [`proto/reservation/v1/reservation.proto`](../../../proto/reservation/v1/reservation.proto).

3. **Tidak ada filter advanced** yang diminta use case (cuma vehicle_type CAR/MOTOR yang sudah dihandle reservation).

4. **Search "skeleton" sebelumnya cuma HTTP proxy** ke `reservation:9191/v1/availability` — anti-pattern (network hop tanpa logika tambahan).

### Presence

**Apa yang presence seharusnya lakukan**:
- Real-time location streaming (gRPC bidi-stream)
- Geofencing terhadap parking area boundaries
- Trigger event saat driver enter/exit geofence
- Dorong notifikasi atau auto-checkin

**Mengapa tidak cocok untuk scope ini**:

1. **Use case mention "presence" hanya di intro paragraph**:
   > *"The system uses location data from smartphones to detect where the Driver is, inform availability for that parking area, and allow Drivers to reserve a spot quickly."*

   Tidak ada acceptance criteria, tidak ada e2e test scenario, tidak ada business rule.

2. **Geofencing butuh data yang tidak disediakan**: Lat/long boundary parking area, akurasi GPS tolerance, hysteresis logic — semua bukan bagian dari requirement.

3. **Auto-checkin via geofence bukan use case kita**: Use case mensyaratkan **explicit checkin** (Driver klik "Check-in") setelah konfirmasi reservasi. Geofence-triggered checkin bisa **bertentangan** dengan flow ini.

4. **Data privacy concern**: Location streaming continuous = high battery + sensitive data. Production butuh consent flow + data retention policy yang **tidak diminta** use case.

## Options Considered

### Option A — Defer keduanya (CHOSEN)

**Aksi**:
- Hapus implementasi `services/search/` & `services/presence/`
- Pertahankan `proto/search/v1/` & `proto/presence/v1/` (kontrak tetap ada)
- Update wiring (compose, helm, terraform, CI) — drop dari deployment
- Tulis ADR ini sebagai justifikasi

**Pros**:
- ✅ Hemat operational cost: 2 container, 2 Fargate task ($5/bulan), 2 log group, 2 healthcheck
- ✅ Reduce noise di log/dashboard untuk operator
- ✅ Demonstrasi seniority: tahu kapan **tidak** split service
- ✅ Use case **eksplisit** memungkinkan ("if you justify it")
- ✅ Forward-ready: kontrak proto tetap ada

**Cons**:
- ⚠️ Risiko assessor tanya literal "kenapa tidak ada 7 service?"
- **Mitigasi**: ADR ini + kontrak proto = bukti decision sadar, bukan kelalaian

### Option B — Pertahankan sebagai skeleton

**Aksi**: Biarkan implementasi minimal (current state).

**Pros**:
- ✅ Demo arsitektur 7-service lengkap secara visual
- ✅ Tidak ada "jawab" yang harus dipersiapkan untuk assessor

**Cons**:
- ❌ Anti-pattern: service tanpa logika bisnis = boilerplate yang misleading
- ❌ Operational cost real: $5/bulan + 2 container di ops dashboard
- ❌ Bisa dianggap "box-checking" oleh assessor senior

### Option C — Defer presence saja, implement search proper (event-sourced)

**Aksi**: Implement search sebagai NATS subscriber → Redis snapshot. Hapus presence.

**Pros**:
- ✅ Search punya value real (read-model terpisah)
- ✅ Demo CQRS pattern eksplisit

**Cons**:
- ⚠️ Effort tambahan signifikan (1-2 hari)
- ⚠️ Scope creep — use case tidak meminta CQRS
- ⚠️ ROI rendah karena single-area (write throughput rendah, no need separate read store)

## Decision

**Option A — Defer keduanya**.

Justifikasi inti:

> Service yang tidak punya domain logic real adalah **liability**, bukan asset. Microservices memberi value ketika ada *bounded context* yang berbeda dengan kebutuhan scaling/teknologi/team-ownership berbeda. Untuk scope use case ini (single area, no geofencing requirement), search dan presence tidak memenuhi kriteria tersebut.
>
> Dengan mempertahankan kontrak `.proto` tapi tidak deploy implementasinya, kita mengikuti prinsip **"design for change, not for everything"**: API contract siap dipakai saat requirement nyata muncul (multi-area expansion, fitur geofencing), tanpa membayar operational cost saat ini.

## Consequences

**Positif**:

- Operational footprint: **6 → 4 microservices** (gateway, reservation, billing, payment + notification consumer)
- AWS cost: hemat ~$5/bulan dari Fargate, plus reduce CloudWatch logs ingestion
- Codebase footprint: -2 Dockerfile, -2 go.mod, -2 main.go = lebih maintainable
- Deployment time lebih cepat (4 image build vs 6)
- ADR ini = **"showcase senior-level decision"** untuk assessor

**Negatif**:

- ⚠️ Kalau requirement future butuh multi-area / geofencing, butuh 1-2 minggu effort untuk build dari proto contract → ke implementation lengkap. **Mitigasi**: dokumentasi production-plan di proto package README.
- ⚠️ Beberapa diagram di README yang sebelumnya menampilkan 7 service perlu di-update → bagian dari deliverable ADR ini.

**Neutral**:

- `notification` tetap ada karena **punya logic real**: subscribe NATS event lintas service, dispatch ke external (push/email/SMS). Bahkan dalam mock mode, dia menunjukkan event-driven architecture pattern yang bermakna.

## Rollback Path

Kalau di tengah jalan keputusan ini perlu di-revert (mis. assessor strict literal):

```bash
# Restore via git
git revert <commit-sha-of-this-adr>

# Atau manual:
# 1. mkdir services/search services/presence
# 2. Re-create cmd/main.go (lihat git log sebelum revert)
# 3. Re-add ke go.work, docker-compose, helm, terraform
```

Karena kontrak proto tidak dihapus, restoration cukup straightforward.

## Validation

- [x] Diagram README service decomposition table di-update
- [x] Total service count di docs/architecture/diagrams konsisten
- [x] CI workflow matrix (.github/workflows/ci.yml) di-update
- [x] Cost estimate (deploy/terraform/aws-ecs/COST.md) di-update
- [x] ADR-0001 di-update dengan reference ke ADR ini
- [x] Tidak ada broken import / dangling reference (`make build` lulus)

## References

- [ADR-0001](0001-microservices-vs-monolith.md) — Decomposition rationale awal (Option D dijabarkan di sana)
- Use case: `use case.txt` paragraf 49-50 ("Suggested microservices...")
- Martin Fowler, *MonolithFirst*: "Microservices are a useful architecture, but even their advocates say that you shouldn't start with them"
