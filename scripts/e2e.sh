#!/usr/bin/env bash
# Run end-to-end tests against running docker-compose stack.
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"

echo "==> Verifying gateway is up..."
./scripts/wait-healthy.sh

echo ""
echo "==> Running Go e2e tests..."
cd test
go test -tags=e2e -v -timeout=5m ./e2e/...

echo ""
echo "==> Running shell scenario walkthrough..."
cd ..
./scripts/demo.sh

echo ""
echo "✅ E2E test selesai"
