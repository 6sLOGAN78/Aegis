---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 05
subsystem: control-plane
tags: [idempotency, ops-02, smoke]
requires: [10-01, 10-02]
key-files:
  modified:
    - internal/control/idempotency.go
    - internal/control/api.go
    - internal/control/api_test.go
    - internal/control/gateway_handler_test.go
    - tests/integration/operator_telemetry_test.go
    - deployments/compose/seed/seed.sh
    - scripts/compose-smoke.sh
    - tests/compose/smoke_script_test.go
  created:
    - internal/control/idempotency_required_test.go
decisions:
  - "Key enforced per route via RequireIdempotencyKey; IdempotencyMiddleware stays skip-when-empty"
metrics:
  tasks: 2
  completed: 2026-10-11
requirements: [OPS-02]
---

# Phase 10 Plan 05: Idempotency-Key required on mutations Summary

Nine mutating control-plane route registrations now return 400 IDEMPOTENCY_KEY_REQUIRED without the header, and every in-repo Go caller and script sends a fresh key.

## Tasks
1. RequireIdempotencyKey middleware, wiring on routes, Go callers keyed - 3321cc7
2. seed.sh and compose-smoke.sh keyed; smoke adds a 65s idle check that every replica is healthy - 972c962

## Verification
`go test -race ./internal/control/... ./tests/integration/...` and `go test ./tests/compose/...` pass; `bash -n` OK on both scripts. Live `make compose-smoke` not run (no Docker stack in this worktree).

## Deviations from Plan
- TestRequireIdempotencyKey lives in new file internal/control/idempotency_required_test.go rather than api_test.go (heredoc append was blocked by tooling); the shared `newMutatingRequest` helper was replaced by inline `Header.Set` after each mutating request plus a key in the `do` helper.
- concurrency_test.go untouched: it tests IdempotencyMiddleware on a standalone /test handler, not the gated routes.
- Integration test keys are added centrally in the doAuthRequest helper for all non-GET calls.
- seed.sh also sends a key on GETs (harmless).
- Dashboard callers (web/dashboard/src/components/*.tsx) are not in this plan's files and were not changed; they must send keys (expected from plan 10-06).

## Known Stubs
None.

## Self-Check: PASSED
