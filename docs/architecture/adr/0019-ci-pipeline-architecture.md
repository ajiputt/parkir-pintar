# ADR 0019 — CI Pipeline Architecture (Reusable Workflows + Orchestrator)

**Status**: Accepted
**Date**: 2026-05-16
**Supersedes parts of**: monolithic `ci.yml` (deleted)
**Related**: ADR-0016 (security posture), ADR-0017 (deployment EKS), ADR-0018 (lint quality gate)

## Context

ParkirPintar CI/CD awalnya pakai 4 workflow file independent:

| File | Trigger | Scope |
|---|---|---|
| `ci.yml` | push/PR main | lint, unit-test, integration, e2e, build-images |
| `security.yml` | push/PR + weekly | gosec, govulncheck, trivy, checkov, gitleaks |
| `sonar.yml` | push/PR | SonarCloud Quality Gate |
| `deploy-eks.yml` | manual | Helm deploy |

Tiap workflow ditrigger paralel by GitHub di event yang sama. Issue:

1. **Tidak ada cross-workflow gating** — `deploy-eks.yml` (manual) tidak tahu apakah `security.yml` & `sonar.yml` pass. Operator harus visual check Actions tab sebelum trigger deploy.
2. **Test duplication** — `unit-test` di `ci.yml` run tests, lalu `sonar.yml` run tests ulang untuk dapat coverage. ~3 menit wasted.
3. **DAG implicit di branch protection** — required checks didefinisi di repo settings (UI), bukan di code. Auditor butuh check 2 source: workflow YAML + branch protection settings.

Pertanyaan: gimana strict-gate deploy ke pass-status semua workflow upstream, tanpa coupling yang berat?

## Decision

**Adopt reusable workflows + single orchestrator pattern.**

### File structure

```
.github/workflows/
├── pipeline.yml            ← Orchestrator (triggered on push/PR)
│
├── _lint.yml               ← Reusable: workflow_call only
├── _unit-test.yml          ← Reusable: workflow_call only (uploads coverage artifact)
├── _integration-test.yml   ← Reusable: workflow_call only
├── _e2e-test.yml           ← Reusable: workflow_call only
├── _build-images.yml       ← Reusable: workflow_call only (matrix)
│
├── security.yml            ← workflow_call + schedule (weekly)
├── sonar.yml               ← workflow_call only (downloads coverage artifact)
└── deploy-eks.yml          ← workflow_call + workflow_dispatch (manual)
```

Convention: `_` prefix = pure reusable (workflow_call only). Tanpa prefix = standalone-callable juga (schedule, dispatch).

### DAG

```
push/PR main → pipeline.yml:
  lint
    └─ unit-test ────┐
         ├─ integration-test
         ├─ e2e-test
         ├─ security
         └─ sonar (consume coverage artifact dari unit-test)
                          │
                          all converge
                          │
                          └─ build-images (push only on main)
                                  └─ deploy-staging (main only, MANUAL approval gate)
                                  └─ pipeline-result (always — aggregate signal)
```

### Single required check for branch protection

`pipeline-result` job aggregate semua gate status jadi 1 pass/fail signal. Branch protection cuma butuh require **1 check**: `Pipeline / Pipeline Result`. Simpler audit story — 1 source of truth (code), bukan 6-7 checkbox di UI.

### Deploy gate — manual approval via GitHub Environments

`deploy-staging` job IS declared di pipeline.yml (bukan di workflow terpisah), tapi pause di **Environment protection rule** sampai operator approve.

**Pattern**: Job declare `environment: staging` → GitHub cek environment config di repo settings → kalau ada "Required reviewers", job stuck di "Waiting" state → reviewer dapat email + Actions UI tunjukin "Review deployments" button → klik Approve → job lanjut.

**Setup (one-time, manual via GitHub UI)**:

1. Repo → Settings → Environments → New environment: `staging`
2. Required reviewers: tambah username sendiri (atau team)
3. (Optional) Wait timer: `0 min` — no forced delay
4. (Optional) Deployment branches: restrict ke `main` only

Pattern yang sama berlaku untuk `production` environment di future (deploy ke prod butuh approval terpisah, possibly dari role yang berbeda).

**Kenapa pattern ini lebih bagus dari workflow_dispatch terpisah**:

- ✅ Single workflow run di Actions UI — full lineage visible (commit → tests → security → sonar → build → deploy approval → deployed).
- ✅ Image tag auto-passed dari `build-images` output (no manual entry).
- ✅ Audit trail otomatis: GitHub log siapa approve + timestamp.
- ✅ Email notification ke reviewer otomatis.
- ✅ Multiple environments handled sama (staging + prod = beda environment config, sama job code).

**Trade-off**: Setup environment di Settings UI bukan di code. Ini convention GitHub yang tidak bisa di-bypass — kompensasi: setup sekali, jalan terus.

## Trade-offs

✅ **Pro**:

