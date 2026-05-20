# Runbook — Deployment

## Deploy Pipeline Overview

```
git push tag → release.yml → build images → push GHCR → manual approve → helm upgrade → smoke test
```

## Standard Deploy

### To dev (auto on merge to main)

Auto via `ci.yml` job `build-images`. Helm upgrade triggered by ArgoCD watch (atau manual via:):

```bash
helm upgrade parkir-pintar deploy/helm/parkir-pintar \
  -f deploy/helm/parkir-pintar/values.dev.yaml \
  --set global.imageTag=$(git rev-parse --short HEAD) \
  -n parkir-dev
```

### To prod

```bash
git tag v1.2.3
git push origin v1.2.3
# release.yml akan build & push images, dan trigger manual approval di GitHub Environments

# Setelah approve:
helm upgrade parkir-pintar deploy/helm/parkir-pintar \
  -f deploy/helm/parkir-pintar/values.yaml \
  --set global.imageTag=v1.2.3 \
  -n parkir-prod \
  --atomic --timeout 10m
```

## Pre-Deploy Checklist

- [ ] CI green (lint, unit, integration, e2e, security)
- [ ] DB migration: backward-compat? (lihat `deploy/migrations/<svc>/<n>.up.sql`)
- [ ] Feature flag default state OK?
- [ ] Rollback plan tested (`helm rollback`)
- [ ] On-call notified
- [ ] Status page draft

## Post-Deploy Verification

```bash
# Smoke test (5 menit setelah deploy)
./scripts/smoke-test.sh --env=prod

# Verify metrics
- error_rate < 1%
- p95_latency < 250ms
- pod_ready_replicas == desired
```

## Rollback

```bash
helm history parkir-pintar -n parkir-prod
helm rollback parkir-pintar <revision> -n parkir-prod
```

DB migration rollback (manual, hati-hati!):

```bash
migrate -path deploy/migrations/reservation \
  -database "$DB_URL&search_path=reservation" down 1
```

## Database Migration Strategy

1. **Expand-Contract pattern**:
   - Step 1: Add new column (nullable) — deploy
   - Step 2: Backfill data — async job
   - Step 3: App writes both old + new — deploy
   - Step 4: App reads new only — deploy
   - Step 5: Drop old column — deploy

2. **Online migration** untuk table besar pakai `pg_repack` atau `pgroll`.

3. **Always test migration di staging dengan production-like data volume.**

## Canary / Progressive Rollout

Pakai Argo Rollouts atau Flagger:

```yaml
# Contoh canary di Helm values:
canary:
  enabled: true
  steps:
    - setWeight: 10
    - pause: { duration: 10m }
    - setWeight: 50
    - pause: { duration: 30m }
    - setWeight: 100
  analysis:
    metrics:
      - name: error-rate
        threshold: 1
      - name: latency-p95
        threshold: 300ms
```

## Disaster Recovery

| Scenario | RTO | RPO | Procedure |
|---|---|---|---|
| Single pod crash | < 1 min | 0 | K8s auto-restart |
| Region failure | < 1 hour | < 5 min | Promote DR region (Multi-AZ RDS read replica → primary) |
| DB corruption | < 4 hour | < 15 min | Restore from PITR (RDS automated backup) |
| Total disaster | < 24 hour | < 1 hour | Restore S3 backups → terraform apply → seed historical events_log |

Backup test: monthly restore drill (lihat `runbooks/backup-restore-drill.md`).
