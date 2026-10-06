---
phase: 02-workload-identity-enforced-bypass-prevention
plan: 03
subsystem: auth
tags: [mtls, spiffe, ed25519, docker-compose, bypass-prevention, defense-in-depth, middleware]

requires:
  - phase: 02-workload-identity-enforced-bypass-prevention
    provides: "Ed25519 assertion token minter & verifier (02-02) and upstream mTLS transport"
provides:
  - "Reusable microservice defense-in-depth middleware verifying peer SPIFFE identity and Ed25519 assertions"
  - "Private microservices (orders, payments, admin) migrated to HTTPS mTLS with health check exemptions"
  - "Local development certificate generation CLI utility (scripts/certificates/main.go)"
  - "Hardened Docker Compose MVP deployment profile enforcing zero published backend host ports"
affects:
  - "02-04 integration testing and end-to-end container bypass validation"

tech-stack:
  added: []
  patterns:
    - "Dual-layer defense-in-depth middleware (Transport SPIFFE verification + Application assertion signature validation)"
    - "Zero published host ports container bridge isolation"
    - "Automated local PKI generation with SPIFFE URI SANs"

key-files:
  created:
    - services/middleware/auth_middleware.go
    - services/middleware/auth_middleware_test.go
    - scripts/certificates/main.go
  modified:
    - cmd/services/orders/main.go
    - cmd/services/orders/main_test.go
    - cmd/services/payments/main.go
    - cmd/services/payments/main_test.go
    - cmd/services/admin/main.go
    - cmd/services/admin/main_test.go
    - cmd/gateway/main.go
    - deployments/compose/Dockerfile
    - deployments/compose/docker-compose.mvp.yml

key-decisions:
  - "Configured BackendAuthMiddleware with RFC 7807 problem details responses for 401 Unauthorized and 403 Forbidden errors"
  - "Exempted /health liveness endpoint from mTLS client cert and assertion checks across all backend services"
  - "Implemented dual fallback for gateway assertion public key loading (file path, environment base64, deterministic demo seed)"
  - "Added curl to runtime container stages to empower container-exec lateral network bypass security tests"
  - "Configured zero published host ports for orders, payments, and admin in Docker Compose, restricting ingress solely through Aegis Gateway ports 8080 and 9443"

patterns-established:
  - "Pattern 4: BackendAuthMiddleware wrapping microservice handlers with transport & assertion claims injection"
  - "Pattern 7: Private backend microservice mTLS server bootstrap with ClientAuth RequireAndVerifyClientCert"

requirements-completed:
  - BYP-02
  - BYP-03

duration: 12min
completed: 2026-10-06
---

# Plan 02-03: Reusable Backend Authentication Middleware and Docker Network Isolation Summary

**Reusable dual-layer defense-in-depth authentication middleware, HTTPS mTLS microservice server migration, automated PKI generation, and Docker bridge network isolation with zero published host ports**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-10-06T17:25:34Z
- **Completed:** 2026-10-06T17:37:00Z
- **Tasks:** 3 completed
- **Files created/modified:** 11

## Accomplishments

- **Reusable Defense-in-Depth Middleware (`services/middleware/auth_middleware.go`)**:
  - Validates transport identity (peer client certificate presents Gateway SPIFFE ID `spiffe://aegis.local/ns/gateway/sa/aegis-gateway`). Peer microservices or direct connections without gateway client cert fail with HTTP 403 Forbidden or HTTP 401 Unauthorized.
  - Validates application assertion (`X-Aegis-Assertion`) against expected service ID audience, HTTP method, and canonical request path with Ed25519 signature verification.
  - Formats all rejection responses according to RFC 7807 problem details (`application/problem+json`).
  - Provides configurable path exemptions preserving unauthenticated `/health` container liveness probes.
  - Verified across 8 test scenarios under race detection (`go test -v -race ./services/middleware/ -run TestAuthMiddleware`).

