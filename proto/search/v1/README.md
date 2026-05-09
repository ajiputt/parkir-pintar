# Search Service Contract — DEFERRED

> **Status**: API contract designed, **implementasi di-defer**.
> Lihat [ADR-0009](../../../docs/architecture/adr/0009-defer-search-presence-services.md) untuk justifikasi.

## Why Deferred

Use case ParkirPintar saat ini: **single parking area, no multi-area search radius**.
`reservation.GetAvailability` sudah memenuhi kebutuhan read-side untuk scope ini.

Search service akan punya value real ketika:
- Multi-area expansion (cari parking dekat lokasi user)
- Filter advanced (harga, fitur, vehicle type, accessibility)
- Read-throughput tinggi yang butuh dedicated read store (Redis sorted set, Elasticsearch)

## Implementation Plan (Future)

```
search/
├── cmd/main.go
└── internal/
    ├── domain/         # Spot search query model
    ├── usecase/
    │   └── query.go    # QuerySpots, FilterByDistance, RankByPreference
    └── adapter/
        ├── nats/       # Subscribe spot.status_changed.v1 → maintain snapshot
        ├── redis/      # Sorted set per area, score = distance + freshness
        └── grpcserver/ # gRPC handler implementing SearchService
```

Effort estimate: **3-5 hari** dari kontrak proto ini ke implementation lengkap dengan tests.

## Contract

Lihat [`search.proto`](search.proto) — kontrak gRPC sudah final dan siap pakai.
