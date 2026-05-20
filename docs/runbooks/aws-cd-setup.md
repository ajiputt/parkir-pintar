# Runbook — AWS CD Setup (One-Time)

> Setup yang harus dilakukan sekali sebelum GitHub Actions bisa deploy ke AWS via OIDC (no long-lived AWS access keys di GitHub).

## Why OIDC, bukan AWS Access Key?

| Aspect | Access Key | OIDC |
|---|---|---|
| Credential rotation | Manual setiap 90 hari | Automatic per workflow run |
| Blast radius kalau leak | Long-lived | Single workflow execution |
| Compliance (PCI/SOC2) | Less preferred | Best practice |
| Setup effort | Mudah | Sedikit lebih ribet (one-time) |

OIDC = no static secret di GitHub. Workflow request short-lived token dari AWS STS pakai signed JWT dari GitHub.

## One-Time Setup Steps

### 1. Buat OIDC provider di AWS account

```bash
# Cek dulu kalau sudah ada (kalau pernah pakai untuk repo lain di akun sama)
aws iam list-open-id-connect-providers

# Kalau belum ada:
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1
```

### 2. Buat IAM Role yang trust GitHub Actions

Simpan trust policy:

```bash
ACCT=$(aws sts get-caller-identity --query Account --output text)
GH_REPO="ajiperdana/parkir-pintar"   # ganti sesuai repo kamu

cat > trust.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "Federated": "arn:aws:iam::${ACCT}:oidc-provider/token.actions.githubusercontent.com"
    },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
      },
      "StringLike": {
        "token.actions.githubusercontent.com:sub": "repo:${GH_REPO}:*"
      }
    }
  }]
}
EOF

aws iam create-role \
  --role-name parkir-github-deploy \
  --assume-role-policy-document file://trust.json
```

### 3. Attach permissions

Untuk demo / dev environment, attach managed policies (production: tighten dengan custom policy):

```bash
ROLE=parkir-github-deploy

# ECS deploy
aws iam attach-role-policy --role-name $ROLE \
  --policy-arn arn:aws:iam::aws:policy/AmazonECS_FullAccess

# Read RDS (start/stop)
aws iam attach-role-policy --role-name $ROLE \
  --policy-arn arn:aws:iam::aws:policy/AmazonRDSFullAccess

# Read ALB
aws iam attach-role-policy --role-name $ROLE \
  --policy-arn arn:aws:iam::aws:policy/ElasticLoadBalancingFullAccess

# Read Secrets
aws iam attach-role-policy --role-name $ROLE \
  --policy-arn arn:aws:iam::aws:policy/SecretsManagerReadWrite

# Terraform — broad untuk demo, restrict di prod
aws iam attach-role-policy --role-name $ROLE \
  --policy-arn arn:aws:iam::aws:policy/PowerUserAccess
aws iam attach-role-policy --role-name $ROLE \
  --policy-arn arn:aws:iam::aws:policy/IAMFullAccess

# Verify
aws iam list-attached-role-policies --role-name $ROLE
```

### 4. Bucket S3 untuk Terraform state

```bash
USER_SLUG=$(echo $USER | tr -d ' ' | tr 'A-Z' 'a-z')
BUCKET="parkir-tfstate-${USER_SLUG}"

aws s3 mb s3://${BUCKET} --region ap-southeast-1
aws s3api put-bucket-versioning \
  --bucket ${BUCKET} \
  --versioning-configuration Status=Enabled
aws s3api put-bucket-encryption \
  --bucket ${BUCKET} \
  --server-side-encryption-configuration '{
    "Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]
  }'
aws s3api put-public-access-block \
  --bucket ${BUCKET} \
  --public-access-block-configuration "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true"
```

### 5. Set GitHub repository secrets

```bash
ROLE_ARN=$(aws iam get-role --role-name parkir-github-deploy --query Role.Arn --output text)

gh secret set AWS_DEPLOY_ROLE_ARN --body "${ROLE_ARN}"
gh secret set TF_STATE_BUCKET     --body "${BUCKET}"

# Optional: Slack notification
gh secret set SLACK_WEBHOOK_URL --body "https://hooks.slack.com/services/..."
```

### 6. Set Midtrans key di AWS Secrets Manager

```bash
aws secretsmanager put-secret-value \
  --secret-id parkir/demo/midtrans-server-key \
  --secret-string "SB-Mid-server-XXXXXXXXX" \
  --region ap-southeast-1

aws secretsmanager put-secret-value \
  --secret-id parkir/demo/jwt-secret \
  --secret-string "$(openssl rand -hex 32)" \
  --region ap-southeast-1
```

### 7. Test deployment

```bash
# Trigger workflow
gh workflow run deploy-aws.yml \
  -f environment=demo \
  -f apply_terraform=true \
  -f image_tag=latest

# Watch progress
gh run watch
```

---

## Ongoing Operations

### Daily — wake up

```bash
# Manual
./scripts/aws-wake.sh demo

# Atau biarkan cron schedule di .github/workflows/aws-wake.yml
# (auto-jalan setiap weekday jam 08:00 WIB)
```

### Daily — sleep

```bash
./scripts/aws-sleep.sh demo

# Atau cron schedule (auto jam 19:00 WIB)
```

### Per release — re-deploy

```bash
# Setelah merge ke main, image baru sudah di GHCR (via ci.yml).
# Trigger deploy:
gh workflow run deploy-aws.yml \
  -f environment=demo \
  -f apply_terraform=false \
  -f image_tag=$(git rev-parse --short HEAD)
```

### Weekend / pre-presentation — full teardown

```bash
# Snapshot + destroy semua infra
gh workflow run aws-teardown.yml -f environment=demo -f confirm=DESTROY

# Recreate Senin pagi
gh workflow run deploy-aws.yml -f environment=demo -f apply_terraform=true
```

---

## Production Hardening Checklist

Sebelum deploy ke environment `prod`:

- [ ] Replace `PowerUserAccess` + `IAMFullAccess` dengan custom policy minimal
- [ ] Tambah condition `repo:.../environment:prod` di trust policy
- [ ] Required reviewers di GitHub Environment `prod`
- [ ] Pisahkan AWS account untuk prod (organization unit)
- [ ] Enable CloudTrail di prod account
- [ ] Setup AWS Config rules
- [ ] Branch protection: require PR review + CI green sebelum merge

---

## Troubleshooting

### `AccessDenied` saat OIDC assume role
- Cek thumbprint OIDC provider masih valid (kadang AWS update — pakai `e1c8b07e7019a91ba9d2c3edfa25fdb389b5d11d` kalau yg lama gagal)
- Cek `sub` claim di trust policy match dengan repo path
- Cek workflow `permissions: id-token: write` ada

### Terraform state lock error
- Stuck lock biasanya karena workflow di-cancel mid-apply
- Force unlock:
  ```bash
  cd deploy/terraform/aws-ecs
  terraform force-unlock <LOCK_ID>
  ```

### ECS task tidak start
- Cek CloudWatch Logs `/ecs/parkir-demo/<service>`
- Common causes: env var salah, secret tidak accessible, image tag tidak ada
- Cek IAM execution role bisa assume `secretsmanager:GetSecretValue`

### Cost spike tiba-tiba
- Cek Cost Explorer → Group by SERVICE → cari outlier
- Common: NAT Gateway data transfer, CloudWatch logs ingestion
- Mitigasi: tambah CW Logs filter, batasi log level production
