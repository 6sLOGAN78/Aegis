---
phase: 03-control-plane-snapshot-streaming-durable-state
plan: 05
subsystem: fault-injection-resilience
tags: [docker-compose, hardened, failure-testing, redis-outage, lease-expiry, spool-saturation, fail-closed, chaos]

requires:
  - phase: 03-control-plane-snapshot-streaming-durable-state
    plan: 02
    provides: "Dynamic snapshot manager and signed freshness lease validation"
  - phase: 03-control-plane-snapshot-streaming-durable-state
    plan: 03
    provides: "Distributed rate limiting and token revocation store with fail-closed semantics"
  - phase: 03-control-plane-snapshot-streaming-durable-state
    plan: 04
    provides: "Local append-only disk WAL spool with 90% saturation gate and async audit worker"
provides:
  - "Hardened Docker Compose multi-service profile orchestrating full Phase 3 topology with zero published backend host ports"
  - "Makefile automation targets up-hardened, down-hardened, and test-failure"
  - "Automated fault injection integration test suite covering Redis outage fail-closed (REV-04)"
  - "Automated fault injection test suite covering snapshot freshness lease expiry fail-closed (CTRL-04)"
  - "Automated fault injection test suite covering 90% WAL spool saturation fail-closed (AUD-02)"
affects:
  - "04-operator-dashboard-policy-management-convergence"
  - "05-distributed-resilience-performance-slo"

tech-stack:
  added: []
  patterns:
    - "Hardened container orchestration profile (Profile 2) with zero host port exposures for private microservices"
    - "Comprehensive in-memory dependency fault injection and recovery testing under go test -race"
    - "100% fail-closed assertion verification under Redis downtime, severed gRPC leases, and saturated disk spools"

key-files:
  created:
    - deployments/compose/docker-compose.hardened.yml
    - tests/failure/failure_test.go
  modified:
    - deployments/compose/Dockerfile
    - cmd/control-plane/main.go
    - Makefile

key-decisions:
  - "Orchestrated Profile 2 Hardened deployment via deployments/compose/docker-compose.hardened.yml binding only edge ingress ports (:8080, :9443, :8084, :9090, :8085) with zero published host ports for orders, payments, admin"
  - "Added automatic embedded Goose migration execution in cmd/control-plane/main.go upon connecting to PostgreSQL to guarantee immediate schema readiness"
  - "Implemented unified failure test suite in tests/failure/failure_test.go testing all three mandatory fail-closed boundaries (REV-04, CTRL-04, AUD-02) with automatic recovery verification"

patterns-established:
  - "Profile 2 Hardened multi-service compose orchestration"
  - "Continuous race-detected fault injection and recovery integration testing"

requirements-completed:
  - CTRL-04
  - REV-04
  - AUD-02

duration: 15min
completed: 2026-10-06
---

# Plan 03-05: Fault Injection and Restart Tests for Dependency Failure Modes Summary

**Hardened Profile 2 Docker Compose environment and comprehensive automated dependency fault injection test suite verifying 100% fail-closed enforcement across Redis outages, snapshot lease expirations, and WAL spool saturations.**

## Performance

- **Duration:** ~15 min
- **Tasks:** 2 completed
- **Files created/modified:** 5
- **Tests Passing:** 100% across all unit, failure, integration, and security test suites (`go test -v -race ./...` and `opa test policies/rego policies/tests -v`)

## Accomplishments

- **Hardened Multi-Service Docker Compose Profile (`deployments/compose/docker-compose.hardened.yml`, `deployments/compose/Dockerfile`, `Makefile`)**:
  - Implemented `docker-compose.hardened.yml` orchestrating PostgreSQL 16-alpine with healthchecks and persistent storage, Redis 7.2-alpine with bounded memory and healthchecks, Aegis Control Plane on `:8084`/`:9090`, Aegis Gateway on `:8080`/`:9443`, Aegis Audit Worker daemon mounting shared WAL volume (`/var/log/aegis/wal`), Demo Issuer on `:8085`, and private backend services (`orders`, `payments`, `admin`) on the internal bridge network with zero published host ports (BYP-01).
  - Updated `deployments/compose/Dockerfile` to build and provide targets for `control-plane` and `audit-worker`.
  - Configured `cmd/control-plane/main.go` to automatically run embedded Goose migrations via `storage.RunMigrationsWithPool` on startup upon connecting to PostgreSQL.
  - Added Makefile targets `up-hardened`, `down-hardened`, and `test-failure` with comprehensive help documentation.
  - Validated syntax with `docker compose -f deployments/compose/docker-compose.hardened.yml config`.

