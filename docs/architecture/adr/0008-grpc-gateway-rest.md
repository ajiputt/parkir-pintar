# ADR-0008: grpc-gateway untuk Eksposur REST

- **Status**: Accepted
- **Date**: 2026-04-29
- **Tags**: api, gateway

## Context

Mobile app & super-app integration umumnya pakai REST/JSON. Internal service pakai gRPC. Butuh translator REST ↔ gRPC yang DRY (define sekali).

## Decision

Pakai `grpc-ecosystem/grpc-gateway` v2 di `services/gateway`. Annotate proto dengan `google.api.http`, generate REST mux otomatis.

## Implementasi

```proto
service ReservationService {
  rpc CreateReservation(CreateReservationRequest) returns (CreateReservationResponse) {
    option (google.api.http) = {
      post: "/v1/reservations"
      body: "*"
    };
  }
}
```

Gateway proxy mux:
```go
mux := runtime.NewServeMux(
    runtime.WithIncomingHeaderMatcher(idempotencyHeaderMatcher),
    runtime.WithErrorHandler(errorHandler),
)
reservationv1.RegisterReservationServiceHandlerFromEndpoint(ctx, mux, "reservation:9090", opts)
```

OpenAPI:
```bash
buf generate --template buf.gen.openapi.yaml
# output: docs/api/openapi.json
```

## Trade-offs

**Positif**:
- Single source of truth (proto).
- OpenAPI auto-generated → Swagger UI untuk reviewer/QA.
- Konsisten error mapping (`errs` package → gRPC status → HTTP code).

**Negatif**:
- Custom HTTP semantics (e.g. partial update PATCH) butuh annotation hati-hati.
- Streaming jadi WebSocket/SSE — kita batasi REST ke unary, gRPC streaming hanya untuk client gRPC.
