---
phase: 01-mvp-secure-vertical-slice
plan: 03
subsystem: identity-and-routing
tags: [go, jwt, ed25519, rfc-8725, upstream-router, docker-compose, bypass-prevention, zero-trust]

requires:
  - phase: 01-mvp-secure-vertical-slice
    provides: Gateway reverse proxy, zero-repair path validator, and in-memory OPA PDP (01-01, 01-02)
provides:
  - RFC 8725 compliant JWT validator with pinned asymmetric algorithm allowlist (EdDSA, RS256, ES256) and 30s clock skew tolerance
  - Negative penetration test suite verifying rejection of alg:none, HMAC key confusion, forged signatures, expired tokens, and invalid claims
  - Development demo JWT issuer with seeded accounts (developer, finance, admin) and in-memory IP-based login rate throttling (20 req/min)
  - Three private demo backend microservices (orders :8081, payments :8082, admin :8083) returning deterministic business payloads
  - Deterministic upstream route matcher ignoring client Host headers and mapping strictly on (HTTP method, canonical path)
  - Primary gateway binary entrypoint assembling edge limits, concurrency bounds, zero-repair path validation, JWT verification, route matching, OPA policy checks, and reverse proxy dispatch
  - Multi-stage Dockerfile and Docker Compose MVP profile enforcing network isolation with 0 published host ports for private backend services
  - Automated integration test suite verifying backend bypass prevention, host port unavailability, and authorized gateway routing
affects: [01-04-PLAN, phase-02]

tech-stack:
  added:
    - github.com/golang-jwt/jwt/v5@v5.3.1
  patterns:
    - cryptographic-jwt-authenticator
    - demo-jwt-issuer-with-throttling
    - deterministic-route-matcher
    - multi-stage-container-build
    - network-isolation-bypass-prevention

key-files:
  created:
    - internal/identity/jwt.go
    - internal/identity/jwt_test.go
    - tests/security/jwt_negative_test.go
    - cmd/demo-issuer/main.go
    - cmd/demo-issuer/main_test.go
    - cmd/services/orders/main.go
    - cmd/services/orders/main_test.go
    - cmd/services/payments/main.go
    - cmd/services/payments/main_test.go
    - cmd/services/admin/main.go
    - cmd/services/admin/main_test.go
    - internal/proxy/router.go
    - internal/proxy/router_test.go
    - cmd/gateway/main.go
    - deployments/compose/Dockerfile
    - deployments/compose/docker-compose.mvp.yml
    - tests/integration/bypass_test.go
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "Enforce pinned asymmetric algorithm allowlist (EdDSA, RS256, ES256) and reject alg:none and symmetric key substitution attacks (RFC 8725, AUTH-01)"
  - "Configure 30-second clock skew tolerance on JWT expiration and not-before claims to prevent false rejections across distributed nodes (AUTH-01)"
  - "Deterministic route table lookup maps requests strictly by (method, canonicalPath), ignoring client Host headers to defeat SSRF and host spoofing (GW-03, Invariant 6)"
  - "Private microservices (orders, payments, admin) publish zero host ports in Docker Compose, residing strictly on aegis-internal bridge network (BYP-01)"
  - "Demo JWT issuer enforces IP-based rate throttling at 20 attempts/minute to prevent brute-force credential exhaustion (AUTH-04)"

patterns-established:
  - "Pattern 4: Cryptographic JWT Authenticator with RFC 8725 compliance"
  - "Pattern 7: Deterministic Route Table Matcher ignoring client Host"
  - "Pattern 8: Local Demo JWT Issuer with IP-based Login Throttling"
  - "Pattern 9: Private Microservices with Zero Published Host Ports"

requirements-completed:
  - AUTH-01
  - AUTH-04
  - GW-03
  - BYP-01

duration: 15min
completed: 2026-10-06
---

# Phase 01 Plan 03: Mock JWT Issuer, Private Demo Microservices, Route Resolution, and Docker Compose MVP Profile Summary

**Complete integration of cryptographic JWT validation, demo JWT issuer with login throttling, 3 private microservices, deterministic upstream routing, gateway CLI bootstrap, and Docker Compose network isolation verifying zero backend bypass.**

## Performance

- **Duration:** 15 min
- **Started:** 2026-10-06T14:21:16Z
- **Completed:** 2026-10-06T14:35:00Z
- **Tasks:** 4
- **Files modified:** 19

## Accomplishments

