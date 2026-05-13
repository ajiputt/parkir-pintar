# ============================================================================
# ParkirPintar - AWS Sleep Script (light pause, ~5 min resume)
# ============================================================================
# Jalanin di local laptop untuk pause AWS resources TANPA delete.
# Cocok untuk break siang / weekend break (1-2 hari).
#
# Yang DI-PAUSE:
#   - EKS managed nodegroup: scale desiredCapacity ke 0 (no EC2 cost)
#   - RDS Postgres: stop instance (no compute cost, storage masih billed)
#
# Yang TETAP JALAN (small cost ~$2/hari):
#   - EKS control plane: $0.10/jam = ~$2.40/hari
#   - VPC + NAT Gateway: ~$0.045/jam = ~$1/hari
#   - ElastiCache: gak bisa di-stop (~$0.40/hari cache.t3.micro)
#   - Storage RDS: ~$0.10/GB/bulan
#
# Untuk full $0 cost, pakai teardown.ps1 (delete all, butuh 20 menit recreate).
#
# Estimasi: 2-3 menit
# ============================================================================

$ErrorActionPreference = "Continue"

# ---- Config ----
$AWS_REGION     = "ap-southeast-3"
$CLUSTER_NAME   = "ajipur-parkir-staging"
$DB_INSTANCE_ID = "ajipur-parkir-rds"

function Write-Step { param([string]$Message); Write-Host "`n==> $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message); Write-Host "[OK] $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message); Write-Host "[WARN] $Message" -ForegroundColor Yellow }

# ----------------------------------------------------------------------------
# Konfirmasi
# ----------------------------------------------------------------------------
Write-Host ""
Write-Host "============================================================" -ForegroundColor Yellow
Write-Host "  SLEEP ParkirPintar Infrastructure" -ForegroundColor Yellow
Write-Host "============================================================" -ForegroundColor Yellow
Write-Host "  Akan PAUSE:"
Write-Host "   - EKS nodegroup: scale to 0 (no EC2 cost)"
Write-Host "   - RDS '$DB_INSTANCE_ID': stop instance"
Write-Host ""
Write-Host "  TETAP JALAN (~`$2/hari):"
Write-Host "   - EKS control plane, VPC + NAT, ElastiCache, storage"
Write-Host ""
Write-Host "  Resume: pakai resume.ps1 atau wake.ps1 (~5 menit)"
Write-Host "============================================================" -ForegroundColor Yellow
Write-Host ""

$confirm = Read-Host "Lanjut sleep? Ketik 'yes' untuk confirm"
if ($confirm -ne "yes") {
    Write-Host "Cancelled." -ForegroundColor Red
    exit 0
}

# ----------------------------------------------------------------------------
# 1. Scale EKS nodegroup ke 0
# ----------------------------------------------------------------------------
Write-Step "Scale EKS nodegroups ke 0"

$nodegroups = aws eks list-nodegroups --cluster-name $CLUSTER_NAME --region $AWS_REGION --query "nodegroups" --output text 2>$null
if (-not $nodegroups) {
    Write-Warn "Gak ada nodegroup di cluster '$CLUSTER_NAME'. Skip."
} else {
    foreach ($ng in $nodegroups.Split("`t")) {
        Write-Host "  -> $ng"
        aws eks update-nodegroup-config `
            --cluster-name $CLUSTER_NAME `
            --nodegroup-name $ng `
            --scaling-config "desiredSize=0,minSize=0,maxSize=4" `
            --region $AWS_REGION 2>$null | Out-Null
    }
    Write-Ok "Nodegroup scaling requested (instances akan terminate ~2 menit)"
}

# ----------------------------------------------------------------------------
# 2. Stop RDS
# ----------------------------------------------------------------------------
Write-Step "Stop RDS '$DB_INSTANCE_ID'"

# RDS stop max 7 hari, lalu auto-start. Untuk demo bisa di-toleransi.
aws rds stop-db-instance `
    --db-instance-identifier $DB_INSTANCE_ID `
    --region $AWS_REGION 2>$null | Out-Null

if ($LASTEXITCODE -eq 0) {
    Write-Ok "RDS stop requested (status -> stopping, lalu stopped dalam ~3 menit)"
} else {
    Write-Warn "RDS gak ada / status gak valid untuk stop (mungkin udah stopped)"
}

# ----------------------------------------------------------------------------
# Summary
# ----------------------------------------------------------------------------
Write-Host ""
Write-Host "============================================================" -ForegroundColor Green
Write-Host "  SLEEP REQUESTED" -ForegroundColor Green
Write-Host "============================================================" -ForegroundColor Green
Write-Host "  - EKS nodegroup: scaling to 0 (~2 menit untuk EC2 terminate)"
Write-Host "  - RDS: stopping (~3 menit)"
Write-Host "  - Control plane + VPC + ElastiCache: TETAP JALAN (~`$2/hari)"
Write-Host ""
Write-Host "  Resume:" -ForegroundColor Yellow
Write-Host "    .\scripts\aws\resume.ps1   (atau wake.ps1 - keduanya idempotent)"
Write-Host ""
Write-Host "  Verify (tunggu ~3 menit):" -ForegroundColor Cyan
Write-Host "    aws rds describe-db-instances --db-instance-identifier $DB_INSTANCE_ID --region $AWS_REGION --query 'DBInstances[0].DBInstanceStatus'"
Write-Host "    -> 'stopped' = sukses"
Write-Host "============================================================" -ForegroundColor Green
