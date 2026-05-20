# ADR-0013: User Data Ownership — Embedded di Notification, Bukan User Service Terpisah

- **Status**: Accepted
- **Date**: 2026-05-05
- **Tags**: domain, microservices, identity
- **Related**: ADR-0001 (microservices boundaries)

## Context

Saat implement notification service, butuh lookup contact (email, phone, name)
by `driver_id` untuk dispatch email. Pertanyaan klasik DDD:

**Apakah perlu user service terpisah?**

Argumen Pro:
- Single source of truth identity
- Future-proof untuk auth (JWT, password, 2FA)
- GDPR/PDP compliance (single delete entry point)
- Industri standard (Auth0, Cognito, Keycloak pattern)

Argumen Kontra (untuk konteks ParkirPintar saat ini):
- Tambah service ke-6 = +deploy, +monitor, +SPOF
- Cross-service call setiap request (reservation validate driver_id)
- Network latency tambah
- Distributed transaction sign-up flow
- Domain saat ini sederhana — driver cuma punya email + name

## Decision

**Embed user data di notification schema.** Tidak buat user service terpisah.

Schema `notification.user_contact`:
```sql
CREATE TABLE user_contact (
    driver_id   TEXT PRIMARY KEY,
    email       TEXT NOT NULL,
    name        TEXT,
    phone       TEXT,
    opt_in      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at, updated_at TIMESTAMPTZ
);
```

Notification service own this table. Other services (reservation, billing,
payment) **tidak tahu** soal contact data — mereka hanya tahu `driver_id`
sebagai opaque string.

Lookup hanya terjadi di notification service saat dispatch event handler.

## Why ini OK untuk now

| Concern | Mitigation |
|---|---|
| Identity SoT | `driver_id` IS the identity (opaque string). Email/name = communication metadata, OK di notification. |
| Auth & login | Belum ada auth layer di assessment. Future: tambah `pkg/auth` atau service terpisah. |
| GDPR delete | Notification owns the data — single DELETE in `user_contact`. |
| Multi-channel | OK — tambah column `phone`, `fcm_token`, dst di table yang sama. |
| Multi-tenant | Future: tambah `tenant_id` column atau extract ke user service saat ekspansi. |

## Migration path ke user service (kalau scope grows)

Trigger to extract:
- Auth domain dimulai (login, JWT, refresh token, 2FA)
- Multi-tenant (driver belong to organization)
- Real-time presence/heartbeat (driver online status)
- Subscription per channel (email opt-in beda dengan SMS opt-in)
- Contact data dipakai > 1 service (notification + dashboard + analytics)

Migration steps (kalau perlu):
1. Bikin `services/user/` service baru, schema `user`
2. Copy data: `INSERT INTO user.contact SELECT FROM notification.user_contact`
3. Bikin gRPC API `GetUser(driver_id) returns User`
4. Notification service ganti dari direct DB query → gRPC client
5. Drop `notification.user_contact` table
6. Optional: cache layer di notification untuk avoid repeat lookups

Effort estimate: ~1-2 hari untuk setup minimal user service. **Tidak block
demo / assessment.**

## Trade-offs

### Positive
- ✅ Zero impact ke existing services (reservation, billing, payment)
- ✅ Notification self-contained — clean bounded context
- ✅ No additional service to manage
- ✅ Migration path documented & low-cost kalau dibutuhkan nanti
- ✅ YAGNI discipline — tidak build untuk hipotetik future

### Negative
- ⚠️ Kalau auth domain ditambahkan di sembarang service, akan create tension
- ⚠️ User can have data inconsistency kalau ada 2+ source contact yang sync (currently only 1 source: this table)
- ⚠️ Notification service jadi de-facto "user owner" — bisa misleading

### Neutral
- 📝 Dokumentasi di README service notification jelas: "this service owns user contact for now"
- 📝 ADR ini di-supersede saat user service di-extract

## Implementation

### Notification flow

```
┌─────────────────┐
│ NATS event      │
│ (driver_id)     │
└────────┬────────┘
         │
         ▼
┌────────────────────┐    ┌─────────────────┐
│ Notification       │───▶│ user_contact    │
│ event handler      │    │ table (lookup)  │
└────────┬───────────┘    └─────────────────┘
         │ contact = {email, name}
         ▼
┌────────────────────┐
│ Render template    │
│ + Send via SES     │
└────────────────────┘
```

### Driver registration (out of scope)

Untuk demo, contact di-seed via migration (3 driver). Production butuh:
- Endpoint `POST /v1/contacts` di notification atau gateway
- Webhook dari upstream (sign-up flow)
- Admin endpoint untuk update

Skip untuk assessment — assumption: contact pre-existed (di-seed manual).

## Other Approaches Considered

### Alt 1 — Separate user service

Already analyzed in Context. **Rejected** untuk current scope.

### Alt 2 — Embedded di reservation service

Reservation jadi owner driver data. Notification call gRPC reservation untuk
lookup. **Rejected** karena:
- Reservation domain tidak related dengan communication preferences
- Coupling yang aneh (notification depend sync ke reservation)
- Reservation table ramai dengan field non-domain (email, phone)

### Alt 3 — Distributed cache (Redis)

Driver data cached di Redis, semua service lookup dari sini. **Rejected** karena:
- Redis bukan source of truth (cache miss problem)
- Tidak solve siapa "yang menulis"
- Akhirnya butuh DB juga sebagai backing store

## Future Work

- ADR-0014 (kalau dibutuhkan): User service extraction post-MVP
- Endpoint admin untuk manage `user_contact`
- Per-channel opt-in (email_opt_in, sms_opt_in, push_opt_in)
- Notification preferences UI

## References

- [Domain-Driven Design: Tackling Complexity in the Heart of Software](https://www.amazon.com/Domain-Driven-Design-Tackling-Complexity-Software/dp/0321125215) (Eric Evans)
- [Vaughn Vernon — Implementing DDD: Bounded Context](https://www.amazon.com/Implementing-Domain-Driven-Design-Vaughn-Vernon/dp/0321834577)
- [YAGNI principle](https://martinfowler.com/bliki/Yagni.html)
- ADR-0001 (microservices boundaries — supersedes parts about driver identity)
