// k6 load test — reservation create flow (write-heavy).
//
// NOTE: POST /v1/reservations dibatasi Heavy tier rate limit per ADR-0015
// (5 RPS per source IP, burst 10). Test ini sengaja under-limit untuk
// measure actual system capacity tanpa hit rate limit.
//
// Untuk test rate limit defense (200+ RPS dari single IP)
//
// Run:
//   k6 run -e GATEWAY_URL=http://localhost:8080 test/load/reservation.js
//
// SLA target (per-IP):
//   - p95 < 250ms
//   - error rate < 5% (slightly higher than read karena Postgres/Redis/NATS write path)
//   - ~3 RPS sustained (under 5 RPS rate limit safety margin)

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const errorRate = new Rate('errors');
const reservationLatency = new Trend('reservation_latency_ms');

// Test duration tuning:
// Seed = 150 CAR spots (5 floors × 30 mobil per ADR-0011 use case).
// 3 RPS × 45 sec = 135 reservations < 150 spots = safe buffer.
// Kalau test lebih lama, inventory akan exhausted dan threshold gagal
// karena ErrSpotUnavailable (correct behavior, tapi mengaburkan signal latency).
//
// Note: stack perlu reset/cleanup antar test runs supaya spot fresh.
//   docker exec parkirpintar-postgres-1 psql -U parkir -d parkirpintar -c "
//     UPDATE reservation.reservation SET state='CANCELLED' WHERE driver_id LIKE 'driver-load-%';
//     UPDATE reservation.spot SET status='AVAILABLE', version=version+1 WHERE status='HELD';
//   "
export const options = {
  scenarios: {
    load: {
      executor: 'constant-arrival-rate',
      rate: 3,                       // 3 reservations per second (under Heavy 5 RPS)
      timeUnit: '1s',
      duration: '45s',               // 135 reservations < 150 CAR inventory
      preAllocatedVUs: 5,
      maxVUs: 15,
      tags: { phase: 'load' },
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<250'],
    http_req_failed: ['rate<0.05'],
    errors: ['rate<0.05'],
  },
};

const GATEWAY = __ENV.GATEWAY_URL || 'http://localhost:8080';

export default function () {
  const idem = uuidv4();
  // Unique driver per VU+iteration biar tidak hit ErrDriverHasActiveReservation
  // (1-driver-1-active-reservation invariant per ADR-0011).
  const driver = `driver-load-${__VU}-${__ITER}-${Date.now()}`;
  const startAt = new Date(Date.now() + 60_000).toISOString();

  const payload = JSON.stringify({
    driver_id: driver,
    plate_no: `B ${__VU}${__ITER} TST`,
    vehicle_type: 'CAR',
    mode: 'SYSTEM',
    start_at: startAt,
    payment_mode: 'MANUAL',   // Required per ADR-0014
    // end_at deprecated per ADR-0011 (driver gak tau kapan keluar)
  });

  const start = Date.now();
  const res = http.post(`${GATEWAY}/v1/reservations`, payload, {
    headers: {
      'Content-Type': 'application/json',
      'Idempotency-Key': idem,
    },
    timeout: '10s',
  });
  const latency = Date.now() - start;
  reservationLatency.add(latency);

  const ok = check(res, {
    'status is 2xx': (r) => r.status >= 200 && r.status < 300,
    'has reservation id': (r) => {
      try {
        const body = JSON.parse(r.body);
        // CreateReservationResponse shape: { reservation: { id: ... }, booking_fee: ... }
        return body.reservation && body.reservation.id !== undefined;
      } catch { return false; }
    },
  });
  if (!ok) errorRate.add(1);

  // No artificial sleep — arrival-rate executor controls rate independently.
}
