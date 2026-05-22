// k6 load test — availability (read-heavy).
//
// NOTE: /v1/availability dibatasi Light tier rate limit per ADR-0015
// (100 RPS per source IP). Test ini sengaja under-limit untuk measure
// actual system capacity tanpa hit rate limit.
//
// Untuk test rate limit defense (1000 RPS dari single IP), bump target
// ke 1000 dan expect 90% 429 responses — itu confirmed defense working.
//
// Run:
//   k6 run -e GATEWAY_URL=http://localhost:8080 test/load/availability.js
//
// SLA target (per-IP):
//   - p95 < 100ms
//   - error rate < 0.5%
//   - 80 RPS sustained (under 100 RPS rate limit safety margin)

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

const errorRate = new Rate('errors');

export const options = {
  scenarios: {
    sustained: {
      executor: 'ramping-arrival-rate',
      startRate: 20,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 100,
      stages: [
        { duration: '30s', target: 80 },    // Ramp ke 80 RPS (safely under 100 limit)
        { duration: '2m', target: 80 },     // Hold 80 RPS untuk 2 menit
        { duration: '30s', target: 0 },     // Ramp down
      ],
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<100'],
    errors: ['rate<0.005'],
  },
};

const GATEWAY = __ENV.GATEWAY_URL || 'http://localhost:8080';

export default function () {
  const res = http.get(`${GATEWAY}/v1/availability`);
  const ok = check(res, {
    '200 OK': (r) => r.status === 200,
    'has floors': (r) => r.body.includes('floors'),
  });
  if (!ok) errorRate.add(1);
  sleep(0.1);
}
