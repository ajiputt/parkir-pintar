# ADR-0010: Protocol Strategy — REST in (Gateway), gRPC out (Service Mesh), NATS for Events

- **Status**: Accepted
- **Date**: 2026-05-05
- **Tags**: api, gateway, grpc, nats
- **Supersedes**: clarifies & operationalizes ADR-0002 (gRPC), ADR-0005 (NATS), ADR-0008 (grpc-gateway)

## Context

Use case mensyaratkan komunikasi antar service via gRPC over HTTP/2. Sebelum ADR ini, codebase punya tiga inkonsistensi:

1. **Gateway** memakai REST-to-REST proxy (custom `proxy.Forward`) — tidak ada terjemahan ke gRPC.
2. **Reservation gRPC** server ada struct + business logic, tapi `Register()` placeholder — tidak benar-benar register handler ke `*grpc.Server`.
3. **Payment → Billing** sync call memakai HTTP REST, bukan gRPC.

Hasilnya: dari edge sampai ke service backend, **tidak ada satupun call yang aktual gRPC** kecuali health check. Use-case requirement dilanggar diam-diam.

ADR ini mendokumentasikan strategi terpadu setelah refactor.

## Decision

| Boundary | Protocol | Rationale |
|---|---|---|
| Public client → Gateway | **REST/JSON over HTTP/1.1** | Mobile/web kompatibel, debug mudah dengan curl/Postman, browser tidak support raw gRPC. |
| Gateway → Service | **gRPC over HTTP/2** | Auto-translation via `grpc-gateway` runtime. Single source of truth: `proto/{svc}/v1/*.proto` annotation. |
| Service ↔ Service (sync) | **gRPC over HTTP/2** | HTTP/2 multiplex, schema-enforced contract, codegen client + server. Contoh: Payment → Billing GetInvoice. |
| Service ↔ Service (async) | **NATS JetStream** | Event-driven (Confirm → Billing project, Settlement → Mark Paid). Decouple producer dari consumer, enable replay & outbox pattern (ADR-0006). |
| Webhook (Midtrans → kita) | **HTTP/1.1 raw forward** | Provider tidak speak gRPC. Gateway forward verbatim ke payment HTTP karena Midtrans signature pada raw body. |
| Health probes | **HTTP + gRPC health protocol** | K8s/ECS support both; standar industri. |

### Architecture Diagram

```
┌──────────────────────────┐
│  Public client (Postman, │
│  Mobile, curl)           │
└────────────┬─────────────┘
             │ REST/JSON
             ▼
       ┌───────────┐
       │  Gateway  │ :8080 (HTTP)
       │  REST → gRPC translator
       │  middleware: CORS, RateLimit, Auth, RequestID
       └─────┬─────┘
             │ gRPC over HTTP/2
   ┌─────────┼─────────────────────┐
   ▼         ▼                     ▼
┌──────┐  ┌──────┐  ┌──────┐
│ Res. │  │ Bill │  │ Pay  │
│ :9091│  │ :9092│  │ :9093│
└──┬───┘  └──┬───┘  └──┬───┘
   │ event   │ event   │ event
   └────►┌──────────┐◄───┘
        │   NATS   │
        │ JetStream│
        └──────────┘
                ▲
                │ gRPC sync (Pay → Bill GetInvoice)
                └─── from Payment
```

## Why each choice

### REST in (gateway)

- **Mobile-first reality**: assessment mention end-user driver app. Mobile SDK gRPC pricier (proto codegen on every release). REST/JSON ringan.
- **Debug ergonomics**: assessor pasti coba via curl/Postman saat sesi presentasi. gRPC butuh `grpcurl` + import `.proto` — friction tinggi.
- **Browser compat**: kalau nanti tambah web admin dashboard, REST langsung kepake; gRPC perlu gRPC-Web envoy proxy.
- **DRY translator**: `grpc-gateway` baca `option (google.api.http)` annotation, gen reverse-proxy code. Tidak ada hardcoded route mapping di gateway. Edit proto, regenerate, restart — done.

### gRPC out (service mesh)

- **HTTP/2 multiplex**: 1 koneksi TCP, banyak concurrent streams. Latency lower under load dibanding HTTP/1.1 connection pool.
- **Schema-enforced**: salah field name = compile error, bukan 500 saat runtime.
- **Codegen client**: payment-billing client = 30 LOC dari proto, gak perlu manual JSON marshal/unmarshal.
- **gRPC interceptors**: `pkg/grpcutil` punya recovery, request_id, logging, retry, breaker, timeout — uniform across services.

### NATS for async

Lihat ADR-0005 (NATS JetStream pilihan vs Kafka). Ringkas:
- Persistent at-least-once delivery (ack/redeliver, durable consumer).
- Replay window (24 jam) untuk recovery downstream.
- Lightweight: 1 binary, low memory, vs Kafka (3 broker + Zookeeper/KRaft).

### Webhook tetap raw HTTP

Provider gateway (Midtrans) sign body sebelum hash. Re-encoding via JSON parser bisa ubah byte order (whitespace, key order) → signature mismatch. Solusi: **raw passthrough**. Gateway forward bytes verbatim, payment service yang verify.

Internally, payment juga punya `HandleWebhook` gRPC RPC untuk test harness / replay. Tidak dipakai public traffic.

## Rejected Alternatives

### gRPC end-to-end (Postman → Gateway via gRPC)

