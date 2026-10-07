---
phase: 04-operator-experience-telemetry
plan: 01
subsystem: api
tags: [chi, rbac, csrf, etag, idempotency, rego, simulation, quarantine, audit, postgres, pgx]

# Dependency graph
requires:
  - phase: 02-control-plane-core
    provides: gRPC snapshot distribution server, AckTracker, RollbackEngine, and Validator
  - phase: 03-revocation-wal-spooling
    provides: Redis revocation store for quarantine and token JTI revocation
provides:
  - Control plane REST management APIs (/control/v1) on port :8084 using Chi router
  - SessionManager with HttpOnly/SameSite=Strict cookie issuance and CSRF protection
  - Optimistic concurrency control via ETag and If-Match headers returning 412 Precondition Failed
  - Idempotency deduplication store with 409 Conflict locking and 24h response caching
  - Granular RBAC middleware enforcing sec-ops, auditor, and viewer roles with Invariant 11 data-plane token rejection
  - In-memory OPA Rego candidate policy simulator (<2ms)
  - Monotonic snapshot publication and N+1 rollback handlers
  - Emergency principal quarantine and JTI revocation handlers (<5s SLA)
  - Partitioned PostgreSQL audit querying with default 24h bounding and cursor pagination
affects: [04-02-PLAN, 04-03-PLAN, web-dashboard, integration-tests]

# Tech tracking
tech-stack:
  added: [github.com/go-chi/chi/v5]
  patterns: [Double-submit CSRF protection, RFC 7232 optimistic concurrency, UUID idempotency caching, partitioned PostgreSQL cursor pagination]

key-files:
  created:
    - internal/control/session.go
    - internal/control/auth.go
    - internal/control/etag.go
    - internal/control/idempotency.go
    - internal/control/rbac.go
    - internal/control/api.go
    - internal/control/routes_handler.go
    - internal/control/policy_handler.go
    - internal/control/simulate_handler.go
    - internal/control/publish_handler.go
    - internal/control/rollback_handler.go
    - internal/control/quarantine_handler.go
    - internal/control/audit_handler.go
    - internal/control/gateway_handler.go
    - internal/storage/policy_repo.go
    - internal/storage/audit_repo.go
    - internal/control/session_test.go
    - internal/control/concurrency_test.go
    - internal/control/api_test.go
    - internal/storage/audit_repo_test.go
  modified:
    - go.mod
    - go.sum
    - internal/control/validator.go
    - internal/storage/snapshot_repo.go

key-decisions:
  - "In-memory SessionManager provides 8-hour maximum operator session lifetimes with 256-bit entropy session tokens and CSRF secrets."
  - "Mutating methods (POST/PUT/DELETE/PATCH) enforce constant-time X-CSRF-Token comparison; safe methods (GET/HEAD/OPTIONS) skip CSRF checks."
  - "Invariant 11 rejects any data-plane bearer token presented to /control/v1 management APIs immediately with HTTP 403 Forbidden (DATA_PLANE_CREDENTIALS_REJECTED)."
  - "Mutations enforce RFC 7232 ETag/If-Match validation; stale requests receive HTTP 412 Precondition Failed without applying changes."
  - "Audit queries default to [now - 24h, now] to constrain execution to active PostgreSQL range partitions and utilize B-tree indexes."
  - "Candidate Rego simulation compiles in-memory with ast.RegoV1 and returns decision diagnostics and execution latencies in microseconds."

patterns-established:
  - "Pattern: HttpOnly SameSite=Strict cookie authentication paired with X-CSRF-Token double-submit defense for management APIs"
  - "Pattern: FormatETag and RequireIfMatch optimistic concurrency gate on all mutating routes"
  - "Pattern: IdempotencyKey UUID locking returning 409 on concurrent executions and cached response replay with Idempotent-Replay header"
  - "Pattern: Partitioned PostgreSQL time-bounded queries with base64 (timestamp|event_id) cursor pagination"

requirements-completed:
  - OPS-01
  - OPS-02
  - OPS-04

