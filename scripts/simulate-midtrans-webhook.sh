#!/usr/bin/env bash
# simulate-midtrans-webhook.sh
#
# Untuk demo: simulasi Midtrans push notification ke payment service.
# Generate signature_key sesuai algoritma resmi Midtrans:
#   sha512(order_id + status_code + gross_amount + server_key)
#
# Usage:
#   ./scripts/simulate-midtrans-webhook.sh <payment_id> [status]
#
# status: settlement (default) | capture | deny | expire | cancel | failure | pending
#
# Pre-req: openssl, curl, jq
set -euo pipefail

PAYMENT_ID="${1:?usage: $0 <payment_id> [status]}"
STATUS="${2:-settlement}"
GROSS="${GROSS_AMOUNT:-30000.00}"
SERVER_KEY="${MIDTRANS_SERVER_KEY:-SB-Mid-server-FAKE}"
GATEWAY="${GATEWAY_URL:-http://localhost:8080}"

# Status code per Midtrans convention.
case "$STATUS" in
  settlement|capture|pending) STATUS_CODE="200" ;;
  deny|cancel|failure|expire) STATUS_CODE="202" ;;
  *) echo "unknown status: $STATUS"; exit 1 ;;
esac

FRAUD="accept"
if [ "$STATUS" = "settlement" ] || [ "$STATUS" = "capture" ]; then
  FRAUD="${FRAUD_STATUS:-accept}"
fi

# Build signature: sha512(order_id + status_code + gross_amount + server_key)
RAW="${PAYMENT_ID}${STATUS_CODE}${GROSS}${SERVER_KEY}"
SIG=$(printf '%s' "$RAW" | openssl dgst -sha512 -hex | awk '{print $NF}')

PAYLOAD=$(cat <<EOF
{
  "transaction_status": "${STATUS}",
  "order_id": "${PAYMENT_ID}",
  "transaction_id": "midtrans-txn-$(date +%s)",
  "status_code": "${STATUS_CODE}",
  "gross_amount": "${GROSS}",
  "signature_key": "${SIG}",
  "fraud_status": "${FRAUD}",
  "payment_type": "qris"
}
EOF
)

echo "==> POST ${GATEWAY}/v1/payments/midtrans/notification"
echo "    Status:      ${STATUS}"
echo "    Status code: ${STATUS_CODE}"
echo "    Gross:       ${GROSS}"
echo "    Signature:   ${SIG:0:32}..."
echo ""

RESP=$(curl -s -w "\n[HTTP %{http_code}]" -X POST \
  "${GATEWAY}/v1/payments/midtrans/notification" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD")
echo "$RESP"

echo ""
echo "==> Cek payment status setelah webhook:"
curl -s "${GATEWAY}/v1/payments/${PAYMENT_ID}" | jq . 2>/dev/null || curl -s "${GATEWAY}/v1/payments/${PAYMENT_ID}"
