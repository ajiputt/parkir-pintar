# ADR-0020: Test Doubles Strategy — Fakes-First with Mockgen Infrastructure

| Status     | Date       | Decider | Supersedes |
|------------|------------|---------|------------|
| Accepted   | 2026-05-20 | Aji P.  | —          |

## Context

Selama Phase C dari boilerplate alignment effort, kami evaluasi apakah migrate
existing **hand-rolled fakes** ke **gomock-generated mocks** (sesuai konvensi
boilerplate `go.uber.org/mock`).

Setelah analisis:
- ParkirPintar punya ~2,500 baris test code menggunakan fakes
- Fakes existing well-designed: thread-safe (sync.Mutex), error-injectable,
  stateful (map-based storage), production-parity (mirror Postgres constraints)
- Business workflows are stateful (reservation lifecycle: create → confirm →
  check-in → check-out)
- Concurrency safety is a system requirement (anti double-booking)

## Decision

**Adopt hybrid strategy**:

1. **Keep existing fakes** untuk semua test yang sudah menggunakan pattern ini
   (~12 test files, ~2,500 baris).
2. **Setup mockgen infrastructure** (`go.uber.org/mock` v0.6.0) untuk future use
   pada test scenarios yang stateless atau pure interaction verification.
3. **Generate mocks** untuk 28 interfaces ke `_mock/` folder per module sebagai
   ready-to-use scaffolding.

Konsekuensinya: existing tests tidak di-refactor, tapi infrastructure tersedia.

## Rationale

### 1. Both fakes dan mocks valid (Meszaros taxonomy)

Per Gerard Meszaros (*xUnit Test Patterns*), fake dan mock keduanya legitimate
test doubles. Pilihan kontextual, bukan one-size-fits-all.

### 2. Industry references mendukung mixed strategy

- **Martin Fowler** ("Mocks Aren't Stubs"): fakes excel untuk state-driven tests
- **Google Testing Blog** ("Don't Overuse Mocks"): mock-heavy tests brittle, prefer fakes
- **Vaughn Vernon** (DDD): aggregate tests favor in-memory fakes
- **Go stdlib**: `httptest.Server` adalah fake, bukan mock. Idiom Go favor fakes.

### 3. Specific wins fakes untuk ParkirPintar

| Aspect | Why fake wins |
|---|---|
| Stateful workflows | Reservation lifecycle natural sebagai state evolution |
| Concurrency safety | `sync.Mutex` mirrors production anti-double-booking |
| Production parity | Fake reproduces Postgres unique constraint behavior |
| Refactoring resilience | Method rename auto-propagate; mock EXPECT() fragile |
| Hexagonal alignment | Fake adalah test-context adapter, peer dengan Postgres adapter |

### 4. Wins mockgen tetap di-capture

Mockgen infrastructure di-set up untuk **future scenarios** dimana mocks fit:
- Pure stateless gRPC client tests
- External boundary verification (was logger called with this msg?)
- Quick scaffolding tests baru

Mixed strategy = right tool untuk right context.

### 5. Effort vs value

Full migration estimated 8-16 jam manual refactor. **No measurable quality
improvement** (coverage same, behavior same, maintainability potentially worse
untuk stateful tests). Effort di-direct ke higher-value work (E2E coverage,
documentation, observability).

## Consequences

### Positive

- Existing tests stable, refactoring resilient
- Boilerplate convention adopted (mockgen infrastructure available)
- Future tests bisa pakai gomock kalau fit konteks
- Hexagonal architecture preserved (fakes are test adapters)
- 8-16 jam effort di-save untuk higher-impact work

### Negative

- Codebase has 2 test double patterns (fakes + mocks-via-mockgen)
- Contributor onboarding perlu document kapan pakai yang mana
- Strict boilerplate adherence partially relaxed

### Mitigation untuk Negative

- Section ini di-link dari root README untuk diskoverability
- Pattern guideline:
  - **Use fakes** untuk usecase tests, stateful workflows, concurrent ops
  - **Use mockgen** untuk pure interaction tests, stateless boundaries

## Pattern Guidelines untuk Contributors

### Use **fakes** ketika:
- Testing business workflow yang melibatkan state evolution
- Concurrent operations (race conditions matter)
- Production behavior parity penting (constraints, invariants)
- Test scope = multiple method calls evolving state

### Use **mockgen** ketika:
- Testing single-method interaction
- Verifying call to external boundary (logger, metrics, tracer)
- Stateless contracts (single request → single response)
- New test scenarios yang tidak punya state evolution

## References

- Meszaros, G. (2007). *xUnit Test Patterns: Refactoring Test Code*.
- Fowler, M. (2007). "Mocks Aren't Stubs". https://martinfowler.com/articles/mocksArentStubs.html
- Google Testing Blog. "Testing on the Toilet: Don't Overuse Mocks".
- Vernon, V. (2013). *Implementing Domain-Driven Design*.
- Cockburn, A. "Hexagonal Architecture". https://alistair.cockburn.us/hexagonal-architecture/

## Related

- ADR-0011: Reservation aggregate + Postgres unique constraints
- ADR-0014: Overdue invoice pre-check + circuit breaker
- ADR-0018: Lint + quality gate strategy
- Sonar exclusion config: `sonar-project.properties` (excludes `**/_mock/**`)
