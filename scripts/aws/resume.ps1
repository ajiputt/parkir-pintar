# ============================================================================
# ParkirPintar - AWS Resume Script (revert sleep.ps1)
# ============================================================================
# Jalanin SETELAH sleep.ps1 untuk balikin resource ke state running.
#
# Yang DI-RESUME:
#   - EKS nodegroup: scale balik ke desiredSize asli (default 1 baseline + 1 spot)
#   - RDS Postgres: start instance
#
# Estimasi: 4-6 menit
#   - RDS start: 2-3 menit
#   - EKS node provision: 3-5 menit
#   - K8s pods restart (sudah ada deployment, cuma butuh node)
# ============================================================================

$ErrorActionPreference = "Continue"

# ---- Config (must match wake.ps1) ----
$AWS_REGION     = "ap-southeast-3"
$CLUSTER_NAME   = "ajipur-parkir-staging"
$DB_INSTANCE_ID = "ajipur-parkir-rds"

function Write-Step { param([string]$Message); Write-Host "`n==> $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message); Write-Host "[OK] $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message); Write-Host "[WARN] $Message" -ForegroundColor Yellow }

Write-Host ""
Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "  RESUME ParkirPintar Infrastructure" -ForegroundColor Cyan
Write-Host "============================================================" -ForegroundColor Cyan

# ----------------------------------------------------------------------------
# 1. Start RDS (async)
# ----------------------------------------------------------------------------
Write-Step "Start RDS '$DB_INSTANCE_ID'"

aws rds start-db-instance `
    --db-instance-identifier $DB_INSTANCE_ID `
    --region $AWS_REGION 2>$null | Out-Null

if ($LASTEXITCODE -eq 0) {
    Write-Ok "RDS start requested (akan available dalam 2-3 menit)"
} else {
    Write-Warn "RDS gak bisa di-start (mungkin udah running atau gak ada)"
}

# ----------------------------------------------------------------------------
# 2. Scale EKS nodegroups balik
# ----------------------------------------------------------------------------
Write-Step "Scale EKS nodegroups balik"

$nodegroups = aws eks list-nodegroups --cluster-name $CLUSTER_NAME --region $AWS_REGION --query "nodegroups" --output text 2>$null
if (-not $nodegroups) {
    Write-Warn "Gak ada nodegroup. Pakai wake.ps1 untuk full provision."
    exit 0
}

# Restore default scaling sesuai eksctl-cluster.yaml:
# - workers-ondemand: desired=1, min=1, max=2
# - workers-spot:     desired=1, min=0, max=3
foreach ($ng in $nodegroups.Split("`t")) {
    if ($ng -like "*ondemand*") {
        $desired = 1; $min = 1; $max = 2
    } elseif ($ng -like "*spot*") {
        $desired = 1; $min = 0; $max = 3
    } else {
        $desired = 1; $min = 1; $max = 4
    }
    Write-Host "  -> $ng (desired=$desired, min=$min, max=$max)"
    aws eks update-nodegroup-config `
        --cluster-name $CLUSTER_NAME `
        --nodegroup-name $ng `
        --scaling-config "desiredSize=$desired,minSize=$min,maxSize=$max" `
        --region $AWS_REGION 2>$null | Out-Null
}
Write-Ok "Nodegroup scaling requested"

# ----------------------------------------------------------------------------
# 3. Wait RDS available
# ----------------------------------------------------------------------------
Write-Step "Wait RDS available (~2-3 menit)"

aws rds wait db-instance-available --db-instance-identifier $DB_INSTANCE_ID --region $AWS_REGION
Write-Ok "RDS available"

# ----------------------------------------------------------------------------
# 4. Update kubeconfig + wait nodes
# ----------------------------------------------------------------------------
Write-Step "Update kubeconfig + wait nodes ready"

aws eks update-kubeconfig --name $CLUSTER_NAME --region $AWS_REGION | Out-Null

Write-Host "Waiting nodes join cluster..."
$nodeReady = $false
for ($i = 0; $i -lt 30; $i++) {
    $count = (kubectl get nodes --no-headers 2>$null | Measure-Object).Count
    if ($count -gt 0) {
        Write-Ok "$count node(s) ready"
        $nodeReady = $true
        break
    }
    Start-Sleep -Seconds 10
}
if (-not $nodeReady) {
    Write-Warn "Nodes belum ready setelah 5 menit. Cek manual:"
    Write-Host "  kubectl get nodes"
    Write-Host "  aws eks describe-nodegroup --cluster-name $CLUSTER_NAME --nodegroup-name <name> --region $AWS_REGION"
}

# ----------------------------------------------------------------------------
# Summary
# ----------------------------------------------------------------------------
Write-Host ""
Write-Host "============================================================" -ForegroundColor Green
Write-Host "  RESUME COMPLETE" -ForegroundColor Green
Write-Host "============================================================" -ForegroundColor Green
Write-Host "  - RDS: available"
Write-Host "  - EKS nodes: provisioning"
Write-Host "  - Pods akan auto-restart begitu node ready"
Write-Host ""
Write-Host "  Verify pods (tunggu ~2 menit):" -ForegroundColor Yellow
Write-Host "    kubectl get pods -n parkir"
Write-Host ""
Write-Host "  Test smoke:" -ForegroundColor Yellow
Write-Host "    `$alb = kubectl get ingress -n parkir -o jsonpath='{.items[0].status.loadBalancer.ingress[0].hostname}'"
Write-Host "    curl http://`$alb/healthz"
Write-Host "============================================================" -ForegroundColor Green
