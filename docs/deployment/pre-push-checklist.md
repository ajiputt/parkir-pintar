# Pre-Push Checklist — Reservation Service

Sebelum `git push` ke remote (apalagi public repo), jalankan checklist ini untuk memastikan:
- Tidak ada secret leak
- Build & test passing
- Container image bisa di-deploy
- Dokumentasi lengkap

Tujuan: **first push langsung clean**, bukan push-fix-push-fix.

---

## 1. Hygiene Repository

### 1.1 .gitignore audit

Existing `.gitignore` sudah cover scope berikut (sudah ✅):

- Build artifacts (`bin/`, `dist/`, `*.exe`, `*.test`)
- Coverage (`coverage.out`, `*.cover`)
- Generated proto stubs (`proto/gen/`) — **regenerable, jangan commit**
- Editor files (`.vscode/`, `.idea/`, `.DS_Store`)
- Secret-bearing env files (`.env`, `.env.*`) dengan whitelist `.env.example`
- Cert & key (`*.pem`, `*.key`, `*.crt`)
- Terraform state (`*.tfstate`, `.terraform/`)
- Helm packages (`*.tgz`, `Chart.lock`)
- Personal notes (`panduan/`, `notes/`, `.private/`, `*-personal.md`, `TODO.md`)
- Slides (`slides/`, `presentation/`)

**Verifikasi:**

```bash
# Make sure tidak ada .env yang nyangkut staged
git status --ignored | grep -E '\.env(\.|$)'

# Cek tidak ada file di blacklist yang sudah ke-track
git ls-files | grep -E '\.env$|\.pem$|\.key$|tfstate'
# Output harus kosong
```

### 1.2 Secret scan

Sebelum push, run sederhana:

```bash
# Pattern umum yang sering bocor
git ls-files | xargs grep -nE \
  'AKIA[0-9A-Z]{16}|SK_live_|midtrans.*key|password\s*=\s*["'"'"'][^"'"'"']+["'"'"']|jwt.*secret\s*=\s*["'"'"'][^"'"'"']{16,}' \
  2>/dev/null | grep -v '\.example\|test\|mock\|dummy'
```

Audit hasil — kalau ada match, double-check apakah memang dummy atau real secret. Kalau real → **rotate dan ganti** sebelum push.

**Yang aman di repo (sudah dummy):**

```
JWT_SECRET=demo-secret-change-me-in-production-please-32chars   # .env.local — local only, di-gitignore
midtrans-server-key                                              # K8s secret name reference, value via Secrets Manager
```

**Yang HARUS via Secrets Manager (bukan committed):**

- `JWT_SECRET` (production value, min 32 chars random)
- `MIDTRANS_SERVER_KEY` (production server key)
- `DB_URL` (Postgres connection string)
- `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` — **never commit**, gunakan IRSA atau OIDC
- `SLACK_WEBHOOK_URL` (notifications)

### 1.3 Tooling install (gitleaks)