- Implemented RFC 8725 compliant JWT validator (`internal/identity/jwt.go`) enforcing pinned asymmetric algorithm allowlists (`EdDSA`, `RS256`, `ES256`), 30-second clock skew leeway, non-empty subject claims, and returning explicit sentinel errors.
- Created negative security test suite (`tests/security/jwt_negative_test.go`) verifying rejection of `alg: none`, HMAC key confusion attacks (CVE-2015-9235), forged signature bytes, expired tokens (>30s), untrusted issuers, and unauthorized audiences.
- Built development demo JWT issuer (`cmd/demo-issuer/main.go`) with seeded credentials for `developer`, `finance`, and `application-admin`, minting 5-minute Ed25519 tokens, exposing `GET /public-key`, and enforcing 20 req/min login rate throttling.
- Implemented three private Go microservices (`orders` on `:8081`, `payments` on `:8082`, `admin` on `:8083`) returning deterministic JSON payloads with dedicated unit test suites.
- Built deterministic upstream router (`internal/proxy/router.go`) matching strictly on `(method, canonicalPath)` from `routes.json` while completely ignoring client `Host` headers (Invariant 6).
- Assembled primary gateway binary entrypoint (`cmd/gateway/main.go`) executing the full zero-trust pipeline: edge limits & UUID -> concurrency bounds -> zero-repair path validation -> Bearer JWT validation -> deterministic route resolution -> in-memory OPA authorization -> reverse proxy dispatch with header scrubbing and graceful shutdown.
- Created multi-stage Dockerfile (`deployments/compose/Dockerfile`) and Docker Compose MVP profile (`deployments/compose/docker-compose.mvp.yml`) exposing only the gateway (`:8080`) and demo issuer (`:8085`), with ZERO published host ports for private microservices (`orders`, `payments`, `admin`).
- Built automated integration test suite (`tests/integration/bypass_test.go`) validating that backend services are unreachable from host ports directly (`localhost:8081..8083` fail immediately), while authorized calls routed via gateway port `:8080` succeed with HTTP 200.

## Task Commits

Each task was committed atomically:

1. **Task 1: Cryptographic JWT Authenticator with Pinned Algorithm Allowlist and Negative Security Test Suite** - `2896d9a` (feat)
2. **Task 2: Demo JWT Issuer with Login Rate Throttling and Private Microservices** - `25ee5d0` (feat)
3. **Task 3: Deterministic Upstream Route Matcher, Gateway CLI Bootstrap, and Docker Compose MVP Profile** - `3547b83` (feat)
4. **Task 4: Backend Bypass Prevention and Network Isolation Verification** - `0159813` (feat)

## Files Created/Modified

- `internal/identity/jwt.go` - RFC 8725 JWT validator with algorithm allowlist and clock skew leeway
- `internal/identity/jwt_test.go` - Unit tests for valid Ed25519 token parsing and clock skew tolerance
- `tests/security/jwt_negative_test.go` - Penetration tests for alg:none, HMAC confusion, tampered signatures
- `cmd/demo-issuer/main.go` - Mock JWT issuer with seeded accounts and IP-based rate throttling
- `cmd/demo-issuer/main_test.go` - Unit tests for token minting, role rejection, and rate limit triggers
- `cmd/services/orders/main.go` & `main_test.go` - Private orders microservice and tests
- `cmd/services/payments/main.go` & `main_test.go` - Private payments microservice and tests
- `cmd/services/admin/main.go` & `main_test.go` - Private admin microservice and tests
- `internal/proxy/router.go` - Deterministic route table matcher ignoring client Host header
- `internal/proxy/router_test.go` - Unit tests for route catalog matching and unknown route rejection
- `cmd/gateway/main.go` - Primary Aegis gateway binary entrypoint with full request pipeline
- `deployments/compose/Dockerfile` - Multi-stage build targeting gateway, demo issuer, and services
- `deployments/compose/docker-compose.mvp.yml` - MVP profile with internal bridge network and isolated backends
- `tests/integration/bypass_test.go` - Integration test verifying 0 published host ports and gateway routing
- `go.mod` & `go.sum` - Added `golang-jwt/jwt/v5` dependency

## Decisions Made

- Enforced asymmetric key algorithm allowlist (`EdDSA`, `RS256`, `ES256`) to completely eliminate symmetric key substitution vulnerabilities (CVE-2015-9235).
- Configured 30-second leeway in JWT parsing to account for clock skew across independent Docker containers.
- Omitted all host port publishing for backend microservices in Docker Compose to strictly prevent direct bypass and enforce that all requests must traverse the Aegis Gateway security perimeter (BYP-01).
- Applied environment-based upstream scheme override (`AEGIS_UPSTREAM_SCHEME=http`) in Docker Compose MVP profile to allow connecting to plain HTTP demo microservices while keeping static `routes.json` prepared for mTLS in Phase 2.

## Deviations from Plan

None - plan executed strictly as specified.

## Issues Encountered

None. All automated unit, security, and integration tests passed cleanly with race detection enabled.

## User Setup Required

None - no external credentials or manual setup required.

## Next Phase Readiness

- The complete gateway data plane pipeline and private microservices are running and tested in Docker Compose.
- Ready for Plan 01-04: Structured Completion Auditing and End-to-End Integration Test Suite.

---
*Phase: 01-mvp-secure-vertical-slice*
*Completed: 2026-10-06*
