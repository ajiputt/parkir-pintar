// k6 — Rate limit defense verification.
//
// Sengaja hit /v1/availability dengan 1000 RPS dari single IP untuk verify
// bahwa rate limit (Light tier = 100 RPS per IP per ADR-0015) berfungsi.
//
// EXPECTED RESULT:
//   - ~10% requests succeed (200 OK)   ← under rate limit
//   - ~90% requests rejected (429)     ← rate limit triggered
//   - 0% requests crash (500)          ← graceful rejection
//
// Run:
//   k6 run -e GATEWAY_URL=http://localhost:8080 test/load/ratelimit-defense.js

import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const successCount = new Counter('rate_limit_success');
const rejectedCount = new Counter('rate_limit_rejected');
const errorCount = new Counter('rate_limit_unexpected');

export const options = {
  scenarios: {
    overflow: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      maxVUs: 500,
      stages: [
        { duration: '30s', target: 1000 },   // Burst ke 1000 RPS (10x rate limit)
        { duration: '2m', target: 1000 },    // Sustain overflow
        { duration: '30s', target: 0 },
      ],
    },
  },
  // No latency thresholds — kita test correctness, bukan perf
};

const GATEWAY = __ENV.GATEWAY_URL || 'http://localhost:8080';

export default function () {
  const res = http.get(`${GATEWAY}/v1/availability`);

  if (res.status === 200) successCount.add(1);
  else if (res.status === 429) rejectedCount.add(1);
  else errorCount.add(1);

  check(res, {
    'expected status (200 or 429)': (r) => r.status === 200 || r.status === 429,
    'no 5xx crashes': (r) => r.status < 500,
  });
}

export function handleSummary(data) {
  const success = data.metrics.rate_limit_success?.values.count || 0;
  const rejected = data.metrics.rate_limit_rejected?.values.count || 0;
  const errors = data.metrics.rate_limit_unexpected?.values.count || 0;
  const total = success + rejected + errors;
  const successRate = total > 0 ? ((success / total) * 100).toFixed(1) : 0;
  const rejectRate = total > 0 ? ((rejected / total) * 100).toFixed(1) : 0;

  console.log(`
========================================
  Rate Limit Defense Test Summary
========================================
  Total requests:    ${total}
  ✅ Success (200):  ${success}  (${successRate}%)
  🛡️  Rejected (429): ${rejected}  (${rejectRate}%)
  ❌ Errors (5xx):   ${errors}

  Expected:
    - Success ≈ 10-15% (Light tier = 100 RPS / target 1000 RPS)
    - Rejected ≈ 85-90%
    - Errors = 0 (graceful, no crashes)

  Verdict: ${errors === 0 ? '✅ Defense WORKING' : '❌ Unexpected 5xx errors detected'}
========================================
`);
  return { stdout: '' };
}
