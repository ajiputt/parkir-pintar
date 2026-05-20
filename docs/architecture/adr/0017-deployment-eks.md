# ADR-0017 — Deployment Strategy: EKS over ECS Fargate

**Status:** Accepted
**Date:** 2026-05-09
**Deciders:** Aji Perdana Putra
**Supersedes:** Partially supersedes [ADR-0009 (initial AWS deploy via Fargate)](0009-defer-search-presence-services.md) untuk compute layer

---

## Context

ParkirPintar harus deploy ke AWS untuk demo + production-readiness assessment. Dua kandidat utama untuk container orchestration:

1. **Amazon ECS Fargate** — AWS-native, serverless container, simpler ops
2. **Amazon EKS (Elastic Kubernetes Service)** — managed K8s control plane, vendor-neutral

Existing repo sudah punya:
- `deploy/terraform/aws-ecs/` (Terraform module untuk ECS)
- `.github/workflows/deploy-aws.yml` (target ECS Fargate)
- `deploy/helm/parkir-pintar/` (Helm chart, K8s-native)

Repo telah diset cross-platform — Helm chart sudah ada (sebagai opsi K8s/OpenShift), tapi pipeline AWS aktif baru ke ECS. Pertanyaan: untuk **first AWS deploy actual**, pilih ECS atau EKS?

## Decision Drivers

| Driver | Weight | Notes |
|--------|--------|-------|
| Senior-level competency demonstration | High | Assessment context — show K8s breadth |
| Operational complexity | Medium | EKS lebih kompleks tapi bisa di-mitigate via managed addons |
| Portability (multi-cloud / on-prem ready) | High | Asumsikan di masa depan ekspansi atau hybrid |
| Cost (demo env) | Medium | Bedanya ~$73/mo control plane fee |
| Existing artifacts compatible | High | Helm chart sudah ada — direct fit ke K8s |
| Team K8s familiarity | High | Senior backend di Telkomsel context — K8s skill expected |
| NATS JetStream as StatefulSet | High | NATS perlu persistent volume — native di K8s, awkward di ECS |
| Stateful workload future | Medium | Roadmap include caching layer, scheduled jobs — K8s ekosistem matang |
| Service mesh roadmap | Low-Medium | Linkerd/Istio seamless di EKS, ECS Fargate tidak support |
| Time to first deploy | Medium | ECS lebih cepat (~1 jam), EKS perlu addon setup (~3 jam) |

## Considered Options

### Option A — ECS Fargate (existing setup)

**Pros:**
- Setup paling cepat (Terraform module sudah ada, deploy-aws.yml siap)
- No control plane cost (~$73/mo savings)
- AWS-managed everything (network, scheduling, scaling)
- Lower learning curve untuk team yang belum K8s-native

**Cons:**
- Vendor lock-in ke AWS (migration cost ke GCP/Azure tinggi)
- Terbatas untuk stateful workload — NATS JetStream perlu workaround (EFS volume tidak ideal)
- Service discovery via Cloud Map kurang elegan vs K8s ServiceDNS
- Tidak ada service mesh native option
- Setiap update task definition revision baru — slower rollback granularity
- Less impressive untuk Senior assessment ("ECS adalah default, EKS adalah growth")

### Option B — EKS dengan EC2 node group (chosen)

**Pros:**
- Helm chart existing direct reuse — zero migration code
- StatefulSet untuk NATS dengan EBS gp3 = native fit
- IRSA (IAM Roles for Service Accounts) = pod-level AWS access tanpa long-lived keys
- Ekosistem K8s lengkap: External Secrets, ALB Controller, Prometheus, ArgoCD, Linkerd
- Portable — kalau pindah ke on-prem OpenShift, cuma ganti values overrides
- Demonstrasi senior-level competency (K8s + Helm + IRSA + ALB Controller + ESO)
- Spot instance support → cost savings up to 70%

**Cons:**
- Control plane fee $73/mo fixed (tidak bisa scale to zero)
- More moving parts untuk setup (5 addon vs 0 di ECS)
- Operational complexity lebih tinggi (kubectl, helm, RBAC)
- Time-to-first-deploy ~3-4 jam vs ECS ~1 jam

### Option C — EKS Fargate (hybrid)

EKS dengan Fargate profiles bukannya EC2 node group.

**Pros:**
- Serverless di K8s — no node management
- Auto-scale dari 0
- Mengurangi attack surface (no node OS untuk patch)

**Cons:**
- DaemonSet tidak support (FluentBit, Prometheus node-exporter perlu workaround)
- Slower pod startup (~30-60s vs ~5s di EC2)
- StatefulSet limited (NATS perlu EBS persistent volume tidak native di Fargate)
- Cost lebih tinggi untuk steady-state workload
- Overkill untuk single-area demo

