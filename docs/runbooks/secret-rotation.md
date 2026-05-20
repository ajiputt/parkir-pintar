# Runbook: Quarterly Secret Rotation

| Type | Planned operation |
|---|---|
| Frequency | Quarterly (Q1, Q2, Q3, Q4) atau on-demand setelah suspected leak |
| Estimated effort | 2–4 jam |
| Risk | Medium (downtime risk kalau salah sequencing) |

## Scope

Secrets yang perlu rotated:

| Secret | Storage | Used by | Impact kalau bocor |
|---|---|---|---|
| `JWT_SECRET` | AWS Secrets Manager → K8s Secret | gateway | Attacker bisa forge JWT → impersonate user |
| `DB_PASSWORD` | AWS Secrets Manager → K8s Secret | reservation, billing, payment, notification | Database access |
| `MIDTRANS_SERVER_KEY` | AWS Secrets Manager → K8s Secret | payment | Fraudulent payment confirmation |
| `DEV_TOKEN_SECRET` | AWS Secrets Manager → K8s Secret | gateway | Dev token endpoint abuse |
| `GRAFANA_ADMIN_PASSWORD` | K8s Secret | observability ns | Grafana admin takeover |
| `GHCR Personal Access Token` | GitHub Actions Secrets | CI/CD | Image push abuse |
| `AWS IAM Access Keys` | Local + CI | terraform | Full AWS account access |
| `Slack Incoming Webhook URL` | AWS Secrets Manager | Grafana | Notification spoofing |

## Pre-Rotation Checklist

- [ ] **Schedule** maintenance window (low-traffic period, e.g., Sunday 03:00 WIB)
- [ ] **Communicate** ke stakeholders via #incidents Slack (planned change)
- [ ] **Backup** current secret values di personal password manager (recovery)
- [ ] **Test** rotation procedure di staging dulu kalau possible
- [ ] **Identify** all consumers per secret (use `kubectl get deploy -o yaml | grep <secret-key>`)

## Rotation Procedures

### A) JWT_SECRET Rotation

**Strategy**: Zero-downtime via dual-secret support.

```bash
# 1. Generate new secret (32+ chars random)
NEW_JWT_SECRET=$(openssl rand -base64 48)

# 2. Update AWS Secrets Manager
aws secretsmanager update-secret \
  --secret-id ajipur-parkir-pintar/jwt-secret-next \
  --secret-string "$NEW_JWT_SECRET"

# 3. Trigger External Secrets refresh (or wait up to 1h)
kubectl annotate externalsecret parkir-secrets -n parkir \
  force-sync=$(date +%s) --overwrite

# 4. Verify new secret di K8s
kubectl get secret parkir-secrets -n parkir -o jsonpath='{.data.jwt-secret-next}' | base64 -d

# 5. Deploy gateway dengan dual-secret support (existing JWT validation + new key generation)
helm upgrade parkir-pintar deploy/helm/parkir-pintar -n parkir \
  --set gateway.env.JWT_SECRET_CURRENT="$(kubectl get secret parkir-secrets -o jsonpath='{.data.jwt-secret}' | base64 -d)" \
  --set gateway.env.JWT_SECRET_NEW="$NEW_JWT_SECRET"

# 6. Wait for all gateway pods to roll
kubectl rollout status deployment/gateway -n parkir

# 7. After old token TTL expires (1 jam default), promote new -> current
aws secretsmanager update-secret \
  --secret-id ajipur-parkir-pintar/jwt-secret \
  --secret-string "$NEW_JWT_SECRET"

# 8. Remove the dual-secret env, redeploy gateway with single key
helm upgrade parkir-pintar deploy/helm/parkir-pintar -n parkir
```

⚠️ Notes:
- Old JWT (signed with old secret) tetap valid sampai TTL expires (1 jam)
- Existing user sessions akan tetap work
- New tokens akan signed with new secret

### B) Database Password Rotation

**Strategy**: Postgres support dual user untuk grace period.

```bash
# 1. Connect ke Postgres sebagai admin
kubectl exec -n parkir <postgres-pod> -- psql -U postgres

# 2. Create new user OR update password
ALTER USER gopark WITH PASSWORD 'NEW_STRONG_PASSWORD';

# Or create rotated user (preferred — gradual cutover):
CREATE USER gopark_v2 WITH PASSWORD 'NEW_STRONG_PASSWORD';
GRANT ALL ON SCHEMA reservation, billing, payment, notification TO gopark_v2;
GRANT ALL ON ALL TABLES IN SCHEMA reservation, billing, payment, notification TO gopark_v2;

# 3. Update DB_URL di AWS Secrets Manager
aws secretsmanager update-secret \
  --secret-id ajipur-parkir-pintar/db-url \
  --secret-string "postgres://gopark_v2:NEW_STRONG_PASSWORD@parkir-postgres.xxx.rds.amazonaws.com/parkirpintar?sslmode=require"

# 4. Force ESO refresh
kubectl annotate externalsecret parkir-secrets -n parkir force-sync=$(date +%s) --overwrite

# 5. Rolling restart all services
for svc in reservation billing payment notification; do
  kubectl rollout restart deployment/$svc -n parkir
  kubectl rollout status deployment/$svc -n parkir
done

# 6. Verify connections (DB pool should reconnect)
kubectl logs -n parkir deploy/reservation | grep "db.*connected"

# 7. After all services migrated to gopark_v2, revoke old user
psql -U postgres -c "DROP USER gopark;"  # only if zero connections from gopark
```

