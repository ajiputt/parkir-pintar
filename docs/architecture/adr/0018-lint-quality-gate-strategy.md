# ADR 0018 — Lint & Quality Gate Strategy

**Status**: Accepted — SonarCloud Quality Gate enabled (full Phase 3)
**Date**: 2026-05-15

## Context

ParkirPintar memakai `golangci-lint v2` sebagai static analyzer. Tools sejenis di industri (SonarQube, SonarCloud, Codacy) punya konsep **Quality Gate**: kombinasi metrics yang harus pass sebelum code merge. Pertanyaan: gimana kita enforce code quality tanpa block delivery?

## Decision

**Tiered lint enforcement** dengan severity-based blocking:

### Tier 1 — Blocking (Real Bugs)

Linter yang flag actual bugs, resource leaks, atau security issue. **CI FAIL** kalau ada violation:

| Linter | Detects |
|---|---|
| `errcheck` | Error return value yang gak di-check |
| `govet` | Suspicious code (shadowed var, lock copy) |
| `staticcheck` | Nil pointer, unused, dead code |
| `bodyclose` | HTTP response body leak |
| `sqlclosecheck` | DB rows/stmt leak |
| `rowserrcheck` | `rows.Err()` after `Scan()` missing |
| `contextcheck` | Context not propagated |
| `errorlint` | `%v` vs `%w` for error wrapping |
| `nilerr` | Return `nil` after non-nil error |
| `copyloopvar` | Go 1.22+ loop var capture |
| `durationcheck` | `time.Duration` math mistake |

### Tier 2 — Disabled di MVP (Style Only)

Linter yang flag style preference, bukan bug:

| Linter Rule | Why deferred |
|---|---|
| `revive.exported` | Force doc comment di semua exported symbol — high noise, low risk |
| `gocritic` (default rules) | Style suggestions, banyak false positive |
| `prealloc` di test files | Capacity hint kadang gak ketahuan compile-time |

Tier 2 di-enable nanti di **production hardening pass** (post-MVP).

### Security Linter — Separate Workflow

Security-related checks (gosec, govulncheck) di `.github/workflows/security.yml` **TETAP STRICT BLOCKING**, gak ada relaxation. Ini line-in-the-sand: security > delivery speed.

## Comparison dengan SonarQube/SonarCloud

| Capability | golangci-lint (current) | SonarCloud (next phase) |
|---|---|---|
| Bug detection | ✓ Tier 1 | ✓ + Hotspot |
| Code smells | Limited (gocritic) | ✓ + Cognitive complexity |
| Security vulns | gosec/govulncheck separate | Built-in (SAST) |
| Coverage gate | Manual via codecov | Built-in threshold |
| New Code mode | ❌ (no baseline) | ✓ Diff-mode |
| PR decoration | Annotation | Auto comment via app |
| Quality Gate (pass/fail) | Per-linter | Single boolean gate |

**Decision**: SonarCloud integration di **next iteration** (production hardening). Cost: free untuk public repo, paid (~$10/dev/month) untuk private.

## Migration Path (MVP → Production)

### Phase 1: MVP (current state)
- Tier 1 blocking via golangci-lint
- Tier 2 disabled
- Security gate strict (govulncheck fail-on-vuln)

### Phase 2: Production Hardening
1. Enable Tier 2 linter rules incremental:
   - Pakai `--new-from-rev=main` flag → cuma lint diff baru
   - Existing legacy issues di-baseline (issue suppress)
2. Add SonarCloud integration:
   - Quality Gate: "Parkir Pintar Standard"
   - Conditions:
     - New Code Bugs: 0
     - New Code Vulnerabilities: 0
     - New Code Coverage: ≥ 75%
     - New Code Duplicated Lines: < 3%
     - Maintainability Rating: A
3. Add `unparam` (unused param), `gocognit` (cognitive complexity), `nestif`.

### Phase 3: Mature Practice
- Per-team rules customization
- Security Champion review program
- Quality Gate breakdown by team/service

## Trade-offs

✅ **Pro tiered approach**:
- Avoid analysis paralysis di MVP
- Security risk tetap covered (separate strict gate)
- Documented escape hatch (this ADR) — bukan hidden config quirk

