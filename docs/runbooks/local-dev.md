# Local Development Setup

## Prerequisites

- Go 1.22+
- Docker 24+ & Docker Compose v2
- Make
- (optional) buf, protoc, golangci-lint, k6, helm, terraform

Install dev tools cepat:

```bash
make tools
```

## First-time setup

```bash
# 1. Clone & enter
git clone <repo-url> parkir-pintar
cd parkir-pintar

# 2. Copy env file
cp .env.example .env
# Edit kalau perlu (Midtrans key, dsb)

# 3. Boot stack lengkap
make demo-up
make demo-wait
make seed

# 4. Test demo flow
./scripts/demo.sh
```

## Daily workflow

```bash
# Edit code...

# Lint + test cepat per package
cd pkg/pricing
go test -race -v ./...

# Atau full
make test-unit

# Restart 1 service setelah edit
docker compose -f deploy/docker/docker-compose.yml restart reservation

# Lihat log
docker compose -f deploy/docker/docker-compose.yml logs -f reservation
```

## Troubleshooting

### `make demo-up` gagal pull image

Pastikan Docker Hub & GHCR accessible. Untuk offline: pre-build local images dulu:
```bash
docker compose -f deploy/docker/docker-compose.yml build
```

### Postgres EXCLUDE constraint error

Pastikan extension `btree_gist` sudah enabled. Cek `scripts/init-db.sh` ter-mount.

### NATS connection refused

```bash
docker compose logs nats
docker compose restart nats
```

### Tests pass local tapi fail di CI

- Periksa env var berbeda
- Race condition? Run `go test -race -count=10` lokal

## Useful endpoints

- Gateway:     http://localhost:8080
- Swagger:     http://localhost:8080/docs
- Postgres:    localhost:5432 (user: parkir / pass: parkir_dev_only)
- Redis:       localhost:6379
- NATS Mon:    http://localhost:8222
- Jaeger:      http://localhost:16686  (profile: observability)
- Grafana:     http://localhost:3000   (admin/admin)
- Prometheus:  http://localhost:9090

## Code generation

Setelah edit `.proto`:
```bash
make proto
```

Setelah edit SQL schema, buat migration baru:
```bash
migrate create -ext sql -dir deploy/migrations/reservation -seq add_xxx_column
```
