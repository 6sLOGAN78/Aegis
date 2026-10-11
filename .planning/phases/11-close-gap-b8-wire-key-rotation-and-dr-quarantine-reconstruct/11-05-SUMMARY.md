---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 05
subsystem: control-plane security state
tags: [rev-03, write-through, reconstruct, probes, dr]
requires: ["11-01", "11-02"]
provides:
  - control.SecurityStateRepository, control.SecurityState (Reconstruct, Reconcile, Run, Ready)
  - NewSecurityState options WithReconcileInterval, WithMarkerCheckInterval, WithClock, WithPinger, WithSecurityStateMetrics
  - APIServer.SetSecurityRepo / SetSecurityState, GET /livez and /readyz
affects: [11-07, 11-14]
key-files:
  created:
    - internal/control/securitystate.go
    - internal/control/securitystate_test.go
    - internal/control/writethrough_test.go
    - internal/control/probe_test.go
    - tests/dr/securitystate_dr_test.go
  modified:
    - internal/control/api.go
    - internal/control/quarantine_handler.go
    - tests/dr/dr_test.go
decisions:
  - "Reconcile snapshots Redis quarantines before listing the repo so a key written after its PG row is never pruned."
  - "Observing the marker present marks backfill done, so a restarted control plane can prune stray keys without re-running backfill."
  - "Backfill and reconcile errors (including ListRevokedJTIs TTL read failures) are returned, never swallowed; a failed backfill leaves the marker unwritten."
metrics:
  tasks: 3
  completed: 2026-10-12
requirements: [REV-03]
---

# Phase 11 Plan 05: SecurityState service, write-through, probes Summary

Postgres-first write-through for quarantine, unquarantine and jti revoke, a SecurityState service that restores Redis from Postgres with remaining TTLs and writes the marker last, and unauthenticated /livez and /readyz on the control plane.

## Commits
- 0190c25: SecurityState service and unit tests
- cea4ac4: write-through handlers, probes, tests
- 1e0bedc: DR drill repointed at SecurityState

## Behavior notes
- Resurrection race and partial-restore window are bounded by the reconcile interval (30s default); Plan 14 should copy this into the runbook.
- /readyz bodies are a closed enum: SECURITY_STATE_MISSING or DEPENDENCY_FAILURE. With no SecurityState set it returns 200 (legacy hermetic tests only; Plan 07 must always call SetSecurityState).
- Plan 07 must call SetSecurityRepo, SetSecurityState, call Reconstruct at startup and run `go ss.Run(ctx)`.

## Deviations from Plan
- [Rule 3] Added `tests/dr/securitystate_dr_test.go` (in-memory repo for the DR drill); the control package's test fake is not importable from another package.
- Reconcile prunes stray quarantine keys only. There is no jti delete method on revocation.Store and store.go is outside this plan's files, so stray jti keys are not pruned (they expire by TTL).
- /readyz and /livez paths are not in the OpenAPI spec; TestOpenAPIDrift passed without exemptions.

## Verification
`go vet ./internal/control ./tests/dr` and `go test ./internal/control ./tests/dr ./internal/revocation -race -count=1` pass.

## Known Stubs
None.

## Self-Check: PASSED
