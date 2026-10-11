---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
fixed_at: 2026-10-11T12:00:00Z
review_path: .planning/phases/10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct/10-REVIEW.md
iteration: 1
findings_in_scope: 6
fixed: 6
skipped: 0
status: all_fixed
---

# Phase 10: Code Review Fix Report

**Fixed at:** 2026-10-11T12:00:00Z
**Source review:** .planning/phases/10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct/10-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 6 (WR-01..WR-05 plus SEC-01 from the background security scan)
- Fixed: 6
- Skipped: 0
- Info findings (IN-01..IN-06) were out of scope (fix_scope = critical_warning). A handler-level stale-filter test was added as part of WR-03, which also covers IN-02.

Verification: `npx tsc -b` passes, `go vet ./internal/control/ ./tests/...` passes, `go test ./internal/control/ -count=1` passes, `go build ./...` passes. `npm run build` regenerated `web/dashboard/dist`, and `grep -r jsdelivr web/dashboard/dist` returns nothing.

## Fixed Issues

### WR-01: Remount hydration overwrites the parent's unsaved candidate Rego when switching tabs

**Files modified:** `web/dashboard/src/App.tsx`, `web/dashboard/src/components/PolicyStudio.tsx`
**Commit:** d39501f
**Applied fix:** `App` now passes `candidateRego` into `PolicyStudio`. The editor starts from that value. If a candidate already exists, `userEditedRef` starts as true, so a remount no longer replaces the draft with the server copy.
**Status:** fixed: requires human verification (state/logic change)

### WR-02: Hydration is not tied to `policyId`

**Files modified:** `web/dashboard/src/components/PolicyStudio.tsx`, `internal/control/policy_handler.go`
**Commit:** 93815b3
**Applied fix:** The boolean `hydratedRef` is replaced by `hydratedForRef` (keyed by policy ID). Changing the Policy Identifier now re-hydrates the editor unless the operator has unsaved edits. Server-side per-policy filtering was not added because snapshots are fleet-wide. Instead, `HandleGetPolicyVersions` and the client both carry comments stating that the endpoint returns global snapshot history.
**Status:** fixed: requires human verification (state/logic change)

### WR-03: Long-partitioned gateways disappear from the convergence denominator

**Files modified:** `api/openapi/control-v1.yaml`, `pkg/api/control/v1/types.gen.go` (regenerated with oapi-codegen v2.5.0), `internal/control/gateway_handler.go`, `internal/control/gateway_handler_test.go`, `web/dashboard/src/api/types.ts`, `web/dashboard/src/components/ClusterTopology.tsx`
**Commit:** c48feb0
**Applied fix:** Added a required `hidden_stale_count` field to `GatewayListResponse`. The handler counts the stale records it hides. ClusterTopology shows an amber "N stale replica(s) hidden" note. When hidden replicas exist, the status reads "Listed Replicas Synchronized (N stale hidden)" instead of "Fully Synchronized". As the orchestrator requested, hidden replicas still do not count toward the convergence denominator. A new handler test, `TestGatewayListHidesStaleButCountsThem`, checks that a 20-minute APPLIED record is hidden and counted while a 20-minute REJECTED record stays listed. This also covers IN-02.

### WR-04: REJECTED gateways are never stale

**Files modified:** `internal/control/gateway_handler.go`, `internal/control/gateway_handler_stale_test.go`
**Commit:** b9b8ed6
**Applied fix:** REJECTED records now have a finite cutoff: `rejectedStaleAfter = max(24h, 4 * staleAfter)`. This is far beyond the normal stale threshold, so recent failure signals stay visible as threat T-10-10-03 requires. When a rejecter does age out, it is reported in `hidden_stale_count`, never dropped silently. Boundary tests cover 23h and 25h with the default threshold, and 30h and 33h with an 8h threshold.
**Status:** fixed: requires human verification (threshold choice is a product/security trade-off)

### WR-05: Invalid or too-small `AEGIS_GATEWAY_STALE_AFTER` silently replaced with 15m

**Files modified:** `internal/control/gateway_handler.go`, `internal/control/api.go`, `internal/control/gateway_handler_stale_test.go`
**Commit:** 662991f
**Applied fix:** `gatewayStaleAfterFromEnv(getenv)` now runs once in `NewAPIServer` and stores the result on `APIServer.gatewayStaleAfter`. It logs a WARNING when it rejects a value. The handler uses the stored value, and falls back to the default when it is zero, so `&APIServer{}` built directly in tests still works. Rejected values still fall back to the default of 15m rather than being clamped, so the tested behaviour is unchanged.

### SEC-01 (background security scan): Optimistic-concurrency bypass via live `currentETag`

**Files modified:** `web/dashboard/src/App.tsx`, `web/dashboard/src/components/PolicyStudio.tsx`
**Commit:** 7540a94
**Applied fix:**
- The edit session now records a base version. It is set when the editor hydrates, on the first edit of an editor that was never hydrated, or once versions first load for a session that already has edits. It is never moved forward by the 10-second refetch.
- The base version is reset after an explicit "Load latest" reload, after re-hydration for a new policy ID (WR-02), and after a successful publish.
- The base version lives in `App` next to `candidateRego`, so it survives remounts (WR-01).
- Publish sends `If-Match` set to the base ETag, so the server returns 412 if another operator published in between.
- The rollback ETag is fixed when the rollback dialog opens.
- The UI shows the live "Active Snapshot ETag" and the "Editing Based On ETag" separately, plus a "Newer version vN published after your edit began" banner with a "Load latest" button. The 412 message tells the operator to reload.
**Status:** fixed: requires human verification (state/logic change; the dashboard has no unit-test harness)

### Build artifact

**Commit:** 850b88c — rebuilt `web/dashboard/dist` with `npm run build` and removed the stale hashed assets. Confirmed there is no `jsdelivr` reference in dist.

---

_Fixed: 2026-10-11T12:00:00Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
