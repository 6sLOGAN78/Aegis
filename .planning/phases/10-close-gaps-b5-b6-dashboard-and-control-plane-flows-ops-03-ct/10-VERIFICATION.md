---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
verified: 2026-10-11T13:00:00Z
status: human_needed
score: 22/22 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: human_needed
  previous_score: 19/19
  gaps_closed:
    - "Session cookie Secure flag configurable (plan 10-09); demo compose opts out"
    - "Stale gateway records omitted from convergence list (plan 10-10)"
    - "Monaco bundled locally, no CDN (plan 10-11)"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "Open http://localhost:8084/dashboard/ and http://<lan-ip>:8084/dashboard/ (rebuilt stack); log in"
    expected: "Login persists (no 401 after 200); Monaco editor leaves 'Loading...', shows active policy source, no CSP console errors, no request to cdn.jsdelivr.net; publish edited Rego succeeds"
    why_human: "Needs a running browser and stack; CSP and cookie behaviour is browser-enforced"
  - test: "Run stack with a dead gateway record older than 15m"
    expected: "Dead gateway absent from convergence denominator; silent 60s-15m gateways still partitioned"
    why_human: "Needs live stack with stale data"
---

# Phase 10 Verification Report (re-verification after 10-09, 10-10, 10-11)

**Goal:** Dashboard author -> validate -> publish -> swap works end to end; convergence shows healthy gateways as converged.
**Status:** human_needed (all automated checks pass)

## Live-UAT gap closure

| UAT gap | Plan | Status | Evidence |
|---|---|---|---|
| Insecure-cookie login on plain HTTP | 10-09 | VERIFIED (code) | auth.go: `Secure: h.secureCookie` on login and logout; `NewAuthHandler` defaults true; `cookieSecureFromEnv` (only false/0 disables); api.go:63-71 wires it; `AEGIS_COOKIE_SECURE=false` in docker-compose.mvp.yml:42 and distributed.yml:78 only (hardened untouched). Control tests pass. |
| Monaco blocked by CSP | 10-11 | VERIFIED (build), browser check pending | monaco-setup.ts calls `loader.config({ monaco })` with a Vite `?worker` editor worker; imported in PolicyStudio.tsx:2; no "jsdelivr" string in web/dashboard/dist; `tsc -b` clean; CSP in api.go unchanged (worker-src 'self' blob:, script-src includes unsafe-eval blob:). Editor hydrates from versions[0].source_rego once, guarded by userEditedRef. |
| Stale gateway denominator | 10-10 | VERIFIED | gateway_handler.go: `gatewayStaleAfter()` (default 15m, values <=60s fall back), `isStaleReplica` exempts REJECTED, applied in list loop (l.128); 60s partitioned rule retained. Tests pass. |

## Regression of prior truths
Earlier 19 truths (B5 heartbeat acks, B6 save/publish, CTRL-01 422, CTRL-02 mappings, OPS-01/02 drift and idempotency) are unchanged by these plans. `go build ./...` ok; `go test -count=1 ./internal/control/... ./internal/snapshot/...` pass. Orchestrator reports full `go test ./...` passes except the environmental TestBackendBypassPrevention subtest, which also fails on base 0087394. Prior live UAT confirmed B5 (CP restart, gateways stay healthy) and B6 (publish 200 on localhost).

## Requirements coverage
OPS-03, CTRL-01, CTRL-02, CTRL-05, OPS-01, OPS-02 all appear in REQUIREMENTS.md mapped to this phase (gap closure) and are claimed across plans 10-01..10-11. No orphaned IDs. Checkboxes for CTRL-01/02/05, OPS-02, OPS-03 in REQUIREMENTS.md remain unticked; update after human verification.

## Review warnings (10-REVIEW.md, 0 blockers)
- WR-01 (remount hydration overwrites unsaved candidate Rego on tab switch) and WR-02 (hydration not keyed to policyId): UX defects in editor hydration; do not defeat the must-have (editor loads latest source, publish works for the default policy). Should be fixed.
- WR-03 (long-dead partitioned gateways vanish from denominator) and WR-04 (REJECTED never stale): by design per plan trade-off; WR-03 is a fail-open observability risk, accepted at 15m default.
- WR-05 (silent fallback of invalid STALE_AFTER): minor.
None are blockers.

## Anti-patterns
No TBD/FIXME/XXX introduced in the reviewed files. Fragile Vite CDN-strip plugin (IN-05) is defense in depth only.

## Human verification required
1. Browser check on localhost and LAN IP: login persists, Monaco renders with no CSP errors, edit-and-publish works.
2. Live stale-gateway eviction behaviour.

Note: Monaco rendering could not be confirmed without a browser; the dist bundle and wiring are correct statically.

_Verifier: Claude (gsd-verifier)_
