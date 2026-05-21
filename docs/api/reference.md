# API Reference — ParkirPintar

Comprehensive specification untuk semua HTTP endpoints. Source of truth =
proto schema di [`proto/`](../../proto/). Auto-generated OpenAPI spec di
[`docs/api/openapi.swagger.json`](openapi.swagger.json) untuk Swagger UI render.

> **Quick reference**:
> - [Reservation Service](#reservation-service) — 7 endpoints
> - [Billing Service](#billing-service) — 5 endpoints
> - [Payment Service](#payment-service) — 3 endpoints
> - [Common Conventions](#common-conventions)
> - [Error Responses](#error-responses)

---

## Common Conventions

### Base URL

| Environment | Base URL |
|---|---|
| Local (Docker Compose) | `http://localhost:8080` |
| Staging (placeholder) | `https://staging.parkirpintar.id` |
| Production (placeholder) | `https://api.parkirpintar.id` |

### Standard Headers

#### Request

| Header | Required | Description | Example |
|---|---|---|---|
| `Authorization` | Yes (kecuali `/healthz`, `/v1/availability`) | JWT Bearer token | `Bearer eyJhbG...` (lihat [Auth](#authentication)) |
| `Content-Type` | Yes (untuk POST) | Selalu `application/json` | `application/json` |
| `Idempotency-Key` | Yes (untuk mutation) | UUID v4, unique per logical operation | `550e8400-e29b-41d4-a716-446655440000` |
| `X-Request-ID` | No (auto-generated) | Client-provided correlation ID | `req-abc123` |
| `traceparent` | No (auto-injected) | W3C trace context untuk distributed tracing | `00-4bf92f...-00f067aa...-01` |

#### Response

| Header | Description | Example |
|---|---|---|
| `Content-Type` | Always `application/json` | `application/json` |
| `X-Request-ID` | Correlation ID (echo of request or generated) | `req-abc123` |
| `X-Idempotency-Status` | (untuk idempotent endpoint) `created` / `replayed` / `conflict` | `created` |
| `WWW-Authenticate` | (saat 401) Bearer challenge | `Bearer realm="parkirpintar"` |

### Authentication

JWT Bearer token via `Authorization` header. Untuk local dev, generate via:

```bash
curl -X POST http://localhost:8080/dev/token \
  -H "X-Dev-Token-Secret: dev-secret" \
  -H "Content-Type: application/json" \
  -d '{"driver_id": "driver-demo-1"}'
# Response: { "token": "eyJhbGc...", "expires_at": "2026-05-21T..." }
```

⚠️ `/dev/token` endpoint gated by `DEV_TOKEN_SECRET` env, blocked di production via ALB rule (ADR-0016).

### Idempotency

POST endpoints yang mutasi state require `Idempotency-Key` header. Lihat
[docs/api/idempotency.md](idempotency.md) untuk detail.

### Time Format

ISO 8601 / RFC 3339, always UTC: `2026-05-20T14:30:00Z`

### Money Format

Semua amount dalam IDR (integer, no decimal):

```json
{
  "amount": 5000,
  "currency": "IDR"
}
```

---

## Reservation Service

Base path: `/v1/`

### 1. Get Availability

Get realtime parking availability summary per floor + vehicle type.

| | |
|---|---|
| **Path** | `GET /v1/availability` |
| **Auth** | Optional (public read) |
| **Idempotent** | Yes (read-only) |
| **Rate limit tier** | Light |

#### Query Parameters

| Name | Type | Required | Default | Description |
|---|---|---|---|---|
| `parking_area_id` | string | No | `main` | Parking area ID (single area assumption, future-ready) |

#### Response Body

```json
{
  "parking_area_id": "main",
  "parking_area_name": "ParkirPintar Central",
  "total_car_available": 142,
  "total_motor_available": 238,
  "floors": [
    {
      "level": 1,
      "car_available": 28,
      "car_capacity": 30,
      "motor_available": 47,
      "motor_capacity": 50
    },
    {
      "level": 2,
      "car_available": 30,
      "car_capacity": 30,
      "motor_available": 50,
      "motor_capacity": 50
    }
  ],
  "as_of": "2026-05-20T10:30:00Z"
}
```

#### Example

```bash
curl http://localhost:8080/v1/availability | jq
```

---

### 2. List Spots

List individual spots grouped by floor, dengan filter opsional.

| | |
|---|---|
| **Path** | `GET /v1/spots` |
| **Auth** | Required |
| **Idempotent** | Yes |
| **Rate limit tier** | Light |

#### Query Parameters

| Name | Type | Required | Default | Description |
|---|---|---|---|---|
| `floor_level` | int | No | (all) | Filter ke 1 floor saja |
| `vehicle_type` | enum | No | (all) | `CAR` atau `MOTOR` |
| `status` | enum | No | `AVAILABLE` | `AVAILABLE`, `HELD`, `OCCUPIED`, `OUT_OF_SERVICE` |

#### Response Body

```json
{
  "floors": [
    {
      "level": 1,
      "spots": [
        {
          "id": "550e8400-e29b-41d4-a716-446655440010",
          "code": "F1-C-001",
          "floor_level": 1,
          "vehicle_type": "CAR",
          "status": "AVAILABLE"
        },
        {
          "id": "550e8400-e29b-41d4-a716-446655440011",
          "code": "F1-C-002",
          "floor_level": 1,
          "vehicle_type": "CAR",
          "status": "AVAILABLE"
        }
      ],
      "capacity": 30,
      "available": 28
    }
  ],
  "total": 28
}
```

#### Example

```bash
# All available car spots di floor 1
curl "http://localhost:8080/v1/spots?floor_level=1&vehicle_type=CAR&status=AVAILABLE" \
  -H "Authorization: Bearer $TOKEN" | jq
```

---

### 3. Create Reservation

Create reservation dengan SYSTEM-assigned atau USER-selected spot.

| | |
|---|---|
| **Path** | `POST /v1/reservations` |
| **Auth** | Required |
| **Idempotent** | Yes (via `Idempotency-Key` header) |
| **Rate limit tier** | Medium |

#### Headers

| Header | Required | Description |
|---|---|---|
| `Authorization` | Yes | Bearer token |
| `Idempotency-Key` | Yes | UUID v4 |
| `Content-Type` | Yes | `application/json` |

#### Request Body

| Field | Type | Required | Description |
|---|---|---|---|
| `driver_id` | string | Yes | Driver UUID atau external ID |
| `plate_no` | string | Yes | Vehicle plate number (e.g. "B 1234 XYZ") |
| `vehicle_type` | enum | Yes | `CAR` atau `MOTOR` |
| `mode` | enum | Yes | `SYSTEM` (auto-assign) atau `USER` (caller picks spot) |
| `spot_id` | string | Conditional | Required jika `mode=USER`, ignored kalau `mode=SYSTEM` |
| `start_at` | RFC3339 | No | Expected arrival. Default = now (booking instant) |
| `payment_mode` | enum | Yes | `AUTO` (wallet) atau `MANUAL` (QRIS scan, lihat ADR-0014) |

#### Response Body (201 Created)

```json
{
  "reservation": {
    "id": "8a7b6c5d-1234-5678-9abc-def012345678",
    "driver_id": "driver-demo-1",
    "spot": {
      "id": "550e8400-e29b-41d4-a716-446655440010",
      "code": "F1-C-001",
      "floor_level": 1,
      "vehicle_type": "CAR",
      "status": "HELD"
    },
    "plate_no": "B 1234 XYZ",
    "state": "CONFIRMED",
    "start_at": "2026-05-20T10:30:00Z",
    "expires_at": "2026-05-20T11:30:00Z",
    "assignment_mode": "SYSTEM",
    "created_at": "2026-05-20T10:30:00Z",
    "payment_mode": "MANUAL"
  },
  "booking_fee": {
    "amount": 5000,
    "currency": "IDR"
  }
}
```

#### Example

```bash
curl -X POST http://localhost:8080/v1/reservations \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{
    "driver_id": "driver-demo-1",
    "plate_no": "B 1234 XYZ",
    "vehicle_type": "CAR",
    "mode": "SYSTEM",
    "payment_mode": "MANUAL"
  }'
```

#### Possible Errors

| Status | Code | Reason |
|---|---|---|
| `400` | `INVALID_ARGUMENT` | Missing required field, invalid enum value |
| `403` | `OVERDUE_INVOICE_BLOCKED` | Driver punya unpaid invoice > grace period (ADR-0014) |
| `409` | `SPOT_UNAVAILABLE` | Spot already reserved (anti double-booking, ADR-0011) |
| `409` | `IDEMPOTENCY_CONFLICT` | Same key dengan payload berbeda |
| `429` | `RATE_LIMIT_EXCEEDED` | Rate limit hit untuk tier Medium |

---

### 4. Get Reservation

Get reservation detail by ID.

| | |
|---|---|
| **Path** | `GET /v1/reservations/{id}` |
| **Auth** | Required |
| **Idempotent** | Yes |
| **Rate limit tier** | Light |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `id` | UUID | Reservation ID |

#### Response Body — `Reservation`

Same shape sebagai `reservation` field di Create response (lihat above).

#### Example

```bash
curl http://localhost:8080/v1/reservations/8a7b6c5d-1234-5678-9abc-def012345678 \
  -H "Authorization: Bearer $TOKEN"
```

---

### 5. Cancel Reservation

Cancel reservation (sebelum check-in atau setelah check-in untuk early cancellation).

| | |
|---|---|
| **Path** | `POST /v1/reservations/{id}:cancel` |
| **Auth** | Required |
| **Idempotent** | Yes (state-machine: already CANCELLED = no-op) |
| **Rate limit tier** | Medium |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `id` | UUID | Reservation ID |

#### Request Body

| Field | Type | Required | Description |
|---|---|---|---|
| `reason` | string | No | Optional cancellation reason (untuk analytics) |

#### Response Body

`Reservation` dengan `state = "CANCELLED"`.

#### Example

```bash
curl -X POST http://localhost:8080/v1/reservations/8a7b6c5d-.../:cancel \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason": "Changed mind"}'
```

---

### 6. Check In

Mark reservation as ACTIVE (driver tiba di parking).

| | |
|---|---|
| **Path** | `POST /v1/reservations/{id}:checkin` |
| **Auth** | Required |
| **Idempotent** | Yes (state-machine: already CHECKED_IN = no-op) |
| **Rate limit tier** | Medium |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `id` | UUID | Reservation ID |

#### Request Body

Empty `{}`.

#### Response Body

`Reservation` dengan `state = "CHECKED_IN"` dan `checkin_at` populated.

#### Example

```bash
curl -X POST http://localhost:8080/v1/reservations/8a7b6c5d-.../:checkin \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

#### Possible Errors

| Status | Code | Reason |
|---|---|---|
| `404` | `NOT_FOUND` | Reservation ID tidak ada |
| `400` | `INVALID_STATE` | Reservation di state yang ga valid (e.g. CANCELLED → can't check-in) |

---

### 7. Check Out

Mark reservation as COMPLETED, trigger billing calculation.

| | |
|---|---|
| **Path** | `POST /v1/reservations/{id}:checkout` |
| **Auth** | Required |
| **Idempotent** | Yes (state-machine) |
| **Rate limit tier** | Medium |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `id` | UUID | Reservation ID |

#### Request Body

Empty `{}`.

#### Response Body

`Reservation` dengan `state = "CHECKED_OUT"` dan `checkout_at` populated.

Note: invoice calculation triggered async via NATS event. Use [Get Invoice by Reservation](#3-get-invoice-by-reservation) untuk lookup final amount.

#### Example

```bash
curl -X POST http://localhost:8080/v1/reservations/8a7b6c5d-.../:checkout \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

## Billing Service

Base path: `/v1/`

### 1. Get Invoice

Get invoice by ID.

| | |
|---|---|
| **Path** | `GET /v1/invoices/{id}` |
| **Auth** | Required |
| **Idempotent** | Yes |
| **Rate limit tier** | Light |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `id` | UUID | Invoice ID |

#### Response Body

```json
{
  "id": "inv-abc123",
  "reservation_id": "8a7b6c5d-1234-5678-9abc-def012345678",
  "driver_id": "driver-demo-1",
  "status": "ISSUED",
  "total": {
    "amount": 15000,
    "currency": "IDR"
  },
  "items": [
    {
      "id": "item-1",
      "type": "BOOKING_FEE",
      "description": "Reservation booking fee",
      "amount": { "amount": 5000, "currency": "IDR" }
    },
    {
      "id": "item-2",
      "type": "HOURLY",
      "description": "Parking duration (2 hours)",
      "amount": { "amount": 10000, "currency": "IDR" },
      "period_start": "2026-05-20T10:30:00Z",
      "period_end": "2026-05-20T12:30:00Z"
    }
  ],
  "created_at": "2026-05-20T10:30:00Z",
  "issued_at": "2026-05-20T12:30:00Z",
  "payment_mode": "MANUAL"
}
```

#### Example

```bash
curl http://localhost:8080/v1/invoices/inv-abc123 \
  -H "Authorization: Bearer $TOKEN"
```

---

### 2. Get Invoice by Reservation

Lookup invoice via reservation ID (1-to-1 relationship).

| | |
|---|---|
| **Path** | `GET /v1/reservations/{reservation_id}/invoice` |
| **Auth** | Required |
| **Idempotent** | Yes |
| **Rate limit tier** | Light |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `reservation_id` | UUID | Reservation ID |

#### Response Body

Same shape as [Get Invoice](#1-get-invoice).

#### Example

```bash
curl http://localhost:8080/v1/reservations/8a7b6c5d-.../invoice \
  -H "Authorization: Bearer $TOKEN"
```

---

### 3. Issue Invoice (admin/debug)

Manual issue invoice. Biasanya event-driven (auto-trigger from `reservation.checked_out`),
endpoint ini untuk admin/debug only.

| | |
|---|---|
| **Path** | `POST /v1/invoices:issue` |
| **Auth** | Required (admin role) |
| **Idempotent** | Yes (same reservation_id = same invoice) |
| **Rate limit tier** | Medium |

#### Request Body

```json
{
  "reservation_id": "8a7b6c5d-1234-5678-9abc-def012345678"
}
```

#### Response Body

`Invoice` dengan status `ISSUED`.

---

### 4. Mark Paid

Mark invoice as PAID. Triggered oleh payment service setelah Midtrans webhook,
expose untuk admin/manual override.

| | |
|---|---|
| **Path** | `POST /v1/invoices/{id}:mark-paid` |
| **Auth** | Required (admin role atau internal service) |
| **Idempotent** | Yes (already PAID = no-op) |
| **Rate limit tier** | Webhook |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `id` | UUID | Invoice ID |

#### Request Body

```json
{
  "payment_id": "pay-xyz789"
}
```

#### Response Body

`Invoice` dengan status `PAID` dan `paid_at` populated.

---

### 5. Count Overdue by Driver

Count overdue invoice untuk driver tertentu. Dipakai reservation service
untuk pre-check (ADR-0014) — block driver dengan tagihan tertunggak.

| | |
|---|---|
| **Path** | `GET /v1/drivers/{driver_id}/overdue-count` |
| **Auth** | Required (internal service) |
| **Idempotent** | Yes |
| **Rate limit tier** | Light |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `driver_id` | string | Driver ID |

#### Response Body

```json
{
  "count": 2
}
```

`count > 0` = driver punya overdue, **block reservation baru**.

---

## Payment Service

Base path: `/v1/`

### 1. Create Payment

Generate QRIS payment untuk invoice.

| | |
|---|---|
| **Path** | `POST /v1/payments` |
| **Auth** | Required |
| **Idempotent** | Yes (via `Idempotency-Key` header) |
| **Rate limit tier** | Heavy |

#### Headers

| Header | Required | Description |
|---|---|---|
| `Authorization` | Yes | Bearer token |
| `Idempotency-Key` | Yes | UUID v4 |

#### Request Body

| Field | Type | Required | Description |
|---|---|---|---|
| `invoice_id` | UUID | Yes | Invoice yang akan dibayar |
| `method` | enum | Yes | `QRIS` (saat ini hanya QRIS) |

#### Response Body

```json
{
  "id": "pay-xyz789",
  "invoice_id": "inv-abc123",
  "method": "QRIS",
  "gateway": "MIDTRANS",
  "gateway_ref": "midtrans-transaction-abc",
  "qr_string": "00020101021126570014A00000007750415530...",
  "qr_url": "https://api.sandbox.midtrans.com/v2/qris/abc-12345/qr-code",
  "amount": {
    "amount": 15000,
    "currency": "IDR"
  },
  "status": "PENDING",
  "created_at": "2026-05-20T12:30:00Z",
  "expires_at": "2026-05-20T12:45:00Z"
}
```

Client gunakan:
- `qr_url` → render QR code image di UI
- `qr_string` → raw payload kalau mau render QR via library client-side

#### Example

```bash
curl -X POST http://localhost:8080/v1/payments \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{
    "invoice_id": "inv-abc123",
    "method": "QRIS"
  }'
```

#### Possible Errors

| Status | Code | Reason |
|---|---|---|
| `400` | `INVALID_ARGUMENT` | Invoice tidak found atau status bukan `ISSUED` |
| `409` | `PAYMENT_EXISTS` | Payment untuk invoice ini sudah ada (active or success) |
| `502` | `GATEWAY_ERROR` | Midtrans timeout/error |

---

### 2. Get Payment

Get payment status by ID.

| | |
|---|---|
| **Path** | `GET /v1/payments/{id}` |
| **Auth** | Required |
| **Idempotent** | Yes |
| **Rate limit tier** | Light |

#### Path Parameters

| Name | Type | Description |
|---|---|---|
| `id` | UUID | Payment ID |

#### Response Body

`Payment` (same shape as Create response).

---

### 3. Midtrans Webhook

Inbound webhook dari Midtrans payment gateway. **NOT user-facing** — Midtrans
calls langsung ke endpoint ini setelah QRIS payment selesai.

| | |
|---|---|
| **Path** | `POST /v1/payments/midtrans/notification` |
| **Auth** | Signature verification (sha512 HMAC) |
| **Idempotent** | Yes (dedupe via gateway transaction_id) |
| **Rate limit tier** | Webhook |

#### Headers

| Header | Required | Description |
|---|---|---|
| Signature header dari Midtrans | Yes | Verified server-side |

#### Request Body (Midtrans format)

```json
{
  "transaction_time": "2026-05-20 12:35:00",
  "transaction_status": "settlement",
  "transaction_id": "midtrans-transaction-abc",
  "status_message": "Success, transaction is found",
  "status_code": "200",
  "signature_key": "fe5f725ea770c451d488...",
  "payment_type": "qris",
  "order_id": "inv-abc123",
  "merchant_id": "M001",
  "gross_amount": "15000.00",
  "currency": "IDR"
}
```

#### Response Body (200 OK)

```json
{
  "accepted": true
}
```

#### Possible Errors

| Status | Reason |
|---|---|
| `200` | Successfully processed (always, idempotent) |
| `403` | Signature mismatch (forbidden) |
| `400` | Invalid payload format |

#### Manual Testing

Simulate Midtrans webhook via:

```bash
./scripts/simulate-midtrans-webhook.sh <invoice_id> settlement
```

Script ini sign payload dengan local Midtrans key + POST ke gateway.

---

## Common Conventions (Reference)

### Enum Values

#### `VehicleType`
| Value | Description |
|---|---|
| `CAR` | Mobil (4-wheeled) |
| `MOTOR` | Motor (2-wheeled) |

#### `SpotStatus`
| Value | Description |
|---|---|
| `AVAILABLE` | Spot tersedia untuk reserve |
| `HELD` | Reserved (held) untuk driver yang belum check-in |
| `OCCUPIED` | Driver sudah check-in, sedang parkir |
| `OUT_OF_SERVICE` | Maintenance / construction |

#### `ReservationState`
| Value | Description | Next states |
|---|---|---|
| `CONFIRMED` | Reservation valid, belum check-in | `CHECKED_IN`, `CANCELLED`, `EXPIRED` |
| `CHECKED_IN` | Driver sudah di parking, billing aktif | `CHECKED_OUT`, `CANCELLED` |
| `CHECKED_OUT` | Selesai parkir, invoice di-issue | (terminal) |
| `CANCELLED` | Reservation di-batalkan | (terminal) |
| `EXPIRED` | Auto-cancelled karena hold expired (no-show) | (terminal) |

#### `AssignmentMode`
| Value | Description |
|---|---|
| `SYSTEM` | Sistem auto-assign spot dengan floor terendah yang available |
| `USER` | Driver pilih spot spesifik (potensi kontensi, lock acquired) |

#### `BillingMode`
| Value | Description | Lihat |
|---|---|---|
| `AUTO` | Wallet auto-debit saat invoice issued | ADR-0014 |
| `MANUAL` | Driver harus call CreatePayment + scan QRIS | ADR-0014 |

#### `InvoiceStatus`
| Value | Description |
|---|---|
| `DRAFT` | Belum di-issue (sebelum checkout) |
| `ISSUED` | Siap dibayar |
| `PAID` | Sudah dibayar |
| `VOID` | Cancelled (e.g. reservation cancelled before checkout) |
| `OVERDUE` | Lewat grace period untuk MANUAL mode (ADR-0014) |

#### `LineItemType`
| Value | Description | Amount |
|---|---|---|
| `BOOKING_FEE` | Per reservation | 5000 IDR |
| `HOURLY` | Per started hour | 5000 IDR |
| `OVERNIGHT` | Flat kalau cross midnight | 20000 IDR |
| `NO_SHOW_PENALTY` | Booking fee dikenakan tetap kalau no-show | 5000 IDR |

#### `PaymentMethod`
| Value | Description |
|---|---|
| `QRIS` | QRIS payment (current only option) |

#### `PaymentStatus`
| Value | Description |
|---|---|
| `PENDING` | Belum dibayar |
| `SUCCESS` | Sukses |
| `FAILED` | Gagal |
| `EXPIRED` | QR code expired (15 menit) |

---

## Error Responses

Semua error responses follow consistent format:

```json
{
  "code": "INVALID_ARGUMENT",
  "message": "spot_id is required when mode=USER",
  "details": {
    "field": "spot_id",
    "request_id": "req-abc123"
  }
}
```

### Standard HTTP Status Codes

| HTTP Code | gRPC Code | When |
|---|---|---|
| 200 | `OK` | Success |
| 201 | `OK` | Created (POST returning new entity) |
| 400 | `INVALID_ARGUMENT` | Validation error, missing required field |
| 401 | `UNAUTHENTICATED` | Missing atau invalid JWT |
| 403 | `PERMISSION_DENIED` | Token valid tapi authorization failed (e.g. role) |
| 404 | `NOT_FOUND` | Resource ID tidak ada |
| 409 | `ALREADY_EXISTS` / `ABORTED` | Conflict (idempotency, optimistic lock) |
| 422 | `FAILED_PRECONDITION` | State machine violation (e.g. CheckIn on cancelled reservation) |
| 429 | `RESOURCE_EXHAUSTED` | Rate limit hit |
| 500 | `INTERNAL` | Unexpected server error |
| 502 | `UNAVAILABLE` | Downstream service unavailable (Midtrans, etc.) |
| 503 | `UNAVAILABLE` | Service degraded (health check failing) |

### Domain-Specific Error Codes

ParkirPintar-specific codes returned di response body `code` field:

| Code | HTTP | Service | Description |
|---|---|---|---|
| `SPOT_UNAVAILABLE` | 409 | reservation | Spot already reserved |
| `DRIVER_HAS_ACTIVE_RESERVATION` | 409 | reservation | Driver punya active reservation lain |
| `OVERDUE_INVOICE_BLOCKED` | 403 | reservation | Driver dengan unpaid invoice > grace (ADR-0014) |
| `INVALID_STATE` | 422 | reservation | State machine violation |
| `IDEMPOTENCY_CONFLICT` | 409 | gateway | Same key, payload berbeda |
| `MISSING_IDEMPOTENCY_KEY` | 400 | gateway | Header required, missing |
| `PAYMENT_EXISTS` | 409 | payment | Already exists for invoice |
| `PAYMENT_EXPIRED` | 400 | payment | QR code expired (15 min TTL) |
| `GATEWAY_ERROR` | 502 | payment | Midtrans timeout/error |
| `SIGNATURE_MISMATCH` | 403 | payment | Webhook signature verification failed |
| `INVOICE_NOT_ISSUED` | 422 | billing | Cannot pay invoice in DRAFT state |

### Example error responses

#### 400 — Missing field
```http
HTTP/1.1 400 Bad Request
Content-Type: application/json

{
  "code": "INVALID_ARGUMENT",
  "message": "driver_id is required",
  "details": {
    "field": "driver_id",
    "request_id": "req-abc123"
  }
}
```

#### 401 — Invalid token
```http
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer realm="parkirpintar"
Content-Type: application/json

{
  "code": "UNAUTHENTICATED",
  "message": "invalid or expired token"
}
```

#### 409 — Spot unavailable (double-book attempt)
```http
HTTP/1.1 409 Conflict
Content-Type: application/json

{
  "code": "SPOT_UNAVAILABLE",
  "message": "spot is already reserved",
  "details": {
    "spot_id": "550e8400-e29b-41d4-a716-446655440010",
    "request_id": "req-abc123"
  }
}
```

#### 429 — Rate limit
```http
HTTP/1.1 429 Too Many Requests
X-RateLimit-Limit: 20
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1716207600
Retry-After: 60
Content-Type: application/json

{
  "code": "RESOURCE_EXHAUSTED",
  "message": "rate limit exceeded for tier=medium",
  "details": {
    "tier": "medium",
    "request_id": "req-abc123"
  }
}
```

---

## End-to-End Example — Happy Path

Full reservation flow dari create sampai paid:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/dev/token \
  -H "X-Dev-Token-Secret: dev-secret" \
  -d '{"driver_id":"driver-demo-1"}' | jq -r '.token')

# 1. Check availability
curl -s http://localhost:8080/v1/availability | jq '.total_car_available'

# 2. Create reservation (SYSTEM mode)
RES=$(curl -s -X POST http://localhost:8080/v1/reservations \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{
    "driver_id": "driver-demo-1",
    "plate_no": "B 1234 XYZ",
    "vehicle_type": "CAR",
    "mode": "SYSTEM",
    "payment_mode": "MANUAL"
  }')
RES_ID=$(echo $RES | jq -r '.reservation.id')

# 3. Check in
curl -X POST http://localhost:8080/v1/reservations/$RES_ID:checkin \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{}'

# 4. (... parking duration ...)
sleep 5

# 5. Check out (trigger billing)
curl -X POST http://localhost:8080/v1/reservations/$RES_ID:checkout \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{}'

# 6. Get invoice
INVOICE=$(curl -s http://localhost:8080/v1/reservations/$RES_ID/invoice \
  -H "Authorization: Bearer $TOKEN")
INV_ID=$(echo $INVOICE | jq -r '.id')
echo "Invoice $INV_ID, total: $(echo $INVOICE | jq '.total.amount') IDR"

# 7. Create payment
PAYMENT=$(curl -s -X POST http://localhost:8080/v1/payments \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d "{\"invoice_id\": \"$INV_ID\", \"method\": \"QRIS\"}")

echo "QR URL: $(echo $PAYMENT | jq -r '.qr_url')"

# 8. Simulate Midtrans webhook (local dev only)
./scripts/simulate-midtrans-webhook.sh $INV_ID settlement

# 9. Verify invoice paid
curl -s http://localhost:8080/v1/invoices/$INV_ID \
  -H "Authorization: Bearer $TOKEN" | jq '.status'
# Expected: "PAID"
```

---

## Related Documentation

- [CHANGELOG.md](CHANGELOG.md) — API versioning + breaking change history
- [idempotency.md](idempotency.md) — Idempotency-Key contract detail
- [openapi.swagger.json](openapi.swagger.json) — Generated OpenAPI 2.0 spec
- [Postman Collection](../../test/postman/parkir-pintar-demo.postman_collection.json) — Ready-to-import
- [proto/](../../proto/) — Source of truth (gRPC schema)
- [docs/architecture/diagrams/sequences.md](../architecture/diagrams/sequences.md) — Sequence diagrams
- ADRs:
  - [ADR-0007](../architecture/adr/0007-idempotency.md) — Idempotency strategy
  - [ADR-0011](../architecture/adr/0011-anti-overlap-strategy.md) — Anti double-booking
  - [ADR-0014](../architecture/adr/0014-hybrid-payment-overdue-blocking.md) — Payment modes
  - [ADR-0015](../architecture/adr/0015-distributed-rate-limiting.md) — Rate limit tiers
  - [ADR-0016](../architecture/adr/0016-security-posture.md) — Auth + JWT
