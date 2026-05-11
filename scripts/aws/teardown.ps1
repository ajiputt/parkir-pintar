# ============================================================================
# ParkirPintar — AWS Teardown Script (Skenario A end-of-day cleanup)
# ============================================================================
# Jalanin di local laptop sore untuk delete semua resource.
# Aji wajib jalanin ini supaya cost stop. Lupa = $5+/hari kelarung idle.
#
# Yang DI-DELETE (recreated next morning):
#   - EKS cluster + node group
#   - RDS Postgres instance (data hilang, fresh tomorrow)
#   - ElastiCache Redis
#   - VPC + NAT Gateway + subnet + SG (all auto-managed by eksctl)
#
# Yang PERSIST (gak di-delete, jaga untuk setup awal):
#   - IAM OIDC provider, GitHubActionsDeployRole
#   - ECR repositories
#   - Secrets Manager entries
#
# Estimasi: 8-12 menit total
# ============================================================================

$ErrorActionPreference = "Continue"  # lanjut walau ada step yang gagal (best-effort)

# ---- Config (must match wake.ps1) ----
$AWS_REGION       = "ap-southeast-3"
$CLUSTER_NAME     = "parkir-demo"
$DB_INSTANCE_ID   = "parkir-rds"
$REDIS_CLUSTER_ID = "parkir-redis"

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

# ============================================================================
# Konfirmasi (safety gate)
# ============================================================================
Write-Host @"

╔════════════════════════════════════════════════════════════╗
║  TEARDOWN ParkirPintar Infrastructure                      ║
╠════════════════════════════════════════════════════════════╣
║  Akan DELETE:                                              ║
║   - EKS cluster '$CLUSTER_NAME'
║   - RDS Postgres '$DB_INSTANCE_ID' (data hilang)
║   - ElastiCache Redis '$REDIS_CLUSTER_ID'
║   - VPC + subnet + NAT (auto via eksctl)                   ║
║                                                            ║
║  TIDAK di-delete (persist):                                ║
║   - IAM Role, ECR repos, Secrets Manager                   ║
╚════════════════════════════════════════════════════════════╝

"@ -ForegroundColor Yellow

$confirm = Read-Host "Lanjut delete? Ketik 'yes' untuk confirm"
if ($confirm -ne "yes") {
    Write-Host "Cancelled." -ForegroundColor Red
    exit 0
}

# ============================================================================
# 1. Helm uninstall (urutan: app → ESO → NATS → ALB Controller)
# ============================================================================
Write-Step "Uninstall Helm releases"

# Update kubeconfig (mungkin udah expired)
aws eks update-kubeconfig --name $CLUSTER_NAME --region $AWS_REGION 2>$null

helm uninstall parkir-pintar -n parkir 2>$null
helm uninstall nats -n parkir-system 2>$null
helm uninstall external-secrets -n external-secrets 2>$null
helm uninstall aws-load-balancer-controller -n kube-system 2>$null

# Delete namespace
kubectl delete namespace parkir 2>$null
kubectl delete namespace parkir-system 2>$null
kubectl delete namespace external-secrets 2>$null

Write-Ok "Helm uninstall done"

# ============================================================================
# 2. Delete RDS (skip final snapshot — throwaway demo)
# ============================================================================
Write-Step "Delete RDS '$DB_INSTANCE_ID'"

aws rds delete-db-instance `
    --db-instance-identifier $DB_INSTANCE_ID `
    --skip-final-snapshot `
    --delete-automated-backups `
    --region $AWS_REGION 2>$null | Out-Null

if ($LASTEXITCODE -eq 0) {
    Write-Ok "RDS delete requested (async, ~5 menit)"
} else {
    Write-Warn "RDS gak ada atau gagal delete"
}

# ============================================================================
# 3. Delete ElastiCache Redis
# ============================================================================
Write-Step "Delete Redis '$REDIS_CLUSTER_ID'"

aws elasticache delete-cache-cluster `
    --cache-cluster-id $REDIS_CLUSTER_ID `
    --region $AWS_REGION 2>$null | Out-Null

if ($LASTEXITCODE -eq 0) {
    Write-Ok "Redis delete requested (async, ~3 menit)"
} else {
    Write-Warn "Redis gak ada atau gagal delete"
}