❌ **Con**:
- Possible "broken windows" effect — code quality drift kalau Tier 2 di-defer terlalu lama
- Mitigation: schedule Tier 2 enable di sprint planning, max 2 sprints after MVP launch

## Why Not Just Always Strict?

Pertimbangan delivery time:
- ~50+ exported symbols tanpa doc comment (revive `exported` rule)
- Add comment di semua: ~3-4 jam manual work
- For MVP demo: time better spent on observability + lifecycle scripts
- Doc comments NOT security risk, can be addressed post-launch

## Why Not Just Always Soft?

Production code MUST enforce real-bug detection:
- `errcheck` finding: silent error ignored → cascading bugs
- `bodyclose`: HTTP resource leak → memory exhaustion under load
- `staticcheck`: nil pointer → runtime panic

These are blockers untuk production-ready system.

## SonarCloud Quality Gate (Phase 3 — Implemented)

ParkirPintar memakai **SonarCloud** sebagai single source-of-truth untuk Quality Gate. Lihat `sonar-project.properties` + `.github/workflows/sonar.yml`.

### Setup Steps (one-time, manual via SonarCloud UI)

1. Sign up di https://sonarcloud.io via GitHub OAuth.
2. **Import organization**: pilih GitHub user/org → SonarCloud auto-generate org key `ajiputt`.
3. **Add new project**: pilih repo `parkir-pintar` → project key `ajiputt_parkir-pintar`.
4. Catat **Project Key** dan **Organization** — verify match dengan `sonar-project.properties`:
   - `sonar.projectKey=ajiputt_parkir-pintar`
   - `sonar.organization=ajiputt`
   - Note: org key (`ajiputt`) beda dengan display name (`Aji Perdana Putra`). Pakai **key** lowercase di config, bukan display name.
5. **Generate token**: Account → Security → New token (scope: project). Copy token.
6. **GitHub Secret**: di repo Settings → Secrets and variables → Actions → New repository secret:
   - Name: `SONAR_TOKEN`
   - Value: token dari step 5
7. **Disable auto-analysis** (kalau enabled default): supaya CI-based analysis yang dipakai. Sonar Cloud → Project → Administration → Analysis Method → "CI-based analysis only".

### Quality Gate Definition

Quality Gate name: **"ParkirPintar Standard"** (custom, di-define di SonarCloud UI):

```
On New Code:
  - Bugs                          = 0
  - Vulnerabilities               = 0
  - Security Hotspots Reviewed    = 100%
  - Coverage                      ≥ 75%
  - Duplicated Lines Density      < 3%
  - Maintainability Rating        = A
  - Reliability Rating            = A
  - Security Rating               = A

On Overall Code (legacy baseline):
  - Vulnerabilities               = 0
  - Security Hotspots Reviewed    = 100%
```

**New Code Definition**: "Previous version" (sejak last release tag), atau "Number of days: 30".

### CI Integration

- Workflow `.github/workflows/sonar.yml` trigger di push main + PR.
- Step: run tests dengan coverage → upload report → SonarCloud scan → poll Quality Gate result.
- **Gate FAIL** → workflow exit non-zero → PR can't merge (GitHub branch protection enforces).

### PR Decoration

Setelah scan, SonarCloud auto-comment di PR dengan:
- Issues summary per severity
- Coverage delta
- Hotspot list

Plus Quality Gate badge (pass/fail) di PR check.

### Migration Notes

- Tier 1 lint di golangci-lint **tetap aktif** di CI (sebagai early signal sebelum kirim ke Sonar).
- Tier 2 di-handle oleh Sonar (gocritic, prealloc, code smells).
- Real bugs di-block double-gate: lint (gosec-equivalent) + Sonar (Quality Gate).

## References

- SonarCloud config: `sonar-project.properties`
- Sonar workflow: `.github/workflows/sonar.yml`
- golangci-lint config: `.golangci.yml`
- CI workflow: `.github/workflows/ci.yml`
- Security workflow: `.github/workflows/security.yml`
- Related: ADR-0016 (Security posture)
