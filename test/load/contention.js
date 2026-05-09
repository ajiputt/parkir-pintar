// k6 — user-selected spot contention test.
// 100 VU race untuk reserve spot yang sama. Hanya 1 yang boleh berhasil per slot waktu.

import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const successCount = new Counter('reservation_success');
const conflictCount = new Counter('reservation_conflict');
const otherCount = new Counter('reservation_other');

export const options = {
  scenarios: {
    contention: {
      executor: 'shared-iterations',
      vus: 100,
      iterations: 100,
      maxDuration: '30s',
    },
  },
};

const GATEWAY = __ENV.GATEWAY_URL || 'http://localhost:8080';
const SPOT_ID = __ENV.SPOT_ID || ''; // wajib di-set oleh runner script

export default function () {
  if (!SPOT_ID) {
    throw new Error('SPOT_ID env var required');
  }
  const startAt = new Date(Date.now() + 60_000).toISOString();
  const endAt = new Date(Date.now() + 3600_000).toISOString();

  const payload = JSON.stringify({
    driver_id: `contention-${__VU}`,
    plate_no: `B ${__VU} XXX`,
    vehicle_type: 'CAR',
    mode: 'USER',
    spot_id: SPOT_ID,
    start_at: startAt,
    end_at: endAt,
  });

  const res = http.post(`${GATEWAY}/v1/reservations`, payload, {
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': uuidv4() },
  });

  if (res.status === 201 || res.status === 200) successCount.add(1);
  else if (res.status === 409 || res.status === 422) conflictCount.add(1);
  else otherCount.add(1);

  check(res, {
    'expected status': (r) => [200, 201, 409, 422].includes(r.status),
  });
}

export function handleSummary(data) {
  console.log(`
=== Contention Test Summary ===
  Success:  ${data.metrics.reservation_success?.values.count || 0}  (expected: 1)
  Conflict: ${data.metrics.reservation_conflict?.values.count || 0}  (expected: 99)
  Other:    ${data.metrics.reservation_other?.values.count || 0}     (expected: 0)
==============================`);
  return { stdout: '' };
}
