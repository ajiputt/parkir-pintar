# Postmortem — [Title]

> **Blameless** — fokus pada sistem & proses, bukan individu.

| Field | Value |
|---|---|
| Date of incident | YYYY-MM-DD |
| Duration | HH:MM UTC – HH:MM UTC |
| Severity | SEV-X |
| Author | @username |
| Status | Draft / Final |
| Reviewed by | @username, @username |

## TL;DR

<!-- 2-3 kalimat: apa yang terjadi, dampak, fix -->

## Impact

| Metric | Value |
|---|---|
| Users affected | ~N |
| Reservations failed | N |
| Revenue impact | Rp X |
| SLO consumed (error budget) | X% |

## Timeline (UTC)

| Time | Event |
|---|---|
| HH:MM | First alert: `<alert name>` |
| HH:MM | On-call ack |
| HH:MM | Triage start, war-room dibuka |
| HH:MM | Suspected root cause identified |
| HH:MM | Mitigation applied (rollback / scale / config) |
| HH:MM | Metrics recovered |
| HH:MM | Confirmed resolution, war-room closed |

## Root Cause

<!-- Detailed technical explanation. 5 Whys helpful here. -->

### 5 Whys

1. **Why did the alert fire?** ...
2. **Why did that happen?** ...
3. **Why?** ...
4. **Why?** ...
5. **Why?** *(Root cause)*

## Contributing Factors

- ...
- ...

## What Went Well

- ...
- ...

## What Went Poorly

- ...
- ...

## Where We Got Lucky

- ...

## Action Items

| # | Action | Owner | Priority | Due | Tracker |
|---|---|---|---|---|---|
| 1 | ... | @user | P0 | YYYY-MM-DD | LINK |
| 2 | ... | @user | P1 | YYYY-MM-DD | LINK |

### Action item categories

- **Prevention**: cegah kejadian serupa
- **Detection**: deteksi lebih cepat next time
- **Mitigation**: response lebih cepat
- **Process**: improve runbook / on-call

## Lessons Learned

<!-- Insight yang bisa share ke seluruh org -->

## References

- Alert: <link>
- Trace: <Jaeger link>
- Logs: <Loki link>
- Slack thread: <link>
- PR fix: <link>
- Related ADR: <link>
