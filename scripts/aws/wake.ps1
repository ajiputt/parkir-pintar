# ============================================================================
# ParkirPintar - AWS Wake Script (Skenario A daily provision)
# ============================================================================
# Jalanin di local laptop pagi-pagi untuk provision full infrastructure.
#
# Reference: docs/architecture/adr/0017-deployment-eks.md
#
# Prerequisite:
#   - aws cli authenticated (aws sts get-caller-identity)
#   - eksctl, kubectl, helm installed
#   - ECR repos pre-created (ajipur-parkir-pintar/*)
#   - Secrets Manager entries: rds-password, jwt-secret, midtrans-server-key
#
# Estimasi: ~20 menit total
#   - eksctl create cluster: 12-15 menit
#   - RDS Postgres: 5-8 menit (parallel)
#   - ElastiCache Redis: 4-6 menit (parallel)
#   - Helm install addons: 2-3 menit
# ============================================================================

# Default ErrorActionPreference = "Continue" supaya non-zero exit code dari
# aws/eksctl (kalau resource gak ada saat check existence) gak hentikan script.
# Per-step error handling pakai explicit $LASTEXITCODE check.
$ErrorActionPreference = "Continue"

# ---- Config ----
$AWS_REGION       = "ap-southeast-3"
$CLUSTER_NAME     = "ajipur-parkir-staging"
$DB_INSTANCE_ID   = "ajipur-parkir-rds"
$REDIS_CLUSTER_ID = "ajipur-parkir-redis"
$SECRET_PREFIX    = "ajipur-parkir-pintar"
$DEPLOY_ROLE_NAME = "AjipurParkirPintarGitHubActionsDeployRole"  # role yang dipakai workflow OIDC
$REPO_ROOT        = (Get-Item $PSScriptRoot).Parent.Parent.FullName

# ---- Helpers ----
function Write-Step {
    param([string]$Message)
    Write-Host "`n==> $Message" -ForegroundColor Cyan
}

function Write-Ok {
    param([string]$Message)
    Write-Host "[OK] $Message" -ForegroundColor Green
}

function Write-Warn {
    param([string]$Message)
    Write-Host "[WARN] $Message" -ForegroundColor Yellow
}

function Test-Tool {
    param([string]$Name)
    # Pakai Get-Command instead of run --version, supaya consistent untuk semua tool
    # (aws pakai --version, eksctl/kubectl/helm pakai version, beda flag).
    $cmd = Get-Command $Name -ErrorAction SilentlyContinue
    return $null -ne $cmd
}

# ============================================================================
# 1. Pre-flight checks
# ============================================================================
Write-Step "Pre-flight checks"

foreach ($tool in @("aws", "eksctl", "kubectl", "helm")) {
    if (-not (Test-Tool $tool)) {
        Write-Error "[$tool] tidak ke-install atau gak di PATH. Install dulu."
        exit 1
    }
}
Write-Ok "Semua tool ready (aws, eksctl, kubectl, helm)"

# Verify AWS CLI authenticated
$identity = aws sts get-caller-identity --output json | ConvertFrom-Json
if (-not $identity.Account) {
    Write-Error "AWS CLI gak authenticated. Run 'aws configure' dulu."
    exit 1
}
$ACCOUNT_ID = $identity.Account
Write-Ok "AWS Account: $ACCOUNT_ID, User: $($identity.Arn)"

# Verify region
$configured_region = aws configure get region
if ($configured_region -ne $AWS_REGION) {
    Write-Warn "Default region '$configured_region' beda dengan target '$AWS_REGION'. Override via flag."
}

# ============================================================================
# 2. Create EKS Cluster (12-15 menit)
# ============================================================================
Write-Step "Provision EKS cluster '$CLUSTER_NAME' (estimasi 12-15 menit)"

# Check existence dengan suppress stderr + cek $LASTEXITCODE
eksctl get cluster --name $CLUSTER_NAME --region $AWS_REGION 2>$null | Out-Null
$clusterExists = ($LASTEXITCODE -eq 0)

if ($clusterExists) {
    Write-Warn "Cluster '$CLUSTER_NAME' sudah ada. Skip create."
} else {
    Write-Host "Cluster belum ada. Creating..."
    eksctl create cluster -f "$PSScriptRoot\eksctl-cluster.yaml"
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[FAIL] eksctl create cluster failed (exit $LASTEXITCODE)" -ForegroundColor Red
        exit 1
    }
    Write-Ok "EKS cluster created"
}

