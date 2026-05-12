# Postman Collection — ParkirPintar Demo

Collection siap-pakai untuk demo Smart Parking Marketplace. Semua skenario di-organize per folder dan bisa dijalankan urut via Collection Runner.

## Files

| File | Purpose |
|------|---------|
| `parkir-pintar-demo.postman_collection.json` | Collection utama, 14 folder skenario |
| `local.postman_environment.json` | Environment vars untuk local dev (localhost:8080) |
| `aws.postman_environment.json` | Environment vars untuk AWS EKS deployment (ALB DNS) |

## Import

1. Buka Postman → **Import** → drop collection + environment file yang relevan
2. Pilih environment di dropdown kanan-atas:
   - **"ParkirPintar - Local"** untuk testing local docker-compose / standalone services
   - **"ParkirPintar - AWS EKS"** untuk testing deployment AWS (replace baseUrl dulu dengan ALB DNS)
3. Pastikan semua service running (lihat prerequisite di bawah untuk local, atau pods Running di AWS)

### Setup AWS environment

Setelah Helm deploy ke EKS, ambil ALB DNS:

```powershell
$alb = kubectl get ingress -n parkir -o jsonpath='{.items[0].status.loadBalancer.ingress[0].hostname}'

# Replace placeholder di env file
$content = Get-Content test/postman/aws.postman_environment.json -Raw
$content -replace 'REPLACE-WITH-ALB-DNS\.ap-southeast-3\.elb\.amazonaws\.com', $alb | Set-Content test/postman/aws.postman_environment.json -NoNewline
```

## Prerequisite

Service yang harus up sebelum mulai:

```
postgres      :5432
redis         :6379
nats          :4222
gateway       :8080  (HTTP entrypoint)
reservation   :9091  (gRPC) + :9191 (HTTP probes)
billing       :9092  (gRPC) + :9192 (HTTP probes)
payment       :9093  (gRPC) + :9193 (HTTP probes + webhook)
notification           (NATS subscriber, no public port)
```

Cara start cepat:

```powershell
# Dari repo root
docker compose up -d postgres redis nats
.\scripts\dev\run-reservation.ps1
.\scripts\dev\run-billing.ps1
.\scripts\dev\run-payment.ps1
.\scripts\dev\run-notification.ps1
.\scripts\dev\run-gateway.ps1
```

Quick health check sebelum demo:

```bash
curl http://localhost:8080/readyz | jq
```

Semua check harus `status: ready`.

## Folder Structure

| # | Folder | Demonstrates |
|---|--------|--------------|
| 00 | Auth | Issue JWT via dev-token endpoint (auto-set ke env) |
| 01 | Happy Flow: AUTO Payment | E2E auto-debit (no QRIS) |
| 02 | Happy Flow: MANUAL Payment | E2E QRIS scan flow |
| 03 | Expired Flow | ExpiryWorker auto-set EXPIRED + no-show penalty |
| 04 | Overnight Stay | Pricing OVERNIGHT 20000 IDR flat |
| 05 | Cancel After CONFIRMED | Booking fee charged (ADR-0014) |
| 06 | Manual Until Blocked | OVERDUE worker → driver block |
| 07 | Idempotency | Same key = same response (Stripe-style) |
| 08 | One Driver, One Active | RES-031 enforcement |
| 09 | User-Selected Spot | mode=USER + spot_id |
| 10 | Concurrent Booking | Race condition (run via Runner) |
| 11 | Rate Limit Burst | Heavy tier 5rps burst 10 → 429 |
| 12 | Health & Readiness | Probe gateway + tiap service |
| 13 | Webhook Simulation | Midtrans push (mock + signature fail) |
| 14 | Reference | Per-resource single requests |

## How to Run

### Demo manual (per request)

Buka folder → klik request → **Send**. Test scripts otomatis ekstrak ID ke env (bisa lihat di Console untuk verify).

### Demo otomatis (per folder via Runner)

1. Right-click folder → **Run folder**
2. Klik **Run ParkirPintar — Demo Scenarios**
3. Tunggu sampai semua hijau

Untuk skenario yang butuh waktu (expired, overdue), set timer manual atau pakai env override (lihat tip di tiap folder description).

### Demo skenario khusus

