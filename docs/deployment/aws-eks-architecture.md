# AWS Architecture — EKS Deployment

Service mapping dan justifikasi untuk deploy ParkirPintar ke AWS EKS. Single-region (ap-southeast-1 / Jakarta), 3 AZ untuk HA.

---

## 1. Service Mapping (AWS Components)

| Layer | AWS Service | Purpose | Why this choice |
|-------|-------------|---------|------------------|
| **Compute** | **EKS (managed Kubernetes)** | Run pod 5 service | Portable, multi-cloud ready, ekosistem K8s lengkap. Fargate untuk pod stateless, EC2 node group untuk stateful (Postgres alternatif self-managed) |
| **Container Registry** | **ECR (private)** | Image hosting | Tight IAM integration, vulnerability scanning built-in, no egress cost ke EKS in same region |
| **Database** | **RDS PostgreSQL 16 (Multi-AZ)** | Primary OLTP store | Managed: backup, PITR, automatic minor patching, auto-failover. Multi-AZ untuk HA |
| **Cache + Distributed Lock + Rate Limit** | **ElastiCache Redis 7 (cluster mode disabled, Multi-AZ)** | Reservation locking, gateway rate limit | Single primary + replica, AOF persistence on, automatic failover |
| **Event Bus** | **NATS JetStream (self-managed di EKS)** | Async events (billing, notification) | NATS sudah dipakai di kode. Run sebagai StatefulSet di-EKS dengan EBS gp3 volume. Alternatif (out-of-scope): MSK kalau scale ke Kafka |
| **Object Storage** | **S3** | Backup, Terraform state, exported reports | Standard untuk Terraform state lock via DynamoDB |
| **Secrets Management** | **AWS Secrets Manager** | DB password, JWT secret, Midtrans key | Rotated automatically. Sync ke K8s via External Secrets Operator |
| **DNS** | **Route 53** | Public + private hosted zone | Healthcheck integration ke ALB target |
| **TLS Cert** | **ACM (free)** | TLS termination di ALB | Auto-renewal |
| **Load Balancer** | **AWS Load Balancer Controller (ALB Ingress)** | L7 entry untuk gateway | Native ALB integration, cost-efficient vs NLB+gateway-LB. Path/host routing |
| **Webhook (Midtrans)** | Same ALB, **dedicated path /v1/payments/midtrans/notification** | Public endpoint Midtrans push | Sticky route ke gateway → forward ke payment service |
| **Egress Email** | **SES (sudah live di kode)** | Notification reminders | sesv2 SDK, domain verified, sandbox lifted |
| **Logs** | **CloudWatch Logs + Container Insights** | Stdout dari pod | FluentBit DaemonSet sebagai log driver |
| **Metrics** | **Prometheus (in-cluster) + CloudWatch metrics** | Service metrics, autoscale signal | Prometheus untuk fine-grained app metrics (Helm `kube-prometheus-stack`), CloudWatch untuk infra metrics |
| **Tracing** | **AWS X-Ray** atau **Jaeger (in-cluster)** | Distributed tracing | OTLP exporter sudah di kode. X-Ray punya sampling otomatis, Jaeger lebih kontrol penuh |
| **CI/CD Compute** | **GitHub Actions (cloud runner)** | Build + test + deploy | OIDC ke AWS, no long-lived keys |
| **Identity** | **IAM + IRSA** | Pod-level AWS access | ServiceAccount → IAM role mapping (notification → SES, payment → Secrets Manager) |
| **Network** | **VPC (3-AZ, public+private subnets)** | Isolation, NAT egress | EKS nodes di private subnet, ALB di public, RDS/Redis di private |
| **Bastion / Operator Access** | **Session Manager (no SSH)** | kubectl tunnel via SSM | No bastion EC2 needed, IAM-based access |

---

## 2. Network Topology

```
Internet
   │
   ▼
┌──────────────────────────┐
│  Route 53 (DNS)          │ → api.parkirpintar.example.com
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│  ACM (TLS cert)          │ → wildcard *.parkirpintar.example.com
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐  ─── public subnet (3 AZ)
│  ALB (Application LB)    │
│  + AWS WAF (optional)    │
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────────────────────────┐  ─── private subnet (3 AZ)
│  EKS cluster (managed control plane)         │
│  ┌────────────────────────────────────────┐  │
│  │ Node Group: Fargate or EC2 (m5.large)  │  │
│  │  ┌──────────┐ ┌──────────┐ ┌────────┐  │  │
│  │  │ gateway  │ │reservation│ │billing │  │  │
│  │  │ (3 pod)  │ │  (3 pod) │ │(2 pod) │  │  │
│  │  └────┬─────┘ └─────┬────┘ └───┬────┘  │  │
│  │  ┌────▼─────┐ ┌─────▼────┐ ┌──▼──────┐ │  │
│  │  │ payment  │ │notification│ │  NATS  │ │  │
│  │  │ (2 pod)  │ │  (1 pod)   │ │ JetStream│ │
│  │  └──────────┘ └─────────────┘ └────────┘ │  │
│  └────────────────────────────────────────┘  │
└──────────────────────┬───────────────────────┘
                       │
            ┌──────────┼──────────┬──────────────┐
            ▼          ▼          ▼              ▼
       ┌─────────┐  ┌──────┐  ┌────────────┐ ┌─────────┐
       │   RDS   │  │Redis │  │ Secrets    │ │   SES   │
       │ Postgres│  │Cache │  │ Manager    │ │ (email) │
       │ (Multi-│  │ (Multi│  │            │ │         │
       │  AZ)   │  │  AZ) │  │            │ │         │
       └─────────┘  └──────┘  └────────────┘ └─────────┘
       (private)   (private)  (VPC endpoint)  (regional)
```