# Update kubeconfig
aws eks update-kubeconfig --name $CLUSTER_NAME --region $AWS_REGION

# Verify nodegroup actually created + has nodes ready.
# eksctl create cluster kadang return 0 padahal nodegroup gagal launch
# (Spot capacity issue, ASG launch error). Detect early supaya gak fail di
# step Helm install yang lebih lambat.
Write-Step "Verify worker nodes available"
$nodeCount = 0
for ($i = 0; $i -lt 30; $i++) {
    $nodeCount = (kubectl get nodes --no-headers 2>$null | Measure-Object).Count
    if ($nodeCount -gt 0) {
        Write-Ok "$nodeCount node(s) ready"
        break
    }
    Write-Host "Waiting nodes to join cluster ($($i+1)/30)..."
    Start-Sleep -Seconds 10
}
if ($nodeCount -eq 0) {
    Write-Host "[FAIL] Cluster gak punya worker node setelah 5 menit." -ForegroundColor Red
    Write-Host "Diagnose dengan:" -ForegroundColor Yellow
    Write-Host "  aws eks list-nodegroups --cluster-name $CLUSTER_NAME --region $AWS_REGION"
    Write-Host "  aws autoscaling describe-auto-scaling-groups --region $AWS_REGION"
    Write-Host ""
    Write-Host "Kemungkinan Spot capacity issue. Manual fix - create On-Demand nodegroup:" -ForegroundColor Yellow
    Write-Host "  eksctl create nodegroup --cluster $CLUSTER_NAME --name workers-ondemand-manual ``"
    Write-Host "    --node-type t3.medium --nodes 2 --node-private-networking --region $AWS_REGION"
    exit 1
}
Write-Ok "kubeconfig updated"

# ----------------------------------------------------------------------------
# Grant cluster admin ke GitHub Actions deploy role via EKS Access Entry
# ----------------------------------------------------------------------------
# eksctl bikin cluster pakai IAM identity Aji (laptop) yang auto jadi cluster admin.
# Tapi GitHub Actions pakai role beda (OIDC) -> harus di-grant terpisah supaya
# CD pipeline bisa kubectl ke API server.
# Idempotent: kalau access entry sudah ada, aws CLI return error tapi continue.
Write-Step "Grant EKS access entry ke GitHubActionsDeployRole"

$deployRoleArn = "arn:aws:iam::${ACCOUNT_ID}:role/${DEPLOY_ROLE_NAME}"

aws eks create-access-entry `
    --cluster-name $CLUSTER_NAME `
    --principal-arn $deployRoleArn `
    --region $AWS_REGION 2>$null | Out-Null

aws eks associate-access-policy `
    --cluster-name $CLUSTER_NAME `
    --principal-arn $deployRoleArn `
    --policy-arn "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy" `
    --access-scope type=cluster `
    --region $AWS_REGION 2>$null | Out-Null

Write-Ok "Access entry granted ke $DEPLOY_ROLE_NAME (cluster admin)"

# ============================================================================
# 3. Get VPC + subnet IDs untuk RDS/Redis (di-create eksctl)
# ============================================================================
Write-Step "Get VPC + private subnet IDs"

$VPC_ID = aws eks describe-cluster --name $CLUSTER_NAME --region $AWS_REGION `
    --query "cluster.resourcesVpcConfig.vpcId" --output text

$PRIVATE_SUBNETS = aws ec2 describe-subnets --region $AWS_REGION `
    --filters "Name=vpc-id,Values=$VPC_ID" "Name=tag:kubernetes.io/role/internal-elb,Values=1" `
    --query "Subnets[].SubnetId" --output text
$PRIVATE_SUBNETS_LIST = $PRIVATE_SUBNETS.Split("`t")

# Get EKS node security group untuk allow ke RDS/Redis
$NODE_SG = aws eks describe-cluster --name $CLUSTER_NAME --region $AWS_REGION `
    --query "cluster.resourcesVpcConfig.clusterSecurityGroupId" --output text

Write-Ok "VPC: $VPC_ID, Node SG: $NODE_SG, Private subnets: $($PRIVATE_SUBNETS_LIST -join ', ')"

# ============================================================================
# 4. Create RDS Postgres (parallel dengan Redis, 5-8 menit)
# ============================================================================
Write-Step "Provision RDS Postgres '$DB_INSTANCE_ID'"

# Get DB password dari Secrets Manager
$DB_PASSWORD = aws secretsmanager get-secret-value `
    --secret-id "$SECRET_PREFIX/rds-password" `
    --region $AWS_REGION `
    --query SecretString --output text | ConvertFrom-Json | Select-Object -ExpandProperty password

