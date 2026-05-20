# Runbook: Database Connection Pool Saturated

| Severity | SEV2 |
|---|---|
| Alert source | Grafana → `DB Connection Pool Exhausted` |
| Threshold | `go_sql_open_connections == go_sql_max_open_connections` selama 1 menit |
| Estimated MTTR | 10–30 menit |

## Symptom

- gRPC requests fail dengan `Internal: dial tcp: connection pool exhausted`
- Postgres logs: `FATAL: too many connections for role "gopark"`
- Grafana panel "DB Pool Usage" mencapai 100%
- Cascading impact: reservation/billing/payment all degrade

## Quick Triage

| Question | Yes | No |
|---|---|---|
| Single service atau multi? | Single → bug di service tsb (leak / slow query) | Multi → Postgres-level issue |
| Recent traffic spike? | Yes → autoscale | No → connection leak suspect |
| Postgres CPU/IO saturated? | Yes → query optimization | No → app-side bottleneck |

## Diagnostic Steps

### 1. Identify hot service

```promql
# Connection usage per service
go_sql_open_connections{service=~"reservation|billing|payment"} /
go_sql_max_open_connections{service=~"reservation|billing|payment"}
```

Service yang ratio = 1.0 adalah suspect.

### 2. Check Postgres side

```bash
# SSH ke Postgres atau kubectl exec
kubectl exec -n parkir <postgres-pod> -- psql -U gopark -d parkirpintar -c "
SELECT pid, usename, application_name, state, query_start, 
       now() - query_start AS duration, left(query, 80) AS query
FROM pg_stat_activity
WHERE state != 'idle'
ORDER BY duration DESC
LIMIT 20;
"
```

Look for:
- Queries dengan duration > 5s (slow query)
- Stuck `idle in transaction` (leaked transaction)
- Many duplicate queries (missing connection close)

### 3. Check connection metadata

```sql
-- Connections per app
SELECT application_name, count(*) 
FROM pg_stat_activity 
WHERE datname = 'parkirpintar' 
GROUP BY application_name 
ORDER BY count DESC;

-- Idle vs active
SELECT state, count(*) FROM pg_stat_activity GROUP BY state;

-- Max settings
SHOW max_connections;
SHOW shared_buffers;
```

## Resolution Paths

### Path A — Connection leak di app

Sign: `idle in transaction` > 5 menit, growing over time.

```bash
# Immediate: restart leaky service to free connections
kubectl rollout restart deployment/<service> -n parkir

# Verify
go_sql_open_connections{service="<service>"}
# Should drop to baseline
```

Long-term fix:
1. Audit code untuk missing `defer rows.Close()` atau `defer tx.Rollback()`
2. Check `db.Conn` lifecycle — pastikan `Close()` di-call
3. Test dengan goroutine leak detector

### Path B — Slow query causing pile-up

```sql
-- Kill long-running query (be careful)
SELECT pg_cancel_backend(pid) FROM pg_stat_activity 
WHERE state = 'active' AND now() - query_start > '60 seconds'::interval
LIMIT 1;
```

Long-term:
- Add index untuk slow query (cek EXPLAIN ANALYZE)
- Consider pagination kalau full-table-scan
- Move read-heavy ops ke replica (kalau available)

### Path C — Legitimate traffic spike

```bash
# Scale service horizontally
kubectl scale deployment/<service> -n parkir --replicas=5

# Each new pod akan add connections — verify total < Postgres max_connections
```

Postgres connection budget:
- Default `max_connections = 100`
- Per-pod budget = (max - reserved_for_superuser) / total_pods_across_all_services
- Recommended: `max_open_conns = budget * 0.8`

### Path D — Postgres saturated CPU

Scale Postgres tier (AWS RDS):
```bash
aws rds modify-db-instance --db-instance-identifier parkir-postgres \
  --db-instance-class db.r6g.xlarge \
  --apply-immediately
```

Atau emergency: enable RDS Performance Insights untuk identify top queries.

### Path E — Tune pool config

Edit Helm values:
```yaml
# deploy/helm/parkir-pintar/values.yaml
services:
  reservation:
    env:
      DB_MAX_OPEN_CONNS: "20"      # default 10
      DB_MAX_IDLE_CONNS: "5"
      DB_CONN_MAX_LIFETIME: "5m"
      DB_CONN_MAX_IDLE_TIME: "1m"
```

Re-deploy:
```bash
helm upgrade parkir-pintar deploy/helm/parkir-pintar -n parkir -f values.yaml
```

⚠️ **Caution**: Total `MAX_OPEN_CONNS * replicas` per service harus < Postgres `max_connections`. Otherwise Postgres reject new connections.

## Verification

```promql
# Pool usage should normalize
go_sql_open_connections{service=~"reservation|billing|payment"}

# Plus error rate down
rate(grpc_requests_total{grpc_code="Internal"}[5m])
```

## Prevention

1. **Always defer Close()**:
   ```go
   rows, err := db.Query(ctx, "...")
   if err != nil { return err }
   defer rows.Close()  // ← critical
   ```

2. **Transaction lifecycle**:
   ```go
   tx, err := db.Begin(ctx)
   if err != nil { return err }
   defer tx.Rollback()  // safe: no-op if Commit() succeeded
   // ... work
   return tx.Commit()
   ```

3. **Context timeouts** di setiap query:
   ```go
   ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
   defer cancel()
   ```

4. **Monitoring**: connection pool metrics exported via `pkg/db/postgres.go`

## Related

- [ADR-0017](../architecture/adr/0017-deployment-eks.md) — Postgres deployment model
- [pkg/db/postgres.go](../../../pkg/db/postgres.go) — connection pool config
- [high-error-rate.md](./high-error-rate.md) — upstream alert
