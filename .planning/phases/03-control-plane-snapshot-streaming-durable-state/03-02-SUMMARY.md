---
phase: 03-control-plane-snapshot-streaming-durable-state
plan: 02
subsystem: control-plane
tags: [snapshot, grpc, ed25519, lease, fail-closed, atomic-swap, streaming]

requires:
  - phase: 03-control-plane-snapshot-streaming-durable-state
    plan: 01
    provides: "PostgreSQL schemas, repositories, and Goose migrations"
provides:
  - "Ed25519 digital signature signing and verification for configuration snapshots and freshness leases"
  - "Monotonic integer version enforcement rejecting stale or equal versions"
  - "RollbackEngine that republishes historical configuration under strictly higher monotonic version N+1"
  - "gRPC SnapshotDistributionService bidirectional streaming daemon with bufconn support"
  - "10-second signed freshness lease ticker broadcasting periodic heartbeats"
  - "Gateway AckTracker persisting activation and rejection telemetry to PostgreSQL"
  - "Lock-free atomic snapshot swap on the gateway hot path via sync/atomic.Pointer[ActiveState]"
  - "Fail-closed gateway boundary returning HTTP 503 (POLICY_LEASE_EXPIRED) when lease age > 60s"
  - "gRPC StreamClient with randomized exponential backoff and jitter"
  - "Standalone Control Plane daemon cmd/control-plane/main.go"
affects:
  - "03-03 Redis rate limiting and token revocation store"
  - "03-04 Pre-forward WAL disk spool and asynchronous PostgreSQL audit worker"
  - "04-operator-dashboard-policy-management-convergence"

tech-stack:
  added:
    - "google.golang.org/grpc v1.83.2"
    - "google.golang.org/protobuf v1.36.12"
    - "crypto/ed25519"
    - "sync/atomic.Pointer"
  patterns:
    - "Atomic in-memory configuration swapping using sync/atomic.Pointer[ActiveState]"
    - "Bounded freshness leases with 10s emission and 60s fail-closed boundary"
    - "Monotonic versioning where rollbacks republish historical state under N+1"
    - "Randomized exponential reconnect backoff with jitter on gRPC bidirectional streams"

key-files:
  created:
    - internal/snapshot/signer.go
    - internal/snapshot/signer_test.go
    - internal/snapshot/verifier.go
    - internal/control/rollback.go
    - internal/control/server.go
    - internal/control/lease.go
    - internal/control/ack.go
    - internal/control/control_test.go
    - internal/snapshot/manager.go
    - internal/snapshot/client.go
    - cmd/control-plane/main.go
    - tests/integration/snapshot_streaming_test.go
    - tests/failure/lease_expiry_test.go
  modified:
    - internal/proxy/router.go
    - internal/config/config.go
    - cmd/gateway/main.go
    - internal/audit/logger.go
    - internal/policy/engine.go

key-decisions:
  - "Signed SHA-256 payload digests using Ed25519, preserving constant-time verification without external CGO overhead"
  - "Enforced strictly increasing monotonic integer versions; rollbacks mint new version N+1 rather than decrementing (CTRL-02, CTRL-06, Invariant 9)"
  - "Implemented out-of-band policy precompilation and atomic pointer swap (sync/atomic.Pointer[ActiveState]) to keep request hot path lock-free"
  - "Enforced 60-second fail-closed boundary on gateway: if signed lease renewal is not received for >60s, gateway drops readiness and returns HTTP 503 POLICY_LEASE_EXPIRED"
  - "Configured OPA Rego v1 parser options by default in NewEngine to maintain consistency across the engine and validator"

patterns-established:
  - "Pattern 1: Lock-Free Atomic Pointer Swap for Gateway Configuration"
  - "Pattern 2: Bounded Freshness Lease with 60-Second Fail-Closed Enforcement"
  - "Pattern 3: Monotonic Rollback Republishing Historical Configurations at N+1"
  - "Pattern 4: Bidirectional gRPC Streaming with Fleet Convergence Telemetry"

requirements-completed:
  - CTRL-02
  - CTRL-03
  - CTRL-04
  - CTRL-05
  - CTRL-06

duration: 25min
completed: 2026-10-06
---

# Plan 03-02: Monotonic Ed25519 Snapshot Signer, gRPC Streaming, 10s Freshness Lease, and 60s Fail-Closed Timeout Summary

