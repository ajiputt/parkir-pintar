#!/usr/bin/env bash
# Wait sampai gateway responsive.
set -euo pipefail

GATEWAY="${GATEWAY_URL:-http://localhost:8080}"
TIMEOUT=60
elapsed=0

echo "Menunggu gateway healthy di $GATEWAY..."
until curl -sf "$GATEWAY/healthz" > /dev/null 2>&1; do
  if [ "$elapsed" -ge "$TIMEOUT" ]; then
    echo "❌ Timeout setelah ${TIMEOUT}s"
    exit 1
  fi
  sleep 2
  elapsed=$((elapsed + 2))
  printf "."
done
echo ""
echo "✅ Gateway ready (${elapsed}s)"

echo "Menunggu reservation availability..."
until curl -sf "$GATEWAY/v1/availability" > /dev/null 2>&1; do
  sleep 2
  elapsed=$((elapsed + 2))
  if [ "$elapsed" -ge "$TIMEOUT" ]; then
    echo "❌ Reservation belum siap"
    exit 1
  fi
done
echo "✅ Stack siap dipakai"