if (-not $DB_PASSWORD) {
    Write-Error "Gagal ambil rds-password dari Secrets Manager '$SECRET_PREFIX/rds-password'"
    exit 1
}

# Create DB subnet group
aws rds create-db-subnet-group `
    --db-subnet-group-name "ajipur-parkir-rds-subnet" `
    --db-subnet-group-description "ParkirPintar RDS subnet group" `
    --subnet-ids $PRIVATE_SUBNETS_LIST `
    --region $AWS_REGION `
    --tags "Key=Project,Value=parkir-pintar" 2>$null

# Create RDS security group, allow from EKS node SG
$RDS_SG = aws ec2 create-security-group `
    --group-name "ajipur-parkir-rds-sg" `
    --description "Allow Postgres from EKS nodes" `
    --vpc-id $VPC_ID `
    --region $AWS_REGION `
    --query GroupId --output text 2>$null

if ($RDS_SG) {
    aws ec2 authorize-security-group-ingress `
        --group-id $RDS_SG `
        --protocol tcp --port 5432 `
        --source-group $NODE_SG `
        --region $AWS_REGION | Out-Null
    Write-Ok "RDS SG created: $RDS_SG"
} else {
    $RDS_SG = aws ec2 describe-security-groups --filters "Name=group-name,Values=ajipur-parkir-rds-sg" --region $AWS_REGION --query "SecurityGroups[0].GroupId" --output text
    Write-Warn "RDS SG sudah ada, pakai existing: $RDS_SG"
}