**Monotonic Ed25519 snapshot signer, verifier, monotonic rollback engine, bidirectional gRPC distribution service, 10s freshness lease generator, fleet convergence acknowledgment tracking, and 60s fail-closed gateway boundary**

## Performance

- **Duration:** ~25 min
- **Tasks:** 4 completed
- **Files created/modified:** 18
- **Tests Passing:** 100% across all unit, bufconn, failure, security, and integration suites (`go test -v -race ./...`)

## Accomplishments

- **Monotonic Ed25519 Cryptographic Signer, Verifier, and Rollback Engine (`internal/snapshot/`, `internal/control/rollback.go`)**:
  - Implemented `snapshot.Signer` signing snapshot payload SHA-256 digests and deterministic lease digests using Ed25519 private keys (CTRL-02, Invariant 4).
  - Implemented `snapshot.Verifier` verifying cryptographic signatures, payload SHA-256 checksums, strictly increasing monotonic versions, and lease validity timestamps.
  - Implemented `control.RollbackEngine` recovering historical snapshot contents and republishing them under strictly higher version $N+1$ (CTRL-06, Invariant 9).
  - Comprehensive unit tests in `internal/snapshot/signer_test.go` verifying positive signing roundtrips, corrupted payload detection, signature tampering rejection, monotonic rejection of equal or lower versions, expired lease rejection, and monotonic rollback increments.

- **gRPC Snapshot Distribution Server, 10s Freshness Lease Generator, and Ack Tracker (`internal/control/`)**:
  - Implemented `control.SnapshotDistributionServer` fulfilling protobuf `SnapshotDistributionServiceServer` with bidirectional streaming (`StreamSnapshots`), instant cold-start snapshot delivery, thread-safe broadcasting (`BroadcastSnapshot`, `BroadcastLease`), and graceful client disconnection management (CTRL-03).
  - Implemented `control.LeaseGenerator` running a configurable background loop (default 10s) minting signed 15-second freshness leases for the active snapshot version (CTRL-04).
  - Implemented `control.AckTracker` ingesting `SnapshotAck` telemetry (`ACK_STATUS_ACTIVATED` / `ACK_STATUS_REJECTED`) from gateway replicas and persisting status to PostgreSQL (CTRL-05).
  - Bufconn in-memory integration test suite in `internal/control/control_test.go` verifying active snapshot streaming, dynamic broadcast propagation, ack ingestion, lease emissions, and clean unregistration under race detection.

- **Gateway Snapshot Manager, Stream Client, Router Extension, and Fail-Closed Boundary (`internal/snapshot/`, `internal/proxy/`, `cmd/gateway/`)**:
  - Extended `proxy.Router` and `proxy.Route` with protobuf conversion (`AddProtobufRoute`, `NewRouterFromProtobuf`) and support for rate limits, timeouts, and SPIFFE identifiers.
  - Extended `config.Config` and `LoadConfig()` with `ControlPlaneGRPCAddr`, Redis settings, and WAL spool parameters.
  - Implemented `snapshot.Manager` with `sync/atomic.Pointer[ActiveState]` providing out-of-band OPA query compilation, atomic swap, and thread-safe lease freshness verification.
  - Implemented `snapshot.StreamClient` with randomized exponential reconnect backoff with jitter (`base: 100ms`, `max: 5s`, `factor: 1.5`, `jitter: 0.2`).
  - Wired `cmd/gateway/main.go` to use dynamic snapshot state and enforce fail-closed checks on uninitialized state (HTTP 503 `UNINITIALIZED`) and expired leases (HTTP 503 `POLICY_LEASE_EXPIRED`).
  - Implemented integration test in `tests/integration/snapshot_streaming_test.go` asserting live snapshot streaming, atomic swap from v1 to v2, and rejection of corrupted or downgraded snapshots.
  - Implemented failure test in `tests/failure/lease_expiry_test.go` asserting that when lease age exceeds 60 seconds, readiness drops and all traffic fails closed with HTTP 503 RFC 7807 problem details (`https://aegis.local/errors/policy-lease-expired`).

