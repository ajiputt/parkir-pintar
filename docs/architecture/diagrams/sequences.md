# Sequence Diagrams — Critical Business Flows

Sequence diagrams untuk complex multi-service flows di ParkirPintar.
Format: Mermaid. View di GitHub atau VSCode Markdown Preview Enhanced.

## 1. Reservation Lifecycle — Happy Path

End-to-end happy path dari driver open app → check-out + invoice paid.

```mermaid
sequenceDiagram
    autonumber
    actor Driver
    participant Gateway
    participant Reservation
    participant Redis
    participant Postgres
    participant NATS
    participant Billing
    participant Payment
    participant Midtrans
    participant Notification

    Note over Driver,Notification: Phase 1 — Search & Reserve

    Driver->>Gateway: GET /v1/availability
    Gateway->>Reservation: gRPC GetAvailability
    Reservation->>Postgres: SELECT spots WHERE status='AVAILABLE'
    Postgres-->>Reservation: 150 spots
    Reservation-->>Gateway: AvailabilityResponse
    Gateway-->>Driver: 200 OK + spot list

    Driver->>Gateway: POST /v1/reservations<br/>Idempotency-Key: uuid
    Gateway->>Reservation: gRPC CreateReservation

    Reservation->>Redis: SET NX lock:spot:abc TTL 10s
    Redis-->>Reservation: OK (acquired)

    Reservation->>Postgres: BEGIN<br/>SELECT spot FOR UPDATE
    Postgres-->>Reservation: spot row<br/>(active reservation check)

    Reservation->>Postgres: INSERT reservation (state=CONFIRMED)<br/>UPDATE spot.status='HELD'<br/>COMMIT
    Postgres-->>Reservation: success

    Reservation->>NATS: publish<br/>reservation.confirmed
    Reservation->>Redis: DEL lock:spot:abc
    Reservation-->>Gateway: ReservationCreated
    Gateway-->>Driver: 200 OK + reservation_id

    Note over NATS,Billing: Phase 2 — Async Invoice Creation

    NATS->>Billing: reservation.confirmed event
    Billing->>Postgres: INSERT invoice (status=ISSUED)
    Billing->>NATS: publish<br/>invoice.issued
    NATS->>Notification: invoice.issued event
    Notification->>Driver: Email confirmation

    Note over Driver,Postgres: Phase 3 — Check In

    Driver->>Gateway: POST /v1/reservations/{id}/checkin
    Gateway->>Reservation: gRPC CheckIn
    Reservation->>Postgres: UPDATE reservation state=CHECKED_IN<br/>UPDATE spot status=OCCUPIED
    Reservation->>NATS: publish reservation.checked_in
    Reservation-->>Gateway: success
    Gateway-->>Driver: 200 OK

    Note over Driver,Midtrans: Phase 4 — Check Out + Pay

    Driver->>Gateway: POST /v1/reservations/{id}/checkout
    Gateway->>Reservation: gRPC CheckOut
    Reservation->>Postgres: UPDATE reservation state=CHECKED_OUT<br/>UPDATE spot status=AVAILABLE
    Reservation->>NATS: publish reservation.checked_out
    
    NATS->>Billing: reservation.checked_out event
    Billing->>Postgres: UPDATE invoice<br/>(calculate final amount)
    Billing->>NATS: publish invoice.finalized
    
    Driver->>Gateway: POST /v1/payments<br/>Idempotency-Key: uuid
    Gateway->>Payment: gRPC CreatePayment
    Payment->>Billing: gRPC GetInvoice
    Billing-->>Payment: invoice details
    Payment->>Midtrans: Create QRIS charge
    Midtrans-->>Payment: snap_url + payment_id
    Payment->>Postgres: INSERT payment (status=PENDING)
    Payment-->>Gateway: snap_url
    Gateway-->>Driver: 200 OK + QR code URL

    Driver->>Midtrans: Scan QRIS + pay
    Midtrans->>Gateway: POST /v1/payments/midtrans/notification<br/>(webhook + signature)
    Gateway->>Payment: gRPC HandleWebhook
    Payment->>Payment: Verify signature
    Payment->>Postgres: UPDATE payment status=SUCCESS
    Payment->>NATS: publish payment.succeeded
    
    NATS->>Billing: payment.succeeded event
    Billing->>Postgres: UPDATE invoice status=PAID
    Billing->>NATS: publish invoice.paid
    NATS->>Notification: invoice.paid event
    Notification->>Driver: Email receipt
```