# Create RDS instance (async - return langsung, instance create di background)
aws rds describe-db-instances --db-instance-identifier $DB_INSTANCE_ID --region $AWS_REGION 2>$null | Out-Null
if ($LASTEXITCODE -eq 0) {
    Write-Warn "RDS '$DB_INSTANCE_ID' sudah ada. Skip create."
} else {
    aws rds create-db-instance `
        --db-instance-identifier $DB_INSTANCE_ID `
        --db-instance-class db.t3.micro `
        --engine postgres `
        --engine-version 16 `
        --master-username parkir `
        --master-user-password $DB_PASSWORD `
        --allocated-storage 20 `
        --storage-type gp3 `
        --db-name parkirpintar `
        --vpc-security-group-ids $RDS_SG `
        --db-subnet-group-name "ajipur-parkir-rds-subnet" `
        --backup-retention-period 0 `
        --no-publicly-accessible `
        --no-multi-az `
        --no-storage-encrypted `
        --region $AWS_REGION `
        --tags "Key=Project,Value=parkir-pintar" | Out-Null
    Write-Ok "RDS create requested (akan ready dalam 5-8 menit)"
}

# ============================================================================
# 5. Create ElastiCache Redis
# ============================================================================
Write-Step "Provision ElastiCache Redis '$REDIS_CLUSTER_ID'"

# Cache subnet group
aws elasticache create-cache-subnet-group `
    --cache-subnet-group-name "ajipur-parkir-redis-subnet" `
    --cache-subnet-group-description "ParkirPintar Redis subnet group" `
    --subnet-ids $PRIVATE_SUBNETS_LIST `
    --region $AWS_REGION 2>$null | Out-Null

# Redis security group
$REDIS_SG = aws ec2 create-security-group `
    --group-name "ajipur-parkir-redis-sg" `
    --description "Allow Redis from EKS nodes" `
    --vpc-id $VPC_ID `
    --region $AWS_REGION `
    --query GroupId --output text 2>$null

if ($REDIS_SG) {
    aws ec2 authorize-security-group-ingress `
        --group-id $REDIS_SG `
        --protocol tcp --port 6379 `
        --source-group $NODE_SG `
        --region $AWS_REGION | Out-Null
} else {
    $REDIS_SG = aws ec2 describe-security-groups --filters "Name=group-name,Values=ajipur-parkir-redis-sg" --region $AWS_REGION --query "SecurityGroups[0].GroupId" --output text
}

aws elasticache describe-cache-clusters --cache-cluster-id $REDIS_CLUSTER_ID --region $AWS_REGION 2>$null | Out-Null
if ($LASTEXITCODE -eq 0) {
    Write-Warn "Redis '$REDIS_CLUSTER_ID' sudah ada. Skip create."
} else {
    # Note: cache.t4g.* (Graviton) gak available di ap-southeast-3 Jakarta.
    # Pakai cache.t3.micro (Intel-based, compatible cross-region).
    aws elasticache create-cache-cluster `
        --cache-cluster-id $REDIS_CLUSTER_ID `
        --engine redis `
        --cache-node-type cache.t3.micro `
        --num-cache-nodes 1 `
        --cache-subnet-group-name "ajipur-parkir-redis-subnet" `
        --security-group-ids $REDIS_SG `
        --region $AWS_REGION `
        --tags "Key=Project,Value=parkir-pintar" | Out-Null
    Write-Ok "Redis create requested (akan ready dalam 4-6 menit)"
}

# ============================================================================
# 6. Wait RDS + Redis ready
# ============================================================================
Write-Step "Wait RDS + Redis ready (paralel, ~5-8 menit)"

Write-Host "Waiting RDS..."
aws rds wait db-instance-available --db-instance-identifier $DB_INSTANCE_ID --region $AWS_REGION
Write-Ok "RDS ready"

Write-Host "Waiting Redis..."
aws elasticache wait cache-cluster-available --cache-cluster-id $REDIS_CLUSTER_ID --region $AWS_REGION
Write-Ok "Redis ready"

# Get endpoints
$DB_HOST = aws rds describe-db-instances --db-instance-identifier $DB_INSTANCE_ID --region $AWS_REGION `
    --query "DBInstances[0].Endpoint.Address" --output text
$REDIS_HOST = aws elasticache describe-cache-clusters --cache-cluster-id $REDIS_CLUSTER_ID `
    --show-cache-node-info --region $AWS_REGION `
    --query "CacheClusters[0].CacheNodes[0].Endpoint.Address" --output text

Write-Ok "DB endpoint: $DB_HOST"
Write-Ok "Redis endpoint: $REDIS_HOST"

# ============================================================================
# 7. Update Secrets Manager db-url
# ============================================================================
Write-Step "Update Secrets Manager db-url"

$DB_URL = "postgres://parkir:${DB_PASSWORD}@${DB_HOST}:5432/parkirpintar?sslmode=require"

aws secretsmanager update-secret `
    --secret-id "$SECRET_PREFIX/db-url" `
    --secret-string $DB_URL `
    --region $AWS_REGION | Out-Null
Write-Ok "db-url updated di Secrets Manager"

# ============================================================================
# 8. Install Helm: AWS Load Balancer Controller
# ============================================================================
Write-Step "Install AWS Load Balancer Controller"

helm repo add eks https://aws.github.io/eks-charts 2>$null
helm repo update 2>$null

helm upgrade --install aws-load-balancer-controller eks/aws-load-balancer-controller `
    --namespace kube-system `
    --set clusterName=$CLUSTER_NAME `
    --set serviceAccount.create=false `
    --set serviceAccount.name=aws-load-balancer-controller `
    --set region=$AWS_REGION `
    --set vpcId=$VPC_ID `
    --wait
Write-Ok "ALB Controller installed"

# ============================================================================
# 8.5. Set default StorageClass untuk PVC (NATS JetStream butuh)
# ============================================================================
# EBS CSI Driver addon bikin gp2 StorageClass otomatis tapi gak set sebagai
# default. PVC tanpa explicit storageClassName akan Pending forever.
# Set gp2 sebagai default supaya NATS PVC auto-bind.
Write-Step "Set default StorageClass"

kubectl annotate sc gp2 storageclass.kubernetes.io/is-default-class=true --overwrite 2>$null
if ($LASTEXITCODE -eq 0) {
    Write-Ok "gp2 set sebagai default StorageClass"
} else {
    Write-Warn "gp2 StorageClass gak ada. Cek 'kubectl get sc' manual."
}

# ============================================================================
# 9. Install Helm: External Secrets Operator
# ============================================================================
Write-Step "Install External Secrets Operator"

helm repo add external-secrets https://charts.external-secrets.io 2>$null
helm repo update 2>$null

helm upgrade --install external-secrets external-secrets/external-secrets `
    --namespace external-secrets --create-namespace `
    --set serviceAccount.create=false `
    --set serviceAccount.name=external-secrets-sa `
    --wait --timeout 5m

# Wait sampai semua ESO deployment (controller + cert-controller + webhook) Available.
# helm --wait kadang return sebelum webhook bener-bener ready, padahal CD pipeline
# butuh webhook reachable untuk apply ExternalSecret tanpa validation error.
kubectl wait deployment -n external-secrets --all --for=condition=Available --timeout=180s
Write-Ok "ESO installed + webhook ready"

# ============================================================================
# 10. Install Helm: NATS JetStream
# ============================================================================
Write-Step "Install NATS JetStream"

helm repo add nats https://nats-io.github.io/k8s/helm/charts/ 2>$null
helm repo update 2>$null

kubectl create namespace parkir-system 2>$null

# JetStream MemoryStore (no PVC) supaya sleep/resume gak nemu issue
# EBS volume cross-AZ. Trade-off: state hilang saat pod restart.
# Aman untuk demo karena event ephemeral + idempotency handle replay.
# Production: ganti ke fileStore + topology-pinned StorageClass.
helm upgrade --install nats nats/nats `
    --namespace parkir-system `
    --set config.jetstream.enabled=true `
    --set config.jetstream.fileStore.enabled=false `
    --set config.jetstream.memoryStore.enabled=true `
    --set config.jetstream.memoryStore.maxSize=128Mi `
    --wait
Write-Ok "NATS installed (JetStream MemoryStore - no PVC, sleep/resume safe)"

# ============================================================================
# 10.5. Install Observability stack — kube-prometheus-stack + Loki + Tempo
# ============================================================================
# Demo observability: Prometheus (metrics) + Grafana (dashboard) + Loki (logs) + Tempo (traces).
# Disable storage persistence (emptyDir) supaya sleep/resume gak nemu PVC issue
# kayak NATS. Trade-off: data hilang saat pod restart - OK untuk demo.
Write-Step "Install Observability stack (Prometheus + Grafana + Loki + Tempo)"

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>$null
helm repo add grafana https://grafana.github.io/helm-charts 2>$null
helm repo update 2>$null

kubectl create namespace observability 2>$null

# kube-prometheus-stack (Prometheus + Grafana + Alertmanager)
#
# Flag *NilUsesHelmValues=false x 4 — disable default scoping yang restrict
# Prometheus operator ke namespace observability only. Tanpa ini, ServiceMonitor
# di namespace parkir (atau lainnya) gak akan ke-discover.
#   - serviceMonitorSelectorNilUsesHelmValues : disable label `release: prometheus` filter
#   - serviceMonitorNamespaceSelectorNilUsesHelmValues : watch all namespaces (bukan cuma observability)
#   - podMonitorSelector / podMonitorNamespaceSelector : sama, untuk PodMonitor CRD
# Auto-register Loki + Tempo sebagai Grafana datasource via additionalDataSources.
# Cluster DNS: <service-name>.<namespace>.svc.cluster.local — bisa di-shorten ke
# just <service-name> kalau Grafana same namespace (observability).
helm upgrade --install prometheus prometheus-community/kube-prometheus-stack `
    --namespace observability `
    --set grafana.adminPassword="parkirpintar-demo" `
    --set "grafana.additionalDataSources[0].name=Loki" `
    --set "grafana.additionalDataSources[0].type=loki" `
    --set "grafana.additionalDataSources[0].url=http://loki:3100" `
    --set "grafana.additionalDataSources[0].access=proxy" `
    --set "grafana.additionalDataSources[1].name=Tempo" `
    --set "grafana.additionalDataSources[1].type=tempo" `
    --set "grafana.additionalDataSources[1].url=http://tempo:3200" `
    --set "grafana.additionalDataSources[1].access=proxy" `
    --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false `
    --set prometheus.prometheusSpec.serviceMonitorNamespaceSelectorNilUsesHelmValues=false `
    --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false `
    --set prometheus.prometheusSpec.podMonitorNamespaceSelectorNilUsesHelmValues=false `
    --set prometheus.prometheusSpec.storageSpec=null `
    --set alertmanager.enabled=false `
    --set prometheus.prometheusSpec.resources.requests.memory=400Mi `
    --set prometheus.prometheusSpec.retention=2h `
    --wait --timeout 5m

Write-Ok "Prometheus + Grafana installed"

# Loki + Promtail via loki-stack chart (Loki 2.x bundled).
# Note: loki-stack chart is deprecated but Loki 3.x current chart bermasalah
# dengan Grafana datasource health check (parse error). Loki 2.x lebih stabil
# untuk demo. Migrate ke Loki 3.x kalau Grafana plugin compatibility fixed.
helm upgrade --install loki grafana/loki-stack `
    --namespace observability `
    --set "loki.persistence.enabled=false" `
    --set "loki.config.auth_enabled=false" `
    --set "loki.service.type=ClusterIP" `
    --set "grafana.enabled=false" `
    --set "prometheus.enabled=false" `
    --set "promtail.enabled=true" `
    --wait --timeout 5m

if ($LASTEXITCODE -ne 0) {
    Write-Host "WARNING: Loki install gagal (exit $LASTEXITCODE) — log akan kosong di Grafana" -ForegroundColor Yellow
}

# Promtail bundled di loki-stack — no separate install needed.
Write-Ok "Loki + Promtail (bundled) installed"

# Tempo (traces backend, terima OTLP gRPC dari pkg/tracing services)
helm upgrade --install tempo grafana/tempo `
    --namespace observability `
    --set "tempo.storage.trace.backend=local" `
    --set "persistence.enabled=false" `
    --wait 2>$null

Write-Ok "Tempo installed"

# ============================================================================
# 11. Wait ESO CRDs ready, lalu Apply External Secret manifest
# ============================================================================
Write-Step "Wait ESO CRDs registered + apply ExternalSecret"

kubectl create namespace parkir 2>$null

# Wait CRDs registered di K8s API server (ESO baru saja install, butuh ~10-20 detik)
Write-Host "Waiting ExternalSecret CRDs ready (max 60 detik)..."
$crdReady = $false
for ($i = 0; $i -lt 30; $i++) {
    kubectl get crd clustersecretstores.external-secrets.io 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) {
        $crdReady = $true
        Write-Ok "CRDs registered"
        break
    }
    Start-Sleep -Seconds 2
}

