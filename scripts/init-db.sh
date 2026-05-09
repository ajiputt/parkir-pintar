#!/usr/bin/env bash
# Init script — di-mount oleh postgres image untuk create extensions sebelum migration.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
  CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
  CREATE EXTENSION IF NOT EXISTS "btree_gist";
EOSQL

echo "✅ extensions ready"
