---
phase: 01-mvp-secure-vertical-slice
plan: 01
subsystem: proxy
tags: [go, reverse-proxy, path-validation, header-sanitization, concurrency-limiter, rfc7807]

requires: []
provides:
  - Go HTTP reverse proxy foundation with Go 1.20+ httputil.ReverseProxy.Rewrite hook
  - Strict zero-repair wire-path validator rejecting traversal sequences (.., %2f, %5c, //, %00) with HTTP 400
  - Ingress concurrency bounding semaphore returning HTTP 429 Too Many Requests
  - Ingress listener limits (16 KiB header block, 1 MiB body) and UUID v4 X-Request-ID correlation assignment
  - Ingress header scrubbing stripping client X-Aegis-*, Forwarded, and hop-by-hop headers
  - RFC 7807 problem details error responses
affects: [01-02-PLAN, 01-03-PLAN, 01-04-PLAN, phase-02]

tech-stack:
  added:
    - github.com/google/uuid@v1.6.0
    - github.com/stretchr/testify@v1.12.1
    - github.com/open-policy-agent/opa@v1.21.1
    - github.com/golang-jwt/jwt/v5@v5.3.1
    - github.com/go-chi/chi/v5@v5.3.2
  patterns:
    - httputil.ReverseProxy.Rewrite
    - zero-repair-path-validation
    - counting-semaphore-limiter
    - rfc7807-problem-details

key-files:
  created:
    - internal/config/config.go
    - internal/config/config_test.go
    - internal/proxy/errors.go
    - internal/proxy/limiter.go
    - internal/proxy/limiter_test.go
    - internal/proxy/validator.go
    - internal/proxy/validator_test.go
    - internal/proxy/proxy.go
    - internal/proxy/proxy_test.go
    - internal/proxy/server.go
    - tests/security/path_traversal_test.go
    - tests/security/header_spoofing_test.go
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "Reject malicious paths immediately on raw r.RequestURI wire bytes without calling path.Clean() to eliminate path desynchronization (Invariant 7)"
  - "Decouple inbound and outbound proxy requests via Go 1.20+ Rewrite hook, purging all client X-Aegis-* and Forwarded headers unconditionally (Invariant 8)"
  - "Bound unauthenticated ingress concurrency using buffered counting semaphore returning HTTP 429 with Retry-After: 1 (REV-02)"
  - "Enforce 16 KiB header and 1 MiB body limits at socket and middleware layers with UUID v4 correlation IDs (GW-01)"

patterns-established:
  - "Pattern 1: Go 1.20+ httputil.ReverseProxy with Rewrite hook for upstream dispatch"
  - "Pattern 2: Strict zero-repair raw wire-path validation before router or PDP evaluation"
  - "Pattern 5: Counting semaphore concurrency bounding at ingress"

requirements-completed:
  - GW-01
  - GW-02
  - GW-04
  - REV-02

duration: 15min
completed: 2026-10-06
---

# Phase 01 Plan 01: Go Gateway Reverse Proxy Summary

**Go HTTP reverse proxy foundation featuring strict zero-repair wire-path traversal rejection (HTTP 400), unauthenticated concurrency throttling (HTTP 429), listener limits (16 KiB headers, 1 MiB body), and Rewrite hook ingress header scrubbing.**

## Performance

- **Duration:** 15 min
- **Started:** 2026-10-06T13:55:00Z
- **Completed:** 2026-10-06T14:10:00Z
- **Tasks:** 3
- **Files modified:** 14

## Accomplishments

- Implemented runtime configuration loading with environment overrides (`AEGIS_PORT`, `AEGIS_MAX_CONCURRENT`, `AEGIS_ROUTES_PATH`) and RFC 7807 problem details serializer.
- Implemented unauthenticated ingress concurrency limiter using a buffered channel counting semaphore, returning HTTP 429 with `Retry-After: 1` when saturated.
- Implemented strict zero-repair raw wire-path validator on `r.RequestURI` rejecting `..`, `/./`, `%2e%2e`, `%2f`, `%5c`, `//`, `\x00`, `%00`, and missing leading `/` with HTTP 400 without invoking `path.Clean()`.
- Built negative security test suite verifying immediate rejection of path traversal vectors with zero downstream handler invocation.
- Implemented modern Go 1.20+ `httputil.ReverseProxy.Rewrite` hook decoupling inbound from outbound requests, forwarding exact canonical path bytes, scrubbing client `X-Aegis-*`, `Forwarded`, `X-Forwarded-*`, and `Authorization` headers, and injecting UUID v4 `X-Request-ID`.
- Enforced edge listener limits on `http.Server` (16 KiB headers, 1 MiB body) and verified under race detection.

## Task Commits

Each task was committed atomically:

1. **Task 1: Runtime Configuration, RFC 7807 Problem Details, and Unauthenticated Concurrency Limiter** - `55a3ebe` (feat)
2. **Task 2: Strict Zero-Repair Path Validator and Path Traversal Security Test Suite** - `0e49b4e` (feat)
3. **Task 3: Reverse Proxy Dispatcher with Rewrite Hook, Ingress Header Sanitization, and HTTP Gateway Server** - `7763372` (feat)

## Files Created/Modified

- `internal/config/config.go` - Gateway runtime configuration model and environment loader
- `internal/config/config_test.go` - Configuration unit tests with env overrides and invalid value handling
- `internal/proxy/errors.go` - RFC 7807 problem details struct and response serializer
- `internal/proxy/limiter.go` - Counting semaphore unauthenticated concurrency limiter middleware
- `internal/proxy/limiter_test.go` - Concurrency tests validating saturation 429, Retry-After header, and capacity release
- `internal/proxy/validator.go` - Strict zero-repair raw wire-path validator rejecting traversal sequences
- `internal/proxy/validator_test.go` - Unit test suite verifying sentinel errors across traversal and encoding variants
- `internal/proxy/proxy.go` - Reverse proxy with Go 1.20+ Rewrite hook, header scrubbing, and 502 error handler
- `internal/proxy/proxy_test.go` - Unit tests for listener limits (16 KiB header, 1 MiB body) and UUID v4 correlation ID
- `internal/proxy/server.go` - Server wrapper around http.Server with edge limits, concurrency bounds, and UUID assignment
- `tests/security/path_traversal_test.go` - Negative penetration tests for path traversal attacks over wire transport
- `tests/security/header_spoofing_test.go` - Security tests verifying stripping of client X-Aegis-* and Forwarded headers
- `go.mod` - Go module dependencies
- `go.sum` - Checksums for Go dependencies

## Decisions Made

- Enforced path validation directly on raw `r.RequestURI` before URL decoding or standard library router cleaning, preventing CWE-22 and CWE-444 parser impedance mismatch vulnerabilities.
- Applied Go 1.20+ `httputil.ProxyRequest.Rewrite` hook rather than legacy `Director`, ensuring full isolation between inbound and outbound request states.
- Bound unauthenticated ingress concurrency using counting semaphore channels before cryptographic verification, mitigating asymmetric compute exhaustion attacks.
- Enforced 16 KiB max header bytes and 1 MiB request body limits at both server and middleware levels.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] sync.Once in ConcurrencyLimiter test**
- **Found during:** Task 1 (`TestConcurrencyLimiter`)
- **Issue:** Test handler called `close(handlerRunning)` on a re-invoked handler during subsequent capacity verification, causing panic on closing an already-closed channel.
- **Fix:** Guarded `close(handlerRunning)` with `sync.Once`.
- **Files modified:** `internal/proxy/limiter_test.go`
- **Verification:** `go test -v -race ./internal/proxy/ -run TestConcurrencyLimiter` passed.
- **Committed in:** `55a3ebe` (Task 1 commit)

**2. [Rule 1 - Bug] Ingress header size calculation for MaxHeaderBytes**
- **Found during:** Task 3 (`TestListenerLimits`)
- **Issue:** Go stdlib `http.Server` adds 4096 bytes of buffer slop (`int64(maxHeaderBytes) + 4096`) before rejecting with 431 on raw wire reading, allowing headers between 16 KiB and 20 KiB to reach handlers.
- **Fix:** Added ingress header byte summation check in `server.go` middleware to strictly reject headers exceeding `cfg.MaxHeaderBytes` with HTTP 431.
- **Files modified:** `internal/proxy/server.go`
- **Verification:** `go test -v -race ./internal/proxy/ -run TestListenerLimits` passed.
- **Committed in:** `7763372` (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (both correctness bugs during testing)
**Impact on plan:** Zero scope creep; both fixes ensured strict adherence to security and test contracts.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Edge proxy, path validation, request bounding, and header scrubbing are in place.
- Ready for Plan 01-02: Deterministic Upstream Router and In-Memory OPA Authorization Engine (`policies/rego/authz.rego`).

---
*Phase: 01-mvp-secure-vertical-slice*
*Completed: 2026-10-06*
