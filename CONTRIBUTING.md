# Contributing to ParkirPintar

Selamat datang. Dokumen ini menjelaskan cara berkontribusi ke ParkirPintar:
branch strategy, commit conventions, PR workflow, dan quality gates.

## Quick reference

| Aspect | Convention |
|---|---|
| **Branch strategy** | Trunk-based development (short-lived feature branches) |
| **Default branch** | `main` |
| **Commit format** | [Conventional Commits 1.0.0](https://www.conventionalcommits.org/) |
| **PR review** | ≥ 1 approver, all CI checks green |
| **Coverage gate** | ≥ 75% (per ADR-0018) |
| **Lint gate** | `golangci-lint` clean (per ADR-0018) |
| **Security gate** | Trivy + gosec + gitleaks + govulncheck clean |
| **Proto gate** | `buf lint` + `buf breaking` check |

---

## 1. Branch Strategy — Trunk-Based Development

ParkirPintar mengikuti **Trunk-Based Development** (TBD), bukan GitFlow.
Alasan: TBD lebih cocok untuk continuous deployment + small team + microservices.

### Branch types

```
main                                          (protected, always deployable)
 │
 ├── feat/reservation-overdue-check           (short-lived, ≤ 3 days)
 ├── fix/payment-webhook-signature
 ├── chore/upgrade-go-1.26
 └── docs/api-changelog
```

### Rules

1. **`main` adalah single source of truth** — always green, always deployable.
2. **Feature branches ≤ 3 hari umur** — merge cepat, hindari long-lived branches.
3. **No `develop` / `release` branches** — TBD melarang stable parallel branches.
4. **Hotfix langsung di-branch dari `main`** + merge back ke `main`.
5. **Production deploy dari `main`** via CI pipeline (`deploy-eks.yml`).

### Branch naming

| Prefix | Use case | Example |
|---|---|---|
| `feat/` | New feature/endpoint | `feat/reservation-cancel-policy` |
| `fix/` | Bug fix | `fix/jwt-expiry-validation` |
| `chore/` | Tooling, deps, refactor non-feature | `chore/upgrade-otel-1.40` |
| `docs/` | Documentation only | `docs/runbook-pod-crashloop` |
| `test/` | Test additions/improvements | `test/expiry-worker-edge-cases` |
| `refactor/` | Code refactor (no behavior change) | `refactor/extract-billing-pricing` |
| `security/` | Security fix/hardening | `security/rate-limit-tightening` |

---

## 2. Commit Conventions — Conventional Commits

Format: `<type>(<scope>): <subject>`. Body + footer optional.

### Types

| Type | Use |
|---|---|
| `feat` | New feature |
| `fix` | Bug fix |
| `docs` | Documentation only |
| `chore` | Tooling, deps, no logic change |
| `refactor` | Code restructure, no behavior change |
| `test` | Test additions |
| `perf` | Performance improvement |
| `security` | Security fix/hardening |
| `ci` | CI/CD changes |
| `revert` | Revert previous commit |

### Scopes (suggested)

- Service: `gateway`, `reservation`, `billing`, `payment`, `notification`
- Layer: `pkg`, `proto`, `deploy`, `observability`
- Cross-cutting: `lint`, `test`, `deps`, `devx`

### Examples

```
feat(reservation): add overdue invoice pre-check before create

ADR-0014: prevent driver dengan unpaid invoice > 24h dari create
new reservation. Behavior: return PERMISSION_DENIED dengan error code
OVERDUE_INVOICE_BLOCKED. Bypass kalau billing service down (graceful
degradation).

Refs: #123
```

```
fix(payment): handle Midtrans webhook timeout properly

Midtrans webhook bisa timeout setelah 30s. Sebelumnya kita return 200
mid-process → Midtrans assume success → no retry. Sekarang return 200
hanya setelah persist + ack ke NATS.

Fixes: #145
```

```
chore(deps): bump otel libraries v1.37 -> v1.40
```

```
docs(api): add Idempotency-Key header documentation
```

### Breaking changes

Tambahkan `!` setelah type/scope ATAU footer `BREAKING CHANGE:`:

```
feat(api)!: rename reservation.status enum values

BREAKING CHANGE: STATUS_PENDING -> STATUS_AWAITING_CHECKIN.
Client harus update enum mapping. Migration guide di docs/api/CHANGELOG.md.
```

---

## 3. Pull Request Workflow

### Before opening PR

```bash
# Pre-flight checklist
make lint                    # golangci-lint clean
make test                    # unit tests pass
make proto-lint              # proto schema valid
make proto-breaking          # no breaking change (kecuali intentional)
make unit-test-coverage      # ≥ 75% coverage
```

### PR template

GitHub akan auto-populate via `.github/pull_request_template.md`. Minimum:

```markdown
## Summary
<satu paragraf — apa & kenapa>

## Type of change
- [ ] Bug fix (non-breaking)
- [ ] New feature (non-breaking)
- [ ] Breaking change (BREAKING in commit msg)
- [ ] Documentation only

## Test plan
- [ ] Unit tests added/updated
- [ ] Integration tests added (kalau pakai DB/Redis/NATS)
- [ ] E2E tests added (kalau new user-facing flow)
- [ ] Manual testing steps di-dokumentasi

## Checklist
- [ ] Code follow hexagonal architecture (ports/adapters)
- [ ] Errors wrapped dengan `fmt.Errorf("%w", err)`
- [ ] Logging structured via `zap` (no `fmt.Println`)
- [ ] Tracing span di-create untuk new endpoint
- [ ] Metrics added kalau ada new SLI
- [ ] ADR written kalau architectural decision
- [ ] CHANGELOG.md updated kalau breaking change

## Related ADRs / Issues
- Closes #123
- Related: ADR-0011

## Risk assessment
- [ ] Low — internal refactor / docs / tests
- [ ] Medium — new feature, isolated to one service
- [ ] High — multi-service, schema change, or security-relevant
```

### Review SLA

| Risk | Expected first review | Approval threshold |
|---|---|---|
| Low | 1 day | 1 approver |
| Medium | 2 days | 1 approver |
| High | 3 days | 2 approvers + ADR |

### CI Checks (must all pass)

Defined di `.github/workflows/pipeline.yml`:

1. **Lint** — `golangci-lint` + `buf lint`
2. **Unit tests** — `go test -race -cover` per module
3. **Integration tests** — testcontainers (Postgres, Redis, NATS)
4. **E2E tests** — full docker-compose stack
5. **Security scan** — Trivy (image) + gosec + gitleaks + govulncheck
6. **Sonar Quality Gate** — coverage ≥ 75%, no critical bugs/vulnerabilities
7. **Build images** — multi-stage Docker, push ke GHCR
8. **Proto breaking check** — `buf breaking --against main`

### Merge strategy

- **Squash merge** untuk feature branches (single commit di main)
- **Merge commit** untuk hotfix branches (preserve audit trail)
- **No fast-forward only** (preserve PR context via merge commit)

---

## 4. Local Development Setup

### Prerequisites

- Go 1.26+
- Docker + Docker Compose
- `make`
- `buf` v1.34+ (atau via `make tools`)
- `golangci-lint` v1.59+

### First-time setup

```bash
git clone https://github.com/ajiperdana/parkir-pintar.git
cd parkir-pintar

# Install dev tools
make tools

# Copy env templates
cp .env.example .env
cp services/gateway/example.env services/gateway/.env.local
# (repeat for each service)

# Boot full stack via Docker Compose
make demo-up

# Wait for healthy
make demo-wait

# Tail logs
make demo-logs
```

### Running tests

```bash
make test                  # unit tests (race + coverage)
make test-integration      # integration (testcontainers)
make test-e2e              # E2E (docker-compose stack)
make test-load             # load tests (k6)
```

### Code generation

```bash
make proto                 # regenerate gRPC stubs from .proto
make gen-mocks             # regenerate mocks for interfaces with //go:generate
```

---

## 5. Code Style

### Go conventions

- Follow [Effective Go](https://go.dev/doc/effective_go) + [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Use `gofmt` (enforced via `golangci-lint`)
- Package comments via `// Package xxx ...` on each `.go` file's first declaration

### ParkirPintar specifics

- **Hexagonal Architecture**: `internal/usecase/` = business logic, `internal/adapter/` = I/O boundaries, `internal/domain/` = entities
- **Error wrapping**: `fmt.Errorf("operation context: %w", err)` untuk preserve error chain
- **Logging**: `zap.Logger` injected via constructor, never global
- **Tracing**: OTel span per usecase entry point, attributes for business identifiers
- **Configuration**: env vars via `getenv()` helper, defaults documented di `example.env`
- **Tests**: prefer fakes over mocks for stateful workflows (ADR-0020)

### Architectural decisions

Setiap architectural choice yang affect > 1 service atau punya long-term consequence WAJIB di-document sebagai ADR di `docs/architecture/adr/`. Template di `docs/architecture/adr/0000-template.md`.

---

## 6. Quality Gates (Enforced)

Lihat [ADR-0018](docs/architecture/adr/0018-lint-quality-gate-strategy.md) untuk full rationale.

| Gate | Threshold | Tool |
|---|---|---|
| Test coverage | ≥ 75% | go test + Sonar |
| Lint clean | 0 issues | golangci-lint |
| Cyclomatic complexity | ≤ 15 | gocyclo |
| Cognitive complexity | ≤ 15 | gocognit |
| Code duplication | ≤ 3% | Sonar |
| Critical bugs | 0 | Sonar |
| Critical vulnerabilities | 0 | Trivy + gosec |
| Hardcoded secrets | 0 | gitleaks |
| Proto breaking changes | 0 (auto-approved breaks ditolak) | buf breaking |

---

## 7. Reporting Issues

Use GitHub Issues dengan template:

- **Bug report**: include reproduction steps, expected vs actual, logs (sensitized), env (dev/staging/prod)
- **Feature request**: include user story, acceptance criteria, why current approach insufficient
- **Security**: **JANGAN public report**. Email `ajiperdanaputra90@gmail.com` dengan PGP encrypted detail.

---

## 8. Maintainer Approval Path

Untuk PR yang touch hal sensitif berikut, approval dari `@ajiperdana` required:

- `.github/workflows/**` — CI/CD pipelines
- `deploy/**` — infrastructure config
- `docs/architecture/adr/**` — architectural decisions
- `proto/**` — API contract
- Any file dengan `CODEOWNERS` rule (lihat `.github/CODEOWNERS`)

---

## References

- [Conventional Commits Spec](https://www.conventionalcommits.org/en/v1.0.0/)
- [Trunk-Based Development](https://trunkbaseddevelopment.com/)
- [Semantic Versioning](https://semver.org/)
- [ADR-0018](docs/architecture/adr/0018-lint-quality-gate-strategy.md) — quality strategy
- [ADR-0019](docs/architecture/adr/0019-ci-pipeline-architecture.md) — CI pipeline architecture
- [ADR-0020](docs/architecture/adr/0020-test-doubles-strategy.md) — test doubles strategy
