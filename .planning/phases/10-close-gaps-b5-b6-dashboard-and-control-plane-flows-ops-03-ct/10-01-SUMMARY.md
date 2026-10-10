---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 01
subsystem: control-plane / snapshot
tags: [heartbeat, convergence, grpc]
requires: []
provides: [lease-heartbeat-ack, pong-recording]
affects: [gateway convergence view]
key-files:
  modified:
    - internal/snapshot/client.go
    - internal/snapshot/client_test.go
    - internal/control/server.go
    - internal/control/control_test.go
    - internal/control/gateway_handler_test.go
decisions:
  - "Acks sent only from the Recv goroutine (no ticker/goroutine) to avoid concurrent stream Send"
  - "Pong uses server time and preserves a prior REJECTED status"
metrics:
  tasks: 2
  completed: 2026-10-11
requirements: [CTRL-05, OPS-03]
---

# Phase 10 Plan 01: Heartbeat ack on verified lease and Pong handling Summary

Gateway now acks every verified 10s lease with its last real snapshot status (B5), and the control plane records Pong without upgrading REJECTED replicas.

## Tasks

1. Gateway acks verified leases (commit b5bdf93): lastStatus/lastErr tracked in the stream loop; ack sent inside the VerifyLease success branch; helpers `buildHeartbeatAck` and `nextAckState`; 3 Heartbeat unit tests.
2. Control plane Pong handling (see second commit on branch): `GetPong()` branch in server.go Recv goroutine; tests TestControl_Pong_RecordsAck, TestControl_Pong_DoesNotMaskRejected, TestControl_HeartbeatOnLease (real StreamClient over bufconn); TestGatewayConvergence extended with 50s healthy and 70s partitioned cases.

## Verification

`go test -race -count=1 ./internal/control/... ./internal/snapshot/...` passes.

## Deviations from Plan

- The worktree was reset to the specified base b887fcc at start (its initial HEAD was an older commit).
- Task 1 tests are helper-level (no over-the-wire fake stream); the no-ack-on-invalid-lease test asserts VerifyLease rejects an empty lease, with structural guarantee that Send lives inside the err == nil branch. Over-the-wire proof is TestControl_HeartbeatOnLease.
- Pre-existing gofmt diffs in api_test.go, publish_handler.go, server.go (trailing blank line) left untouched.

## Known Stubs

None.

## Self-Check: PASSED
