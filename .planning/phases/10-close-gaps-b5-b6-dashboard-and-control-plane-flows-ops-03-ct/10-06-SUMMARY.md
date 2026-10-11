---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 06
subsystem: control-plane-api
tags: [openapi, drift-test, idempotency]
requires: [10-05]
provides: [openapi-drift-guard, strict-idempotency-contract]
affects: [api/openapi/control-v1.yaml, pkg/api/control/v1/types.gen.go]
tech-stack:
  added: [gopkg.in/yaml.v3 (promoted from indirect to direct)]
key-files:
  created: [internal/control/openapi_drift_test.go]
  modified: [api/openapi/control-v1.yaml, pkg/api/control/v1/types.gen.go, go.mod]
requirements-completed: [OPS-01, OPS-02]
metrics:
  completed: 2026-10-11
---

# Phase 10 Plan 06: OpenAPI drift guard and strict contract Summary

TestOpenAPIDrift (chi.Walk vs parsed yaml paths) plus spec fill-in: all 25 /control/v1 operations documented, Idempotency-Key `required: true` on exactly the 9 key-gated ops.

## Tasks
1. Drift test (RED confirmed: 6 missing ops, optional key, no 422) then spec fill-in - commit cffae81
   - Added login, logout, me, policies/simulate, snapshots/rollback, audit/events alias; LoginRequest/LoginResponse schemas; publish 422.
   - Test also asserts non-gated ops do not reference IdempotencyKey.
2. Regenerated types.gen.go via scripts/generate.sh (second run is a no-op) - separate chore commit.

## Verification
- go test ./internal/control -run TestOpenAPIDrift: 3 pass; go build ./...: ok; go test -race control/snapshot: ok.
- make test-full: all green except the known pre-existing TestBackendBypassPrevention (curl exit 58).
- `npm run build` not run: web/dashboard has no node_modules in the worktree and no dashboard files changed.
- go.mod diff is only the yaml.v3 indirect-to-direct move; go.sum unchanged.

## Deviations
- Logout documented as 200 with `{status: logged_out}` to match handler (not 204).
- Manual UAT (idle >60s, dashboard publish) not performed.

## Self-Check: PASSED
