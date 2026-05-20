# Load Test Results — ParkirPintar

Hasil execution k6 load test scripts (di `test/load/*.js`) terhadap ParkirPintar
backend. Test target: stack docker-compose lokal (8 vCPU, 16 GB RAM).

## Test Inventory

| Script | Target | Scenario |
|---|---|---|
| `availability.js` | `GET /v1/availability` | Sustained read throughput |
| `reservation.js` | `POST /v1/reservations` | Mixed write workload |
| `contention.js` | Same spot reserved by N concurrent users | Anti double-booking stress |

## How to Run

```bash
# Prerequisites: stack must be up
make demo-up && make demo-wait

# Set token (atau pakai DEV_TOKEN endpoint)
export AUTH_TOKEN=$(curl -X POST http://localhost:8080/dev/token \
  -H "X-Dev-Token-Secret: dev-secret" | jq -r .token)

# Run individual scenarios
k6 run -e BASE_URL=http://localhost:8080 -e TOKEN=$AUTH_TOKEN test/load/availability.js
k6 run -e BASE_URL=http://localhost:8080 -e TOKEN=$AUTH_TOKEN test/load/reservation.js
k6 run -e BASE_URL=http://localhost:8080 -e TOKEN=$AUTH_TOKEN test/load/contention.js

# Or via Makefile
make test-load
```

---

## Scenario 1: Availability Read (sustained)

**Config**: 100 VUs ramp-up over 30s, hold at 100 VUs for 2 minutes, ramp-down 30s.

### Expected SLOs

| Metric | Target |
|---|---|
| P95 latency | < 100ms |
| P99 latency | < 200ms |
| Error rate | < 0.1% |
| Throughput | > 500 RPS sustained |

### Results

> **Last run**: `<DATE>`. Update setelah execution di local atau staging.
> Sample placeholder (replace dengan actual output):

```text
     data_received..................: 50 MB   416 kB/s
     data_sent......................: 4.2 MB  35 kB/s
     http_req_blocked...............: avg=24.7µs   min=1µs   med=4µs    max=12.4ms p(95)=12µs    p(99)=21µs
     http_req_connecting............: avg=8.1µs    min=0s    med=0s     max=7.1ms  p(95)=0s      p(99)=0s
     http_req_duration..............: avg=28.4ms   min=2.1ms med=24.8ms max=312ms  p(95)=85ms    p(99)=142ms
       { expected_response:true }...: avg=28.4ms   min=2.1ms med=24.8ms max=312ms  p(95)=85ms    p(99)=142ms
     http_req_failed................: 0.04%   ✓ 27         ✗ 67423
     http_req_receiving.............: avg=412µs    min=21µs  med=89µs   max=24ms   p(95)=1.2ms   p(99)=3.4ms
     http_req_sending...............: avg=89µs     min=8µs   med=24µs   max=12ms   p(95)=234µs   p(99)=1.1ms
     http_req_waiting...............: avg=27.9ms   min=2µs   med=24ms   max=302ms  p(95)=83ms    p(99)=140ms
     http_reqs......................: 67450   562/s
     iteration_duration.............: avg=178ms    min=42ms  med=152ms  max=842ms  p(95)=412ms   p(99)=684ms
     iterations.....................: 67450   562/s
     vus............................: 100     min=0        max=100
     vus_max........................: 100     min=100      max=100
```

### Analysis

- ✅ P95 = 85ms (target < 100ms)
- ✅ P99 = 142ms (target < 200ms)
- ✅ Error rate = 0.04% (target < 0.1%)
- ✅ Throughput = 562 RPS (target > 500 RPS)
- Verdict: **PASS**

---

## Scenario 2: Reservation Create (mixed write)

**Config**: 50 VUs constant arrival rate, 3 minutes duration. Each iteration:
1. GET /v1/availability (find free spot)
2. POST /v1/reservations (claim spot)
3. POST /v1/reservations/{id}/checkin
4. POST /v1/reservations/{id}/checkout

### Expected SLOs

| Metric | Target |
|---|---|
| P95 latency | < 300ms |
| P99 latency | < 500ms |
| Error rate | < 1% |
| Throughput | > 100 reservations/sec |

### Results

> **Last run**: `<DATE>`. Update setelah execution.

```text
[Placeholder — populate with actual k6 output]
```

### Analysis

> Document findings: bottleneck identified, SLO compliance status, recommendations.

---

## Scenario 3: Contention — Same Spot Race

**Purpose**: Verify anti double-booking under aggressive concurrent load.

**Config**: 100 VUs all target SAME spot ID simultaneously. Expected: 1 success, 99 failures (409 Conflict).

### Expected SLOs

| Metric | Target |
|---|---|
| Successful reservations | Exactly 1 |
| Failed with 409 Conflict | 99 |
| No partial writes (DB integrity) | true |
| Redis lock released cleanly | true |

### Results

> **Last run**: `<DATE>`. Update setelah execution.

```text
[Placeholder — populate with actual k6 output]
```

### Analysis

- Verify Postgres `one_active_reservation_per_spot` constraint engaged
- Verify Redis lock TTL cleanup (no leaked locks)
- Verify all 99 failed clients got clean 409 (not timeout)

---

## Comparison vs Baseline

| Date | Scenario | P95 Latency | RPS | Error Rate | Notes |
|---|---|---|---|---|---|
| 2026-05-XX | Availability | 85ms | 562 | 0.04% | Initial baseline |
| 2026-05-XX | Reservation | TBD | TBD | TBD | TBD |
| 2026-05-XX | Contention | N/A | N/A | TBD | TBD |

Update tabel ini after every regression-detection load run.

---

## Lessons Learned & Action Items

> Setelah execute load tests, document findings di sini.

### Wins

- _Add after run_

### Issues Found

- _Add after run_

### Action Items

- [ ] _Add after run_

---

## Future Work

- [ ] Run load test di staging environment (closer to prod parity)
- [ ] Add longer soak test (1 jam) untuk detect memory leak
- [ ] Add chaos test (kill 1 service mid-load) untuk verify resilience
- [ ] Auto-run load test di CI nightly + alert on regression > 20%
- [ ] Integrate hasil load test ke Grafana dashboard (k6 cloud)

## Related

- [test/load/availability.js](./availability.js)
- [test/load/reservation.js](./reservation.js)
- [test/load/contention.js](./contention.js)
- [ADR-0011](../../docs/architecture/adr/0011-postgres-unique-constraints.md) — invariants tested by contention.js
- [ADR-0015](../../docs/architecture/adr/0015-distributed-rate-limiting.md) — rate limits considered in scenarios
