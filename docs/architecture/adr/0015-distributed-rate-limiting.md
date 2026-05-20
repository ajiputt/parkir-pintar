# ADR-0015: Distributed Rate Limiting via Redis Token Bucket

- **Status**: Accepted
- **Date**: 2026-05-05
- **Tags**: gateway, infrastructure, ops
- **Related**: ADR-0004 (Redis lock pattern, redsync)
- **Supersedes**: in-memory `middleware.RateLimit` di gateway (kept untuk fallback)

## Context

Gateway awalnya pakai in-memory token bucket per-IP. Issue saat scaling:
1. Multi-instance — N pod = effective N×rps (state tidak shared)
2. Memory leak — map[ip]*bucket grow forever
3. Cold start — restart pod = lose state
4. No per-endpoint differentiation
5. No per-user fairness (NAT)

## Decision

Replace dengan **Redis-based token bucket** (`pkg/ratelimit`):

- Atomic via Lua script (refill + decrement single round-trip)
- Distributed — semua gateway instance share bucket di Redis
- Auto-cleanup — TTL 1 jam per key
- Per-endpoint config (heavy/medium/light/webhook tiers)
- Fail-open kalau Redis flap (avoid DDoS-self saat outage)
- Headers exposed: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `Retry-After`

## Tier policy

| Tier | Endpoints | Limit |
|---|---|---|
| Heavy | `POST /v1/reservations`, `POST /v1/payments` | 5 rps, burst 10 |
| Medium | `POST /v1/reservations/{id}:checkin/checkout/cancel` | 30 rps, burst 60 |
| Light | All `GET` (read-side) | 100 rps, burst 200 |
| Webhook | Midtrans notification | 200 rps, burst 400 |
| Default | Fallback | 50 rps, burst 100 |

Override per-tier via env (`GATEWAY_RATELIMIT_*_RPS`, `GATEWAY_RATELIMIT_*_BURST`).

## Trade-offs

### Positive
- Multi-instance safe
- No memory leak (TTL eviction)
- Per-endpoint discipline
- Fail-open during Redis outage
- Reuse existing Redis (already di reservation service untuk lock)

### Negative
- Redis round-trip +1-3ms per request
- Redis SPOF (mitigation: production HA + future local fallback)
- Lua script complexity

## Production checklist

- [ ] Redis HA (master-replica atau cluster)
- [ ] Tracing per Check call
- [ ] Metrics: rate_limited counter
- [ ] Per-driver_id key (butuh JWT auth)
- [ ] Adaptive throttling

## Test scenarios

```bash
# Heavy burst → 11+ kena 429
for i in 1..15; do curl -X POST http://localhost:8080/v1/reservations ...; done

# Redis outage → fail-open
docker stop parkir-redis
# curl tetap 201, log "ratelimit redis error — fail open"

# Multi-instance distributed
GATEWAY_HTTP_PORT=:8080 ./bin/gateway &
GATEWAY_HTTP_PORT=:8081 ./bin/gateway &
# burst lewat 2 port — kombinasi rps tetap di-cap di Redis
```

## Rejected Alternatives

| Approach | Reason rejected |
|---|---|
| Sliding window log | Memory overhead, slower |
| Sliding window counter | Implementation complex |
| API gateway native (Envoy/Kong) | Out-of-code, infra-level — recommend production but out of demo scope |
| Cloudflare DDoS | Edge-only, app-level still needed |

## References

- [Stripe Rate Limiting Engineering](https://stripe.com/blog/rate-limiters)
- ADR-0004 (Redis usage pattern)
