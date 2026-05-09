# Payment Service

QRIS payment via **Midtrans Sandbox** integration. Bagian dari microservices ParkirPintar.

## Quick Reference

| Endpoint | Method | Description |
|---|---|---|
| `/healthz` | GET | DB readiness probe |
| `/v1/payments` | POST | Create QRIS payment (idempotent, support `Idempotency-Key`) |
| `/v1/payments/{id}` | GET | Get payment status |
| `/v1/payments/midtrans/notification` | POST | Webhook dari Midtrans (verify SHA512 signature) |

## Environment

| Var | Default | Purpose |
|---|---|---|
| `PAYMENT_HTTP_ADDR` | `:9193` | HTTP listen |
| `DB_URL` | (compose) | Postgres DSN dengan `search_path=payment` |
| `NATS_URL` | `nats://localhost:4222` | Event bus |
| `BILLING_HTTP_URL` | `http://billing:9192` | Billing service untuk lookup invoice amount |
| `BILLING_STUB` | `false` | `true` → stub invoice (fixed 30k IDR) untuk smoke test |
| `PAYMENT_MOCK_MODE` | `true` | `true` → mock Midtrans, `false` → real sandbox call |
| `MIDTRANS_SERVER_KEY` | — | `SB-Mid-server-XXX` dari dashboard sandbox |
| `MIDTRANS_BASE_URL` | `https://api.sandbox.midtrans.com` | sandbox vs prod |

## Architecture

Hexagonal layering:

```
internal/
├── domain/               # Payment aggregate, status transitions
├── usecase/              # CreatePayment, HandleMidtransNotification, GetPayment
└── adapter/
    ├── midtrans/         # HTTP client + signature verify + mock fallback
    ├── billingclient/    # HTTP client ke billing service (with circuit breaker)
    ├── postgres/         # Payment repo + webhook_log
    └── nats/             # Event publisher (PaymentSucceeded/Failed)
```

## Production Features

- **Idempotency-Key**: Server-side dedup via `pkg/idempotency` (24h TTL).
- **Idempotent QR generation**: Same invoice → return existing PENDING payment.
- **Circuit breaker**: `gobreaker` di billingclient (5 consecutive failures → open).
- **Signature verification**: SHA512 algoritma resmi Midtrans, `constantTimeEqual` anti-timing-attack.
- **Webhook audit trail**: Semua webhook (verified atau invalid) di-log ke `webhook_log` table.
- **Idempotent webhook handler**: Domain `MarkSuccess/Failed/Expired` return nil kalau sudah di state target — safe untuk Midtrans redelivery.
- **Status mapping lengkap**: settlement, capture (+ fraud_status), deny, expire, cancel, failure, pending, unknown.
- **Graceful degradation**: Kalau NATS down, payment tetap bisa dibuat (publisher Noop fallback).

## Test

```bash
# Unit tests (domain logic, signature verify, webhook handler)
cd services/payment
go test -race -v ./...

# Mock end-to-end
make demo-up
./scripts/demo.sh

# Simulate Midtrans webhook
./scripts/simulate-midtrans-webhook.sh <payment_id> settlement
./scripts/simulate-midtrans-webhook.sh <payment_id> expire
./scripts/simulate-midtrans-webhook.sh <payment_id> deny
```

## See Also

- [Midtrans integration runbook](../../docs/runbooks/midtrans-integration.md) — full sandbox setup, troubleshooting, security
- [ADR-0007 Idempotency](../../docs/architecture/adr/0007-idempotency.md)
- [Threat model — Payment](../../docs/security/threat-model.md#payment-service)