---

## Decision

**Adopt EKS dengan EC2 managed node group + Spot instance untuk demo, with path to Multi-AZ on-demand untuk production.**

Rationale:
1. Helm chart existing zero-rewrite — direct deploy
2. NATS JetStream butuh EBS persistence → StatefulSet native
3. Senior assessment context: EKS + IRSA + Helm = stronger competency signal
4. Spot instance + scheduled scaling (`aws-sleep.yml` workflow) bring demo cost ke $80-120/mo

**Existing ECS setup (Terraform + deploy-aws.yml) di-keep** sebagai documented alternative — kalau cost-sensitive prod nanti switch ke ECS Fargate, Helm chart tinggal compile ke task definition. Tidak active deploy ke ECS.

## Consequences

### Positive

- **Portfolio/assessment value:** demonstrates senior K8s competency (Helm packaging, IRSA, External Secrets, ALB Controller).
- **Future-proof:** any cloud (GCP GKE, Azure AKS, on-prem OpenShift) requires only values override.
- **Native fit untuk NATS** — StatefulSet + EBS volume.
- **Service mesh ready** — Linkerd/Istio dapat di-install incremental tanpa rewrite app code.
- **GitOps ready** — ArgoCD watch repo, auto-sync ke cluster.

### Negative

- **Control plane fee:** $73/mo non-negotiable (vs $0 di ECS Fargate). Mitigasi: gunakan demo cluster on-demand, teardown dengan `aws-teardown.yml` setelah demo selesai.
- **Setup overhead:** 5 cluster addon untuk first deploy (ALB Controller, ESO, Metrics Server, Cluster Autoscaler, Prometheus). Mitigasi: dokumentasikan urutan install di [aws-eks-architecture.md](../../deployment/aws-eks-architecture.md) §5.2.
- **Operational complexity:** kubectl/helm familiarity required untuk on-call. Mitigasi: runbook di `docs/deployment/runbook-eks.md` (TBD), training session sebelum prod cutover.
- **Observability stack lebih besar:** Prometheus + Grafana + Jaeger semua perlu dipikirkan vs CloudWatch built-in untuk ECS. Mitigasi: pakai `kube-prometheus-stack` chart untuk one-shot setup.

### Neutral

- **Dual-track CI:** existing `deploy-aws.yml` (ECS) tetap valid untuk dokumentasi alternative path. Active deploy via `deploy-eks.yml` (akan ditambah).
- **Image registry:** ECR sebagai default untuk EKS (low-egress dari same region). Existing CI push ke GHCR sebagai mirror untuk public visibility.

## Implementation Plan

### Phase 1 — Foundation (this sprint)

1. ✅ Helm chart audit — pastikan EKS-compatible (already done, single chart bisa run di any K8s)
2. 🔲 Tambah `deploy/helm/parkir-pintar/templates/serviceaccount.yaml` dengan IRSA annotation support
3. 🔲 Tambah `deploy/helm/parkir-pintar/values.eks.yaml` overrides (ALB ingress class, IRSA roles)
4. 🔲 Tambah `deploy/k8s/eks/external-secret.yaml` untuk ESO sync
5. 🔲 Tambah `.github/workflows/deploy-eks.yml` workflow (Helm-based, OIDC auth)

### Phase 2 — Production hardening

1. Migrate ke Multi-AZ RDS + Redis
2. Setup ArgoCD untuk GitOps
3. Service mesh (Linkerd) untuk mTLS antar service
4. PodDisruptionBudget + Pod Priority untuk graceful eviction
5. CloudWatch Container Insights + custom Prometheus alerts

### Phase 3 — Scale (future)

1. Cluster autoscaler tuning
2. Karpenter sebagai pengganti Cluster Autoscaler (better bin-packing)
3. MSK kalau NATS volume melewati 10k msg/s
4. Multi-region active-active (EKS di 2 region + Aurora Global Database)

---

## References

- [AWS EKS Best Practices Guide](https://aws.github.io/aws-eks-best-practices/)
- [Helm Chart Best Practices](https://helm.sh/docs/chart_best_practices/)
- [IRSA — IAM Roles for Service Accounts](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)
- Internal: [aws-eks-architecture.md](../../deployment/aws-eks-architecture.md), [pre-push-checklist.md](../../deployment/pre-push-checklist.md)
- Existing artifacts: `deploy/helm/parkir-pintar/`, `deploy/terraform/aws-ecs/` (kept as alt-path doc)

## Revision History

| Date | Author | Change |
|------|--------|--------|
| 2026-05-09 | Aji | Initial draft, decision recorded |