## 2. Reservation Expiry — No-show Flow

Driver tidak check-in dalam 1 jam → auto-expire + fee.

```mermaid
sequenceDiagram
    autonumber
    participant Worker as ExpiryWorker<br/>(reservation)
    participant Postgres
    participant NATS
    participant Billing
    participant Notification
    actor Driver

    Note over Worker: Run every 30 detik

    Worker->>Postgres: SELECT reservations WHERE<br/>state=CONFIRMED AND<br/>expires_at < NOW()
    Postgres-->>Worker: expired list (batch 100)

    loop Untuk setiap expired
        Worker->>Postgres: BEGIN<br/>UPDATE reservation state=EXPIRED<br/>UPDATE spot status=AVAILABLE<br/>COMMIT
        Worker->>NATS: publish reservation.expired
    end

    NATS->>Billing: reservation.expired event
    Billing->>Postgres: UPDATE invoice<br/>amount = 5000 (booking fee only)<br/>status=ISSUED (still owed)
    Billing->>NATS: publish invoice.issued

    NATS->>Notification: invoice.issued (expired type)
    Notification->>Driver: Email "Reservation expired,<br/>booking fee 5000 IDR due"
```

## 3. Overdue Invoice — Driver Blocking (ADR-0014)

Driver dengan unpaid invoice > 24h tidak bisa create new reservation.

```mermaid
sequenceDiagram
    autonumber
    actor Driver
    participant Gateway
    participant Reservation
    participant Billing
    participant Postgres

    Driver->>Gateway: POST /v1/reservations
    Gateway->>Reservation: gRPC CreateReservation

    Note over Reservation,Billing: Overdue pre-check (ADR-0014)

    Reservation->>Billing: gRPC CountOverdueByDriver<br/>(circuit-breaker wrapped)

    alt Billing healthy + driver has overdue
        Billing->>Postgres: SELECT COUNT(*) WHERE<br/>driver_id=$1 AND<br/>status='OVERDUE'
        Postgres-->>Billing: count=2
        Billing-->>Reservation: count=2
        Reservation-->>Gateway: PERMISSION_DENIED<br/>(OVERDUE_INVOICE_BLOCKED)
        Gateway-->>Driver: 403 Forbidden
    else Billing healthy + driver clean
        Billing-->>Reservation: count=0
        Reservation->>Reservation: Continue normal flow
        Reservation-->>Gateway: ReservationCreated
        Gateway-->>Driver: 200 OK
    else Billing unreachable (circuit-breaker open)
        Note over Reservation: Graceful degradation:<br/>NoopChecker returns 0<br/>(don't block driver)
        Reservation->>Reservation: Continue normal flow
        Reservation-->>Gateway: ReservationCreated
        Gateway-->>Driver: 200 OK
    end
```

## 4. Concurrent Same-Spot Reservation — Anti Double-Booking

2 drivers race untuk same spot. Hexagonal locks ensure exclusive.

```mermaid
sequenceDiagram
    autonumber
    actor DriverA
    actor DriverB
    participant Gateway
    participant Reservation
    participant Redis
    participant Postgres

    par Concurrent requests
        DriverA->>Gateway: POST /v1/reservations<br/>spot=abc
        DriverB->>Gateway: POST /v1/reservations<br/>spot=abc
    end

    Gateway->>Reservation: gRPC CreateReservation (A)
    Gateway->>Reservation: gRPC CreateReservation (B)

    Note over Reservation,Redis: Layer 1: Redis distributed lock

    Reservation->>Redis: SET NX lock:spot:abc (A)
    Reservation->>Redis: SET NX lock:spot:abc (B)
    Redis-->>Reservation: OK (A wins)
    Redis-->>Reservation: nil (B fails)

    par
        Note over Reservation: A: continue
        Reservation->>Postgres: BEGIN<br/>INSERT reservation<br/>(driver_id=A, spot_id=abc)
    and
        Note over Reservation: B: lock contention
        Reservation-->>Gateway: RESOURCE_EXHAUSTED<br/>(SPOT_LOCK_CONTENTION)
        Gateway-->>DriverB: 409 Conflict<br/>"Try another spot"
    end

    Note over Reservation,Postgres: Layer 2: DB unique constraint<br/>(defense in depth, ADR-0011)

    alt Race past Redis (very rare)
        Reservation->>Postgres: INSERT reservation<br/>(driver_id=B, spot_id=abc)
        Postgres-->>Reservation: ERROR 23505<br/>(one_active_reservation_per_spot)
        Reservation->>Reservation: Map to domain.ErrSpotUnavailable
        Reservation-->>Gateway: ALREADY_EXISTS
        Gateway-->>DriverB: 409 Conflict
    end

    Postgres-->>Reservation: success (A)
    Reservation->>Redis: DEL lock:spot:abc
    Reservation-->>Gateway: ReservationCreated (A)
    Gateway-->>DriverA: 200 OK
```