Untuk hardening, install [gitleaks](https://github.com/gitleaks/gitleaks) sebagai pre-commit hook:

```bash
# Install
brew install gitleaks   # mac
choco install gitleaks  # windows

# Run on whole history (one-time audit)
gitleaks detect --source . --verbose --redact

# Run on staged changes (pre-commit hook)
gitleaks protect --staged --verbose --redact
```

Workflow `.github/workflows/security.yml` sudah include gitleaks scan otomatis di CI.

---

## 2. Code Quality

### 2.1 Build verification

```bash
# Per service (root → per-service module)
cd services/reservation && go build ./... && cd -
cd pkg && go build ./... && cd -
cd proto && go build ./... && cd -

# Atau via Makefile
make build
```

**Expected:** zero error, zero warning.

### 2.2 Test pass

```bash
# Unit test dengan race detector
make test

# Atau spesifik per service
cd services/reservation && go test -race ./...
```

**Expected:** semua hijau, coverage minimum 60% (per [ADR-0008 testing strategy]).

### 2.3 Lint pass

```bash
# golangci-lint per module
make lint

# Buf lint (proto)
cd proto && buf lint
```

CI workflow akan re-run ini dengan `continue-on-error: true` di non-pkg modules — tapi lebih bagus pass dulu di local.

### 2.4 Proto regenerable

```bash
# Hapus generated dulu untuk pastikan reproducible
rm -rf proto/gen
make proto

# Cek hasilnya
git diff proto/gen   # harus kosong (tidak ke-commit) atau no diff (kalau di-commit di branch lain)
```

`proto/gen/` di-gitignore — generated stubs tidak ke-commit, regenerated saat build.

---

## 3. Container Image

### 3.1 Dockerfile build berhasil

```bash
# Build reservation image
docker build -f services/reservation/Dockerfile -t parkir-reservation:test .

# Inspect size & layers
docker images parkir-reservation:test
docker history parkir-reservation:test
```

**Expected:**
- Multi-stage build (golang:1.22-alpine builder → distroless runtime)
- Final image size <30 MB (current Dockerfile sudah optimal)
- USER nonroot:nonroot (UID 65532)
- ENTRYPOINT non-shell exec form

### 3.2 Container security scan

```bash
# Trivy (paling popular)
trivy image --severity HIGH,CRITICAL parkir-reservation:test

# Snyk (alternatif)
snyk container test parkir-reservation:test
```

Address CRITICAL findings before push. HIGH bisa di-track via Issues kalau effort besar.

### 3.3 Smoke test container

```bash
# Run dengan minimal env
docker run --rm -p 9091:9091 -p 9191:9191 \
  -e DB_URL='postgres://...' \
  -e REDIS_ADDR='host.docker.internal:6379' \
  -e NATS_URL='nats://host.docker.internal:4222' \
  parkir-reservation:test &

# Probe
curl -f http://localhost:9191/healthz   # should return "ok"
curl http://localhost:9191/readyz | jq  # should return JSON dependency check
```

---

## 4. Documentation

### 4.1 Required files (already in repo)

| File | Status | Purpose |
|------|--------|---------|
| `README.md` (root) | ✅ Lengkap | Overview, HLD, LLD, ERD, ADR list, Quick Start |
| `services/reservation/README.md` | ⚠️  Optional | Per-service onboarding |
| `LICENSE` | ⚠️  Tambah | Internal/MIT/Apache (pick one) |
| `CONTRIBUTING.md` | ⚠️  Optional | Dev workflow, branch policy |
| `docs/architecture/adr/*.md` | ✅ Lengkap | 17 ADRs |

### 4.2 LICENSE (kalau public repo)

Pilih satu (paling umum: MIT untuk demo, Apache 2.0 untuk enterprise):

```bash
# MIT
curl -L https://opensource.org/licenses/MIT > LICENSE.tmp && mv LICENSE.tmp LICENSE
# Edit ganti year + holder name
```

### 4.3 Update README badges

Tambahkan di top README.md kalau belum:

```markdown
![CI](https://github.com/<user>/parkir-pintar/actions/workflows/ci.yml/badge.svg)
![Security](https://github.com/<user>/parkir-pintar/actions/workflows/security.yml/badge.svg)
![Go Report](https://goreportcard.com/badge/github.com/<user>/parkir-pintar)
![License](https://img.shields.io/badge/license-MIT-green)
```

---

## 5. CI/CD Configuration

### 5.1 GitHub Secrets yang harus di-set sebelum first deploy

Di GitHub Repo → Settings → Secrets and variables → Actions:

| Secret | Purpose | How to obtain |
|--------|---------|---------------|
| `AWS_DEPLOY_ROLE_ARN` | OIDC role yang di-assume oleh GH Actions | Create di IAM, trust policy `token.actions.githubusercontent.com` |
| `EKS_CLUSTER_NAME` | EKS cluster name | After `eksctl create cluster` |
| `ECR_REGISTRY` | ECR registry URL | `<account>.dkr.ecr.ap-southeast-1.amazonaws.com` |
| `SLACK_WEBHOOK_URL` | Optional, deploy notification | Slack app config |
| `MIDTRANS_SERVER_KEY` | Production Midtrans key | Midtrans dashboard (production env) |
| `JWT_SECRET_PROD` | Production JWT signing key | `openssl rand -base64 48` |

### 5.2 GitHub Environments

Buat 3 environments di Settings → Environments:

- `demo` — protection: none
- `staging` — protection: 1 reviewer required
- `prod` — protection: 2 reviewer required, deploy branch = main only

### 5.3 OIDC trust policy (one-time setup)

Di AWS IAM, create role `GitHubActionsDeployRole`:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Federated": "arn:aws:iam::<account>:oidc-provider/token.actions.githubusercontent.com" },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
      },
      "StringLike": {
        "token.actions.githubusercontent.com:sub": "repo:<github-user>/parkir-pintar:*"
      }
    }
  }]
}
```

Attach permissions: `AmazonEC2ContainerRegistryPowerUser`, `AmazonEKSClusterPolicy`, plus minimal IAM untuk update kubeconfig.

---

## 6. Pre-Push Final Sanity

```bash
# All-in-one verification
make build && \
make test && \
make lint && \
gitleaks protect --staged --verbose && \
docker build -f services/reservation/Dockerfile -t parkir-reservation:pre-push . && \
trivy image --exit-code 1 --severity CRITICAL parkir-reservation:pre-push && \
echo "✅ Ready to push"
```

Kalau exit 0 di akhir, baru `git push origin main`.

---

## 7. Post-Push (first time)

Setelah push pertama berhasil:

1. **Verify CI hijau** di Actions tab — semua job (lint, test, build-images) harus pass
2. **Verify image ke-publish** di GHCR atau ECR
3. **Verify Docker Hub badge** kalau pakai public registry
4. **Setup branch protection** di Settings → Branches:
   - Require PR before merge ke main
   - Require status checks pass: `lint`, `unit-test`, `build-images`
   - Require linear history (atau allow squash)
5. **Create v0.1.0 tag** untuk first release

```bash
git tag -a v0.1.0 -m "Initial release — reservation service deployed"
git push origin v0.1.0
```

---

## Appendix: Quick reference

### Files yang HARUS ada sebelum push

- [x] `.gitignore`
- [x] `README.md` (root)
- [x] `Makefile`
- [x] `go.work`, `go.work.sum`
- [x] `services/reservation/{Dockerfile,go.mod,go.sum,cmd/main.go,internal/}`
- [x] `pkg/` (shared modules)
- [x] `proto/` (contracts, no `gen/`)
- [x] `deploy/migrations/reservation/*.up.sql` `*.down.sql`
- [x] `deploy/helm/parkir-pintar/{Chart.yaml,values.yaml,templates/}`
- [x] `.github/workflows/{ci.yml,security.yml,deploy-eks.yml}` (deploy-eks: lihat #74)
- [ ] `LICENSE` (tambah kalau public)
- [x] `docs/architecture/adr/*.md`

### Files yang TIDAK BOLEH ada di push

- ❌ `.env`, `.env.local`, `*.env.prod`
- ❌ `*.pem`, `*.key`, `*.crt` (real cert)
- ❌ `*.tfstate`, `.terraform/`
- ❌ `proto/gen/` (regenerable)
- ❌ `bin/`, `dist/` (build output)
- ❌ `panduan/`, `notes/`, slides — di-gitignore tapi double check
