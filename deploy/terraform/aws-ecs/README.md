# Terraform — AWS ECS Fargate

Deploy ParkirPintar ke AWS ECS Fargate dengan **2 optimasi cost** sudah built-in:

1. **NAT Instance (fck-nat)** menggantikan NAT Gateway — hemat $29/bulan
2. **Skip ElastiCache Redis** (default) — hemat $16/bulan; reservation auto-fallback ke memory locker

> Detail breakdown biaya & optimasi tambahan: lihat [`COST.md`](COST.md).

## Estimasi Biaya (default config, ap-southeast-1)

| Komponen | Sizing | 24/7 | Daily 10h |
|---|---|---|---|
| RDS Postgres | db.t3.micro, 20GB gp3 | $17 | $8 |
| ALB | per LCU | $18 | $18 |
| NAT Instance (fck-nat) | t4g.nano | $4 | $4 |
| Fargate (5 svc × 0.25vCPU × 512MiB) | — | $45 | $13 |
| CloudWatch Logs | 14d retention | $2 | $2 |
| Secrets Manager | 2 secrets | $1 | $1 |
| **Total** | | **~$87** | **~$46** |

> Pola **daily 10h × 22 hari kerja = ~$50/bulan** untuk assessment use case.

## Toggle Optimasi

```hcl
# terraform.tfvars
enable_redis = false   # default: false (auto-fallback memory locker)
```

Set `enable_redis = true` kalau:
- Production multi-replica reservation service (memory locker tidak shared)
- High contention rate untuk user-selected spot
- Distributed rate limit di gateway perlu shared state

## Quick Start

```bash
cd deploy/terraform/aws-ecs

# 1. Setup backend state (sekali saja)
aws s3 mb s3://parkir-tfstate-${USER} --region ap-southeast-1
aws s3api put-bucket-versioning --bucket parkir-tfstate-${USER} \
  --versioning-configuration Status=Enabled

terraform init \
  -backend-config="bucket=parkir-tfstate-${USER}" \
  -backend-config="key=parkir-pintar/demo/terraform.tfstate" \
  -backend-config="region=ap-southeast-1"

# 2. Plan
terraform plan -var environment=demo -var image_tag=v1.0.0

# 3. Apply
terraform apply -auto-approve

# 4. Set secrets
aws secretsmanager put-secret-value \
  --secret-id parkir/demo/midtrans-server-key \
  --secret-string "SB-Mid-server-XXX"
aws secretsmanager put-secret-value \
  --secret-id parkir/demo/jwt-secret \
  --secret-string "$(openssl rand -hex 32)"

# 5. Get endpoints
terraform output alb_dns
terraform output rds_endpoint
```

## Daily Operations

```bash
# Pagi: hidupkan stack
make aws-wake AWS_ENV=demo
# Atau lewat workflow GitHub Actions (auto via cron 08:00 WIB)

# Sore: matikan stack
make aws-sleep AWS_ENV=demo

# Cek biaya bulan ini
make aws-cost
```

## NATS Deployment

NATS di-deploy sebagai **Fargate task terpisah** dengan service discovery name `nats.parkir.local`. Untuk persistence, attach EFS volume — atau ganti ke **Amazon MQ** kalau perlu managed.

> TODO: tambahkan `nats-fargate.tf` (currently NATS dianggap sudah running). Untuk demo lokal, sufficient pakai docker-compose.

## Cleanup

```bash
# Soft (pause biaya, simpan state)
make aws-sleep

# Hard (destroy semua infra)
make aws-teardown    # Lewat workflow GH Actions, snapshot otomatis

# Atau lokal:
terraform destroy -var environment=demo
```

## Production Hardening Checklist

- [ ] **Switch back to NAT Gateway** (Multi-AZ) untuk HA — fck-nat single AZ adalah trade-off cost vs availability
- [ ] **Enable Redis** untuk multi-replica reservation service
- [ ] RDS Multi-AZ
- [ ] ElastiCache cluster mode (kalau enable Redis)
- [ ] WAF di ALB
- [ ] AWS Backup untuk RDS (otomatis daily snapshot)
- [ ] CloudWatch Alarms → SNS topic → PagerDuty/Slack
- [ ] AWS Config rules untuk drift detection
- [ ] GuardDuty enabled
- [ ] VPC Flow Logs ke S3 (90 hari retention)
- [ ] Pisahkan AWS account untuk prod (AWS Organizations)
