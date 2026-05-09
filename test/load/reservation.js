// k6 load test — reservation create flow.
//
// Run:
//   k6 run -e GATEWAY_URL=http://localhost:8080 test/load/reservation.js
//
// SLA target:
//   - p95 < 250ms
//   - error rate < 1%
//   - 200 RPS sustained selama 5 menit

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const errorRate = new Rate('errors');
const reservationLatency = new Trend('reservation_latency_ms');

export const options = {
  scenarios: {
    smoke: {
      executor: 'constant-vus',
      vus: 5,
      duration: '30s',
      tags: { phase: 'smoke' },
    },
    load: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 50 },
        { duration: '2m', target: 200 },
        { duration: '2m', target: 200 },
        { duration: '30s', target: 0 },
      ],
      startTime: '40s',
      tags: { phase: 'load' },
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<250'],
    http_req_failed: ['rate<0.01'],
    errors: ['rate<0.01'],
  },
};

const GATEWAY = __ENV.GATEWAY_URL || 'http://localhost:8080';

export default function () {
  const idem = uuidv4();
  const driver = `driver-load-${__VU}-${__ITER}`;
  const startAt = new Date(Date.now() + 60_000).toISOString();
  const endAt = new Date(Date.now() + 7200_000).toISOString();

  const payload = JSON.stringify({
    driver_id: driver,
    plate_no: `B ${__VU}${__ITER} TST`,
    vehicle_type: 'CAR',
    mode: 'SYSTEM',
    start_at: startAt,
    end_at: endAt,
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
      try { return JSON.parse(r.body).id !== undefined; }
      catch { return false; }
    },
  });
  if (!ok) errorRate.add(1);

  sleep(0.5);
}