### C) Midtrans Server Key Rotation

```bash
# 1. Generate new server key di Midtrans dashboard
# https://dashboard.midtrans.com/settings/access_keys
# Click "Generate new server key" — copy new key

# 2. Update Secrets Manager
aws secretsmanager update-secret \
  --secret-id ajipur-parkir-pintar/midtrans-server-key \
  --secret-string "$NEW_MIDTRANS_SERVER_KEY"

# 3. Force ESO refresh + restart payment service
kubectl annotate externalsecret parkir-secrets -n parkir force-sync=$(date +%s) --overwrite
kubectl rollout restart deployment/payment -n parkir
kubectl rollout status deployment/payment -n parkir

# 4. Test payment flow end-to-end di sandbox
curl -X POST https://api.parkirpintar.id/v1/payments \
  -H "Authorization: Bearer $JWT" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"invoice_id": "test-invoice", "method": "QRIS"}'

# 5. After 24h confirmation OK, revoke old key di Midtrans dashboard
```

### D) GitHub PAT / SSH Key Rotation

```bash
# 1. Generate new PAT di GitHub
# Settings → Developer settings → Personal access tokens → Generate new (scopes: repo, packages:write)
# Copy new token

# 2. Update GitHub Actions repo secret
gh secret set GHCR_TOKEN --repo ajiperdana/parkir-pintar --body "$NEW_PAT"

# 3. Trigger CI dengan dummy commit untuk verify
git commit --allow-empty -m "ci: verify PAT rotation"
git push

# 4. Watch CI Build & Push step → should succeed
gh run watch

# 5. Revoke old PAT di GitHub Settings
```

### E) AWS IAM Access Key Rotation

⚠️ **Most disruptive** — affects Terraform + local dev. Schedule carefully.

```bash
# 1. Create new access key (max 2 active per user)
aws iam create-access-key --user-name ajipur-deploy

# Save AccessKeyId + SecretAccessKey output

# 2. Update local ~/.aws/credentials
[parkir-pintar]
aws_access_key_id = AKIA_NEW...
aws_secret_access_key = NEW_SECRET...

# 3. Update GitHub Actions secrets
gh secret set AWS_ACCESS_KEY_ID --body "AKIA_NEW..."
gh secret set AWS_SECRET_ACCESS_KEY --body "NEW_SECRET..."

# 4. Test: trigger deploy workflow
gh workflow run deploy-eks.yml --ref main

# 5. Verify deploy succeeds, then delete OLD key
aws iam delete-access-key --access-key-id AKIA_OLD... --user-name ajipur-deploy
```

## Post-Rotation Verification

### Functional checks

```bash
# 1. Health probes
for svc in gateway reservation billing payment notification; do
  kubectl get pod -l app=$svc -n parkir
done

# 2. Sample API call
curl -X GET "https://api.parkirpintar.id/v1/availability" \
  -H "Authorization: Bearer $NEW_JWT"
# Expected: 200 OK

# 3. Error rate normal di Grafana
# Should be < 5% per usual SLO
```

### Audit log

Document rotation di `docs/security/rotation-log.md`:

```markdown
| Date | Secret | Reason | Operator | Notes |
|---|---|---|---|---|
| 2026-05-20 | JWT_SECRET | Quarterly | aji@... | Zero downtime, dual-key for 1h |
| 2026-05-20 | DB_PASSWORD | Suspected leak after VS Code attack | aji@... | New gopark_v2 user, old revoked after 24h |
```

## Rollback Plan

Kalau service crash setelah rotation:

```bash
# Restore old Secret value
aws secretsmanager update-secret \
  --secret-id ajipur-parkir-pintar/<secret-name> \
  --secret-string "$OLD_VALUE_FROM_PASSWORD_MANAGER"

# Force refresh
kubectl annotate externalsecret parkir-secrets -n parkir force-sync=$(date +%s) --overwrite

# Rolling restart
kubectl rollout restart deployment/<service> -n parkir
```

Always **keep old secret value** di personal password manager untuk 7 hari setelah rotation. Itu kalau ada delayed issue, bisa rollback cepat.

## Related

- [ADR-0016](../architecture/adr/0016-security-posture.md) — secret management strategy
- [External Secrets Operator docs](https://external-secrets.io/) — sync mechanism
- [AWS Secrets Manager docs](https://docs.aws.amazon.com/secretsmanager/) — backend
- [pod-crashloop.md](./pod-crashloop.md) — debug kalau service crash post-rotation