- **Standalone Control Plane Daemon (`cmd/control-plane/main.go`)**:
  - Implemented standalone daemon binary parsing CLI flags and environment variables (`AEGIS_GRPC_PORT`, `AEGIS_PORT`, `AEGIS_DB_*`, `AEGIS_SIGNING_KEY_*`).
  - Integrated PostgreSQL connection pooling (`storage.NewPool`), snapshot and route repositories, Ed25519 key signing, lease generation ticker, and gRPC distribution server.
  - Handled clean graceful shutdown on `SIGINT` / `SIGTERM` stopping lease ticker, stopping gRPC server, and closing database connection pool.

## Task Commits

Each task was committed atomically and pushed to `main`:

1. **Task 1: Monotonic Ed25519 snapshot signer, verifier, and monotonic rollback engine** - `689689a` (feat)
2. **Task 2: gRPC SnapshotDistributionService, 10s freshness lease generator, and ack tracker** - `8abeb30` (feat)
3. **Task 3: Gateway snapshot manager with atomic pointer swap, gRPC stream client, router extension, and fail-closed checks** - `8f4bc46` (feat)
4. **Task 4: Standalone Control Plane daemon binary** - `27ebdc5` (feat)

## Files Created/Modified

- `internal/snapshot/signer.go` - Ed25519 signer for snapshots and freshness leases
- `internal/snapshot/signer_test.go` - Unit tests for signer, verifier, and rollback engine
- `internal/snapshot/verifier.go` - Cryptographic signature and monotonic version verifier
- `internal/control/rollback.go` - Monotonic rollback engine publishing historical state at $N+1$
- `internal/control/server.go` - gRPC distribution server for streaming snapshots and leases
- `internal/control/lease.go` - 10-second freshness lease generator background ticker
- `internal/control/ack.go` - Gateway acknowledgment and convergence tracker
- `internal/control/control_test.go` - Bufconn unit/integration test suite for control plane
- `internal/snapshot/manager.go` - Lock-free atomic snapshot state manager
- `internal/snapshot/client.go` - gRPC bidirectional streaming client with reconnect jitter
- `internal/proxy/router.go` - Protobuf route mapping and policy fields
- `internal/config/config.go` - Configuration fields for control plane gRPC, Redis, and spool
- `cmd/gateway/main.go` - Gateway wiring for dynamic snapshot streaming and 60s fail-closed checks
- `cmd/control-plane/main.go` - Standalone control plane daemon binary
- `tests/integration/snapshot_streaming_test.go` - End-to-end streaming and atomic swap integration tests
- `tests/failure/lease_expiry_test.go` - Security failure test verifying 60s fail-closed lease timeout
- `internal/audit/logger.go` - Dynamic snapshot version tracking in audit context
- `internal/policy/engine.go` - Rego v1 parser option support in in-memory policy engine

## Decisions Made

- Enforced Ed25519 signature over hex-encoded SHA-256 payload bytes to ensure unambiguous, deterministic verification across machines and network serialization boundaries.
- Designed `RollbackEngine` to enforce monotonic versioning ($N+1$) rather than version decrementing, eliminating replay vulnerabilities and guaranteeing gateway acceptance (CTRL-06, Invariant 9).
- Maintained zero lock contention on the gateway request hot path by performing Rego compilation and route validation out-of-band before executing an atomic swap using `sync/atomic.Pointer[ActiveState]`.
- Enforced a hard 60-second freshness lease expiry threshold: gateways drop readiness and return HTTP 503 (`POLICY_LEASE_EXPIRED`) if the control plane becomes unreachable for over 60 seconds (ADR-0004, Invariant 1).

## Deviations from Plan

- Enhanced `internal/audit/logger.go` with `AuditContext.SetSnapshotVersion(int64)` so dynamic snapshot versions from `snapManager.Active().Version` are seamlessly logged in completion audit records without breaking static middleware callers.
- Enabled `rego.SetRegoVersion(ast.RegoV1)` in `internal/policy/engine.go` to ensure consistent Rego v1 syntax compatibility (`if`, `in`, `:=`) across the compiler and validator.

## Next Phase Readiness

- Plan 03-02 completes the control plane distribution backbone and gateway dynamic configuration lifecycle.
- Plan 03-03 (Redis rate limiting and token revocation store) can now build upon the gateway snapshot rate limit policies and fail-closed architecture.

---
*Phase: 03-control-plane-snapshot-streaming-durable-state*
*Plan: 02*
*Completed: 2026-10-06*