- **Single DAG visible** di Actions UI per pipeline run. Audit-friendly.
- **Cross-workflow gating** — `build-images` & `deploy-staging` declared `needs: [integration-test, e2e-test, security, sonar]` di orchestrator. Implicit di YAML, tidak terpisah di repo settings.
- **Coverage artifact passing** — `_unit-test` upload `coverage.out` → `_sonar` download. Skip duplicate test run, save ~3 min.
- **Concurrency control** — `concurrency: group: pipeline-${{ github.ref }}` cancel previous PR runs on rebase. Save CI minutes.
- **Reusable modules** — `_security.yml` callable dari pipeline (every push) AND schedule (weekly deep scan). DRY.
- **PR vs main behavior** — `inputs.push` ke `_build-images` cuma `true` di `push: main`. PR build cuma compile-check, no push to registry. No special handling untuk environment branches.
- **Manual override preserved** — `deploy-eks.yml` masih punya `workflow_dispatch` untuk re-deploy / prod / dry-run.

❌ **Con**:

- **More files** — 9 files vs sebelumnya 4. Cognitive overhead bagi yang gak familiar dengan GitHub Actions reusable workflow pattern.
- **Mitigation**: `_` prefix convention + ADR ini (single doc explain DAG).
- **Reusable workflow inputs/secrets** explicit declaration — boilerplate dibanding job inline. Worth it untuk separation of concern.
- **Limited cross-step state passing** — must use artifacts (file-based, slow) atau job outputs (string only). Tidak ada in-memory volatile state across reusable workflow boundaries.

## Why not alternatives?

### Alt A: Single mega-workflow

Gabungkan semua jobs ke 1 `ci.yml` (~400 line). Mudah trace DAG di 1 file.

❌ **Rejected**:
- Tidak scalable kalau add deploy production / canary / rollback flow.
- Re-run "failed jobs only" untuk file besar = nightmare untuk maintain.
- File 400+ lines = code review burden.

### Alt B: Separate workflows + branch protection (status quo)

Tetap 4 file independent, gate via repo settings "required status checks".

❌ **Rejected**:
- Gate config split antara YAML + UI (2 source of truth).
- No cross-workflow data passing (e.g., coverage artifact untuk Sonar).
- Manual deploy operator harus visual check semua gate, no automation.

### Alt C: workflow_run trigger chain

Workflow A trigger workflow B via `on: workflow_run: [A]`.

❌ **Rejected**:
- Async, susah debug — Actions UI tidak jelas show parent-child relationship.
- Status check rendering inconsistent di PR (workflow_run jalan di main context, tidak nge-decorate PR).
- Secrets handling tricky di workflow_run scope.

## Migration

### Phase 1 (this PR)

1. Split `ci.yml` jobs → reusable `_*.yml` files.
2. Add `workflow_call` ke `security.yml`, `sonar.yml`, `deploy-eks.yml`.
3. Create `pipeline.yml` orchestrator.
4. Delete `ci.yml`.

### Phase 2 (post-merge, manual)

5. Update branch protection rule di repo settings:
   - **Remove** required checks: `Lint`, `Unit Tests + Coverage`, `gosec`, `govulncheck`, `SonarCloud Code Analysis`, etc.
   - **Add** single required check: `Pipeline / Pipeline Result`.
6. Verify first PR setelah migration:
   - All 6 gates fire dalam pipeline DAG.
   - `pipeline-result` job report aggregate.
   - PR merge button block sampai pipeline-result hijau.

### Phase 3 (future hardening)

7. Per-environment deploy gating:
   - Staging: auto-deploy on `push: main`.
   - Production: require manual approval via `environment: production` protection rule.
8. Add canary deploy step (kubectl rollout pause + smoke test) sebelum full rollout.
9. Add rollback workflow: `rollback.yml` callable dari Slack slash command.

## Operational notes

### Re-running failed gates

- "Re-run failed jobs" di Actions UI works correctly — cuma re-run failed reusable workflow.
- "Re-run all jobs" trigger full pipeline dari scratch (lint → ... → deploy).

### Schedule trigger (weekly security)

`security.yml` punya `schedule: cron: '0 4 * * 1'` trigger SELAIN `workflow_call`. Jadi:
- Setiap Senin 04:00 UTC: `security.yml` jalan standalone (semua security jobs, including trivy & gitleaks).
- Setiap PR/push: `security.yml` di-call dari `pipeline.yml` (sama jobs jalan, tapi sebagai bagian dari pipeline DAG).

### Manual deploy override

`deploy-eks.yml` tetap punya `workflow_dispatch` trigger. Operator pilih:
- **Re-deploy**: dispatch ulang dengan `image_tag` dari deploy sebelumnya.
- **Prod**: dispatch dengan `environment: prod` (post-staging validation).
- **Dry-run**: dispatch dengan `dry_run: true` untuk preview Helm diff.

## References

- `pipeline.yml` — orchestrator
- `_lint.yml`, `_unit-test.yml`, `_integration-test.yml`, `_e2e-test.yml`, `_build-images.yml` — reusable building blocks
- `security.yml`, `sonar.yml`, `deploy-eks.yml` — dual-trigger workflows
- ADR-0016 (security posture — gosec/govulncheck/trivy strict gates)
- ADR-0017 (deployment EKS — Helm + OIDC)
- ADR-0018 (Quality Gate via SonarCloud)
- GitHub docs: [Reusing workflows](https://docs.github.com/en/actions/using-workflows/reusing-workflows)