## 5. Payment Webhook — Signature Verification

Midtrans webhook callback dengan defense-in-depth.

```mermaid
sequenceDiagram
    autonumber
    participant Midtrans
    participant ALB as AWS ALB
    participant Gateway
    participant Payment
    participant Postgres
    participant NATS

    Midtrans->>ALB: POST /v1/payments/midtrans/notification<br/>+ signature_key header

    ALB->>ALB: WAF rule: allow Midtrans IP CIDR only<br/>(extra defense)
    ALB->>Gateway: forward request

    Gateway->>Gateway: Apply webhook rate limit<br/>(50 RPS / 100 burst)

    Gateway->>Payment: gRPC HandleWebhook

    Payment->>Payment: Verify signature_key:<br/>sha512(order_id + status_code + gross_amount + server_key)

    alt Signature invalid
        Payment->>Postgres: INSERT webhook_log<br/>(verified=false, raw_payload=...)
        Payment-->>Gateway: PERMISSION_DENIED
        Gateway-->>Midtrans: 403 Forbidden
        Note over Midtrans: Will retry per Midtrans policy
    else Signature valid
        Payment->>Postgres: INSERT webhook_log<br/>(verified=true)
        Payment->>Postgres: SELECT payment WHERE gateway_ref=$order_id
        
        alt Idempotent: already processed
            Payment-->>Gateway: OK (no-op, return cached state)
        else First-time webhook
            Payment->>Postgres: UPDATE payment status=SUCCESS
            Payment->>NATS: publish payment.succeeded
        end
        
        Payment-->>Gateway: OK
        Gateway-->>Midtrans: 200 OK
    end
```

## 6. Event-Driven Reservation Saga (Choreography)

Cross-service workflow via NATS events, no central orchestrator.

```mermaid
sequenceDiagram
    autonumber
    actor Driver
    participant Reservation
    participant Billing
    participant Payment
    participant Notification
    participant NATS as NATS JetStream

    Note over Driver,Notification: Forward saga (happy path)

    Driver->>Reservation: Create reservation
    Reservation->>NATS: reservation.confirmed
    NATS->>Billing: subscribe → create invoice
    Billing->>NATS: invoice.issued
    NATS->>Notification: subscribe → email driver

    Driver->>Reservation: CheckIn
    Reservation->>NATS: reservation.checked_in
    
    Driver->>Reservation: CheckOut
    Reservation->>NATS: reservation.checked_out
    NATS->>Billing: finalize invoice (calc duration)
    Billing->>NATS: invoice.finalized

    Driver->>Payment: Pay invoice
    Payment->>NATS: payment.succeeded
    NATS->>Billing: mark invoice paid
    Billing->>NATS: invoice.paid
    NATS->>Notification: email receipt

    Note over Driver,Notification: Compensation flow (cancel)

    Driver->>Reservation: Cancel (before check-in)
    Reservation->>NATS: reservation.cancelled
    NATS->>Billing: void invoice
    Billing->>NATS: invoice.voided
    NATS->>Notification: email cancel confirmation
```

## Notes on diagram maintenance

- **Update saat business logic berubah** — diagrams sebagai contract
- **Source of truth** = code, diagram is reflection
- **Lint check**: Mermaid syntax via GitHub markdown preview
- **CI integration**: belum (future improvement — auto-render PNG ke wiki)

## Related

- [ADR-0001](../adr/0001-microservices-vs-monolith.md) — service decomposition
- [ADR-0006](../adr/0006-event-driven-nats.md) — NATS choice
- [ADR-0011](../adr/0011-postgres-unique-constraints.md) — anti double-booking
- [ADR-0014](../adr/0014-overdue-invoice-circuit-breaker.md) — graceful degradation
- [docs/api/idempotency.md](../../api/idempotency.md) — header behavior
