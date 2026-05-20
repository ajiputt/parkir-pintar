# Threat Model — ParkirPintar (STRIDE)

> Per-component STRIDE analysis. Update setiap arsitektur signifikan berubah.

## Scope

In-scope: gateway, reservation, billing, payment, NATS, Postgres, Redis, Midtrans webhook.
Out-of-scope: client app (driver mobile), Midtrans backend.

## STRIDE per Component

### Gateway

| Threat | Mitigation |
|---|---|
| **S**poofing — fake driver token | JWT verify signature (RS256 prod), expiry check, audience claim |
| **T**ampering — request modification in transit | TLS 1.3 termination, HSTS header |
| **R**epudiation — driver claim "I didn't book" | Audit log dengan request_id, IP, user agent, timestamp |
| **I**nformation disclosure — error leak | Generic error responses, detailed log only di server, no stack trace ke client |
| **D**enial of Service — DDoS | Rate limit per IP/token, ALB has shield basic, request body limit 1MB |
| **E**levation of privilege — driver→admin | Role claim verified per endpoint, deny default |

### Reservation Service

| Threat | Mitigation |
|---|---|
| **S** — fake reservation owner | Reservation owner = driver_id from JWT, validated per endpoint |
| **T** — race condition double-book | Three-layer locking (Redis + row lock + EXCLUDE constraint), lihat ADR-0004 |
| **R** — disputed cancellation | Audit `events_log`, reservation state history immutable |
| **I** — leak driver list | Pagination + auth required, query index tidak expose driver_id |
| **D** — flood reservations dari 1 IP | Rate limit + max 5 active reservations per driver |
| **E** — change spot_id of reservation | spot_id immutable post-create; modify only allowed via admin role |

### Billing Service

| Threat | Mitigation |
|---|---|
| **S** — fake event injection | Subscribe NATS only via durable consumer with auth (NATS NKEY) |
| **T** — modify invoice amount | Invoice di-derive dari `events_log`; events immutable, append-only |
| **R** — disputed billing | Full event sourcing audit trail; rebuild possible |
| **I** — leak invoice ke driver lain | Authorization cek driver_id == requestor |
| **D** — slow consumer build-up | Max ack pending 256, DLQ setelah 5 retry, alert metric |
| **E** — modify pricing rules runtime | Pricing config via versioned constant; perubahan via deployment + ADR |

### Payment Service

| Threat | Mitigation |
|---|---|
| **S** — fake Midtrans webhook | HMAC SHA512 signature verify (`pkg/midtrans.VerifySignatureKey`) |
| **T** — modify amount via webhook | Cross-check webhook amount vs DB invoice amount |
| **R** — payment dispute | `webhook_log` table simpan raw payload + signature + verified flag |
| **I** — leak QR string | QR string tied ke specific payment, tidak boleh accessed lewat ID lain |
| **D** — webhook flood | Rate limit per IP (Midtrans IP allowlist) + idempotent processing |
| **E** — replay old webhook | `payment.gateway_ref` UNIQUE → duplicate webhook diabaikan |

### Postgres

| Threat | Mitigation |
|---|---|
| **S** | Username/password rotation via Secrets Manager |
| **T** | Encryption at rest (RDS), parameterized query (no SQL injection) |
| **R** | pgAudit ekstensi untuk audit DDL/DML |
| **I** | TLS in-transit, principle of least privilege per service user |
| **D** | Connection pool limit, slow query log + alert |
| **E** | No SUPERUSER untuk app users; only schema-level perms |

### Redis (lock)

| Threat | Mitigation |
|---|---|
| **T** — lock stealing | Redlock value = unique token, only owner can release |
| **D** — Redis exhaustion | TTL on every key, monitoring memory usage |
| **I** — sensitive data | No PII di Redis (lock keys saja) |

### NATS JetStream

| Threat | Mitigation |
|---|---|
| **S** | NATS NKEY auth + accounts isolation per service |
| **T** | TLS antar broker; consumer ack signed |
| **I** | Subject-level permission (publish vs subscribe) |
| **D** | Stream max size + age, consumer max ack pending |

## Sensitive Data Inventory

| Data | Where | Protection |
|---|---|---|
| Driver email/phone | Postgres | Encrypted at rest, masked in log |
| Plate number | Postgres | At-rest encryption |
| QR string | Postgres (short TTL) | Tied to payment_id, not exposed by other endpoints |
| JWT secret | AWS Secrets Manager | KMS-encrypted, rotated 90 days |
| Midtrans server key | AWS Secrets Manager | Manual rotation per Midtrans dashboard |
| DB password | AWS Secrets Manager / RDS managed | Auto rotation 30 days |

## Security Test Coverage

- Unit: signature verification edge cases.
- Integration: SQL injection attempts via fuzz.
- E2E: authz tests (driver A access driver B's reservation = 403).
- Penetration: scheduled annual external pentest.
