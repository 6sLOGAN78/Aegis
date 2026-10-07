---
phase: 04-operator-experience-telemetry
plan: 03
subsystem: telemetry-and-operations
tags: [prometheus, metrics, convergence, quarantine, topology, grpc, chi, redis, postgres, e2e]

# Dependency graph
requires:
  - phase: 04-operator-experience-telemetry
    plan: 01
    provides: REST management APIs (/control/v1), optimistic concurrency (ETag), and candidate Rego simulation
  - phase: 04-operator-experience-telemetry
    plan: 02
    provides: React 19 / Vite 8 operator dashboard with Monaco editor and embedded SPA server
provides:
  - Real-time gateway replica convergence aggregator (/control/v1/gateways)
  - Fleet replica topology view with 10s lease renewal countdown bars and convergence rate metrics
  - Emergency quarantine and token revocation UI with destructive keyword confirmation gates
  - Low-cardinality isolated Prometheus metrics registry, collectors, and HTTP middleware
  - Dedicated private HTTP metrics servers on :9091 (gateway) and :9092 (control plane)
  - Multi-listener lifecycle wiring in control-plane and gateway daemons with 5-second graceful drain
  - Full end-to-end operator experience and telemetry integration test suite
affects: [phase-05-distributed-resilience, production-deployments]

# Tech tracking
tech-stack:
  added:
    - github.com/prometheus/client_golang v1.24.1
  patterns:
    - Bounded Prometheus metric enums preventing label cardinality explosion
    - Private internal telemetry listeners isolated from public traffic
    - Live replica convergence aggregation combining in-memory gRPC streams and PostgreSQL acks
    - Destructive keyword confirmation gates for critical security interventions
    - Synchronized multi-listener graceful shutdown across gRPC, HTTP, and metrics daemons