# Metrics
duration: 25min
completed: 2026-10-07
---

# Plan 04-01: Control Plane REST Management APIs (/control/v1) with RBAC, CSRF Protection, and Optimistic Concurrency Summary

**Delivered authoritative /control/v1 REST management router on port :8084 with session cookies, CSRF defense, RFC 7232 optimistic concurrency, idempotency deduplication, candidate Rego simulation, and partitioned audit querying.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-10-07T04:20:00Z
- **Completed:** 2026-10-07T04:45:00Z
- **Tasks:** 3
- **Files modified/created:** 24

## Accomplishments
- Bootstrapped `github.com/go-chi/chi/v5` router with security headers (CSP, nosniff, DENY, strict-origin) and mounted `/control/v1` routes aligned with `api/openapi/control-v1.yaml`.
- Implemented `SessionManager` issuing `HttpOnly; SameSite=Strict; Secure` cookies, double-submit `X-CSRF-Token` validation on mutating requests, and strict Invariant 11 data-plane token rejection returning HTTP 403.
- Implemented optimistic concurrency controls (`ETag` and `If-Match` returning `412 Precondition Failed` on stale edits) and idempotency deduplication (`IdempotencyStore` returning `409 Conflict` on concurrent requests and replaying cached responses for 24h).
- Created PostgreSQL repositories: `PolicyRepo` for drafts and `AuditRepo` for partitioned `audit_events` with default 24h bounding and opaque cursor pagination.
- Built all management handlers: route catalog, policy draft lifecycle, sub-millisecond candidate Rego dry-run simulator, monotonic snapshot publisher, N+1 monotonic rollback, emergency principal quarantine and token revocation (<5s SLA), and gateway convergence aggregation.
- Verified all components with unit and integration tests passing with `-race` enabled across storage and control packages.

## Task Commits

Each task was committed atomically:

1. **Task 1: Session Security, CSRF Protection, Optimistic Concurrency, and Idempotency Engines** - `1908320` (feat)
2. **Task 2: Storage Repositories for Policy Drafts and Partitioned Audit Events** - `eec65ad` (feat)
3. **Task 3: Chi REST Router Bootstrap, Security Middleware, and /control/v1 API Handlers** - `f76e499` (feat)

## Files Created/Modified
- `go.mod` / `go.sum` - Added `github.com/go-chi/chi/v5 v5.2.1`
- `internal/control/session.go` - Thread-safe session management, cookie parsing, CSRF checking, and error response formatting
- `internal/control/auth.go` - Operator login, logout, and `/auth/me` endpoints with seeded demo operator credentials
- `internal/control/etag.go` - ETag formatting, `If-Match` validation, and 412 Precondition Failed helpers
- `internal/control/idempotency.go` - `IdempotencyStore` with in-flight concurrency locking (409) and 24h cached response replay
- `internal/control/rbac.go` - Role-based access control middleware enforcing `sec-ops`, `auditor`, and `viewer` roles, with Invariant 11 data-plane token rejection
- `internal/storage/policy_repo.go` - PostgreSQL CRUD repository for `policy_drafts`
- `internal/storage/audit_repo.go` - Filtered, parameterized queries for partitioned `audit_events` with cursor pagination
- `internal/storage/snapshot_repo.go` - Added `ListSnapshots` query method
- `internal/control/routes_handler.go` - `/control/v1/routes` handlers (GET, POST)
- `internal/control/policy_handler.go` - `/control/v1/policies` draft creation, version inspection, and validation
- `internal/control/simulate_handler.go` - In-memory Rego dry-run simulation handler
- `internal/control/publish_handler.go` - Monotonic configuration snapshot signing, saving, and gRPC broadcast
- `internal/control/rollback_handler.go` - N+1 monotonic rollback handler
- `internal/control/quarantine_handler.go` - Emergency Redis principal quarantine and token JTI revocation handlers
- `internal/control/audit_handler.go` - Audit querying and single event inspection handlers
- `internal/control/gateway_handler.go` - Replica convergence status aggregator
- `internal/control/api.go` - Chi router setup, security headers middleware, and route mounting
- `internal/control/session_test.go` - Tests for session creation, cookie generation, CSRF enforcement, and RBAC
- `internal/control/concurrency_test.go` - Tests for ETag matching (412) and idempotency locking/caching (409/replay)
- `internal/storage/audit_repo_test.go` - pgxmock tests for audit repo filtering, default 24h bounds, cursor pagination, and policy repo
- `internal/control/api_test.go` - End-to-end API tests covering routes, policies, simulation, publish, rollback, and quarantine

