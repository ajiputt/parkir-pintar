# ADR-0012: Billing Policy + Auto-Pay (Mock Wallet)

- **Status**: Accepted
- **Date**: 2026-05-05
- **Tags**: billing, payment, domain, events
- **Related**: ADR-0006 (event sourcing billing), ADR-0007 (idempotency)

## Context

Sebelum ADR ini, billing handler punya inkonsistensi:

1. **CONFIRMED** → invoice DRAFT dengan booking_fee
2. **CHECKED_OUT** → tambah hourly + overnight, ISSUE
3. **EXPIRED** → tambah no_show_penalty, ISSUE
4. **CANCELLED** → invoice **VOID** (booking fee TIDAK ditagih)

Masalah dengan VOID di CANCELLED:
- **Abuse vector**: driver bisa book → cancel berulang, lock spot inventory tanpa penalty
- **Inconsistent**: EXPIRED kena penalty (no-show), CANCELLED tidak (intentional cancel = "punishment" lebih ringan padahal sama-sama waste resource)
- **Industry mismatch**: Grab/Gojek/hotel booking semua charge cancel fee setelah confirm

Plus, payment flow saat ini **manual**: driver harus call `POST /v1/payments` setelah invoice ISSUED untuk generate QRIS, lalu scan untuk bayar. Risk:
- Driver gak bayar setelah CHECKED_OUT → invoice stuck ISSUED forever
- Tidak ada force collection untuk booking_fee saat CANCELLED
- UX friction tambah step

## Decision

### 1. Billing policy: booking fee ALWAYS charged on terminal state

| Reservation State | Lines added | Total | Status outcome |
|---|---|---|---|
| **CHECKED_OUT** (complete) | booking_fee + hourly × N + overnight (jika lewat tengah malam) | 5,000 + (N × 5,000) + 20,000 | ISSUED → auto-PAID |
| **EXPIRED** (no-show by worker) | booking_fee + no_show_penalty | 5,000 + 5,000 = 10,000 | ISSUED → auto-PAID |
| **CANCELLED** (driver-initiated) | booking_fee saja | 5,000 | ISSUED → auto-PAID |

Rule: **sekali driver berhasil book (CONFIRMED), booking_fee 5,000 IDR ditagih regardless of terminal state.** Tidak ada VOID. Konsisten + aligned with industri.

### 2. Auto-pay model (mock wallet)

Saat invoice ISSUED, payment service auto-charge:
1. Listen NATS subject `billing.invoice.issued.v1`
2. Create Payment record dengan `gateway="MOCK"` + `amount = invoice.total`
3. Immediately `MarkSuccess()` (mock = unlimited wallet, always succeed)
4. Publish `payment.succeeded.v1`
5. Billing handler `HandlePaymentSucceeded` mark invoice PAID

Toggleable via env `PAYMENT_AUTO_PAY=true|false`:
- `true` (demo, default): event-driven auto-charge
- `false` (production traditional): manual flow via `POST /v1/payments` + Midtrans QRIS

Independent dari `PAYMENT_MOCK_MODE` — kombinasi:

| AUTO_PAY | MOCK_MODE | Flow |
|---|---|---|
| true | true | **Demo** — instant auto-charge, mock gateway |
| true | false | Production wallet — auto-charge real Midtrans (butuh stored token) |
| false | true | Manual flow + mock Midtrans (QRIS string fake) |
| false | false | Manual flow + real Midtrans (production legacy) |

## Implementation

### Billing service — Cancel handler change

```go
// services/billing/internal/usecase/handlers.go
func (s *Service) HandleReservationCancelled(ctx context.Context, env eventbus.Envelope) error {
    // ... seen check ...
    inv, err := s.Invoices.GetByReservationID(ctx, resID)
    if err != nil {
        // Race-safe: kalau ConfirmedHandler belum sempat create, do it now
        inv = domain.New(resID, pl.DriverID)
        inv.AddLine(s.Engine.BookingLine())
    }
    inv.Issue(time.Now().UTC())               // ← was: Status = VOID
    s.Invoices.Save(ctx, inv)
    s.Pub.PublishInvoiceIssued(ctx, inv)      // ← trigger auto-pay
    return s.Events.Append(ctx, env)
}
```

### Payment service — auto-pay subscriber