---

## 3. Cost Estimate (rough, 1 demo env, ap-southeast-1)

| Resource | Spec | Monthly cost (USD) |
|----------|------|---------------------|
| EKS control plane | Fixed | $73 |
| EC2 node group | 2× t3.medium on-demand | ~$60 |
| RDS Postgres | db.t3.medium Multi-AZ + 20GB gp3 | ~$80 |
| ElastiCache Redis | cache.t3.micro Multi-AZ | ~$28 |
| ALB | 1 ALB + ~10 LCU | ~$25 |
| ECR | 5 images × ~200MB | ~$1 |
| Data transfer | ~50GB egress | ~$5 |
| CloudWatch Logs | 5GB ingest + retention | ~$5 |
| Route 53 | 1 hosted zone + queries | ~$1 |
| Secrets Manager | 5 secrets | ~$2 |
| SES | <10k emails | ~$0.10 |
| **Total** | | **~$280/month** |

**Cost optimization untuk demo:**
- Pakai Spot instance untuk node group → save 50-70%
- Single-AZ RDS untuk demo (no Multi-AZ) → save 50%
- Schedule node scaling down di luar jam kerja via `aws-wake.yml` / `aws-sleep.yml` workflow
- Workflow `aws-teardown.yml` sudah ada untuk teardown setelah demo selesai

Estimasi demo (single-AZ + spot + scheduled scaling) ≈ **$80-120/month**.

---

## 4. Deployment Stack Choice Justification

### 4.1 Why EKS (vs ECS Fargate)?

| Criterion | EKS | ECS Fargate |
|-----------|-----|-------------|
| Portability | ✅ Vendor-neutral K8s | ❌ AWS-only |
| Ecosystem | ✅ Helm, Operators, Service Mesh | ⚠️ Limited |
| Learning curve | ❌ Steep | ✅ Easy |
| Control plane cost | ❌ $73/mo fixed | ✅ Free |
| Pod-to-pod networking | ✅ Native via CNI | ⚠️ Service discovery via Cloud Map |
| Stateful workload | ✅ StatefulSet + EBS | ❌ Tidak native |
| **Verdict for assessment** | **✅ Better demonstration of senior-level competency** | Simpler but less impressive |

**Decision:** EKS dipilih karena:
1. Portfolio value lebih tinggi untuk Senior Backend competency demo
2. NATS perlu StatefulSet (EBS persistence) — EKS native, ECS perlu workaround
3. Helm chart sudah ada — direct reuse di EKS
4. Service mesh roadmap (Istio/Linkerd) lebih natural di K8s

Trade-off: cost +$73/mo dan operational complexity. Dokumentasi lengkap di [ADR-0017](../architecture/adr/0017-deployment-eks.md).

### 4.2 Why NATS in-cluster (vs MSK)?

NATS sudah dipakai di kode (`pkg/eventbus/`, JetStream durable consumer). Switch ke MSK = rewrite eventbus adapter + management overhead.

In-cluster NATS pakai StatefulSet 3 replica + EBS gp3 50GB per pod = ~$15/month, vs MSK starter $250/month. Untuk volume eventbus ParkirPintar (peak ~100 msg/s), NATS lebih dari cukup.

Migration path: kalau scale ke 10k msg/s atau cross-region replication, swap ke MSK via adapter pattern (eventbus interface sudah abstract).

### 4.3 Why Postgres RDS (vs Aurora)?

| Criterion | RDS Postgres | Aurora Postgres |
|-----------|--------------|------------------|
| Performance | Standard | 3-5× faster reads |
| Cost | Lower | Higher (storage & IOPS) |
| Failover | 60-120s | <30s |
| Compatibility | 100% Postgres | Mostly Postgres |
| **For demo** | **Sufficient** | Overkill |

Migration ke Aurora trivial (snapshot + restore) kalau scale meningkat.

---

## 5. Production Readiness Checklist

### 5.1 EKS cluster setup (one-time)

