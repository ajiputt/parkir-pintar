# ParkirPintar - Local DB Migration Script
#
# Jalanin migration ke local Postgres (docker-compose atau standalone).
# Pre-create extensions + 4 schemas, lalu migrate up per schema.
#
# Prerequisite:
#   - migrate CLI installed (choco install migrate)
#   - psql installed (choco install postgresql)
#   - Postgres running di localhost:5432 (docker-compose up postgres)
#
# Default credential pakai docker-compose dev creds. Override via env var:
#   $env:LOCAL_DB_USER = "parkir"
#   $env:LOCAL_DB_PASSWORD = "your-password"
#   $env:LOCAL_DB_HOST = "localhost"
#   $env:LOCAL_DB_PORT = "5432"
#   $env:LOCAL_DB_NAME = "parkirpintar"

$ErrorActionPreference = "Stop"

# ---- Config ----
$DB_USER     = if ($env:LOCAL_DB_USER)     { $env:LOCAL_DB_USER }     else { "parkir" }
$DB_PASSWORD = if ($env:LOCAL_DB_PASSWORD) { $env:LOCAL_DB_PASSWORD } else { "parkir_dev_only" }
$DB_HOST     = if ($env:LOCAL_DB_HOST)     { $env:LOCAL_DB_HOST }     else { "localhost" }
$DB_PORT     = if ($env:LOCAL_DB_PORT)     { $env:LOCAL_DB_PORT }     else { "5432" }
$DB_NAME     = if ($env:LOCAL_DB_NAME)     { $env:LOCAL_DB_NAME }     else { "parkirpintar" }

$REPO_ROOT       = (Get-Item $PSScriptRoot).Parent.Parent.FullName
$MIGRATIONS_ROOT = Join-Path $REPO_ROOT "deploy\migrations"
$DB_URL_BASE     = "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"

# ---- Helpers ----
function Write-Step { param([string]$Message); Write-Host "`n==> $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message); Write-Host "[OK] $Message" -ForegroundColor Green }
function Write-Fail { param([string]$Message); Write-Host "[FAIL] $Message" -ForegroundColor Red }

# ---- Pre-flight ----
Write-Step "Pre-flight check"

foreach ($tool in @("psql", "migrate")) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        Write-Fail "'$tool' tidak ke-install. Install dulu:"
        Write-Host "  choco install postgresql migrate -y" -ForegroundColor Yellow
        exit 1
    }
}
Write-Ok "Tools ready (psql, migrate)"

if (-not (Test-Path $MIGRATIONS_ROOT)) {
    Write-Fail "Migrations folder tidak ada: $MIGRATIONS_ROOT"
    exit 1
}

# ---- Test DB connection ----
Write-Step "Test koneksi DB ke ${DB_HOST}:${DB_PORT}"

$env:PGPASSWORD = $DB_PASSWORD
psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -c "SELECT 1;" 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Fail "psql connection failed. Cek:"
    Write-Host "  - Postgres jalan? (docker compose -f deploy/docker/docker-compose.yml up -d postgres)" -ForegroundColor Yellow
    Write-Host "  - Credentials bener? user=$DB_USER, db=$DB_NAME" -ForegroundColor Yellow
    exit 1
}
Write-Ok "DB connection OK"

# ---- Pre-create extensions + schemas ----
Write-Step "Pre-create extensions + 4 schemas"

$initSQL = @'
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE SCHEMA IF NOT EXISTS reservation;
CREATE SCHEMA IF NOT EXISTS billing;
CREATE SCHEMA IF NOT EXISTS payment;
CREATE SCHEMA IF NOT EXISTS notification;
'@

$initSQL | psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME
if ($LASTEXITCODE -ne 0) {
    Write-Fail "Init extensions + schemas failed"
    exit 1
}
Write-Ok "Extensions + 4 schemas ready"

# ---- Run migrations ----
Write-Step "Run migrations per schema"

foreach ($schema in @("reservation", "billing", "payment", "notification")) {
    $schemaPath = Join-Path $MIGRATIONS_ROOT $schema
    if (-not (Test-Path $schemaPath)) {
        Write-Host "[SKIP] $schemaPath gak ada" -ForegroundColor Yellow
        continue
    }

    $schemaUrl = $DB_URL_BASE + "&search_path=" + $schema
    Write-Host "==> migrate $schema"
    migrate -path $schemaPath -database $schemaUrl up

    if ($LASTEXITCODE -eq 0) {
        Write-Ok "Schema '$schema' migrated"
    } else {
        Write-Fail "Schema '$schema' migration failed"
        exit 1
    }
}

# ---- Summary ----
Write-Host ""
Write-Host "============================================================" -ForegroundColor Green
Write-Host "  Local migration COMPLETE" -ForegroundColor Green
Write-Host "============================================================" -ForegroundColor Green
Write-Host "  DB:      ${DB_HOST}:${DB_PORT}/${DB_NAME}"
Write-Host "  Schemas: reservation, billing, payment, notification"
Write-Host ""
Write-Host "  Next: start services" -ForegroundColor Yellow
Write-Host "    .\scripts\dev\run-reservation.ps1"
Write-Host "    .\scripts\dev\run-billing.ps1"
Write-Host "    .\scripts\dev\run-payment.ps1"
Write-Host "    .\scripts\dev\run-notification.ps1"
Write-Host "    .\scripts\dev\run-gateway.ps1"
Write-Host ""
