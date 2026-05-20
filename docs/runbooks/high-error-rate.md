# Runbook: High Error Rate — Core Services

| Severity | SEV2 |
|---|---|
| Alert source | Grafana → `High Error Rate - Core Services` |
| Threshold | gRPC error rate > 5% selama 2 menit |
| Target services | reservation, billing, payment, notification |
| Estimated MTTR | 15–45 menit |

## Symptom

Grafana alert fires dengan format:
```
🚨 [FIRING:N] High Error Rate - Core Services
🔥 Firing:
• Service: `<service-name>` | Severity: `warning`
  Error rate <service> = 0.0XXX (threshold 0.05)
```

Plus indicators:
- `grpc_requests_total{grpc_code!~"OK|NotFound"}` rate naik
- Grafana ParkirPintar Overview dashboard menunjukkan red SR per service
- Possibly: client report 5xx via support channel

## Quick Triage (30 detik)

| Question | Yes | No |
|---|---|---|
| 1 service atau multiple? | 1 → fokus root cause service itu | Multi → cek shared dep (DB, Redis, NATS) |
| Errors mulai recently atau gradual? | Recently (< 10 min) → deploy regression? | Gradual → resource saturation? |
| 5xx atau 4xx errors? | 5xx → server bug / dep down | 4xx → client misuse / rate limit |

## Diagnostic Steps

### 1. Identify error pattern

Buka [Grafana Tempo](http://grafana.parkirpintar.id/explore/tempo) → Service Name filter → search recent traces dengan `status_code=ERROR`. Klik salah satu trace → identify failure span.

Atau via PromQL di Grafana Explore:
```promql
# Top error endpoints
topk(5, sum by (grpc_service, grpc_method) (
  rate(grpc_requests_total{grpc_code!~"OK|NotFound"}[5m])
))

# Error breakdown by code
sum by (grpc_code, service) (
  rate(grpc_requests_total{grpc_code!~"OK"}[5m])
)
```

### 2. Check logs around error

Loki query:
```logql
{namespace="parkir", app="<service>"} |= "error" | json | level="error"
```

Pakai time range 15 menit terakhir. Look for stack trace, root cause keyword.

### 3. Check upstream dependencies

```bash
# Postgres connection saturated?
kubectl exec -n parkir <pod> -- nc -zv postgres 5432

# Redis reachable?
kubectl exec -n parkir <pod> -- nc -zv redis 6379

# NATS up?
kubectl exec -n parkir <pod> -- nc -zv nats 4222
```

Atau via Prometheus:
```promql
# DB connection pool usage
go_sql_open_connections{service="<service>"}
go_sql_max_open_connections{service="<service>"}

# Redis errors
rate(redis_client_errors_total[5m])
```

### 4. Recent deploy?

```bash
# Last 3 deploys
kubectl rollout history deployment/<service> -n parkir | tail -5
helm history parkir-pintar -n parkir | tail -5

# Check correlation with error spike
git log --since='30 minutes ago' main
```

## Resolution Paths

### Path A — Recent deploy regression (most common)

```bash
# Rollback ke previous version
kubectl rollout undo deployment/<service> -n parkir

# Verify rollback healthy
kubectl rollout status deployment/<service> -n parkir
kubectl get pods -n parkir -l app=<service>

# Verify error rate drop dalam 2-5 menit di Grafana
```

Setelah rollback stable:
1. Identify root cause di code yang baru
2. Add regression test
3. Re-deploy fix dengan canary (kalau available) atau scheduled non-peak hours

### Path B — Database connection exhausted

Lihat [db-connection-saturated.md](./db-connection-saturated.md).

### Path C — Downstream service down

Cek service map di Tempo (Trace → Service Map). Identify which dep returning errors.

```bash
# Cek health downstream
kubectl get pods -n parkir
kubectl logs -n parkir deploy/<dependent-service> --tail=50
```

Kalau billing down → reservation falls back to NoopChecker (graceful degradation per ADR-0014). Verify circuit breaker correctly opened.

Kalau payment can't reach Midtrans:
```bash
# Cek Midtrans status: https://status.midtrans.com
# Verify outbound network
kubectl exec -n parkir deploy/payment -- nc -zv api.sandbox.midtrans.com 443
```

### Path D — Resource saturation (CPU/memory)

```promql
# CPU per pod
sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="parkir"}[5m]))

# Memory
sum by (pod) (container_memory_working_set_bytes{namespace="parkir"})
```

Kalau saturated:
```bash
# Increase replicas
kubectl scale deployment/<service> -n parkir --replicas=3

# Long-term: update Helm values resources.requests/limits
```

### Path E — External dependency degradation

Common culprits:
- Postgres slow query → enable `log_min_duration_statement = 1000` di Postgres
- Redis OOM → cek `INFO memory` + evictions
- NATS stream slow → lihat [nats-stream-lag.md](./nats-stream-lag.md)

## Verification

Alert auto-resolve setelah error rate < 5% selama threshold period. Slack akan dapat:
```
✅ [RESOLVED] High Error Rate - Core Services
```

Plus verify manual:
```promql
# Should return < 0.05 untuk all services
(
  sum by (service) (rate(grpc_requests_total{service=~"reservation|billing|payment|notification",grpc_code!~"OK|NotFound"}[5m]))
  or
  sum by (service) (rate(grpc_requests_total{service=~"reservation|billing|payment|notification"}[5m])) * 0
)
/
sum by (service) (rate(grpc_requests_total{service=~"reservation|billing|payment|notification"}[5m]))
```

## Postmortem Hooks

Setelah resolved, draft postmortem dalam 48 jam jika:
- Customer-facing impact > 5 menit
- Multiple services affected
- Root cause unclear

Template di `docs/postmortems/TEMPLATE.md`. Include:
- Timeline (UTC)
- Root cause (technical + contributing factors)
- Detection delay analysis (kenapa alert telat)
- Action items (prioritized)
- Lessons learned

## Related

- [Grafana Dashboard: ParkirPintar Overview](http://grafana.parkirpintar.id/d/parkir-overview)
- [ADR-0014](../architecture/adr/0014-overdue-invoice-circuit-breaker.md) — graceful degradation
- [ADR-0015](../architecture/adr/0015-distributed-rate-limiting.md) — rate limit might cause 4xx
- [pkg/grpcutil/](../../../pkg/grpcutil/) — gRPC interceptors + metrics