if (-not $crdReady) {
    Write-Warn "ESO CRDs not ready after 60s. Skip ExternalSecret apply. Run manually nanti:"
    Write-Warn "  kubectl apply -f deploy/k8s/eks/external-secret.yaml"
} else {
    kubectl apply -f "$REPO_ROOT\deploy\k8s\eks\external-secret.yaml"

    # Wait Secret ter-populate (ESO sync)
    Write-Host "Waiting ESO sync ke K8s Secret (max 60 detik)..."
    for ($i = 0; $i -lt 30; $i++) {
        kubectl get secret parkir-secrets -n parkir 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) {
            Write-Ok "K8s Secret 'parkir-secrets' populated"
            break
        }
        Start-Sleep -Seconds 2
    }
}

# ============================================================================
# 12. DB migration - handled by Helm pre-install/pre-upgrade Job
# ============================================================================
Write-Step "DB migration"

Write-Ok "Migration auto-handled via Helm pre-install/pre-upgrade hook."
Write-Host "  - ConfigMap (templates/migration-configmap.yaml) bundle SQL files"
Write-Host "  - Job (templates/migration-job.yaml) jalan SEBELUM app pods"
Write-Host "  - migrate CLI loop ke 4 schema (reservation, billing, payment, notification)"
Write-Host "  - Kalau Job fail, Helm rollback otomatis (atomic install/upgrade)"
Write-Host ""
Write-Host "  Trigger via: GitHub Actions 'Deploy to AWS EKS' workflow." -ForegroundColor Yellow