**Folder 10 — Concurrent Booking:**
- Run dengan `Iterations: 5`, `Delay: 0`, `Persist environment: yes`
- Atau pakai bash one-liner di description folder

**Folder 11 — Rate Limit Burst:**
- Run dengan `Iterations: 15`, `Delay: 0`
- Iterasi 1-10 → 200, iterasi 11+ → 429

## Variable Conventions

| Var | Auto-set by | Description |
|-----|-------------|-------------|
| `baseUrl` | env (manual) | `http://localhost:8080` |
| `driver_id` | env / dev-token | UUID driver |
| `jwt_token` | dev-token test | Bearer token (collection-level auth) |
| `reservation_id` | CreateReservation test | Latest reservation |
| `invoice_id` | GetInvoice test | Latest invoice |
| `payment_id` | CreatePayment test | Latest payment |
| `spot_id`, `user_spot_id` | response data | Spot ID picked |
| `idempotency_key` | collection prerequest | Auto-gen kalau kosong |

## Demo Tips

### Speed up "expired" demo
Set di `services/reservation/.env.local`:
```
RESERVATION_HOLD_DURATION=30s
RESERVATION_EXPIRY_SCAN_INTERVAL=5s
```
Restart reservation service. Sekarang reservation expire dalam ~30 detik.

### Speed up "overdue" demo
Set di `services/billing/.env.local`:
```
BILLING_OVERDUE_GRACE=30s
BILLING_OVERDUE_INTERVAL=10s
```
Restart billing service.

### Backdate untuk overnight demo
Run SQL langsung:
```sql
UPDATE reservation.reservation
SET checkin_at = NOW() - INTERVAL '12 hour'
WHERE id = '<reservation_id>';
```

### Auth modes
- `AUTH_PASSTHROUGH_NO_TOKEN=true` (default): demo gak perlu JWT, request tanpa Bearer tetap lewat
- `AUTH_PASSTHROUGH_NO_TOKEN=false`: enforce mode, wajib jalankan **00 — Auth** dulu

### Webhook signature (live mode)
Kalau `PAYMENT_MOCK_MODE=false`, signature di-verify SHA-512:
```
sha512(order_id + status_code + gross_amount + server_key)
```
Generate di terminal:
```bash
echo -n "ORDER_ID200.5000.00YOUR_SERVER_KEY" | sha512sum
```
Lalu paste hasil hex ke header `signature_key` di body.

## Recommended Demo Order (15 menit)

1. **00 — Auth** (jaga-jaga kalau enforce mode)
2. **12 — Health** › **Readyz gateway** (tunjukin all green)
3. **01 — Happy Flow AUTO** (E2E, ~30 detik)
4. **02 — Happy Flow MANUAL** (E2E + QRIS)
5. **07 — Idempotency** (replay same key)
6. **08 — One Driver, One Active** (business rule)
7. **05 — Cancel After Confirmed** (booking fee, bukan VOID)
8. **11 — Rate Limit Burst** (Run 15× → lihat 429)
9. **06 — Manual Until Blocked** (kalau ada waktu, perlu wait grace)

Skenario 03/04/10 di-mention saja kalau interviewer tanya, atau jalankan sebagai "follow-up demo" pakai env override.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| `connection refused :8080` | Gateway down | `.\scripts\dev\run-gateway.ps1` |
| `connection refused :9091` (di /readyz) | Reservation backend down | Start reservation service |
| 401 di semua request | `AUTH_PASSTHROUGH_NO_TOKEN=false` + jwt_token kosong | Run **00 — Auth › Get Dev Token** dulu |
| Idempotency error 422 | DB schema belum migrated | `migrate -path migrations/* -database "$DB_URL" up` |
| 429 unexpected | Rate limit hit (Redis state) | Tunggu 1 detik atau `redis-cli FLUSHDB` |
| `payment_mode field unknown` | proto stub belum re-generated | `make proto` |

## Update Collection

Kalau proto endpoint berubah:

1. Edit file collection JSON manual, atau
2. Hapus + import ulang dari Postman (export ke folder ini, commit)

Untuk import dari OpenAPI spec (auto-gen, less customization): `http://localhost:8080/openapi.json` → Postman Import → URL.