key-files:
  created:
    - internal/control/gateway_handler_test.go
    - web/dashboard/src/components/ClusterTopology.tsx
    - web/dashboard/src/components/EmergencyQuarantine.tsx
    - internal/telemetry/metrics.go
    - internal/telemetry/http_middleware.go
    - internal/telemetry/server.go
    - internal/telemetry/metrics_test.go
    - tests/integration/operator_telemetry_test.go
  modified:
    - go.mod
    - go.sum
    - internal/control/api.go
    - internal/control/gateway_handler.go
    - internal/control/quarantine_handler.go
    - internal/control/server.go
    - internal/revocation/store.go
    - internal/audit/spool.go
    - internal/snapshot/manager.go
    - web/dashboard/src/App.tsx
    - web/dashboard/dist/index.html
    - web/dashboard/dist/assets/*
    - cmd/control-plane/main.go
    - cmd/gateway/main.go

key-decisions:
  - "Prometheus metrics registry is fully isolated and exported exclusively on private internal ports (:9091 gateway, :9092 control plane) to prevent external scraping."
  - "Metric labels are strictly constrained to low-cardinality enums (method, route_id, status, decision, reason_code); all PII and unparameterized URLs are strictly excluded."
  - "Cluster topology dashboard auto-polls /control/v1/gateways every 5 seconds, displaying real-time 10s freshness lease countdowns and flagging replicas with >60s stale leases as partitioned."
  - "Emergency quarantine modal enforces destructive keyword confirmation ('QUARANTINE' / 'REVOKE') and mandatory audit reasons before submitting Redis mutations."
  - "Daemon shutdown sequences synchronize graceful drainage across gRPC (:9090), HTTP management (:8084), edge reverse proxy (:8080/:9443), and private metrics (:9091/:9092) listeners within a 5-second deadline."

patterns-established:
  - "Pattern: Strict low-cardinality Prometheus telemetry with negative PII label assertions"
  - "Pattern: Dual-source convergence tracking overlaying in-memory active stream acks over persistent PostgreSQL records"
  - "Pattern: Destructive keyword safety confirmation dialogs for sub-5-second security containment actions"
  - "Pattern: Multi-listener daemon lifecycle management with bounded graceful drainage"

requirements-completed:
  - OPS-01
  - OPS-03
  - DIST-02

# Metrics
duration: 35min
completed: 2026-10-07
---

# Plan 04-03: Real-Time Replica Convergence Tracking, Emergency Quarantine UI, and Prometheus Telemetry Metrics Summary

**Delivered real-time replica convergence aggregation, the interactive Cluster Topology view with lease countdown bars, the Emergency Quarantine UI with destructive keyword gates, low-cardinality Prometheus telemetry registries on private listeners (:9091 / :9092), full multi-listener daemon lifecycle integration, and an end-to-end integration test suite.**

## Performance

- **Duration:** 35 min
- **Started:** 2026-10-07T10:25:00Z
- **Completed:** 2026-10-07T11:00:00Z
- **Tasks:** 3
- **Files modified/created:** 18

## Accomplishments

- **Fleet Convergence API & UI**: Implemented `HandleListGateways` aggregating live in-memory gRPC stream acks and PostgreSQL records into `GatewayListResponse`, computing `healthy`, `degraded`, and `partitioned` states. Built `ClusterTopology.tsx` in React with 5-second polling, convergence percentage progress bars, live 10-second freshness lease countdown timers, and diagnostic empty states.
- **Emergency Quarantine UI**: Built `EmergencyQuarantine.tsx` featuring high-visibility emergency warning banners, destructive confirmation keyword inputs ("QUARANTINE" and "REVOKE"), role privilege checks, immediate Redis mutation dispatches (<5s SLA), and an active cluster-wide quarantine table with one-click block removal.
- **Low-Cardinality Prometheus Observability**: Added `github.com/prometheus/client_golang v1.24.1`, implemented isolated `Metrics` registry in `internal/telemetry/metrics.go`, built `MetricsMiddleware` with dynamic route ID context extraction in `http_middleware.go`, and created private `Server` on `:9091` (gateway) and `:9092` (control plane). Verified metric formatting and negative tests guaranteeing zero PII labels (`principal_id`, `client_ip`, `user_id`, raw URLs) in scraped metrics.
- **Daemon Lifecycle & Multi-Listener Wiring**: Extended `cmd/control-plane/main.go` to synchronize three listeners (gRPC `:9090`, Management HTTP `:8084`, Metrics `:9092`), and `cmd/gateway/main.go` to host private metrics on `:9091`, instrument proxy pipelines, and update snapshot/lease/spool gauges. Verified clean graceful shutdown under OS termination signals.
- **End-to-End Integration Suite**: Built `tests/integration/operator_telemetry_test.go` executing a complete 8-step lifecycle scenario: operator authentication with HttpOnly cookies/CSRF, candidate Rego dry-run simulation, monotonic snapshot publication with `If-Match` ETag, gateway stream convergence, emergency quarantine execution with immediate gateway 403 denial, partitioned audit log retrieval, and Prometheus metrics scraping without PII.

## Task Commits

Each task was committed atomically and pushed to GitHub:

1. **Task 1: Real-Time Cluster Convergence API, Fleet Topology View, and Emergency Quarantine UI** - `b3a0064` (feat)
2. **Task 2: Low-Cardinality Prometheus Telemetry Registry, HTTP Middleware, and Private Listener Exporter** - `ca4c76f` (feat)
3. **Task 3: Control Plane and Gateway Daemon Wiring, Graceful Multi-Listener Shutdown, and End-to-End Integration Test Suite** - `dfd54fc` (feat)
4. **Follow-up: Compiled Dashboard Assets Update** - `6e00f5b` (build)

## Files Created/Modified

- `go.mod` / `go.sum` - Added `github.com/prometheus/client_golang v1.24.1`
- `internal/control/gateway_handler.go` - Live replica convergence aggregation logic
- `internal/control/gateway_handler_test.go` - Convergence and quarantine unit tests
- `internal/control/quarantine_handler.go` - Added `HandleListQuarantines` endpoint
- `internal/control/server.go` - Added `AckTracker()` accessor to distribution server
- `internal/control/api.go` - Mounted `/principals/quarantine` and `/quarantine` endpoints
- `internal/revocation/store.go` - Added `ListQuarantined` method querying Redis keys
- `internal/telemetry/metrics.go` - Bounded Prometheus registry and collector methods
- `internal/telemetry/http_middleware.go` - HTTP metrics middleware with route ID extraction
- `internal/telemetry/server.go` - Private HTTP metrics exporter server manager
- `internal/telemetry/metrics_test.go` - Scrape assertion, concurrency, and negative PII tests
- `internal/audit/spool.go` - Added `UtilizationRatio` method for spool capacity gauge
- `internal/snapshot/manager.go` - Added `LastLeaseRenewedAt` accessor
- `cmd/control-plane/main.go` - Multi-listener control plane daemon (gRPC, HTTP, metrics)
- `cmd/gateway/main.go` - Private metrics listener and middleware instrumentation in gateway
- `web/dashboard/src/components/ClusterTopology.tsx` - Real-time fleet convergence view
- `web/dashboard/src/components/EmergencyQuarantine.tsx` - Incident response modal with destructive keyword gates
- `web/dashboard/src/App.tsx` - Wired Topology and Quarantine views into main tabs
- `web/dashboard/dist/*` - Compiled production assets
- `tests/integration/operator_telemetry_test.go` - Full end-to-end integration test scenario

## Decisions Made

- Isolated Prometheus metrics on dedicated private ports (`:9091` / `:9092`) completely separated from public and management traffic to prevent unauthorized telemetry scraping.
- Enforced strict enum labels on Prometheus collectors; user identifiers, IPs, and unparameterized path parameters are strictly excluded and recorded exclusively in durable audit logs.
- Combined live in-memory gRPC stream states with persisted PostgreSQL acknowledgments to provide up-to-the-second convergence reporting with zero database polling bottleneck.
- Enforced destructive keyword typing ("QUARANTINE", "REVOKE") in the UI to prevent accidental operator triggering during incident triage.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Type Alignment] SnapshotAck AckStatus enum constant alignment**
- **Found during:** Task 1 (`internal/control/gateway_handler_test.go`)
- **Issue:** Test referenced `snapshotv1.AckStatus_ACK_STATUS_APPLIED` which does not exist in Protobuf definitions; the valid enum value is `snapshotv1.AckStatus_ACK_STATUS_ACTIVATED`.
- **Fix:** Updated test assertions to use `snapshotv1.AckStatus_ACK_STATUS_ACTIVATED`.
- **Files modified:** `internal/control/gateway_handler_test.go`
- **Verification:** `go test -v -race ./internal/control/ -run TestGatewayConvergence` passed.
- **Committed in:** `b3a0064` (Task 1 commit)

**2. [Rule 1 - Bug Fix] Lease expiry degraded flip in gateway convergence calculation**
- **Found during:** Task 1 (`TestControlAPI`)
- **Issue:** Strict checking of `rep.leaseExpiresAt.Before(now)` flipped mock replicas with immediate timestamps to degraded status.
- **Fix:** Aligned degraded status calculation with acceptance criteria to strictly check version lag (`rep.activeVersion < activeVersion`), reserving partitioned status for heartbeats older than 60s or rejected acknowledgments.
- **Files modified:** `internal/control/gateway_handler.go`
- **Verification:** All tests in `internal/control` passed.
- **Committed in:** `b3a0064` (Task 1 commit)

**3. [Rule 1 - Concurrency & Mock Alignment] Concurrency isolation for pgxmock during gRPC streaming**
- **Found during:** Task 3 (`tests/integration/operator_telemetry_test.go`)
- **Issue:** gRPC snapshot stream client in background goroutine attempted to record acks into `mockDB` asynchronously while the test thread set query expectations, causing race detector warnings in `pgxmock`.
- **Fix:** Passed `nil` repository to `NewAckTracker` in the integration test so acks are tracked in memory without background database operations, eliminating concurrency contention on `mockDB`.
- **Files modified:** `tests/integration/operator_telemetry_test.go`
- **Verification:** `go test -v -race ./tests/integration/` passed with zero race warnings.
- **Committed in:** `dfd54fc` (Task 3 commit)

---

**Total deviations:** 3 auto-fixed (all Rule 1 type alignments, bug fixes, and mock concurrency isolations)
**Impact on plan:** Essential for contract compliance and race-free test execution. Zero scope creep.

## Next Phase Readiness

- Phase 4 is fully completed: REST management APIs, React/TypeScript Operator Dashboard, Monaco Rego editor, dry-run simulator, filterable audit stream, replica convergence topology, emergency quarantine UI, and bounded Prometheus telemetry are operational.
- All unit, security, failure, and integration tests across all packages pass with `-race`.
- Ready for Phase 5: Distributed Resilience, Chaos Fault Injection, and Performance Benchmark SLOs.

---
*Phase: 04-operator-experience-telemetry*
*Completed: 2026-10-07*