# ============================================================================
# 4. Wait RDS + Redis fully deleted (sebelum delete SG)
# ============================================================================
Write-Step "Wait RDS + Redis deleted"

Write-Host "Waiting RDS..."
aws rds wait db-instance-deleted --db-instance-identifier $DB_INSTANCE_ID --region $AWS_REGION 2>$null
Write-Ok "RDS deleted"

Write-Host "Waiting Redis..."
aws elasticache wait cache-cluster-deleted --cache-cluster-id $REDIS_CLUSTER_ID --region $AWS_REGION 2>$null
Write-Ok "Redis deleted"

# ============================================================================
# 5. Delete custom SG + subnet groups
# ============================================================================
Write-Step "Delete RDS/Redis subnet groups + SG"

aws rds delete-db-subnet-group --db-subnet-group-name "parkir-rds-subnet" --region $AWS_REGION 2>$null
aws elasticache delete-cache-subnet-group --cache-subnet-group-name "parkir-redis-subnet" --region $AWS_REGION 2>$null

# Delete custom SG (yang dibuat di wake.ps1)
$rds_sg = aws ec2 describe-security-groups --filters "Name=group-name,Values=parkir-rds-sg" --region $AWS_REGION --query "SecurityGroups[0].GroupId" --output text 2>$null
if ($rds_sg -and $rds_sg -ne "None") {
    aws ec2 delete-security-group --group-id $rds_sg --region $AWS_REGION 2>$null
    Write-Ok "RDS SG deleted: $rds_sg"
}

$redis_sg = aws ec2 describe-security-groups --filters "Name=group-name,Values=parkir-redis-sg" --region $AWS_REGION --query "SecurityGroups[0].GroupId" --output text 2>$null
if ($redis_sg -and $redis_sg -ne "None") {
    aws ec2 delete-security-group --group-id $redis_sg --region $AWS_REGION 2>$null
    Write-Ok "Redis SG deleted: $redis_sg"
}

# ============================================================================
# 6. Delete EKS cluster (terakhir — supaya VPC bisa di-delete eksctl)
# ============================================================================
Write-Step "Delete EKS cluster '$CLUSTER_NAME' (estimasi 8-10 menit)"

eksctl delete cluster --name $CLUSTER_NAME --region $AWS_REGION --wait
if ($LASTEXITCODE -eq 0) {
    Write-Ok "EKS cluster deleted (VPC + subnet + NAT + SG auto-cleaned by eksctl)"
} else {
    Write-Warn "eksctl delete partial — cek manual di Console kalau ada orphan resource"
}

# ============================================================================
# 7. Reset Secrets Manager db-url ke placeholder
# ============================================================================
Write-Step "Reset db-url Secrets Manager ke placeholder"

$PLACEHOLDER = "postgres://parkir:CHANGE_AT_WAKE@TBD-HOST:5432/parkirpintar?sslmode=require"
aws secretsmanager update-secret `
    --secret-id "ajipur-parkir-pintar/db-url" `
    --secret-string $PLACEHOLDER `
    --region $AWS_REGION 2>$null | Out-Null
Write-Ok "db-url reset (akan auto-update next wake)"

# ============================================================================
# Summary
# ============================================================================
Write-Step "TEARDOWN COMPLETE"
Write-Host @"

╔════════════════════════════════════════════════════════════╗
║  AWS Resources Deleted                                     ║
╠════════════════════════════════════════════════════════════╣
║  - EKS cluster + node group: DELETED                       ║
║  - RDS Postgres:               DELETED                     ║
║  - ElastiCache Redis:          DELETED                     ║
║  - VPC + subnet + NAT:         DELETED                     ║
║                                                            ║
║  Persist (untuk wake besok):                               ║
║  - IAM Role, ECR repos, Secrets Manager                    ║
║                                                            ║
║  Cost stop. Bisa wake lagi besok pagi via:                 ║
║   .\scripts\aws\wake.ps1                                   ║
╚════════════════════════════════════════════════════════════╝

VERIFY MANUAL (recommended):
  aws ec2 describe-instances --region $AWS_REGION --query 'Reservations[].Instances[?State.Name==``running``].InstanceId'
  → Output kosong = no instance running = 100% clean

"@ -ForegroundColor Green
