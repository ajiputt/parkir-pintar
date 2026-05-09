// k6 load test — availability (read-heavy).
// Target: p95 < 100ms, 1000 RPS sustained.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

const errorRate = new Rate('errors');

export const options = {
  scenarios: {
    spike: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      maxVUs: 500,
      stages: [
        { duration: '30s', target: 1000 },
        { duration: '2m', target: 1000 },
        { duration: '30s', target: 0 },
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
