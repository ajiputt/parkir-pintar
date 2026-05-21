# API Changelog

> **Looking for endpoint specs?** See [reference.md](reference.md) untuk full
> documentation (path, headers, body, response, examples per endpoint).

All notable API changes ke ParkirPintar HTTP/gRPC endpoints di-track di sini.
Format mengikuti [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) dan
project menganut [Semantic Versioning](https://semver.org/).

API surface = `proto/**/*.proto` (source of truth) → generated REST endpoints via
grpc-gateway. Setiap breaking change di proto wajib ada entry di sini + ADR
kalau impact > 1 service.

---

## [Unreleased]

### Added
- `/dev/token` endpoint (gateway) — generate JWT untuk integration testing
  (gated by `DEV_TOKEN_SECRET` header, blocked di production via ALB rule)

### Changed
- Strict auth mode di-enforce (`AUTH_PASSTHROUGH_NO_TOKEN=false` default)
  — semua endpoint kecuali `/healthz`, `/readyz`, `/v1/availability` require JWT

### Security
- Block `/metrics`, `/healthz`, `/readyz`, `/docs`, `/openapi.json` dari
  public traffic via AWS ALB Ingress rules
- Add Idempotency-Key header support untuk endpoints `POST /v1/reservations`,
  `POST /v1/payments`, `POST /v1/billing/invoices/{id}/pay`

---

## [1.0.0] — 2026-05-16

### Added — Core API surface

#### `reservation.v1.ReservationService` (gRPC port 9091)

| Endpoint | HTTP equivalent | Idempotent | Description |
|---|---|---|---|
| `CreateReservation` | `POST /v1/reservations` | Yes (via Idempotency-Key) | System-assigned atau user-selected spot |
| `GetReservation` | `GET /v1/reservations/{id}` | Yes (read) | Get reservation by ID |
| `CheckIn` | `POST /v1/reservations/{id}/checkin` | Yes (state-machine) | Mark reservation as ACTIVE |
| `CheckOut` | `POST /v1/reservations/{id}/checkout` | Yes (state-machine) | Mark COMPLETED + trigger billing |
| `Cancel` | `POST /v1/reservations/{id}/cancel` | Yes (state-machine) | Release spot + void invoice |
| `GetAvailability` | `GET /v1/availability` | Yes (read) | List available spots per floor/vehicle type |

#### `billing.v1.BillingService` (gRPC port 9092)

| Endpoint | HTTP equivalent | Idempotent | Description |
|---|---|---|---|
| `GetInvoice` | `GET /v1/billing/invoices/{id}` | Yes (read) | Get invoice by ID |
| `GetInvoiceByReservation` | `GET /v1/billing/invoices?reservation_id=` | Yes (read) | Lookup invoice via reservation_id |

#### `payment.v1.PaymentService` (gRPC port 9093)

| Endpoint | HTTP equivalent | Idempotent | Description |
|---|---|---|---|
| `CreatePayment` | `POST /v1/payments` | Yes (via Idempotency-Key) | Initiate QRIS payment via Midtrans |
| `GetPayment` | `GET /v1/payments/{id}` | Yes (read) | Get payment by ID |
| `HandleWebhook` | `POST /v1/payments/midtrans/notification` | Yes (signature verification) | Midtrans callback handler |

### Conventions

- **API versioning**: URL-based (`/v1/`). Future breaking changes → `/v2/`.
  Header-based versioning via `X-API-Version` di-defer ke v2.0.0 milestone.
- **Pagination**: cursor-based via `page_token` parameter (RFC-compliant).
- **Error responses**: gRPC status codes mapped ke HTTP via grpc-gateway
  default. Custom error details via `google.rpc.Status` proto.
- **Time format**: RFC3339 (`2026-05-20T14:30:00Z`), always UTC.
- **Currency**: All amounts in IDR (Indonesian Rupiah), integer (no fractional).
- **IDs**: UUID v4 (string format `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`).

### Breaking Changes

This is the initial public version (v1.0.0). No breaking changes from previous releases.

---

## Migration Guide untuk Future Breaking Changes

Saat breaking change diperlukan (mis. proto v2):

1. **Buat proto package baru**: `proto/reservation/v2/` (parallel ke v1)
2. **Deploy both versions side-by-side** selama minimum 1 release cycle
3. **Update OpenAPI doc** dengan deprecation notice di v1 endpoints
4. **Sunset v1 endpoints** setelah analytics menunjukkan < 1% traffic
5. **Document migration steps** di CHANGELOG + dedicated ADR

### Buf breaking change protection

Repo menggunakan **Buf breaking change check** di CI untuk auto-detect proto
breaking changes vs main branch. Lihat `make proto-breaking` dan
`.github/workflows/_lint.yml`. Setiap PR yang break proto schema akan
diblokir kecuali ada explicit override comment + ADR.

---

## Related References

- [ADR-0001](../architecture/adr/0001-microservices-vs-monolith.md) — service decomposition
- [ADR-0011](../architecture/adr/0011-postgres-unique-constraints.md) — reservation invariants
- [ADR-0014](../architecture/adr/0014-overdue-invoice-circuit-breaker.md) — billing pre-check
- [ADR-0016](../architecture/adr/0016-security-posture.md) — auth + secret management
- [ADR-0019](../architecture/adr/0019-ci-pipeline-architecture.md) — pipeline reusability
- [docs/api/idempotency.md](./idempotency.md) — Idempotency-Key header behavior
