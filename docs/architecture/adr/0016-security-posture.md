# ADR-0016: Security Posture — JWT, Security Headers, PII Masking

- **Status**: Accepted
- **Date**: 2026-05-05
- **Tags**: security, auth, headers, pii, ops
- **Related**: ADR-0007 (idempotency), ADR-0015 (rate limiting)

## Context

Sebelum ADR ini, security posture ParkirPintar:
- ✅ Webhook HMAC SHA-512 (Midtrans)
- ✅ Idempotency anti-replay
- ✅ SQL injection safe (pgx prepared)
- ✅ Rate limiting Redis-based
- ✅ Schema isolation per service
- ⚠️ Authn middleware **passthrough** (no JWT verify)
- ⚠️ No security headers
- ⚠️ PII di-log full (email, phone, plate)

Untuk demonstrate senior-level security awareness, tambah 3 layer:

1. JWT authentication (HS256) — replace passthrough
2. Security headers middleware (HSTS, X-Frame-Options, dst)
3. PII masking di structured log

## Decision

### 1. JWT (HS256) authentication

`pkg/auth` package:
- Sign + Verify HS256 (no external dep, just `crypto/hmac`)
- Claims: `sub` (driver_id), `iss`, `iat`, `exp`
- Middleware verify Bearer token, inject `driver_id` ke context + `X-Driver-ID` header

**Demo mode**: `AUTH_PASSTHROUGH_NO_TOKEN=true` — endpoint bisa diakses tanpa
token (untuk Postman testing). Production: set `false` → enforce.

**Skip paths** (always public):
- `/healthz`, `/readyz` — probes
- `/docs`, `/openapi.json` — Swagger UI
- `/v1/availability`, `/v1/spots` — read-only public discovery
- `/v1/payments/midtrans/notification` — webhook (Midtrans signs body, not JWT)
- `/v1/auth/dev-token` — dev self-service

**Dev-token endpoint** (only `APP_ENV != prod`):
```bash
POST /v1/auth/dev-token
{ "driver_id": "driver-postman-1", "ttl_sec": 3600 }

→ 200 OK
{ "token": "eyJhbGc...", "expires_in": 3600, "sub": "driver-postman-1" }
```

Pakai header `Authorization: Bearer <token>` di subsequent request.

### 2. Security headers

`services/gateway/internal/middleware.SecurityHeaders` set:

| Header | Value | Purpose |
|---|---|---|
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | Force HTTPS (set saat TLS only) |
| `X-Content-Type-Options` | `nosniff` | Cegah MIME sniffing |
| `X-Frame-Options` | `DENY` | Cegah clickjacking |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Limit referrer leak |
| `Permissions-Policy` | `geolocation=(), microphone=(), camera=(), payment=()` | Disable browser features |
| `Cross-Origin-Resource-Policy` | `same-origin` | Cegah cross-site resource load |

CSP **tidak** di-set karena `/docs` Swagger UI butuh CDN. Future: per-route CSP.

### 3. PII masking helpers

`pkg/logger/sanitize.go` — pure functions untuk mask sebelum log:

| Function | Input | Output |
|---|---|---|
| `MaskEmail` | `aji@gmail.com` | `a**@gmail.com` |
| `MaskPhone` | `+6281234567890` | `+62*****7890` |
| `MaskPlate` | `B 1234 ABC` | `B *** ABC` |
| `MaskID` | `driver-postman-1` | `driver-p***` |
| `MaskUUIDPrefix` | `550e8400-e29b-...` | `550e8400-...` |

Apply di:
- `services/notification/internal/usecase/dispatcher.go` — log `to_masked` instead of full email saat send

Future: extend ke audit log admin action, payment event log, dst.

## Trade-offs

### Positive

- ✅ JWT signature enforcement (production-ready saat passthrough off)
- ✅ Security headers — defense-in-depth, low cost (~5 LOC middleware)
- ✅ PII masking — GDPR/PDP awareness, audit-friendly
- ✅ Dev-token endpoint — frictionless testing tanpa OAuth flow
- ✅ Skip paths jelas — webhook & probe tidak ke-block

### Negative

- ⚠️ HS256 = symmetric — kalau secret leak, semua token compromised. Production:
  upgrade ke RS256/ES256 + JWKS endpoint.
- ⚠️ No refresh token, no revocation list — token valid sampai exp. Production:
  consider Redis-backed token blacklist atau short TTL.
- ⚠️ JWT body claims tidak verified by reservation/billing/payment service.
  Mereka percaya gateway sudah verify (zero-trust gap). Future: forward signed
  metadata ke gRPC, atau pakai mTLS internal.
- ⚠️ Demo mode `passthrough` — production HARUS set `false`, dokumentasi jelas.

### Neutral

- 📝 Notification service log dengan masked email — backward-compat (existing
  log structure tetap, cuma value di-mask).
