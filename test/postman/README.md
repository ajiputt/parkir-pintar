# Postman Collection — Local Dev

Collection untuk test ParkirPintar end-to-end via **gateway** (port 8080).

## Arsitektur

```
Postman / curl
     │  HTTP/1.1
     ▼
┌──────────────┐  HTTP/2 + gRPC
│   Gateway    │ ─────────────────────────────┐
│  :8080 (REST)│                              │
└──────────────┘                              │
     │ grpc-gateway runtime mux                │
     │ (auto-routing dari .proto annotation)   │
     ▼                                         ▼
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ Reservation │  │   Billing   │  │   Payment   │
│   :9091     │  │    :9092    │  │    :9093    │
│   (gRPC)    │  │   (gRPC)    │  │   (gRPC)    │
└─────────────┘  └─────────────┘  └─────────────┘
                                          │
                                          │ gRPC (Payment → Billing)
                                          ▼
                                    [Billing :9092]
```

Public client cuma kontak gateway. Antar service pakai gRPC. NATS jalan paralel untuk async events.

## Cara Pakai

1. **Install Postman**: https://www.postman.com/downloads/

2. **Import collection**:
   - Postman → File → Import → pilih `ParkirPintar-Local.postman_collection.json`

3. **Run service stack** (urutan):
   ```powershell
   # Terminal 1 — Reservation
   $env:RESERVATION_GRPC_ADDR=":9091"; cd services\reservation; go run .\cmd

   # Terminal 2 — Billing
   $env:BILLING_GRPC_ADDR=":9092"; cd services\billing; go run .\cmd

   # Terminal 3 — Payment (point ke billing gRPC)
   $env:PAYMENT_GRPC_ADDR=":9093"; $env:BILLING_GRPC_ADDR_DIAL="localhost:9092"; cd services\payment; go run .\cmd

   # Terminal 4 — Gateway
   $env:GATEWAY_HTTP_PORT=":8080"
   $env:RESERVATION_GRPC_ADDR_DIAL="localhost:9091"
   $env:BILLING_GRPC_ADDR_DIAL="localhost:9092"
   $env:PAYMENT_GRPC_ADDR_DIAL="localhost:9093"
   cd services\gateway; go run .\cmd
   ```

4. **Run request**:
   - Smoke: **Health & Info** → **Healthz**
   - Happy path: **Availability** → **Reservation Lifecycle** → (event triggers Billing) → **Billing → Get Invoice by Reservation** → **Payment → Create Payment** → **Get Payment**

## Struktur Collection

| Folder | Kapan dipakai | Endpoint |
|---|---|---|
| **Health & Info** | Smoke test gateway | `{{baseUrl}}/healthz` |
| **Availability (Read)** | Verify spot data | `GET /v1/availability` |
| **Reservation Lifecycle** | Test happy path booking | `POST /v1/reservations`, `:checkin`, `:checkout`, `:cancel` |
| **Idempotency Tests** | Verify dedup behavior | Same key → cached response |
| **Error Cases** | Validation tests | Missing fields, bad UUIDs |
| **Billing Service** | Test invoice query | `GET /v1/invoices/{id}`, `GET /v1/reservations/{id}/invoice` |
| **Payment Service** | Test QRIS + webhook | `POST /v1/payments`, `GET /v1/payments/{id}`, webhook |

## Variables

| Variable | Source | Purpose |
|---|---|---|
| `baseUrl` | static | Gateway URL `http://localhost:8080` (default) |
| `reservationDirectUrl` | static | Reservation health probe `http://localhost:9191` |
| `billingDirectUrl` | static | Billing health probe `http://localhost:9192` |
| `paymentDirectUrl` | static | Payment health probe `http://localhost:9193` |
| `reservationId` | Auto-set dari `Create Reservation` test script | Dipakai Check In/Out, Cancel, Get Invoice |
| `spotId` | Auto-set dari `Create Reservation` | Dipakai Create User-selected |
| `invoiceId` | Manual setelah cek billing | Dipakai Create Payment |
| `paymentId` | Auto-set dari `Create Payment` | Dipakai Get Payment |
| `idempotencyKey` | Pre-request script (per request) | Mutating endpoint header |
| `fixedIdemKey` | Manual untuk idempotency test | Sama untuk replay |

## Catatan Penting

### Routing Source of Truth

Semua REST path di-derive otomatis dari `option (google.api.http)` annotation di `proto/{service}/v1/*.proto`. Mau ubah path? Edit proto, run `make proto`, restart gateway. **Jangan edit gateway main.go** — `runtime.NewServeMux` reads dari registered handlers.

### Webhook Khusus

`POST /v1/payments/midtrans/notification` adalah satu-satunya HTTP route di gateway yang **bukan** grpc-gateway. Diteruskan verbatim (raw body + headers) ke payment HTTP `:9193` karena Midtrans signature di-verify pada raw bytes.

### Direct Service URL untuk Debugging

`{{billingDirectUrl}}/healthz` dan sejenisnya bypass gateway, useful saat menyelidiki apakah masalah di gateway atau service itu sendiri.

## Debugging

### "Could not get any response" di gateway endpoint

Gateway tidak running, atau salah satu backend gRPC tidak bisa di-dial. Cek:
```powershell
curl.exe http://localhost:8080/healthz
curl.exe http://localhost:9191/healthz   # reservation
curl.exe http://localhost:9192/healthz   # billing
curl.exe http://localhost:9193/healthz   # payment
```

### Gateway log: "register reservation handler: connection refused"

Backend service belum running saat gateway startup. grpc-gateway dial saat init (eager). Solusi: jalanin service backend dulu, baru gateway.

### "404 Not Found" untuk endpoint business

Path tidak match annotation. Verify proto annotation, run `make proto`, rebuild gateway.

### "500 Internal Server Error" + log "relation reservation does not exist"

Migration belum apply atau search_path issue. Re-apply:
```powershell
psql -U gopark -d gopark -f migrations\reservation\001_init.up.sql
```

### Idempotency test gagal: response berbeda

Cek log service. Kalau ada warning `idempotency: store error`, tabel `idempotency_keys` belum ada. Re-run migration.

## Run via Newman (CLI)

```bash
npm install -g newman
newman run test/postman/ParkirPintar-Local.postman_collection.json \
  --env-var baseUrl=http://localhost:8080 \
  --reporters cli,json --reporter-json-export newman-result.json
```

Cocok untuk integrasi ke CI/CD pipeline (`.github/workflows/e2e.yml`).
