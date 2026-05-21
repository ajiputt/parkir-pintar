#!/usr/bin/env bash
# Demo end-to-end flow lewat curl. Pakai ini untuk live presentation.
#
# Cover scenarios:
#   - Get availability (public)
#   - Create reservation (system-assigned, MANUAL payment mode)
#   - Idempotency replay (same key → cached response)
#   - Check-in
#   - Check-out (trigger billing)
#   - Get invoice (lookup by reservation)
#   - Create payment QRIS (mock Midtrans)
#   - Webhook simulation
#
# Updated 2026-05-21:
#   - Add payment_mode field (required per ADR-0014)
#   - Remove deprecated end_at field (per ADR-0011)
#   - Use correct invoice endpoint path (per proto schema)

set -euo pipefail

GW="${GATEWAY_URL:-http://localhost:8080}"

# Verify jq tersedia — demo.sh pakai jq untuk parse + extract fields dari JSON.
# Install:
#   Windows: winget install jqlang.jq  OR  choco install jq
#   macOS:   brew install jq
#   Linux:   apt install jq  OR  yum install jq
if ! command -v jq >/dev/null 2>&1; then
  cat <<EOF >&2
ERROR: jq tidak tersedia di PATH.
demo.sh butuh jq untuk parse JSON response + extract reservation_id, dll.

Install:
  Windows: winget install jqlang.jq
  macOS:   brew install jq
  Linux:   sudo apt install jq

Atau skip demo dan run Go e2e tests aja:
  cd test && go test -tags=e2e -v -timeout=5m ./e2e/...

EOF
  exit 1
fi

# ----- Helper: pretty-print response, fail kalau status non-2xx -----
check_response() {
  local label="$1"
  local response="$2"
  if echo "$response" | jq -e 'has("code") and (.code | type == "number") and .code != 0' >/dev/null 2>&1; then
    echo "❌ $label gagal:"
    echo "$response" | jq .
    exit 1
  fi
  echo "$response" | jq .
}

echo "==> 1. Cek availability"
curl -s "$GW/v1/availability" | jq '{parking_area_name, total_car_available, total_motor_available, floors: (.floors | length)}'

echo ""
echo "==> 2. Buat reservation (SYSTEM-assigned, mobil, MANUAL payment)"
START=$(date -u -d "+1 minute" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -v+1M +"%Y-%m-%dT%H:%M:%SZ")
IDEM=$(uuidgen 2>/dev/null || python3 -c 'import uuid; print(uuid.uuid4())')

RES=$(curl -s -X POST "$GW/v1/reservations" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $IDEM" \
  -d "{
    \"driver_id\":\"demo-driver-1\",
    \"plate_no\":\"B 1234 ABC\",
    \"vehicle_type\":\"CAR\",
    \"mode\":\"SYSTEM\",
    \"start_at\":\"$START\",
    \"payment_mode\":\"MANUAL\"
  }")
check_response "Create reservation" "$RES"
RES_ID=$(echo "$RES" | jq -r '.reservation.id // .id')
echo "   Reservation ID: $RES_ID"

echo ""
echo "==> 3. Coba ulang dengan idempotency key yang sama (harus return cached, no double-book)"
REPLAY=$(curl -s -X POST "$GW/v1/reservations" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $IDEM" \
  -d "{
    \"driver_id\":\"demo-driver-1\",
    \"plate_no\":\"B 1234 ABC\",
    \"vehicle_type\":\"CAR\",
    \"mode\":\"SYSTEM\",
    \"start_at\":\"$START\",
    \"payment_mode\":\"MANUAL\"
  }")
REPLAY_ID=$(echo "$REPLAY" | jq -r '.reservation.id // .id')
if [ "$REPLAY_ID" = "$RES_ID" ]; then
  echo "   ✓ Replay returned same reservation_id ($REPLAY_ID) — idempotency works"
else
  echo "   ⚠️  Replay returned different ID — idempotency might be broken"
  echo "$REPLAY" | jq .
fi

echo ""
echo "==> 4. Check-in"
CHECKIN=$(curl -s -X POST "$GW/v1/reservations/$RES_ID:checkin" \
  -H "Content-Type: application/json" -d '{}')
check_response "Check-in" "$CHECKIN"

echo ""
echo "==> 5. Check-out (setelah 'parkir' 2 detik)"
sleep 2
CHECKOUT=$(curl -s -X POST "$GW/v1/reservations/$RES_ID:checkout" \
  -H "Content-Type: application/json" -d '{}')
check_response "Check-out" "$CHECKOUT"

echo ""
echo "==> 6. Lihat invoice (event-sourced dari NATS — wait 3s untuk event handler)"
sleep 3
INVOICE=$(curl -s "$GW/v1/reservations/$RES_ID/invoice")
if echo "$INVOICE" | jq -e '.id' >/dev/null 2>&1; then
  echo "$INVOICE" | jq '{id, status, total, items: (.items | length)}'
  INV_ID=$(echo "$INVOICE" | jq -r .id)
  echo "   Invoice ID: $INV_ID"
else
  echo "   ⚠️  Invoice belum ready — coba lagi nanti"
  echo "$INVOICE" | jq .
  INV_ID=""
fi

echo ""
echo "==> 7. Buat payment QRIS via Midtrans (mock mode default)"
if [ -n "$INV_ID" ] && [ "$INV_ID" != "null" ]; then
  PAYMENT_IDEM=$(uuidgen 2>/dev/null || python3 -c 'import uuid; print(uuid.uuid4())')
  PAYMENT=$(curl -s -X POST "$GW/v1/payments" \
    -H "Content-Type: application/json" \
    -H "Idempotency-Key: $PAYMENT_IDEM" \
    -d "{\"invoice_id\":\"$INV_ID\",\"method\":\"QRIS\"}")
  check_response "Create payment" "$PAYMENT"
  PAYMENT_ID=$(echo "$PAYMENT" | jq -r .id)
  echo "   Payment ID: $PAYMENT_ID"

  echo ""
  echo "==> 8. Get payment by ID"
  curl -s "$GW/v1/payments/$PAYMENT_ID" | jq '{id, invoice_id, status, amount}'

  echo ""
  echo "==> 9. Simulate Midtrans webhook (mock mode)"
  if [ -x "scripts/simulate-midtrans-webhook.sh" ]; then
    bash scripts/simulate-midtrans-webhook.sh "$INV_ID" settlement || \
      echo "   ⚠️  Webhook simulation skipped (script error)"
  else
    echo "   (manual: ./scripts/simulate-midtrans-webhook.sh $INV_ID settlement)"
  fi
else
  echo "   ⚠️  Skip payment — invoice ID missing"
fi

echo ""
echo "✅ Demo selesai. Explore lewat:"
echo "   - Swagger UI: $GW/docs"
echo "   - Grafana:    http://localhost:3000 (admin/admin)"
echo "   - Jaeger:     http://localhost:16686"