```go
// services/payment/internal/usecase/service.go
func (s *Service) HandleInvoiceIssued(ctx context.Context, env eventbus.Envelope) error {
    if !s.AutoPayMode {
        return nil // manual mode → skip
    }
    pl, _ := eventbus.Decode[InvoiceIssuedPayload](env)
    invoiceID, _ := uuid.Parse(pl.InvoiceID)

    // Idempotency: payment for invoice already SUCCESS? skip.
    if existing, err := s.Payments.GetByInvoiceID(ctx, invoiceID); err == nil && existing != nil {
        if existing.Status == domain.StatusSuccess {
            return nil
        }
        // PENDING (manual flow) → upgrade ke SUCCESS
        existing.MarkSuccess(s.now(), "auto-"+...)
        s.Payments.Save(ctx, existing)
        return s.Pub.PublishPaymentSucceeded(ctx, existing)
    }

    // Create + immediate SUCCESS
    p := domain.New(invoiceID, MethodQRIS, money.IDR(pl.TotalIDR))
    p.Gateway = "MOCK"
    p.MarkSuccess(s.now(), "auto-"+p.ID.String()[:8])
    s.Payments.Save(ctx, p)
    return s.Pub.PublishPaymentSucceeded(ctx, p)
}
```

### NATS topology (after change)

```
RESERVATION                BILLING                 PAYMENT
   │                          │                       │
   │── reservation.confirmed ─→│                      │
   │                          │ DRAFT invoice         │
   │                          │                       │
   │── reservation.cancelled ─→│ ← (one of these triggers)
   │── reservation.expired ───→│                      │
   │── reservation.checked_out→│                      │
   │                          │ Add lines + ISSUE     │
   │                          │── billing.invoice.issued ──→│
   │                          │                       │ AutoCharge mock SUCCESS
   │                          │←── payment.succeeded ─│
   │                          │ Mark PAID             │
   │                          │── billing.invoice.paid ───→ (notification)
```

Bidirectional pub-sub: billing publishes invoice.*, payment publishes payment.*. Each subscribes to other's events. Loose coupling, no circular dep at code level (events are async data).

## Consequences

### Positive

- ✅ Booking fee selalu ditagih → no abuse vector (cancel berulang)
- ✅ Konsistensi billing antar terminal states
- ✅ Auto-pay = guaranteed collection, no UX friction
- ✅ Toggleable mode (demo vs production)
- ✅ Decoupled via events — easy to plug real Midtrans later
- ✅ Idempotent dedup (Payment.GetByInvoiceID check)
- ✅ Backward-compatible — manual `Create Payment` endpoint tetap works

### Negative

- ⚠️ Driver tidak ada UI confirmation (bukan masalah real saat wallet pre-loaded)
- ⚠️ Mock mode = unlimited wallet — production butuh real wallet domain (saldo, top-up, withdraw)
- ⚠️ Refund flow not implemented — kalau dispute, perlu manual VOID + adjust
- ⚠️ Single charge per reservation (di terminal state) → driver gak ke-charge selama parking session active. OK untuk demo, real-world might want pre-auth.

### Neutral

- 📝 NATS subscription baru (`billing.invoice.issued`) di payment service. Durable consumer name `payment-invoice-issued` untuk replay capability.
- 📝 Event payload `InvoiceIssuedPayload` mirror antara billing publisher dan payment subscriber — duplikasi struct kecil tapi acceptable untuk loose coupling. Future: extract ke shared schema (proto event).

## Future Work

- **Real wallet domain**: balance management, top-up endpoint, transaction log. Pre-charge booking_fee at CONFIRMED (not at terminal state).
- **Pre-authorization**: hold amount saat CONFIRMED, capture saat terminal. Standard untuk credit card.
- **Refund flow**: Cancel → partial/full refund. Domain `RefundIntent` aggregate.
- **Cancellation tier**: cancel < 5 min after confirm = no charge. > 5 min = booking fee. Out of scope demo.

## References

- ADR-0006 (event sourcing billing — events_log dedup pattern)
- ADR-0007 (idempotency — kombinasi dengan auto-pay subscriber)
- [Midtrans CoreAPI charge docs](https://docs.midtrans.com/reference/charge-api)
- [Stripe Pre-authorization patterns](https://stripe.com/docs/payments/place-a-hold-on-a-payment-method)