- 📝 Body field `driver_id` di CreateReservation tetap accepted — kalau JWT ada,
  override. Tradeoff: backward-compat vs strict security. Production: enforce
  match (error kalau body driver_id ≠ token sub).

## Production checklist

| Item | Status | Action |
|---|---|---|
| JWT signature verify | ✅ | Done |
| AUTH_PASSTHROUGH_NO_TOKEN=false | ✅ | Default code changed to `false`. EKS values explicit `false`. |
| Dev-token endpoint gated by secret | ✅ | `X-Dev-Secret` header required (constant-time compare). Endpoint 404 kalau secret env kosong. |
| Strong JWT_SECRET (32+ chars) | ⚠️ | Rotate via SecretsManager |
| `/metrics` blocked from public ALB | ✅ | ALB fixed-response action returns 404 |
| RS256/ES256 + JWKS | ❌ | Upgrade later (multi-issuer / key rotation) |
| Token revocation list | ❌ | Redis blacklist on logout |
| Per-service mTLS | ❌ | cert-manager / SPIFFE |
| CSP per-route | ❌ | Tighter for /v1/*, looser for /docs |
| HSTS preload list | ❌ | Submit ke hstspreload.org setelah live |
| PII mask di semua log | ⚠️ | Extend ke billing/payment event log |
| Audit log admin actions | ❌ | Future endpoint admin |
| Dependency scan (govulncheck) | ✅ | CI workflow .github/workflows/security.yml |
| WAF di ALB (production) | ❌ | AWS WAF managed rule sets — post-launch |
| Driver authorization (data ownership) | ⚠️ | Verify driver_id di JWT match resource owner per handler |

## Auth posture (deployment matrix)

| Setting | Local dev | Staging EKS (current) | Production |
|---|---|---|---|
| `APP_ENV` | `dev` | `staging` | `prod` |
| `AUTH_PASSTHROUGH_NO_TOKEN` | `true` (optional) | `false` (strict) | `false` (strict) |
| `DEV_TOKEN_SECRET` | optional | set (Postman demo) | NOT SET (endpoint disabled) |
| `/v1/auth/dev-token` access | open | needs `X-Dev-Secret` header | 404 (endpoint gak ke-register) |
| `/v1/spots`, `/v1/availability` | public | public | public |
| Protected endpoints (`/v1/reservations`, dll) | passthrough OK | needs Bearer JWT | needs Bearer JWT |
| `/docs`, `/openapi.json` | public | blocked (404 di ALB) | blocked (404 di ALB) |

### Akses Swagger di staging/prod

Karena `/docs` di-block dari public ALB, demo/admin akses via port-forward:

```powershell
kubectl port-forward -n parkir svc/gateway 8080:80
# Browser → http://localhost:8080/docs
```

Rationale: Swagger reveal API surface (endpoint list, schema, error codes) yang mempermudah recon attacker. Block di edge gak prevent legitimate developer akses karena port-forward butuh `kubectl` access (RBAC-gated).

## Test scenarios

### Test 1: dev-token issuance
```bash
curl -X POST http://localhost:8080/v1/auth/dev-token \
  -H "Content-Type: application/json" \
  -d '{"driver_id":"driver-postman-1"}'
# Expected: { "token": "eyJ...", "expires_in": 3600, "sub": "driver-postman-1" }
```

### Test 2: protected endpoint with token
```bash
TOKEN=$(curl -s -X POST http://localhost:8080/v1/auth/dev-token -d '{"driver_id":"driver-postman-1"}' | jq -r .token)

curl -X POST http://localhost:8080/v1/reservations \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"driver_id":"driver-postman-1",...}'
# Expected: 201 (token verified, X-Driver-ID injected)
```

### Test 3: invalid token rejected (set AUTH_PASSTHROUGH_NO_TOKEN=false)
```bash
curl -X POST http://localhost:8080/v1/reservations \
  -H "Authorization: Bearer invalid.token.here" \
  -d '...'
# Expected: 401 { "error": "unauthorized", "message": "invalid or expired token" }
# Header: WWW-Authenticate: Bearer realm="parkirpintar"
```

### Test 4: security headers
```bash
curl -I http://localhost:8080/healthz
# Expected response headers:
#   X-Content-Type-Options: nosniff
#   X-Frame-Options: DENY
#   Referrer-Policy: strict-origin-when-cross-origin
#   Permissions-Policy: geolocation=(), ...
#   Cross-Origin-Resource-Policy: same-origin
```

### Test 5: PII masking di log
```
{"level":"info","msg":"notification sent","to_masked":"a**@gmail.com","subject":"..."}
                                          ^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                                          Tidak ada full email di log
```

## References

- [RFC 7519 — JSON Web Token](https://datatracker.ietf.org/doc/html/rfc7519)
- [OWASP Secure Headers Project](https://owasp.org/www-project-secure-headers/)
- [Permissions Policy MDN](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Permissions-Policy)
- ADR-0007 (idempotency)
- ADR-0015 (rate limiting)
