#!/usr/bin/env bash
# Sleep AWS stack (end-of-day) — hemat biaya pas tidak demo
# Usage: ./scripts/aws-sleep.sh [demo|staging]
set -euo pipefail

ENV="${1:-demo}"
REGION="${AWS_REGION:-ap-southeast-1}"
CLUSTER="parkir-${ENV}"
RDS="parkir-${ENV}"

if [ "$ENV" = "prod" ]; then
  echo "❌ Refuse to sleep prod"
  exit 1
fi

echo "==> [1/2] Scale ECS services to 0..."
for svc in gateway reservation billing payment search notification; do
  printf "     %-15s → 0\n" "$svc"
  aws ecs update-service --region "$REGION" \
    --cluster "$CLUSTER" --service "$svc" \
    --desired-count 0 \
    --no-cli-pager > /dev/null || true
done

echo ""
echo "==> [2/2] Stop RDS instance..."
STATUS=$(aws rds describe-db-instances --region "$REGION" \
  --db-instance-identifier "$RDS" \
  --query 'DBInstances[0].DBInstanceStatus' --output text 2>/dev/null || echo "missing")
echo "     Current: $STATUS"
if [ "$STATUS" = "available" ]; then
  aws rds stop-db-instance --region "$REGION" --db-instance-identifier "$RDS" > /dev/null
  echo "     ✅ RDS stopping (max 7 hari → AWS auto-restart)"
else
  echo "     skip (state=$STATUS)"
fi

echo ""
echo "💰 Biaya yang berhenti charge sementara:"
printf "   %-25s ~\$%s/jam\n" "ECS Fargate (6 svc)"  "0.074"
printf "   %-25s ~\$%s/jam\n" "RDS compute"          "0.026"
echo ""
echo "💸 Yang TETAP charge (passive infra):"
printf "   %-25s ~\$%s/jam (\$%s/bulan)\n" "ALB"          "0.022" "16"
printf "   %-25s ~\$%s/jam (\$%s/bulan)\n" "ElastiCache"  "0.022" "16"
printf "   %-25s ~\$%s/jam (\$%s/bulan)\n" "NAT Gateway"  "0.045" "32"
printf "   %-25s        \$%s/bulan\n"      "RDS storage 20GB" "2.30"
printf "   %-25s        \$%s/bulan\n"      "Secrets Manager"  "0.80"
echo ""
echo "📅 Untuk hemat lagi (weekend / no-demo): jalankan 'make aws-teardown'"
