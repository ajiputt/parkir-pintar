# ADR-0002: gRPC over HTTP/2 untuk Komunikasi Antar Service

- **Status**: Accepted
- **Date**: 2026-04-29
- **Tags**: api, protocol

## Context

Use case mewajibkan gRPC over HTTP/2 untuk inter-service communication. Pilihan tetap ada untuk:
- Style API external (REST vs gRPC-Web)
- Codegen tooling (protoc vs buf)
- Protocol buffers vs alternative (Thrift, Avro)

## Decision

1. **Internal**: gRPC + Protocol Buffers v3.
2. **External (gateway → client)**: REST/JSON via `grpc-gateway` v2.
3. **Codegen**: `buf` untuk lint, breaking-change check, dan generate (pengganti protoc langsung).
4. **Schema versioning**: package `.v1` di nama proto (`reservation.v1.ReservationService`).
5. **Validation**: `protoc-gen-validate` untuk validasi field.

## Rationale

| Aspek | gRPC | REST | Pilihan |
|---|---|---|---|
| Performance | binary, HTTP/2, multiplex | text JSON, HTTP/1.1 | gRPC internal |
| Schema | strict (proto) | optional (OpenAPI) | gRPC = type safety |
| Streaming | native bi-di | SSE/WebSocket workaround | gRPC untuk presence |
| Browser compat | gRPC-Web (limited) | full | REST untuk client |
| Tooling | protoc/buf, mature | mature | dual (gateway translate) |

`grpc-gateway` solve "best of both worlds" — define sekali (proto), expose dua (gRPC + REST) — DRY dan mengurangi drift.

## Consequences

**Positif**: type safety, performance, streaming, tooling mature, Swagger/OpenAPI auto-generate.

**Negatif**:
- Build pipeline butuh proto compiler (mitigasi: `buf` di Makefile + Docker).
- Debugging gRPC butuh `grpcurl` (vs `curl`) — tutorial di runbook.

## Alternatives Considered

- **REST only**: ditolak — tidak memenuhi requirement, hilang HTTP/2 multiplexing.
- **GraphQL**: lebih cocok untuk client-aggregator, bukan service-to-service.
- **Thrift**: ekosistem Go kalah dari gRPC.