# ============================================================================
# Summary
# ============================================================================
Write-Step "WAKE COMPLETE"
Write-Host ""
Write-Host "============================================================" -ForegroundColor Green
Write-Host "  AWS Infrastructure Ready" -ForegroundColor Green
Write-Host "============================================================" -ForegroundColor Green
Write-Host "  EKS Cluster:    $CLUSTER_NAME (region $AWS_REGION)"
Write-Host "  VPC:            $VPC_ID"
Write-Host "  RDS Postgres:   $DB_HOST"
Write-Host "  Redis:          $REDIS_HOST"
Write-Host "  NATS:           nats.parkir-system.svc.cluster.local:4222"
Write-Host ""
Write-Host "  Next steps:" -ForegroundColor Yellow
Write-Host "  1. Sync migrations ke chart dir (kalau ada perubahan):"
Write-Host "     make helm-sync-migrations   (atau: cp -r deploy/migrations/* deploy/helm/parkir-pintar/migrations/)"
Write-Host "  2. Trigger 'Deploy to AWS EKS' workflow di GitHub Actions"
Write-Host "     (Helm pre-install Job akan auto-migrate 4 schema)"
Write-Host ""
Write-Host "  Don't forget:" -ForegroundColor Red
Write-Host "  -> Sore: jalanin .\scripts\aws\teardown.ps1 untuk cleanup"
Write-Host "============================================================" -ForegroundColor Green
Write-Host ""
