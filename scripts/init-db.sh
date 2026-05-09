#!/usr/bin/env bash
# Init script — di-mount oleh postgres image untuk create extensions + schemas
# sebelum migration tool jalan.
#
# Schemas pre-created supaya golang-migrate bisa bikin table schema_migrations
# di dalam masing-masing schema (search_path=X di DSN). Tanpa ini, migrate
# fail dengan "schema not found" karena schema baru dibuat INSIDE migration
# 001, sedangkan migrate writes schema_migrations BEFORE running migration.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
  -- Extensions di public schema (default), accessible dari semua schema
  -- via search_path=X,public.
  CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
  CREATE EXTENSION IF NOT EXISTS "btree_gist";

  -- Pre-create schemas untuk golang-migrate compatibility
  CREATE SCHEMA IF NOT EXISTS reservation;
  CREATE SCHEMA IF NOT EXISTS billing;
  CREATE SCHEMA IF NOT EXISTS payment;
  CREATE SCHEMA IF NOT EXISTS notification;
EOSQL

echo "✅ extensions + schemas ready"