- **Microservice HTTPS mTLS Migration (`orders`, `payments`, `admin`)**:
  - Upgraded demo services to listen on HTTPS with `tls.RequireAndVerifyClientCert` against Root CA when certificates are provided.
  - Integrated `BackendAuthMiddleware` protecting `/api/*` endpoints with respective audiences (`orders`, `payments`, `admin`).
  - Added robust assertion public key resolution supporting file path (`GATEWAY_ASSERTION_PUBKEY_PATH`), base64 string (`GATEWAY_ASSERTION_PUBKEY`), and deterministic fallback seed.
  - Verified across all unit test suites under race detection (`go test -v -race ./cmd/services/...`).

- **Development PKI CLI & Hardened Docker Compose (`scripts/certificates/main.go`, `docker-compose.mvp.yml`, `Dockerfile`)**:
  - Implemented standalone Go PKI generator generating Root CA, gateway server cert, gateway client cert (`spiffe://aegis.local/ns/gateway/sa/aegis-gateway`), orders workload cert (`spiffe://aegis.local/workload/orders`), backend server certs, and Ed25519 assertion key pairs into `deployments/certs/`.
  - Added `curl` to container images to enable lateral bypass testing.
  - Hardened Docker Compose configuration: Gateway binds ports `8080:8080` (user) and `9443:9443` (workload); microservices have ZERO published host ports (`ports:` omitted, only private `expose:`).
  - Validated syntax with `docker compose -f deployments/compose/docker-compose.mvp.yml config`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Reusable Microservice Defense-in-Depth Authentication Middleware** - `d921251` (feat)
2. **Task 2: Microservice HTTPS Server Migration & Middleware Integration** - `c92b8f8` (feat)
3. **Task 3: Development Certificate Generation Script and Docker Compose Hardening** - `e2fe592` (feat)

## Files Created/Modified

- `services/middleware/auth_middleware.go` - Reusable transport SPIFFE & assertion verification middleware
- `services/middleware/auth_middleware_test.go` - Test suite covering peer cert verification, assertion validation, path exemptions
- `cmd/services/orders/main.go` - Orders service with HTTPS mTLS and `BackendAuthMiddleware`
- `cmd/services/orders/main_test.go` - Orders service unit tests verifying credentials and 401 rejections
- `cmd/services/payments/main.go` - Payments service with HTTPS mTLS and `BackendAuthMiddleware`
- `cmd/services/payments/main_test.go` - Payments service unit tests
- `cmd/services/admin/main.go` - Admin service with HTTPS mTLS and `BackendAuthMiddleware`
- `cmd/services/admin/main_test.go` - Admin service unit tests
- `cmd/gateway/main.go` - Added base64 file parsing support for `AEGIS_ASSERTION_PRIVATE_KEY_PATH`
- `scripts/certificates/main.go` - Standalone development PKI generator CLI
- `deployments/compose/Dockerfile` - Multi-stage build with curl and dual port exposure
- `deployments/compose/docker-compose.mvp.yml` - Hardened compose configuration with zero backend host ports

## Decisions Made

- Standardized on RFC 7807 problem details (`application/problem+json`) with error type URLs `https://aegis.local/errors/unauthorized` and `https://aegis.local/errors/forbidden`.
- Made `Routes` functions in all microservices variadic (`Routes(assertionPubKeys ...ed25519.PublicKey)`) to allow tests to inject ephemeral test keys while defaulting cleanly in production to environment or demo keys.
- Supported both raw bytes, PEM, and base64 string decoding when reading assertion key files.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - running `go run ./scripts/certificates/main.go` automatically generates all local development PKI assets into `deployments/certs/`.

## Next Phase Readiness

- Backend defense-in-depth middleware and Docker network isolation are fully operational.
- Ready for Plan 02-04 (End-to-End Bypass Prevention & Integration Test Suite).

---
*Phase: 02-workload-identity-enforced-bypass-prevention*
*Completed: 2026-10-06*
