# Runbook: NATS Stream Consumer Lag

| Severity | SEV3 |
|---|---|
| Alert source | Grafana → `NATS Consumer Lag` |
| Threshold | `nats_consumer_num_pending > 1000` selama 5 menit |
| Estimated MTTR | 15–60 menit |

## Symptom

- Billing service reports stale invoice data (latency between reservation event and invoice creation)
- Notification service delays in sending email confirmation
- NATS dashboard menunjukkan growing pending messages
- Grafana panel "Consumer Lag" > 1000 messages

## Impact

- **User-facing**: Booking confirmation email delayed (10-60 sec → minutes)
- **Internal**: Billing invoice creation delayed → analytics dashboard stale
- **Cascading**: Notification DLQ accumulating kalau retry exhausted

## Quick Triage

| Question | Yes | No |
|---|---|---|
| Lag pada specific consumer? | Yes → fokus consumer tersebut | All consumers → NATS-level issue |
| Lag growing atau stable? | Growing → consumer not keeping up | Stable → recent burst (will catch up) |
| Consumer pod healthy? | Yes → throughput issue | No → restart needed |

## Diagnostic Steps

### 1. Identify slow consumer

```bash
# Port-forward NATS monitoring
kubectl port-forward -n parkir svc/nats 8222:8222

# List streams + consumers
curl -s http://localhost:8222/jsz?streams=1&consumers=1 | jq

# Or via nats CLI
nats consumer info PARKIRPINTAR billing-payment-success
```

Output key fields:
- `num_pending` — messages waiting (high = lag)
- `num_ack_pending` — in-flight (high = slow processing)
- `num_redelivered` — failed processing count
- `last_delivered_seq` vs `stream_last_seq` — lag distance

### 2. Check consumer processing time

```promql
# Message processing duration per consumer
histogram_quantile(0.95, 
  sum by (consumer, le) (rate(nats_consumer_process_duration_seconds_bucket[5m]))
)

# Throughput
rate(nats_consumer_acks_total[5m])
```

Normal P95 < 100ms. Kalau > 1s = consumer struggling.

### 3. Check consumer service logs

```bash
kubectl logs -n parkir deploy/billing --tail=200 | grep -i "nats\|consumer\|error"
```

Common error patterns:
- `context deadline exceeded` → downstream service slow
- `failed to ack` → NATS connection issue
- `database error` → consumer can't persist → ack delayed

### 4. Check stream-level config

```bash
nats stream info PARKIRPINTAR

# Key fields:
#   messages: total in stream
#   max_msgs: retention limit  
#   storage: file/memory
```

Kalau `messages` mendekati `max_msgs`, stream akan start dropping → data loss risk.

## Resolution Paths

### Path A — Single consumer slow, growing lag

Scale consumer service:
```bash
# Quick: scale replicas (each pod = independent consumer)
kubectl scale deployment/<consumer-service> -n parkir --replicas=3

# Verify multiple instances pulling
nats consumer info PARKIRPINTAR <consumer-name>
# delivery should distribute across pods
```

Long-term: optimize processing logic
- Batch DB writes (reduce roundtrips)
- Async non-critical side effects (e.g., notification email)
- Index frequently-queried columns

### Path B — Stuck on poison message

Sign: `num_redelivered` growing, same message ID repeated in logs.

```bash
# Inspect poison message
nats stream view PARKIRPINTAR --filter=<subject>

# Find DLQ
nats stream info PARKIRPINTAR.DLQ
```

Fix:
1. Move poison message to DLQ manually (kalau handler ga handle DLQ properly)
2. Fix consumer logic untuk reject/skip invalid payload
3. Re-deploy
4. Process DLQ separately

### Path C — Consumer crashed / OOMKilled

```bash
kubectl get pods -n parkir -l app=<consumer>
# Check RESTARTS

kubectl describe pod <pod> -n parkir | grep -A5 "Last State"
# Look for OOMKilled

# Memory profile
kubectl top pods -n parkir
```

Fix:
- Increase memory limit di Helm values
- Investigate memory leak (heap profiling via pprof)
- Add streaming/pagination kalau processing large payload

### Path D — NATS server overloaded

```promql
# NATS server CPU/memory
nats_server_cpu_seconds_total
nats_server_mem_bytes
```

Fix:
- Scale NATS replicas (kalau cluster mode)
- Move stream storage to file (kalau di-memory dan overflow)
- Add NATS replicas: `nats stream edit PARKIRPINTAR --replicas=3`

### Path E — Burst absorbed (just need patience)

Sometimes lag is from legitimate burst (e.g., end-of-month batch billing run).
If consumer healthy + processing rate > arrival rate, just wait.

```promql
# Lag should drop linearly
nats_consumer_num_pending[10m]
```

## Verification

```bash
# Consumer should be caught up
nats consumer info PARKIRPINTAR <consumer-name>
# num_pending → 0 atau very low (< 100)
```

Plus Grafana alert auto-resolve.

## Prevention

1. **Right-size consumer replicas**: based on peak traffic load test
2. **Set max processing time**:
   ```go
   ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
   defer cancel()
   ```
3. **DLQ everything**: handler return error → message goes to DLQ, not redeliver indefinitely
4. **Monitor consumer lag dashboard**: alert at 1000 (warn) + 10000 (critical)

## Related

- [ADR-0006](../architecture/adr/0006-event-driven-nats.md) — NATS choice rationale
- [pkg/eventbus/nats.go](../../../pkg/eventbus/nats.go) — consumer implementation
- [services/notification/](../../../services/notification/) — DLQ handler example
