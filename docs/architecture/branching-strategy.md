# Branching & Release Strategy

> Memenuhi kompetensi #2.1 (Software Construction → Collaboration tools).

## Strategi: **Trunk-Based Development**

Pilihan ini cocok untuk tim kecil-medium dengan CI/CD matang.

```
main (trunk) ────●─────●─────●─────●────●──── (always shippable)
                /     /   /     /
               /     /   /     /
       feature-x  fix  feature-y
```

### Branch convention

| Branch | Lifetime | Catatan |
|---|---|---|
| `main` | permanent | Always green, deployable, di-protect (require PR + review). |
| `feature/<short-desc>` | < 3 hari | Small, focused. Merge cepat lewat PR. |
| `fix/<ticket-id>` | < 1 hari | Hotfix kecil. |
| `release/<version>` | optional | Hanya kalau perlu cherry-pick stabilization. Trunk-based prefer tagging langsung. |

### Naming examples

```
feature/reservation-overlap-constraint
fix/RES-123-double-charge-edge-case
chore/upgrade-otel-1.28
docs/adr-0009-event-sourcing
```

## Release Strategy

| Channel | Trigger | Versioning |
|---|---|---|
| **dev** | every merge ke `main` | auto-deploy ke dev cluster, tag `dev-<sha>` |
| **staging** | manual workflow_dispatch | `staging-YYYYMMDD-<n>` |
| **prod** | git tag `vX.Y.Z` | semver, signed tag, release notes auto-gen |

### Semantic versioning

- **MAJOR**: breaking change di public API (proto schema breaking, DB migration non-backward-compat).
- **MINOR**: new feature, backward-compatible.
- **PATCH**: bugfix, backward-compatible.

Breaking change wajib lewat ADR + komunikasi 1 sprint sebelumnya.

## Code Review Policy

PR wajib:
1. ≥ 1 reviewer approval.
2. CI lulus (lint, unit, integration, e2e, security).
3. Author melengkapi PR template (lihat `.github/pull_request_template.md`).
4. Linked ke ticket (Jira / GitHub Issues).

Reviewer fokus pada:
- Domain correctness (business rule sesuai use case)
- Concurrency & error handling
- Test coverage delta
- Security: input validation, auth, secret leak
- Performance: N+1, unbounded query

## Hotfix Procedure

```
1. git checkout -b fix/<ticket> main
2. Patch + test
3. PR ke main (label: hotfix, fast-track 1 reviewer)
4. Tag immediate: vX.Y.(Z+1)
5. Promote ke prod via release.yml
6. Postmortem (lihat docs/postmortem-template.md)
```
