# ============================================================================
# ParkirPintar — AWS Wake Script (Skenario A daily provision)
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

$ErrorActionPreference = "Stop"

# ---- Config ----
$AWS_REGION       = "ap-southeast-3"
$CLUSTER_NAME     = "parkir-demo"
$DB_INSTANCE_ID   = "parkir-rds"
$REDIS_CLUSTER_ID = "parkir-redis"
$SECRET_PREFIX    = "ajipur-parkir-pintar"
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
    param([string]$Name, [string]$VersionFlag = "version")
    try {
        $null = & $Name $VersionFlag 2>$null
        return $true
    } catch {
        return $false
    }
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

$existing = eksctl get cluster --name $CLUSTER_NAME --region $AWS_REGION 2>$null
if ($existing) {
    Write-Warn "Cluster '$CLUSTER_NAME' sudah ada. Skip create."
} else {
    eksctl create cluster -f "$PSScriptRoot\eksctl-cluster.yaml"
    if ($LASTEXITCODE -ne 0) {
        Write-Error "eksctl create cluster failed"
        exit 1
    }
    Write-Ok "EKS cluster created"
}

# Update kubeconfig
aws eks update-kubeconfig --name $CLUSTER_NAME --region $AWS_REGION
Write-Ok "kubeconfig updated"

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
    --db-subnet-group-name "parkir-rds-subnet" `
    --db-subnet-group-description "ParkirPintar RDS subnet group" `
    --subnet-ids $PRIVATE_SUBNETS_LIST `
    --region $AWS_REGION `
    --tags "Key=Project,Value=parkir-pintar" 2>$null

# Create RDS security group, allow from EKS node SG
$RDS_SG = aws ec2 create-security-group `
    --group-name "parkir-rds-sg" `
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
    $RDS_SG = aws ec2 describe-security-groups --filters "Name=group-name,Values=parkir-rds-sg" --region $AWS_REGION --query "SecurityGroups[0].GroupId" --output text
    Write-Warn "RDS SG sudah ada, pakai existing: $RDS_SG"
}

# Create RDS instance (async — return langsung, instance create di background)
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
        --db-subnet-group-name "parkir-rds-subnet" `
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
    --cache-subnet-group-name "parkir-redis-subnet" `
    --cache-subnet-group-description "ParkirPintar Redis subnet group" `
    --subnet-ids $PRIVATE_SUBNETS_LIST `
    --region $AWS_REGION 2>$null | Out-Null

# Redis security group
$REDIS_SG = aws ec2 create-security-group `
    --group-name "parkir-redis-sg" `
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
    $REDIS_SG = aws ec2 describe-security-groups --filters "Name=group-name,Values=parkir-redis-sg" --region $AWS_REGION --query "SecurityGroups[0].GroupId" --output text
}

aws elasticache describe-cache-clusters --cache-cluster-id $REDIS_CLUSTER_ID --region $AWS_REGION 2>$null | Out-Null
if ($LASTEXITCODE -eq 0) {
    Write-Warn "Redis '$REDIS_CLUSTER_ID' sudah ada. Skip create."
} else {
    aws elasticache create-cache-cluster `
        --cache-cluster-id $REDIS_CLUSTER_ID `
        --engine redis `
        --cache-node-type cache.t4g.micro `
        --num-cache-nodes 1 `
        --cache-subnet-group-name "parkir-redis-subnet" `
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
# 9. Install Helm: External Secrets Operator
# ============================================================================
Write-Step "Install External Secrets Operator"

helm repo add external-secrets https://charts.external-secrets.io 2>$null
helm repo update 2>$null

helm upgrade --install external-secrets external-secrets/external-secrets `
    --namespace external-secrets --create-namespace `
    --set serviceAccount.create=false `
    --set serviceAccount.name=external-secrets-sa `
    --wait
Write-Ok "ESO installed"

# ============================================================================
# 10. Install Helm: NATS JetStream
# ============================================================================
Write-Step "Install NATS JetStream"

helm repo add nats https://nats-io.github.io/k8s/helm/charts/ 2>$null
helm repo update 2>$null

kubectl create namespace parkir-system 2>$null

helm upgrade --install nats nats/nats `
    --namespace parkir-system `
    --set config.jetstream.enabled=true `
    --set config.jetstream.fileStore.pvc.size=1Gi `
    --wait
Write-Ok "NATS installed"

# ============================================================================
# 11. Apply External Secret manifest + parkir namespace
# ============================================================================
Write-Step "Apply ExternalSecret manifest"

kubectl create namespace parkir 2>$null
kubectl apply -f "$REPO_ROOT\deploy\k8s\eks\external-secret.yaml"

# Wait Secret ter-populate (ESO sync)
Write-Host "Waiting ESO sync (max 60 detik)..."
for ($i = 0; $i -lt 30; $i++) {
    $secret = kubectl get secret parkir-secrets -n parkir 2>$null
    if ($secret) {
        Write-Ok "K8s Secret 'parkir-secrets' populated"
        break
    }
    Start-Sleep -Seconds 2
}

# ============================================================================
# 12. Run DB migration (via temp pod)
# ============================================================================
Write-Step "Apply DB migration"

# Run migrate dengan ephemeral pod
kubectl run migrate --rm -i --restart=Never `
    --image=migrate/migrate:v4.17.1 `
    --namespace parkir `
    --env="DB_URL=$DB_URL" `
    --command -- /bin/sh -c "
        echo '==> migrate reservation' && migrate -path /tmp/migrations/reservation -database `$DB_URL up &&
        echo '==> migrate billing' && migrate -path /tmp/migrations/billing -database `$DB_URL up &&
        echo '==> migrate payment' && migrate -path /tmp/migrations/payment -database `$DB_URL up &&
        echo '==> migrate notification' && migrate -path /tmp/migrations/notification -database `$DB_URL up
    "
# Note: kubectl exec migration approach disederhanakan — actual run via init container atau CI job step lebih baik.
# Untuk demo, manual psql apply juga work.

# ============================================================================
# Summary
# ============================================================================
Write-Step "WAKE COMPLETE"
Write-Host @"

╔════════════════════════════════════════════════════════════╗
║  AWS Infrastructure Ready                                  ║
╠════════════════════════════════════════════════════════════╣
║  EKS Cluster:    $CLUSTER_NAME (region $AWS_REGION)
║  VPC:            $VPC_ID
║  RDS Postgres:   $DB_HOST
║  Redis:          $REDIS_HOST
║  NATS:           nats.parkir-system.svc.cluster.local:4222
║                                                            ║
║  Next step:                                                ║
║  → Trigger 'Deploy to AWS EKS' workflow di GitHub Actions  ║
║                                                            ║
║  Don't forget:                                             ║
║  → Sore: jalanin .\scripts\aws\teardown.ps1 untuk cleanup  ║
╚════════════════════════════════════════════════════════════╝

"@ -ForegroundColor Green
