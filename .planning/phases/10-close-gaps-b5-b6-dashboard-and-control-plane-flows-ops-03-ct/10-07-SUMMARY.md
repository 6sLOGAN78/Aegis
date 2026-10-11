---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 07
subsystem: snapshot
tags: [grpc, snapshot, reconnect, CTRL-05]
requires: []
provides:
  - Idempotent re-delivery of the active snapshot (CR-01 closed)
affects: [internal/snapshot]
key-files:
  modified:
    - internal/snapshot/client.go
    - internal/snapshot/client_test.go
    - internal/snapshot/manager.go
decisions:
  - Short-circuit only when Version AND PayloadSha256 both match the verified active state
  - On short-circuit, lastStatus/lastErr are left unchanged (not forced to ACTIVATED)
metrics:
  tasks: 1
  completed: 2026-10-11
---

# Phase 10 Plan 07: Snapshot re-delivery idempotency Summary

Stream reconnect re-pushing the active snapshot now yields an ACTIVATED ack without re-verification, so gateways no longer flip to REJECTED and show "partitioned" (CR-01).

## What changed
- `ActiveState` gains `PayloadSha256`, set in `ValidateAndActivate`.
- Snapshot case extracted into `StreamClient.processSnapshot`; matching Version+hash returns ACTIVATED and preserves prior status. Anything else goes through full `ValidateAndActivate`.
- Tests: `TestSnapshotRedelivery_ActiveVersionStaysActivated`, `_OlderStillRejected`, `_SameVersionDifferentHashRejected` (all pass, `-race`).

Commit: see `git log` (fix(10-07)).

## Deviations from Plan
- Worktree base was stale; reset to d39b8a1 per the startup check.
- Test helper payload omits policy modules (the sample Rego in signer_test.go contains literal `\n` and is not a safe activation payload). The optional bufconn reconnect test was skipped; the mandatory processSnapshot-to-heartbeat proof is in place.
- `gofmt -l` flags manager.go and signer_test.go; pre-existing formatting, left untouched.

## Deferred
CR-02 (unauthenticated gRPC / spoofable GatewayId), WR-01..WR-03 out of scope.

## Self-Check: PASSED
`go build ./...`, `go vet ./internal/snapshot/`, `go test ./internal/snapshot/... -race` all pass.
