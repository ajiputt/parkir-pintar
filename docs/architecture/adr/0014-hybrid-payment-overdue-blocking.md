# ADR-0014: Hybrid Payment Mode + Overdue Worker + Driver Blocking

- **Status**: Accepted
- **Date**: 2026-05-05
- **Tags**: payment, billing, domain, ops, events
- **Related**: ADR-0006 (event sourcing billing), ADR-0012 (auto-pay)
- **Supersedes**: bagian "auto-pay always-on" di ADR-0012 (sekarang per-reservation toggle)

## Context

ADR-0012 implement auto-pay sebagai service-level toggle (`PAYMENT_AUTO_PAY=true`).
Semua invoice ISSUED langsung di-charge mock wallet. Bagus untuk demo, tapi:

1. **Tidak realistic** untuk Indonesian market — banyak driver belum pakai
   wallet system, lebih familiar QRIS scan-and-pay.
2. **No flexibility** — single mode, tidak bisa per-driver atau per-reservation.
3. **Tidak demonstrate "user gak bayar" handling**.

## Decision

### 1. Hybrid payment mode (per-reservation choice)

Driver wajib pilih saat booking:
- `AUTO` — wallet auto-debit, no friction
- `MANUAL` — QRIS scan flow, risk overdue

### 2. Overdue worker (Tier 2)

Background scan invoice MANUAL + ISSUED + age > grace period (15 min default).
Mark OVERDUE + publish event → notification email reminder.

State machine: `DRAFT → ISSUED → PAID` atau `DRAFT → ISSUED → OVERDUE → PAID`.

### 3. Driver blocking

Reservation pre-check via billing gRPC `CountOverdueByDriver(driver_id)`.
Driver dengan overdue > 0 → 409 `RES-032 ErrDriverHasOverdueInvoice`.

Graceful degradation: billing down → NoopChecker, booking proceed.

## Trade-offs

| Pros | Cons |
|---|---|
| Driver flexibility | Cross-service latency +5-20ms |
| Realistic Indonesian market | Worker overhead (minimal) |
| Demonstrate payment failure handling | Migration backfill complexity |
| Event-driven, consistent with ExpiryWorker | Billing-down = no blocking |

## Configuration

```dotenv
BILLING_OVERDUE_INTERVAL=5m
BILLING_OVERDUE_GRACE=15m
BILLING_GRPC_ADDR_DIAL=localhost:9092
```

## Future Work

- Wallet domain service untuk balance management
- Escalation ladder (15min → 1h → 24h → 7d)
- Admin endpoint untuk write-off
- Stripe-style pre-auth saat CONFIRMED
- Driver banned table (hard block setelah X overdue)

## References

- ADR-0006, ADR-0011, ADR-0012, ADR-0013
- [Grab Cancellation Policy](https://help.grab.com/)
- [Stripe Pre-authorization](https://stripe.com/docs/payments/place-a-hold-on-a-payment-method)