- **Comprehensive Dependency Fault Injection Integration Test Suite (`tests/failure/failure_test.go`)**:
  - Implemented `TestFailure_RedisOutageFailClosed` (REV-04, Invariant 1):
    - Baseline request with valid JWT succeeds (HTTP 200) and reaches upstream mock backend.
    - Fault injection shuts down Redis (`mr.Close()`).
    - 10 concurrent requests all fail closed with **HTTP 503 Service Unavailable** (`type: "https://aegis.local/errors/dependency-unavailable"`, reason `DEPENDENCY_OUTAGE_REDIS`).
    - Upstream mock backend receives **0** requests during the outage (100% fail-closed, zero bypass).
    - Recovery restarts Redis (`mr.Restart()`); gateway resumes normal authorization (HTTP 200).
  - Implemented `TestFailure_SnapshotLeaseExpiryFailClosed` (CTRL-04, Invariant 1, Invariant 9):
    - Baseline request succeeds (HTTP 200) and readiness probe `/healthz/ready` returns 200 OK.
    - Fault injection stops lease renewals; during transient grace window ($\le 60$s), cached snapshot continues serving requests.
    - Once lease age exceeds 60 seconds, readiness drops to **HTTP 503** and all protected routes return **HTTP 503 Service Unavailable** (`type: "https://aegis.local/errors/policy-lease-expired"`).
    - Upstream backend receives **0** requests past the 60-second boundary.
    - Recovery records fresh lease renewal; readiness restores to 200 OK and protected requests succeed immediately.
  - Implemented `TestFailure_SpoolSaturationFailClosed` (AUD-02, Invariant 1, Invariant 10):
    - Baseline request appends pre-forward fsync WAL record and reaches upstream mock (HTTP 200).
    - Fault injection floods spool until $\ge 90\%$ capacity threshold.
    - Admission circuit breaker activates; subsequent request fails closed with **HTTP 503 Service Unavailable** (`type: "https://aegis.local/errors/spool-saturated"`).
    - Upstream mock receives **0** unlogged requests during saturation.
    - Recovery prunes archived WAL segments, bringing utilization below 90%; gateway resumes admitting traffic (HTTP 200).

## Task Commits

Each task was committed atomically and pushed to `main`:

1. **Task 1: Hardened multi-service compose environment and test make targets** - `6bbb215` (feat)
2. **Task 2: Comprehensive dependency fault injection and recovery integration test suite** - `b3e468e` (feat)

## Files Created/Modified

- `deployments/compose/docker-compose.hardened.yml` - Complete Phase 3 hardened service topology
- `deployments/compose/Dockerfile` - Added control-plane and audit-worker build targets
- `cmd/control-plane/main.go` - Added automatic Goose migration execution on database connection
- `Makefile` - Added `up-hardened`, `down-hardened`, and `test-failure` targets
- `tests/failure/failure_test.go` - Comprehensive dependency fault injection and recovery test suite

## Decisions Made

- Enforced Profile 2 Hardened deployment via `deployments/compose/docker-compose.hardened.yml` binding only ingress ports (`:8080`, `:9443`, `:8084`, `:9090`, `:8085`) and isolating backend services (`orders`, `payments`, `admin`) with zero published host ports to prevent direct bypass (BYP-01).
- Executed embedded Goose migrations on control-plane boot to guarantee that PostgreSQL schemas are automatically ready without manual operator intervention.
- Automated testing of Redis restart recovery via `miniredis.Restart()` on identical network addresses to prove fast reconnection recovery.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Added fmt import and exact ed25519 seed size in failure_test.go**
- **Found during:** Task 2 (Failure test suite execution)
- **Issue:** Missing `fmt` package in test imports and string-based seed length miscount caused runtime panic in `ed25519.NewKeyFromSeed`.
- **Fix:** Added `fmt` to imports and used `make([]byte, ed25519.SeedSize)` for guaranteed 32-byte seed allocation.
- **Files modified:** `tests/failure/failure_test.go`
- **Verification:** All tests passed cleanly under `go test -v -race ./tests/failure/...`.
- **Committed in:** `b3e468e` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 bug).
**Impact on plan:** None; standard test setup refinement.

## Next Phase Readiness

- Phase 3 is now 100% complete across all 5 plans (03-01 through 03-05).
- All Phase 3 requirements (CTRL-01..04, REV-01..04, AUD-01..03) are fully satisfied and verified under race detection.
- Ready for Phase 4: Operator Dashboard, Policy Management, and Live Convergence.

---
*Phase: 03-control-plane-snapshot-streaming-durable-state*
*Plan: 05*
*Completed: 2026-10-06*
