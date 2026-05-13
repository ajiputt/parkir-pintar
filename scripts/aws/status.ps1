# ============================================================================
# ParkirPintar - AWS Status Check
# ============================================================================
# Quick health check semua AWS resource + K8s pods. Pakai kapan aja untuk
# verify state habis sleep/resume/wake/teardown.

$AWS_REGION     = "ap-southeast-3"
$CLUSTER_NAME   = "ajipur-parkir-staging"
$DB_INSTANCE_ID = "ajipur-parkir-rds"
$REDIS_ID       = "ajipur-parkir-redis"

function Write-Section { param([string]$Message); Write-Host "`n=== $Message ===" -ForegroundColor Cyan }

# ---- EKS cluster ----
Write-Section "EKS Cluster"
$clusterStatus = aws eks describe-cluster --name $CLUSTER_NAME --region $AWS_REGION --query "cluster.status" --output text 2>$null
if ($clusterStatus) {
    Write-Host "Status: $clusterStatus" -ForegroundColor $(if ($clusterStatus -eq "ACTIVE") {"Green"} else {"Yellow"})
} else {
    Write-Host "Cluster gak ada (deleted / belum wake)" -ForegroundColor Red
}

# ---- Nodegroups ----
Write-Section "EKS Nodegroups"
$nodegroups = aws eks list-nodegroups --cluster-name $CLUSTER_NAME --region $AWS_REGION --query "nodegroups" --output text 2>$null
if (-not $nodegroups) {
    Write-Host "Gak ada nodegroup" -ForegroundColor Yellow
} else {
    foreach ($ng in $nodegroups.Split("`t")) {
        $info = aws eks describe-nodegroup --cluster-name $CLUSTER_NAME --nodegroup-name $ng --region $AWS_REGION --query "nodegroup.{Status:status,Desired:scalingConfig.desiredSize,Min:scalingConfig.minSize,Max:scalingConfig.maxSize}" --output json | ConvertFrom-Json
        $color = if ($info.Status -eq "ACTIVE" -and $info.Desired -gt 0) {"Green"} elseif ($info.Desired -eq 0) {"Yellow"} else {"Cyan"}
        Write-Host ("  {0,-25} status={1} desired={2} min={3} max={4}" -f $ng, $info.Status, $info.Desired, $info.Min, $info.Max) -ForegroundColor $color
    }
}

# ---- Worker EC2 instances ----
Write-Section "EC2 Worker Instances"
$instances = aws ec2 describe-instances --region $AWS_REGION --filters "Name=tag:eks:cluster-name,Values=$CLUSTER_NAME" --query "Reservations[].Instances[].[InstanceId,State.Name,InstanceType]" --output text 2>$null
if (-not $instances) {
    Write-Host "Gak ada EC2 worker (cluster scaled to 0 / deleted)" -ForegroundColor Yellow
} else {
    Write-Host $instances
}

# ---- RDS ----
Write-Section "RDS Postgres"
$rdsStatus = aws rds describe-db-instances --db-instance-identifier $DB_INSTANCE_ID --region $AWS_REGION --query "DBInstances[0].DBInstanceStatus" --output text 2>$null
if ($rdsStatus) {
    $color = switch ($rdsStatus) {
        "available" { "Green" }
        "stopped"   { "Yellow" }
        default     { "Cyan" }
    }
    Write-Host "Status: $rdsStatus" -ForegroundColor $color
} else {
    Write-Host "RDS gak ada (deleted)" -ForegroundColor Red
}

# ---- Redis ----
Write-Section "ElastiCache Redis"
$redisStatus = aws elasticache describe-cache-clusters --cache-cluster-id $REDIS_ID --region $AWS_REGION --query "CacheClusters[0].CacheClusterStatus" --output text 2>$null
if ($redisStatus) {
    Write-Host "Status: $redisStatus" -ForegroundColor $(if ($redisStatus -eq "available") {"Green"} else {"Yellow"})
} else {
    Write-Host "Redis gak ada (deleted)" -ForegroundColor Red
}

# ---- K8s ----
Write-Section "Kubernetes State"
$nodeCount = (kubectl get nodes --no-headers 2>$null | Measure-Object).Count
Write-Host "Nodes ready: $nodeCount"

$pods = kubectl get pods -n parkir --no-headers 2>$null
if ($pods) {
    $running = ($pods | Select-String "Running").Count
    $total = ($pods | Measure-Object).Count
    Write-Host "Pods parkir: $running/$total Running"
} else {
    Write-Host "Gak bisa connect ke cluster (kubeconfig expired? cluster deleted?)" -ForegroundColor Yellow
}

# ---- Ingress + ALB ----
Write-Section "Ingress + ALB"
$ingress = kubectl get ingress -n parkir -o jsonpath='{.items[0].status.loadBalancer.ingress[0].hostname}' 2>$null
if ($ingress) {
    Write-Host "ALB DNS: http://$ingress" -ForegroundColor Green
} else {
    Write-Host "ALB belum provisioned / Ingress gak ada" -ForegroundColor Yellow
}

Write-Host ""
