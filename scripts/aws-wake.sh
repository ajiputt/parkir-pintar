#!/usr/bin/env bash
# Wake up AWS stack (start-of-day)
# Usage: ./scripts/aws-wake.sh [demo|staging]
set -euo pipefail

ENV="${1:-demo}"
REGION="${AWS_REGION:-ap-southeast-1}"
CLUSTER="parkir-${ENV}"
RDS="parkir-${ENV}"

echo "==> [1/4] Start RDS instance ($RDS)..."
STATUS=$(aws rds describe-db-instances --region "$REGION" \
  --db-instance-identifier "$RDS" \
  --query 'DBInstances[0].DBInstanceStatus' --output text 2>/dev/null || echo "missing")
echo "     Current: $STATUS"
if [ "$STATUS" = "stopped" ]; then
  aws rds start-db-instance --region "$REGION" --db-instance-identifier "$RDS" > /dev/null
  echo "     Waiting available..."
  aws rds wait db-instance-available --region "$REGION" --db-instance-identifier "$RDS"
  echo "     ✅ RDS ready"
elif [ "$STATUS" = "available" ]; then
  echo "     ✅ Already available"
else
  echo "     ⚠️ State: $STATUS (manual check needed)"
fi

echo ""
echo "==> [2/4] Scale ECS services up..."
declare -A REPLICAS=(
  [gateway]=2 [reservation]=2 [billing]=1
  [payment]=1 [search]=1 [notification]=1
)
for svc in "${!REPLICAS[@]}"; do
  printf "     %-15s → %s replicas\n" "$svc" "${REPLICAS[$svc]}"
  aws ecs update-service --region "$REGION" \
    --cluster "$CLUSTER" --service "$svc" \
    --desired-count "${REPLICAS[$svc]}" \
    --no-cli-pager > /dev/null
done

echo ""
echo "==> [3/4] Wait services stable (timeout 5min)..."
aws ecs wait services-stable --region "$REGION" \
  --cluster "$CLUSTER" \
  --services gateway reservation billing payment search notification

echo ""
echo "==> [4/4] Smoke test..."
ALB=$(aws elbv2 describe-load-balancers --region "$REGION" \
  --names "parkir-${ENV}" --query 'LoadBalancers[0].DNSName' --output text)
for i in 1 2 3 4 5; do
  if curl -sf "http://${ALB}/healthz" > /dev/null; then
    echo "     ✅ Stack awake!"
    echo ""
    echo "     Gateway:   http://${ALB}"
    echo "     Swagger:   http://${ALB}/docs"
    exit 0
  fi
  sleep 10
done
echo "     ❌ Healthcheck failed — cek ECS task logs"
exit 1