```bash
# Pakai eksctl untuk simplicity (alternatif: Terraform)
eksctl create cluster \
  --name parkir-demo \
  --region ap-southeast-1 \
  --version 1.30 \
  --nodegroup-name workers \
  --node-type t3.medium \
  --nodes 2 --nodes-min 1 --nodes-max 4 \
  --managed --spot \
  --with-oidc \
  --enable-secrets-encryption

# Verify
aws eks update-kubeconfig --name parkir-demo --region ap-southeast-1
kubectl get nodes
```

### 5.2 Required EKS Add-ons

```bash
# AWS Load Balancer Controller (ALB Ingress)
helm repo add eks https://aws.github.io/eks-charts
helm install aws-lb-controller eks/aws-load-balancer-controller \
  -n kube-system \
  --set clusterName=parkir-demo \
  --set serviceAccount.create=true \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::<account>:role/AWSLoadBalancerControllerRole

# External Secrets Operator (sync Secrets Manager → K8s)
helm repo add external-secrets https://charts.external-secrets.io
helm install external-secrets external-secrets/external-secrets \
  -n external-secrets --create-namespace

# Metrics Server (untuk HPA)
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml

# Cluster Autoscaler (optional, kalau pakai EC2 not Fargate)
helm repo add autoscaler https://kubernetes.github.io/autoscaler
helm install cluster-autoscaler autoscaler/cluster-autoscaler \
  -n kube-system \
  --set autoDiscovery.clusterName=parkir-demo

# kube-prometheus-stack (optional, observability)
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install prom prometheus-community/kube-prometheus-stack -n monitoring --create-namespace
```

### 5.3 IAM Roles for Service Accounts (IRSA)

| ServiceAccount | IAM Permissions |
|----------------|-----------------|
| `notification` | `sesv2:SendEmail`, `secretsmanager:GetSecretValue` |
| `payment` | `secretsmanager:GetSecretValue` (Midtrans key), `sqs:SendMessage` (DLQ optional) |
| `gateway` | (none — TLS via ACM, no AWS API calls) |
| `reservation` | `secretsmanager:GetSecretValue` (DB credentials) |
| `billing` | `secretsmanager:GetSecretValue` (DB credentials) |

Setiap ServiceAccount dianotasikan dengan IAM role:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: notification
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::<account>:role/parkir-notification
```

Pod yang pakai `serviceAccountName: notification` otomatis dapat AWS credentials via OIDC (no env vars). Lihat `deploy/helm/parkir-pintar/templates/serviceaccount.yaml` (akan ditambah).

### 5.4 Network policies & security groups

EKS default = full mesh allow di dalam cluster. Lock down via NetworkPolicy:

- `gateway` ← internet (via ALB)
- `gateway` → `reservation`, `billing`, `payment` (gRPC port)
- `*` → RDS:5432, Redis:6379, NATS:4222
- Block `gateway` ↔ `notification` (no direct path needed)

NetworkPolicy template sudah ada di Helm chart (`templates/networkpolicy.yaml`).

### 5.5 Secrets via External Secrets Operator

```yaml
# deploy/k8s/eks/external-secret-db.yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: parkir-db-credentials
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: ClusterSecretStore
  target:
    name: parkir-secrets
  data:
    - secretKey: db-url
      remoteRef:
        key: parkir-pintar/db-url
    - secretKey: midtrans-server-key
      remoteRef:
        key: parkir-pintar/midtrans-server-key
    - secretKey: jwt-secret
      remoteRef:
        key: parkir-pintar/jwt-secret
```

---

## 6. Deployment Workflow (high-level)

```
Developer push to main
   │
   ▼
GitHub Actions: ci.yml
   ├── Lint (golangci-lint, buf lint)
   ├── Test (race + integration + e2e)
   └── Build images → push ECR (path-filtered)
   │
   ▼
Manual trigger: deploy-eks.yml
   ├── Configure kubeconfig (AWS OIDC)
   ├── Helm upgrade --install parkir-pintar deploy/helm/parkir-pintar \
   │     -f values.yaml -f values.eks.yaml \
   │     --set global.imageTag=$SHA
   ├── kubectl rollout status deploy/reservation
   └── Smoke test gateway /healthz
```

Lihat `.github/workflows/deploy-eks.yml` untuk detail.

---

## 7. Roadmap

| Tier | Item | Effort | Trigger |
|------|------|--------|---------|
| 1 | Single-AZ demo cluster | Small | Hari ini (assessment) |
| 1 | Multi-AZ RDS + Redis | Small | Production ready |
| 2 | Service mesh (Linkerd) | Medium | Inter-service mTLS |
| 2 | GitOps via ArgoCD | Medium | Multi-env (staging+prod) |
| 3 | MSK migration | Large | Volume >10k msg/s |
| 3 | Multi-region active-active | Large | International expansion |
