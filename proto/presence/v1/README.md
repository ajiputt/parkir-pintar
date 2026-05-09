# Presence Service Contract — DEFERRED

> **Status**: API contract designed, **implementasi di-defer**.
> Lihat [ADR-0009](../../../docs/architecture/adr/0009-defer-search-presence-services.md) untuk justifikasi.

## Why Deferred

Use case mention "presence" hanya di intro paragraph, **tanpa acceptance criteria** atau e2e test scenario. Geofencing rules, location accuracy, hysteresis behavior — semua tidak didefinisikan di scope.

Presence service akan punya value real ketika:
- Auto-checkin lewat geofence (vs explicit checkin sekarang)
- Driver app butuh hint "kamu sudah dekat parking" / "spot kamu di lantai 2"
- Reward/penalty berdasarkan time-to-arrival
- Anti-fraud detection (driver tidak benar-benar di lokasi)

## Implementation Plan (Future)

```
presence/
├── cmd/main.go
└── internal/
    ├── domain/         # GeoPoint, Geofence, PresenceState
    ├── usecase/
    │   ├── stream.go   # Bidi-stream LocationUpdate ↔ PresenceHint
    │   └── geofence.go # Point-in-polygon, hysteresis logic
    └── adapter/
        ├── grpcserver/ # gRPC bidi-stream handler
        ├── nats/       # Publish presence.location_updated.v1
        └── postgres/   # Geofence config storage
```

Plus consent flow + data retention policy (data privacy compliance).

Effort estimate: **5-7 hari** termasuk privacy review.

## Contract

Lihat [`presence.proto`](presence.proto) — kontrak gRPC sudah final dengan bidi-stream pattern.
