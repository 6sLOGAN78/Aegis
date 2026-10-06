---
phase: 03-control-plane-snapshot-streaming-durable-state
plan: 03
subsystem: gateway-ratelimit-revocation
tags: [redis, ratelimit, gcra, revocation, quarantine, fail-closed]

requires:
  - phase: 03-control-plane-snapshot-streaming-durable-state
    plan: 02
    provides: "Gateway dynamic snapshot manager and streaming client"
provides:
  - "Redis GCRA token-bucket distributed rate limiting per principal and per route with Retry-After calculation"
  - "Sub-5s Redis ephemeral JTI revocation and principal quarantine store with 200ms pipelined check"
  - "Strict fail-closed gateway boundary returning HTTP 503 (DEPENDENCY_OUTAGE_REDIS) on Redis partition, outage, or timeout"
affects:
  - "03-04 Pre-forward WAL disk spool and asynchronous PostgreSQL audit worker"
  - "04-operator-dashboard-policy-management-convergence"

tech-stack:
  added:
    - "github.com/go-redis/redis_rate/v10 v10.0.1"
    - "github.com/redis/go-redis/v9 v9.22.0"
    - "github.com/alicebob/miniredis/v2 v2.34.0"
  patterns:
    - "Pattern 3: Pipelined Redis Revocation with Rigid 200ms Deadline"
    - "Generic Cell Rate Algorithm (GCRA) token bucket rate limiting with Retry-After"
    - "Fail-Closed Gateway Boundary (HTTP 503) on Cache Outage"

key-files:
  created:
    - internal/ratelimit/limiter.go
    - internal/ratelimit/limiter_test.go
    - internal/revocation/store.go
    - internal/revocation/store_test.go
    - tests/failure/redis_outage_test.go
  modified:
    - cmd/gateway/main.go
    - internal/proxy/proxy.go

key-decisions:
  - "Enforced Generic Cell Rate Algorithm (GCRA) via go-redis/redis_rate/v10 executing atomic Lua script to calculate token bucket consumption and Retry-After durations (REV-01)"
  - "Pipelined JTI revocation (revocation:jti:<jti>) and principal quarantine (quarantine:principal:<id>) checks in a single roundtrip (<0.5ms) for sub-5-second cluster-wide revocation convergence (REV-03)"
  - "Bounded every Redis interaction on request hot path to a strict 200ms context deadline (REV-04)"
  - "Fails closed with HTTP 503 Service Unavailable (DEPENDENCY_OUTAGE_REDIS) on any Redis error, timeout, or partition; never failing open (Invariant 1, ADR-0005)"

patterns-established:
  - "Distributed Atomic GCRA Rate Limiting per Principal and Route"
  - "Pipelined Revocation and Quarantine Verification"
  - "200ms Deadline Fail-Closed Semantics on Shared Cache Failure"

requirements-completed:
  - REV-01
  - REV-03
  - REV-04

duration: 15min
completed: 2026-10-06
---

# Plan 03-03: Redis Atomic Token-Bucket Rate Limiting and <5s JTI Revocation / Principal Quarantine with Fail-Closed Semantics Summary

**Redis GCRA token-bucket rate limiter, ephemeral JTI revocation and principal quarantine store, 200ms context deadlines, and fail-closed HTTP 503 outage enforcement**

## Performance

- **Duration:** ~15 min
- **Tasks:** 3 completed
- **Files created/modified:** 7
- **Tests Passing:** 100% across all unit, failure, and security test suites (`go test -v -race ./...`)

## Accomplishments

- **Redis GCRA Token Bucket Distributed Rate Limiter (`internal/ratelimit/`)**:
  - Implemented `ratelimit.RateLimiter` utilizing `go-redis/redis_rate/v10` executing atomic GCRA Lua scripts in Redis (REV-01).
  - Enforced per-principal and per-route rate limiting key schemas (`ratelimit:{principal}:{route}`).
  - Calculated exact `Retry-After` headers on rate limit exhaustion.
  - Implemented comprehensive unit tests in `internal/ratelimit/limiter_test.go` with `miniredis/v2` verifying burst limits, positive `Retry-After`, principal isolation, and timeout error handling.

