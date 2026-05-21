# Getting Started — Run ParkirPintar Locally

Panduan step-by-step untuk **assessor / reviewer** yang ingin menjalankan
ParkirPintar di local machine. Estimasi waktu: **5-10 menit** dari clone sampai
service running + happy flow tested.

> Untuk **deployment ke cloud** (AWS EKS, ECS Fargate), lihat
> [docs/deployment/](docs/deployment/) atau [ADR-0017](docs/architecture/adr/0017-deployment-eks.md).
> Untuk **kontribusi code**, lihat [CONTRIBUTING.md](CONTRIBUTING.md).

---

## TL;DR (untuk yang males baca)

```bash
git clone https://github.com/ajiperdana/parkir-pintar.git
cd parkir-pintar
make demo-up && make demo-wait && make seed
make test-e2e   # verify everything works
```

Buka `http://localhost:8080/docs` untuk Swagger UI.

---

## 1. Prerequisites

Pastikan sudah ter-install di local kamu:

| Tool | Version | Verify | Install |
|---|---|---|---|
| **Docker** | 20.10+ | `docker --version` | [Docker Desktop](https://www.docker.com/products/docker-desktop) |
| **Docker Compose** | v2.x (plugin) | `docker compose version` | Included with Docker Desktop |
| **make** | Any | `make --version` | macOS: built-in. Linux: `apt install make`. Windows: WSL2 atau Git Bash. |
| **curl** | Any | `curl --version` | Pre-installed di hampir semua OS |
| **bash** | 4.0+ | `bash --version` | Windows: included with Git for Windows. macOS/Linux: pre-installed |
| **jq** | 1.6+ | `jq --version` | For demo.sh JSON parsing. Windows: `winget install jqlang.jq` atau `choco install jq`. macOS: `brew install jq`. Linux: `apt install jq` |

**Optional** (untuk yang mau eksplor lebih):

- **Postman** — untuk hit endpoints via UI. Import [`test/postman/parkir-pintar-demo.postman_collection.json`](test/postman/parkir-pintar-demo.postman_collection.json)
- **k6** — untuk load test. `brew install k6` atau download di [k6.io](https://k6.io/docs/getting-started/installation/)
- **Go 1.26+** — kalau mau build/test dari source. `brew install go`

**Resource yang dibutuhkan**:
- RAM: minimum 4 GB free (8 GB recommended)
- Disk: ~3 GB untuk Docker images + data
- Ports yang harus available: `8080`, `5432`, `6379`, `4222`, `3000`, `16686`, `9090`, `8222`

### ⚠️ Windows users — gunakan Git Bash untuk run `make` commands

PowerShell + cmd.exe **tidak bisa execute `.sh` scripts** (shebang `#!/usr/bin/env bash` ga di-parse). Solusi:

| Option | Setup | Recommendation |
|---|---|---|
| **Git Bash** (Recommended) | Auto-included dengan [Git for Windows](https://git-scm.com/download/win) | Open Git Bash → `cd ke project folder` → `make demo-up` |
| WSL2 (Ubuntu) | `wsl --install` (admin PowerShell, reboot) | Heavier setup, but full Linux experience |
| Pure PowerShell | Manually add `C:\Program Files\Git\bin` ke PATH | Works tapi error message ga clean |

**For ParkirPintar specifically**: open Git Bash (Start menu → "Git Bash"), `cd` ke project folder, lalu jalankan `make` commands.

---

## 2. Clone Repository

```bash
git clone https://github.com/ajiperdana/parkir-pintar.git
cd parkir-pintar
```

Quick check structure:

```bash
ls
# Expected:
# README.md  CONTRIBUTING.md  GETTING_STARTED.md  Makefile
# services/  pkg/  proto/  deploy/  docs/  test/  scripts/
```

---

## 3. Boot Full Stack

Single command boot semua services + dependencies:

```bash
make demo-up
```

Yang akan di-boot:

| Service | Port | Role |
|---|---|---|
| `postgres` | 5432 | Database (schema-per-service) |
| `redis` | 6379 | Distributed lock + rate limit |
| `nats` | 4222 / 8222 | Event bus (JetStream) |
| `migrate` | — | One-shot DB migration runner |
| `gateway` | **8080** | API entry point (HTTP REST) |
| `reservation` | 9091 (gRPC) / 9191 (HTTP) | Reservation service |
| `billing` | 9092 / 9192 | Billing service |
| `payment` | 9093 / 9193 | Payment service |
| `notification` | 9196 | Notification (mock SES) |
| `jaeger` | 16686 | Distributed tracing UI |
| `prometheus` | 9090 | Metrics |
| `grafana` | 3000 | Dashboards |

First run akan **download images** (~2 GB) + **build service images** (~5-8 menit).
Subsequent run akan cached, jauh lebih cepat (~30 detik).

> **💡 Note**: First build agak lama karena Dockerfile auto-generate proto stubs
> di dalam container (no need install `buf` + `protoc-gen-go` locally). Cached
> layers di subsequent build.

### Wait for healthy

```bash
make demo-wait
```

Script ini poll `/healthz` endpoint setiap service sampai semua return 200.
Estimasi 15-30 detik setelah `demo-up` selesai.

---

## 4. Seed Initial Data

Database baru di-init kosong. Untuk demo, butuh parking area + spots:

```bash
make seed
```

Output: 1 parking area, 5 floors, 750 spots (30 mobil + 50 motor per floor).

Verify:

```bash
curl -s http://localhost:8080/v1/availability | jq '.spots | length'
# Expected: 750 (atau jumlah AVAILABLE saat ini)
```

---

## 5. Verify Everything Works

### Quick health check

```bash
# Gateway
curl http://localhost:8080/healthz
# Expected: ok

# Per-service health (via Docker internal)
docker compose -f deploy/docker/docker-compose.yml exec gateway wget -qO- http://reservation:9191/healthz
# Expected: ok
```

### Run E2E test suite

```bash
make test-e2e
```

Output akan show semua skenario use case di-test:
- ✅ Happy path reservation
- ✅ Double-book prevention
- ✅ Reservation expiry (no-show)
- ✅ Cancellation
- ✅ Extended stay billing
- ✅ Overnight fee
- ✅ Payment QRIS success
- ✅ Payment failure handling

Estimasi 30-60 detik untuk semua skenario.

---

## 6. Try the API

### Option A: Browser — Swagger UI

Buka [http://localhost:8080/docs](http://localhost:8080/docs)

Swagger UI auto-generated dari proto schema. Try endpoint langsung dari browser.

> **📖 Untuk detail spec per endpoint** (headers, body, response, error codes, examples), lihat [docs/api/reference.md](docs/api/reference.md).

### Option B: Postman

1. Buka Postman
2. Import collection: `test/postman/parkir-pintar-demo.postman_collection.json`
3. Import environment: `test/postman/local.postman_environment.json`
4. Select environment "ParkirPintar Local"
5. Run collection — auto-run semua skenario

### Option C: Demo script via curl

```bash
./scripts/demo.sh
```

Script ini eksekusi happy flow end-to-end:
1. Get availability
2. Create reservation
3. Check-in
4. Check-out
5. Get invoice
6. Create payment
7. Simulate Midtrans webhook (mark paid)
8. Verify invoice paid

### Option D: Manual curl (untuk understanding)

```bash
# 0. Get auth token (dev endpoint, gated by X-Dev-Token-Secret)
TOKEN=$(curl -s -X POST http://localhost:8080/dev/token \
  -H "X-Dev-Token-Secret: dev-secret" \
  -H "Content-Type: application/json" \
  -d '{"driver_id": "driver-demo-1"}' | jq -r '.token')

echo "Token: ${TOKEN:0:30}..."

# 1. Check availability
curl -s http://localhost:8080/v1/availability \
  -H "Authorization: Bearer $TOKEN" | jq '.summary'

# 2. Create reservation
SPOT_ID=$(curl -s http://localhost:8080/v1/availability \
  -H "Authorization: Bearer $TOKEN" | jq -r '.spots[0].id')

RES=$(curl -s -X POST http://localhost:8080/v1/reservations \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d "{
    \"driver_id\": \"driver-demo-1\",
    \"spot_id\": \"$SPOT_ID\",
    \"vehicle_type\": \"CAR\"
  }")
RES_ID=$(echo $RES | jq -r '.id')
echo "Reservation created: $RES_ID"

# 3. Check-in
curl -X POST http://localhost:8080/v1/reservations/$RES_ID/checkin \
  -H "Authorization: Bearer $TOKEN"

# 4. Wait 5 detik (simulate parking)
sleep 5

# 5. Check-out
curl -X POST http://localhost:8080/v1/reservations/$RES_ID/checkout \
  -H "Authorization: Bearer $TOKEN"

# 6. Get invoice
curl -s http://localhost:8080/v1/billing/invoices?reservation_id=$RES_ID \
  -H "Authorization: Bearer $TOKEN" | jq
```

---

## 7. Explore Observability

Saat stack running, observability UI bisa di-explore:

### Jaeger (Distributed Tracing)

[http://localhost:16686](http://localhost:16686)

1. Pilih service `gateway` di dropdown
2. Click **Find Traces**
3. Pilih salah satu trace
4. Lihat span flow: gateway → reservation → billing → payment

### Grafana (Metrics + Logs + Traces)

[http://localhost:3000](http://localhost:3000)

- Username: `admin`
- Password: `admin` (first login akan prompt change)

Pre-loaded dashboards:
- **ParkirPintar Overview** — SR, latency, throughput per service
- **gRPC Metrics** — error rate, P95, RPS
- **Database** — connection pool, query duration

### Prometheus (Metrics raw)

[http://localhost:9090](http://localhost:9090)

Try queries:
```promql
# Request rate per service
sum by (service) (rate(grpc_requests_total[5m]))

# Error rate
sum by (service) (rate(grpc_requests_total{grpc_code!~"OK"}[5m]))

# P95 latency
histogram_quantile(0.95, sum by (service, le) (rate(grpc_request_duration_seconds_bucket[5m])))
```

### NATS Monitoring

[http://localhost:8222](http://localhost:8222/jsz?streams=1&consumers=1)

Lihat stream `PARKIRPINTAR` + consumer status per service.

---

## 8. Common Issues & Troubleshooting

### Port already in use

Error: `Bind for 0.0.0.0:8080 failed: port is already allocated`

Fix:
```bash
# Find process using port
lsof -i :8080   # macOS/Linux
netstat -ano | findstr :8080   # Windows

# Either kill the process or override port:
GATEWAY_HTTP_PORT=8081 make demo-up
```

### Out of memory

Error: containers `OOMKilled` atau slow performance

Fix: increase Docker Desktop memory allocation
- macOS/Windows: Docker Desktop → Settings → Resources → Memory → set ≥ 6 GB
- Linux: native Docker tidak ada limit, but verify free RAM via `free -h`

### `make demo-wait` timeout

Error: services tidak ready setelah 60 detik

Diagnostic:
```bash
# Lihat container status
docker compose -f deploy/docker/docker-compose.yml ps

# Cek logs container yang stuck
docker compose -f deploy/docker/docker-compose.yml logs <service-name>
```

Common causes:
- Postgres slow start → wait longer (rerun `make demo-wait`)
- Migration failed → check `migrate` container logs
- Build failed → run `make demo-up` lagi

### `make test-e2e` failing

```bash
# Pastikan stack fully ready
make demo-wait

# Re-seed
make seed

# Run dengan verbose output
./scripts/e2e.sh --verbose
```

Kalau still fail, kasih tau error message via GitHub Issue.

### Need to start fresh

```bash
# Stop + clean volumes (DB data, etc.)
make demo-down

# Boot ulang
make demo-up && make demo-wait && make seed
```

### Docker layer cache stale

Setelah pull update terbaru, rebuild images:

```bash
make demo-down
docker compose -f deploy/docker/docker-compose.yml build --no-cache
make demo-up
```

---

## 9. Cleanup

Saat selesai exploration:

```bash
# Stop semua container, hapus volume (data hilang)
make demo-down

# Atau stop tanpa hapus data (resume dengan demo-up)
docker compose -f deploy/docker/docker-compose.yml stop
```

Disk usage cleanup (kalau perlu):

```bash
# Reclaim Docker disk
docker system prune -af --volumes
```

---

## 10. What to Read Next

Setelah jalanin local, recommended reading order:

| Read | Why |
|---|---|
| [README.md](README.md) — Section 1-2 | Solution overview + production-ready engineering philosophy |
| [README.md](README.md) — Section 3-4 (HLD/LLD) | Architecture diagrams |
| [docs/architecture/diagrams/sequences.md](docs/architecture/diagrams/sequences.md) | Sequence diagrams critical flows |
| [docs/architecture/adr/](docs/architecture/adr/) | 23 ADRs documenting key decisions |
| [docs/api/idempotency.md](docs/api/idempotency.md) | API idempotency contract |
| [docs/runbooks/](docs/runbooks/) | Operational runbooks |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Branch + commit conventions |
| [test/load/RESULTS.md](test/load/RESULTS.md) | Performance benchmarks |

---

## 11. FAQ

### Q: Bisa run tanpa Docker?

A: Bisa, tapi need install + manage Postgres + Redis + NATS manual. Untuk
quick assessment, Docker Compose much easier. Kalau strongly prefer
no-Docker:

```bash
# Install deps manual
brew install postgresql@16 redis nats-server

# Boot deps
brew services start postgresql@16
brew services start redis
nats-server -js &

# Run migrations
make migrate-up DB_URL="postgres://gopark:gopark@localhost/parkirpintar?sslmode=disable"

# Run services via Go
for svc in gateway reservation billing payment notification; do
  (cd services/$svc && cp example.env .env.local && \
    # Edit .env.local for local-network addresses
    go run ./cmd) &
done
```

### Q: Apa minimum endpoint yang harus di-test untuk verify everything works?

A:
1. `GET /healthz` → 200 ok (service running)
2. `GET /v1/availability` → list of 750 spots (DB + seed OK)
3. `POST /v1/reservations` (with auth) → 200 with reservation ID (full flow)
4. `make test-e2e` → all green (everything integrated)

### Q: Apakah perlu Midtrans real account?

A: Tidak. Default `PAYMENT_MOCK_MODE=true` → mock implementation, no real
Midtrans dial. Webhook simulation via `./scripts/simulate-midtrans-webhook.sh`.

### Q: Bagaimana cara akses logs?

A:
```bash
# All services
make demo-logs

# Specific service
docker compose -f deploy/docker/docker-compose.yml logs -f gateway

# Filter by level
docker compose -f deploy/docker/docker-compose.yml logs gateway | grep -i error
```

Production deployment punya Loki untuk structured log search.

### Q: Bisa load test di local?

A: Yes:

```bash
# Install k6
brew install k6

# Run availability load test
k6 run -e BASE_URL=http://localhost:8080 test/load/availability.js

# Lihat hasil di test/load/RESULTS.md template
```

---

## Need Help?

- **Issues**: [GitHub Issues](https://github.com/ajiperdana/parkir-pintar/issues)
- **Documentation**: [docs/](docs/)
- **Author**: Aji Perdana Putra (`ajiperdanaputra90@gmail.com`)

Happy exploring! 🚗💨
