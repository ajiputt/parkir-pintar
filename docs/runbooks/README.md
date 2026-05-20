# Operational Runbooks

Folder ini berisi step-by-step procedures untuk handle production incidents
dan operational tasks ParkirPintar. Setiap runbook follow format konsisten
biar on-call engineer bisa execute cepat under stress.

## Available runbooks

| Runbook | Severity | Use when |
|---|---|---|
| [high-error-rate.md](./high-error-rate.md) | SEV2 | gRPC error rate > 5% selama 2+ menit |
| [db-connection-saturated.md](./db-connection-saturated.md) | SEV2 | Postgres connection pool exhausted |
| [nats-stream-lag.md](./nats-stream-lag.md) | SEV3 | NATS consumer lag > 1000 messages |
| [pod-crashloop.md](./pod-crashloop.md) | SEV2 | Pod restart > 3 dalam 15 menit |
| [secret-rotation.md](./secret-rotation.md) | Planned | Quarterly JWT/DB/API key rotation |

## Severity definitions

- **SEV1** — Total outage, all users affected, immediate response
- **SEV2** — Partial outage atau severe degradation, fast response < 30 min
- **SEV3** — Minor degradation, response within business hours
- **SEV4** — Non-urgent issue, scheduled work

## Runbook format

Setiap runbook punya structure konsisten:

1. **Symptom** — apa yang dilihat user/dashboard
2. **Severity** — SEV1-4
3. **Quick triage** — fast yes/no decision tree
4. **Diagnostic steps** — kumpulan command + dashboard link
5. **Resolution paths** — branching by root cause
6. **Verification** — bagaimana confirm fixed
7. **Postmortem hooks** — catatan apa yang harus di-document

## On-call expectations

- Acknowledge alert dalam **5 menit** untuk SEV1, **15 menit** untuk SEV2
- Initial assessment (severity + triage) dalam **15 menit**
- Status update tiap **30 menit** ke #incidents Slack channel
- Postmortem draft dalam **48 jam** untuk SEV1-2

## References

- [Grafana Alerting](https://grafana.parkirpintar.id/alerting) (internal)
- [Incident channel](https://parkirpintar.slack.com/channels/incidents)
- [Status page](https://status.parkirpintar.id)
- [ADR-0017](../architecture/adr/0017-deployment-eks.md) — EKS deployment model
