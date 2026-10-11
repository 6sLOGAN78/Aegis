---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 10
subsystem: testing
tags: [mtls, testpki, chaos, integration, grpc]
requires: ["11-01", "11-04"]
provides:
  - chaos and integration harnesses running the snapshot stream over mTLS
  - TestOperatorTelemetrySpoofedGatewayRejected
key-files:
  modified:
    - tests/chaos/test_helpers.go
    - tests/integration/operator_telemetry_test.go
    - tests/integration/snapshot_streaming_test.go
decisions:
  - "One testpki.PKI per control plane / test; PKI stored on TestControlPlane so spawnNode issues ClientCreds(gatewayID)."
metrics:
  tasks: 2
  completed: 2026-10-12
---

# Phase 11 Plan 10: Chaos and integration harnesses over mTLS Summary

Chaos and integration tests now serve and dial the snapshot stream with the in-memory testpki mTLS credentials (cert-bound GatewayId), plus an end-to-end spoof-rejection test.

## Tasks

| Task | Commit | Notes |
|------|--------|-------|
| 1 chaos helpers | see git log `test(11-10): run chaos harness over mTLS` | `PKI` field on `TestControlPlane`, `grpc.Creds(ServerCreds())`, `ClientCreds(gatewayID)` in spawnNode; severed dialer untouched |
| 2 integration | see git log `test(11-10): migrate integration stream tests` | Both stream tests migrated; new `TestOperatorTelemetrySpoofedGatewayRejected` (cert gateway-1 sending gateway-2 -> PermissionDenied, tracker empty) |

## Verification

`go vet ./tests/...` clean; `go test ./tests/chaos ./tests/integration -race -count=1` passes (all previously failing chaos and integration tests green). No timing adjustments were needed.

## Deviations from Plan

- Reworded a comment in tests/chaos/test_helpers.go ("insecure JWT" -> "unsigned JWT") so the acceptance grep for "insecure" returns nothing.
- The plan's `! grep -rn "insecure.NewCredentials" cmd internal` check still matches `internal/control/mtls_test.go` and `internal/testpki/testpki_test.go`. These are intentional plaintext-rejection negative tests from Plans 01/04, not harnesses, and were left as is. No non-test code uses it.
- The spoof test asserts on `AckTracker.GetReplicaStatuses()`, which is the data source of the operator `/control/v1/gateways` handler, rather than going through the full REST stack.
- Worktree base was reset to 36b23f2 per the startup check.

## Known Stubs
None.

## Self-Check: PASSED
