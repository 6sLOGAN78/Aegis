---
phase: 05-distributed-resilience-chaos
plan: 01
subsystem: infra
tags: [haproxy, load-balancing, connection-draining, probes, livez, readyz, docker-compose, multi-replica]

# Dependency graph
requires:
  - phase: 04-operator-dashboard-control-plane
    provides: Dynamic snapshot distribution, gRPC streaming, and policy management
provides:
  - Multi-replica gateway cluster (3 instances) behind Layer 7 HAProxy load balancer
  - Unauthenticated /livez and /readyz health probes evaluating lease expiry, active snapshot, and spool saturation
  - Two-phase 30-second graceful connection draining lifecycle on SIGTERM
  - Distributed Docker Compose manifest and Makefile management targets
affects: [05-distributed-resilience-chaos, chaos-testing, k6-benchmarks]

# Tech tracking
tech-stack:
  added: [haproxy:2.8-alpine]
  patterns: [Two-phase graceful connection drain, unauthenticated health/readiness probes, L7 HTTP health checking with L4 mTLS passthrough]

key-files:
  created:
    - internal/proxy/probe.go
    - internal/proxy/probe_test.go
    - cmd/gateway/main_test.go
    - deployments/compose/haproxy/haproxy.cfg
    - deployments/compose/docker-compose.distributed.yml
  modified:
    - internal/audit/spool.go
    - cmd/gateway/main.go
    - Makefile

key-decisions:
  - "Added IsSaturated() boolean query helper to audit.DiskSpool for non-blocking probe evaluation"
  - "Configured HAProxy with 1s HTTP health check interval and fall 2 rise 2 for 2-second eviction of draining nodes"
  - "Configured L4 TCP passthrough for workload port 9443 on HAProxy to preserve end-to-end mTLS client certificate verification"
  - "Dedicated WAL spool volumes per gateway replica (aegis_wal_spool_1, aegis_wal_spool_2, aegis_wal_spool_3) to prevent lock contention"

patterns-established:
  - "Pattern 1: Gateway Readiness Probes & 30s Graceful Connection Draining"
  - "Pattern 2: HAProxy Layer 7 Load Balancer with HTTP health checks and TCP mTLS passthrough"

requirements-completed: [DIST-01]

# Metrics
duration: 15min
completed: 2026-10-07
---

# Phase 05-01: Multi-Replica Gateway Cluster Behind HAProxy Summary

**3-replica distributed gateway cluster deployed behind HAProxy with active `/readyz` health polling and a two-phase 30-second graceful connection draining lifecycle.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-10-07T09:27:00Z
- **Completed:** 2026-10-07T09:42:00Z
- **Tasks:** 3
- **Files modified:** 8

## Accomplishments

- Implemented reusable unauthenticated `/livez` and `/readyz` probes in `internal/proxy/probe.go` with `DrainingState` tracker. Probes dynamically evaluate configuration initialization, 60s lease freshness, WAL spool saturation, and drain signals.
- Configured two-phase 30-second connection draining in `cmd/gateway/main.go`: on SIGTERM, readiness immediately fails (503 `DRAINING`), while `dualServer.Shutdown(ctx)` allows in-flight proxy requests to finish their processing and WAL append lifecycle cleanly.
- Implemented HAProxy 2.8+ configuration (`deployments/compose/haproxy/haproxy.cfg`) with Layer 7 HTTP health checks (`inter 1s fall 2 rise 2`) against `/readyz`, Layer 4 TCP passthrough for mTLS workloads on `:9443`, and a live stats dashboard on `:8404`.
- Authored distributed Docker Compose manifest (`deployments/compose/docker-compose.distributed.yml`) running 3 gateway replicas (`gateway-1`, `gateway-2`, `gateway-3`) with dedicated WAL volumes and zero host-exposed backend ports.
- Added orchestration targets to `Makefile`: `distributed-up`, `distributed-down`, `distributed-logs`, and `distributed-status`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Unauthenticated Health/Readiness Probes** - `10867b5` (feat)
2. **Task 2: Gateway Daemon 30-Second Graceful Connection Draining** - `00d8741` (feat)
3. **Task 3: HAProxy Edge Load Balancer and Distributed Docker Compose Topology** - `be67230` (feat)

## Files Created/Modified

- `internal/proxy/probe.go` - Implemented unauthenticated `/livez` and `/readyz` probe handler and `DrainingState` atomic state machine.
- `internal/proxy/probe_test.go` - Comprehensive unit tests validating probe routes, status codes (200 OK, 503 DRAINING, LEASE_EXPIRED, UNINITIALIZED, SPOOL_SATURATED), and pass-through routing.
- `internal/audit/spool.go` - Added `IsSaturated() bool` helper to `DiskSpool` for probe inspection.
- `cmd/gateway/main.go` - Integrated probe handler wrapping ingress and wired two-phase 30s graceful shutdown on SIGTERM/Interrupt.
- `cmd/gateway/main_test.go` - Integration test `TestGatewayGracefulDrain` confirming active in-flight request completion while concurrent readiness fails with 503, plus `TestGatewayProbes`.
- `deployments/compose/haproxy/haproxy.cfg` - HAProxy configuration for HTTP ingress `:8080`, workload TCP `:9443`, and stats `:8404`.
- `deployments/compose/docker-compose.distributed.yml` - Distributed Docker Compose topology with 3 gateway replicas, HAProxy, Control Plane, DB, Redis, and private backends.
- `Makefile` - Added `distributed-up`, `distributed-down`, `distributed-logs`, and `distributed-status` targets.

## Decisions Made

- Added `IsSaturated() bool` to `DiskSpool` in `internal/audit/spool.go` to provide a clean non-blocking predicate for the probe handler.
- Configured HAProxy with `inter 1s fall 2 rise 2` on `/readyz` HTTP health checks, guaranteeing eviction of draining or unhealthy replicas within 2 seconds.
- Configured HAProxy `:9443` in TCP mode (`mode tcp`) to ensure mutual TLS client certificates terminate directly on gateway replicas rather than the edge proxy, preserving SPIFFE identity verification.
- Provided distinct persistent volumes (`aegis_wal_spool_1`, `aegis_wal_spool_2`, `aegis_wal_spool_3`) for each gateway replica to isolate file system locks.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Capability] Added `IsSaturated()` method to `audit.DiskSpool`**
- **Found during:** Task 1 (Probe handler wiring)
- **Issue:** Plan specified passing `diskSpool.IsSaturated` to `CreateProbeHandler`, but `DiskSpool` only had `CheckSaturation() (bool, error)`.
- **Fix:** Added `IsSaturated() bool` method to `DiskSpool` in `internal/audit/spool.go`.
- **Files modified:** `internal/audit/spool.go`
- **Verification:** Unit tests in `internal/audit/` passed with race detector.
- **Committed in:** `10867b5` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (missing capability query helper).
**Impact on plan:** Essential for clean dependency injection into `CreateProbeHandler`. No scope creep.

## Issues Encountered

None. All automated tests and configuration validation commands passed cleanly on the first run.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Distributed multi-replica gateway cluster and HAProxy load balancer are ready for Plan 05-02 (gRPC Stream Reconnection with Full Jitter and Fleet Convergence under Load).
- Topology manifest is ready for Plan 05-03 (Automated Chaos & Fault Injection Suite).

---
*Phase: 05-distributed-resilience-chaos*
*Plan: 01*
*Completed: 2026-10-07*