- **Why considered**: protocol consistency, no translation layer, "purest" gRPC story.
- **Why rejected**:
  - Browser tidak bisa raw gRPC. Tambah gRPC-Web proxy (Envoy) = infra extra.
  - Postman gRPC mode kurang familiar untuk demo cepat.
  - Mobile app meaning extra binary size + codegen pipeline pada setiap proto change.
  - Tidak match real-world API gateway pattern (Stripe, Twilio, Midtrans semua REST public).

### REST end-to-end (drop gRPC internal)

- **Why considered**: monoglot debug, no codegen step.
- **Why rejected**:
  - Use-case mensyaratkan gRPC service-to-service eksplisit.
  - Hilang manfaat HTTP/2 multiplex, schema-enforced, streaming (kalau nanti dibutuhkan).
  - JSON parser overhead tinggi pada hot path internal.

### Service mesh (Istio / Linkerd) untuk service-to-service

- **Why considered**: mTLS otomatis, traffic shaping, retry policies di sidecar.
- **Why rejected**:
  - Overkill untuk 4 service di assessment. Sidecar memory overhead 50–80MB per pod = 240MB total (3% AWS budget habis).
  - `pkg/grpcutil` interceptor sudah cover retry/breaker/timeout di code.
  - Tidak perlu mTLS di internal VPC (defense-in-depth = future enhancement).

## Implementation

### Proto annotation (single source of truth)

```proto
service ReservationService {
  rpc CreateReservation(CreateReservationRequest) returns (CreateReservationResponse) {
    option (google.api.http) = {
      post: "/v1/reservations"
      body: "*"
    };
  }
  rpc CheckIn(CheckInRequest) returns (Reservation) {
    option (google.api.http) = {
      post: "/v1/reservations/{id}:checkin"
      body: "*"
    };
  }
}
```

Codegen via `make proto` (uses `buf` + remote plugins) menghasilkan:
- `proto/gen/reservation/v1/reservation.pb.go` (messages)
- `proto/gen/reservation/v1/reservation_grpc.pb.go` (server + client interface)
- `proto/gen/reservation/v1/reservation.pb.gw.go` (REST → gRPC reverse proxy)
- `docs/api/openapi.json` (Swagger spec)

### Gateway wiring

```go
gwMux := runtime.NewServeMux(
    runtime.WithIncomingHeaderMatcher(headerMatcher),
)

dialOpts := []grpc.DialOption{
    grpc.WithTransportCredentials(insecure.NewCredentials()),
}
reservationv1.RegisterReservationServiceHandlerFromEndpoint(ctx, gwMux, "localhost:9091", dialOpts)
billingv1.RegisterBillingServiceHandlerFromEndpoint(ctx, gwMux, "localhost:9092", dialOpts)
paymentv1.RegisterPaymentServiceHandlerFromEndpoint(ctx, gwMux, "localhost:9093", dialOpts)
```

### Service-to-service gRPC client

```go
// services/payment/internal/adapter/billingclient/client.go
conn, _ := grpc.NewClient("billing:9092",
    grpc.WithTransportCredentials(insecure.NewCredentials()))
client := billingv1.NewBillingServiceClient(conn)
resp, err := client.GetInvoice(ctx, &billingv1.GetInvoiceRequest{Id: id})
```

### Header propagation

`Idempotency-Key` HTTP header → `x-idempotency-key` gRPC metadata via custom `headerMatcher`. Service handler ambil via `grpcutil.IdempotencyKeyFromContext(ctx)`.

## Consequences

### Positive

- ✅ Use-case requirement (service-to-service via gRPC) terpenuhi 100%.
- ✅ Single source of truth untuk REST routes: `.proto` annotation.
- ✅ Public REST tetap nyaman untuk demo & assessor.
- ✅ Schema enforcement eliminates field-name typos antar service.
- ✅ HTTP/2 multiplex untuk service-to-service (latency lower under load).

### Negative

- ⚠️ Codegen step jadi prerequisite (`make proto`). Onboarding dev butuh `buf` install. Mitigasi: dokumentasi step di root README.
- ⚠️ Gateway dial backend service eager saat startup. Kalau backend belum ready saat gateway start, bakal error. Mitigasi: orchestrator (Compose `depends_on` dengan health-check, K8s readinessProbe).
- ⚠️ Webhook tidak ikut grpc-gateway — separate code path. Mitigasi: dokumentasi eksplisit di gateway main.go.

### Neutral

- 📝 Internal service tidak punya HTTP business endpoint lagi. Curl ke billing/payment direct → 404 untuk business path (health tetap ada). Konsekuensi: debug butuh `grpcurl`. Mitigasi: dokumentasi di postman README, tambah `grpcurl` di dev tooling.

## Migration Path

Dari kondisi sebelum ADR ini:

1. ✅ Generate proto stubs (`make proto`)
2. ✅ Replace gateway REST proxy dengan `grpc-gateway` runtime mux
3. ✅ Wire reservation gRPC server (replace placeholder `Register()`)
4. ✅ Add billing & payment gRPC server
5. ✅ Refactor `billingclient`: HTTP `*http.Client` → gRPC `BillingServiceClient`
6. ✅ Drop HTTP business handler dari billing & payment (keep webhook + health)
7. ✅ Update Postman collection: semua endpoint via gateway
8. 🔜 Update Docker Compose & Helm: ekspor `*_GRPC_ADDR` envvar
9. 🔜 Update CI integration test: dial via gateway

## References

- [grpc-gateway docs](https://grpc-ecosystem.github.io/grpc-gateway/)
- [Google AIP-127 HTTP/JSON transcoding](https://google.aip.dev/127)
- ADR-0002 (gRPC protocol)
- ADR-0005 (NATS JetStream)
- ADR-0006 (Event sourcing billing)
- ADR-0008 (grpc-gateway REST)