## Decisions Made
- Used in-memory `SessionManager` and `IdempotencyStore` for fast, lightweight zero-dependency management operation with thread safety.
- Handled Invariant 11 directly in `SessionMiddleware` and `RBACMiddleware` to guarantee data plane credentials are unconditionally rejected on any management endpoint.
- Structured `SnapshotPayload.PolicyModules` as a slice of `*snapshotv1.PolicyModule` matching the Protobuf contract.
- Checked active snapshot version primarily against in-memory `distServer` to eliminate unnecessary database query overhead on optimistic concurrency checks.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug Fix] Module duplication in Validator.RunRegoTests**
- **Found during:** Task 3 (Policy validation test)
- **Issue:** When candidateRego was passed as both policyRego and testRego, `ast.CompileModulesWithOpt` received two modules with the same package name, causing compilation failure and nil summary.
- **Fix:** Updated `Validator.RunRegoTests` to check if `testRego != policyRego` before adding `test.rego` to the compilation module map, and added a nil check in `policy_handler.go`.
- **Files modified:** `internal/control/validator.go`, `internal/control/policy_handler.go`
- **Verification:** `go test -v ./internal/control/ -run TestPolicyAndSimulationAPI` passed.
- **Committed in:** `f76e499` (Task 3 commit)

**2. [Rule 1 - Type Alignment] Protobuf PolicyModules type alignment**
- **Found during:** Task 3 (Publish and rollback compilation)
- **Issue:** Initial implementation used `map[string]string` for `PolicyModules`, whereas `snapshotv1.SnapshotPayload` generated Protobuf defines `PolicyModules` as `[]*snapshotv1.PolicyModule`.
- **Fix:** Converted `PolicyModules` instantiation and access in `publish_handler.go`, `policy_handler.go`, `simulate_handler.go`, and `api_test.go` to use `[]*snapshotv1.PolicyModule`.
- **Files modified:** `internal/control/publish_handler.go`, `internal/control/policy_handler.go`, `internal/control/simulate_handler.go`, `internal/control/api_test.go`
- **Verification:** Code compiled and all tests passed.
- **Committed in:** `f76e499` (Task 3 commit)

**3. [Rule 1 - Mock Type Alignment] SchemaVersion type matching in pgxmock**
- **Found during:** Task 3 (Snapshot publishing test)
- **Issue:** pgxmock failed on `schema_version` matching `int` (1) against Protobuf's `int32(1)`.
- **Fix:** Cast literal `1` to `int32(1)` in `api_test.go` expectations.
- **Files modified:** `internal/control/api_test.go`
- **Verification:** `TestPolicyAndSimulationAPI/Publish_Policy_with_Optimistic_Concurrency` passed.
- **Committed in:** `f76e499` (Task 3 commit)

---

**Total deviations:** 3 auto-fixed (all Rule 1 bug fixes / type alignments)
**Impact on plan:** Essential for contract compliance and test correctness. Zero scope creep.

## Issues Encountered
- None that were not resolved via automatic fixes.

## User Setup Required
None - local in-memory session and redis/postgres storage use default test doubles or local infrastructure.

## Next Phase Readiness
- Control plane REST management APIs (/control/v1) are completely implemented, secured, and verified.
- Ready for Plan 04-02: React/TypeScript Operator Dashboard (`web/dashboard`) integration with Policy Studio, Dry-Run Simulator, Cluster Topology, and Emergency Quarantine UI.

---
*Phase: 04-operator-experience-telemetry*
*Completed: 2026-10-07*
