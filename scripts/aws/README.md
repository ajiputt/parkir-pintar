# AWS Infrastructure Scripts

Skenario A daily flow untuk ParkirPintar deployment. Local-trigger (laptop), bukan GitHub Actions, untuk infra lifecycle control yang cost-sensitive.

## Files

| File | Purpose |
|------|---------|
| `eksctl-cluster.yaml` | EKS cluster definition (VPC, IAM/IRSA, node group, addons) |
| `wake.ps1` | Daily provision script (~20 menit) |
| `teardown.ps1` | Daily delete script (~10 menit) |

## Prerequisite (one-time setup)

1. **AWS CLI authenticated**:
   ```powershell
   aws sts get-caller-identity
   ```
2. **Tools installed**: `aws`, `eksctl`, `kubectl`, `helm` (di PATH)
3. **AWS resources pre-created** (persistent, never deleted):
   - IAM OIDC provider untuk GitHub Actions
   - IAM Role `GitHubActionsDeployRole`
   - ECR repos: `ajipur-parkir-pintar/{gateway,reservation,billing,payment,notification}`
   - Secrets Manager entries:
     - `ajipur-parkir-pintar/rds-password` ({"password": "..."})
     - `ajipur-parkir-pintar/jwt-secret` (raw string)
     - `ajipur-parkir-pintar/midtrans-server-key` (raw string)
     - `ajipur-parkir-pintar/db-url` (placeholder, auto-update saat wake)
4. **GitHub Secret**: `AWS_DEPLOY_ROLE_ARN` ke role di atas

## Daily Flow (Skenario A)

### Pagi (08:30)

```powershell
cd "C:\path\to\parkir-pintar"

# 1. Provision infrastructure (~20 menit)
.\scripts\aws\wake.ps1

# Output di akhir:
# - EKS cluster name
# - RDS endpoint
# - Redis endpoint
# - NATS internal DNS
```

Setelah wake selesai, **trigger CD pipeline** via GitHub UI:

1. Buka https://github.com/ajiputt/parkir-pintar/actions
2. Pilih workflow **"Deploy to AWS EKS"**
3. Klik **Run workflow** → pilih environment `demo` → Run
4. Tunggu hijau (~5 menit)

Stack siap dipakai. ALB DNS ada di output workflow atau:
```powershell
kubectl get ingress -n parkir
```

### Demo aktif (sepanjang hari)

Test via Postman atau curl ke ALB DNS:
```powershell
curl http://<alb-dns>/v1/availability
```

Monitor pods:
```powershell
kubectl get pods -n parkir
kubectl logs -f deployment/gateway -n parkir
```

### Sore (17:30)

```powershell
# 2. Teardown all resources (~10 menit)
.\scripts\aws\teardown.ps1
```

Script akan **konfirmasi sekali** sebelum delete. Ketik `yes` untuk lanjut.

⚠️ **Wajib jalankan ini** sebelum tidur. Lupa = $5+/hari kelarung idle.

## Cost Estimate (Skenario A)

| Item | Active 8h | Idle 16h | Daily |
|---|---|---|---|
| EKS control plane | $0.80 | $0 | $0.80 |
| 2× t3.medium Spot | $0.19 | $0 | $0.19 |
| RDS db.t3.micro | $0.14 | $0 | $0.14 |
| ElastiCache | $0.16 | $0 | $0.16 |
| ALB | $0.25 | $0 | $0.25 |
| NAT Gateway | $0.40 | $0 | $0.40 |
| ECR + Secrets + IAM | persist | persist | $0.10 |
| **Total** | | | **~$2.04/hari** |

Monthly: ~$45 (22 hari kerja).

## Troubleshooting

### Wake gagal di tengah

Re-run `wake.ps1` — script idempotent (cek existing resources, skip kalau ada). 

Kalau stuck di state inconsistent:
```powershell
# Force teardown
.\scripts\aws\teardown.ps1
# Lalu wake lagi
.\scripts\aws\wake.ps1
```

### Teardown gagal: cluster gak ke-delete

eksctl mungkin nge-block karena ada LoadBalancer/PVC orphan. Cleanup manual:
```powershell
# Delete ingress dulu (release ALB)
kubectl delete ingress --all -n parkir

# Delete LoadBalancer service
kubectl delete service --field-selector spec.type=LoadBalancer --all-namespaces

# Lalu retry
eksctl delete cluster --name ajipur-parkir-staging --region ap-southeast-3 --wait
```

### Verify zero cost

Pastikan SEMUA resource ke-delete:
```powershell
# Cek instance running
aws ec2 describe-instances --region ap-southeast-3 `
    --query "Reservations[].Instances[?State.Name=='running'].InstanceId"
# Output kosong = clean

# Cek EKS cluster
aws eks list-clusters --region ap-southeast-3
# Output: [] = clean

# Cek RDS
aws rds describe-db-instances --region ap-southeast-3 --query "DBInstances[].DBInstanceIdentifier"
# Output: [] = clean

# Cek ALB
aws elbv2 describe-load-balancers --region ap-southeast-3 --query "LoadBalancers[].LoadBalancerName"
# Output: [] = clean

# Cek NAT Gateway (paling mahal kalau orphan)
aws ec2 describe-nat-gateways --region ap-southeast-3 `
    --query "NatGateways[?State!='deleted'].NatGatewayId"
# Output: [] = clean
```

### RDS password salah / connection refused

Verify Secret Manager value:
```powershell
aws secretsmanager get-secret-value --secret-id ajipur-parkir-pintar/db-url --region ap-southeast-3
```

Check format: `postgres://parkir:PASSWORD@HOST:5432/parkirpintar?sslmode=require`

### kubectl gak bisa connect ke cluster

```powershell
aws eks update-kubeconfig --name ajipur-parkir-staging --region ap-southeast-3
kubectl get nodes
```

## Migration ke production-grade

Untuk production:

1. **Multi-AZ RDS**: edit `wake.ps1`, ganti `--no-multi-az` → `--multi-az`
2. **Multi-AZ Redis**: ganti `cache-cluster` ke `replication-group` dengan `num-cache-clusters=2`
3. **Backup snapshot**: edit `teardown.ps1`, ganti `--skip-final-snapshot` → `--final-db-snapshot-identifier parkir-final-YYYYMMDD`
4. **Multi-NAT Gateway**: edit `eksctl-cluster.yaml`, `vpc.nat.gateway: HighlyAvailable`
5. **On-Demand mix**: tambah node group On-Demand 30% di config
6. **TLS via ACM**: setup Route 53 + ACM, update Helm `values.eks.yaml` annotations
7. **WAF**: enable AWS WAF di front of ALB

Architecture identical, naik prod = swap config values.