- **Ephemeral Redis JTI Revocation and Principal Quarantine Store (`internal/revocation/`)**:
  - Implemented `revocation.Store` supporting instantaneous JTI token revocation (`revocation:jti:{jti}`) and administrative principal quarantine (`quarantine:principal:{principalID}`) (REV-03).
  - Created pipelined `CheckRevocation` executing multi-key presence checks in a single Redis network roundtrip (<0.5ms latency).
  - Enforced rigid 200ms context deadline (`context.WithTimeout(ctx, 200*time.Millisecond)`) returning errors wrapped with `ErrRevocationDependency` (REV-04).
  - Implemented unit tests in `internal/revocation/store_test.go` verifying clean passes, revoked JTIs, quarantined principals, un-quarantine transitions, TTL expiration, and connection failure handling.

- **Gateway Ingress Pipeline Integration & Fail-Closed Outage Test (`cmd/gateway/main.go`, `tests/failure/redis_outage_test.go`)**:
  - Wired `revStore` into `gatewayHandler` and `workloadHandler`: immediately blocks revoked tokens (`TOKEN_REVOKED`) and quarantined principals (`PRINCIPAL_QUARANTINED`) with HTTP 403 Forbidden.
  - Wired `rateLimiter` into both handlers before OPA policy evaluation: emits HTTP 429 Too Many Requests with computed `Retry-After` header when limits are exceeded.
  - Enforced Invariant 1 (Default deny and fail closed): on Redis connection failure, partition, or >200ms timeout, the gateway immediately fails closed with HTTP 503 Service Unavailable (`DEPENDENCY_OUTAGE_REDIS`), emitting RFC 7807 problem details (`https://aegis.local/errors/dependency-unavailable`).
  - Added failure integration test `tests/failure/redis_outage_test.go` demonstrating:
    1. Normal authorized traffic reaches backends (HTTP 200).
    2. Quarantined principal is immediately rejected (HTTP 403), with zero upstream calls.
    3. Revoked JTI is immediately rejected (HTTP 403), with zero upstream calls.
    4. Burst rate limit flood returns HTTP 429 with `Retry-After` header.
    5. Severing Redis connection (`mr.Close()`) causes subsequent requests to fail closed with HTTP 503 within 200ms, and zero requests reach upstream backends during the outage.

## Task Commits

Each task was committed atomically and pushed to `main`:

1. **Task 1: Implement Redis GCRA token bucket rate limiter** - `4e82b13` (feat)
2. **Task 2: Implement Redis JTI revocation and principal quarantine store** - `186b3dc` (feat)
3. **Task 3: Integrate rate limiter and revocation store into gateway with fail-closed semantics** - `47d7207` (feat)

## Files Created/Modified

- `internal/ratelimit/limiter.go` - Redis GCRA token bucket rate limiter with 200ms deadline
- `internal/ratelimit/limiter_test.go` - Rate limiter unit tests using miniredis under race detector
- `internal/revocation/store.go` - Pipelined JTI revocation and principal quarantine store
- `internal/revocation/store_test.go` - Revocation and quarantine unit tests using miniredis
- `internal/proxy/proxy.go` - Nil transport safeguard for reverse proxy upstream dispatch
- `cmd/gateway/main.go` - Gateway pipeline wiring for revocation checks, rate limiting, and fail-closed handling
- `tests/failure/redis_outage_test.go` - Security chaos test verifying fail-closed HTTP 503 on Redis outage

## Decisions Made

- Utilized `go-redis/redis_rate/v10` implementing the Generic Cell Rate Algorithm (GCRA) rather than naive sliding window log counters, eliminating memory overhead and multi-roundtrip race conditions (REV-01, ADR-0005).
- Batched principal quarantine and token JTI revocation lookups into a single Redis pipeline to ensure sub-millisecond execution overhead on the gateway request hot path (REV-03).
- Bounded all Redis network operations to a strict 200ms context deadline (`context.WithTimeout(ctx, 200*time.Millisecond)`), preventing slow Redis sockets from exhausting gateway worker pools (REV-04).
- Strictly rejected fail-open fallback: any Redis connection error, timeout, or partition terminates the request immediately with HTTP 503 Service Unavailable (`DEPENDENCY_OUTAGE_REDIS`), upholding Invariant 1 and ADR-0005.

## Deviations from Plan

- Enhanced `internal/proxy/proxy.go` in `NewReverseProxyWithMTLS` to fallback to `http.DefaultTransport` when a nil transport pointer is supplied, preventing typed-nil runtime panics when dispatching requests during testing or default configurations.

## Next Phase Readiness

- Plan 03-03 establishes real-time distributed rate limiting and instantaneous token revocation.
- Plan 03-04 (Pre-forward WAL disk spool and asynchronous PostgreSQL audit worker) will complete Phase 3's durable audit requirements.

---
*Phase: 03-control-plane-snapshot-streaming-durable-state*
*Plan: 03*
*Completed: 2026-10-06*
