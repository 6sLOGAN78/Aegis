---
status: complete
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
source: [10-VERIFICATION.md]
started: 2026-10-11T10:30:00Z
updated: 2026-10-11T13:40:00Z
---

<!-- Re-verification after gap-closure plans 10-09..10-11. Prior UAT round (B5/B6/CR-01/CR-03 pass; cookie + Monaco CSP gaps) is in git history at 9254d41. -->

## Current Test

[testing complete]

## Tests

### 1. Cookie login and Monaco editor over plain HTTP (10-09, 10-11)
expected: On a rebuilt stack, at both http://localhost:8084/dashboard/ and http://<lan-ip>:8084/dashboard/ — login persists (no 401 after the 200 login); Monaco leaves "Loading...", shows the active policy source, and the console shows no CSP violations and no cdn.jsdelivr.net requests; publishing edited Rego succeeds.
result: pass
notes: Driven via Playwright on a freshly rebuilt distributed profile (3 gateways). LAN IP 192.170.9.86:8084 (isSecureContext=false) — login 200 then all calls 200; session survived page reloads. Monaco rendered and hydrated with the stored mvp-authz source; zero cross-origin resource loads, no CSP/jsdelivr console errors. Edited Rego (added "# uat-10 edit marker"), validate 200, save draft POST /policies 201, publish 200 → v12; all 3 gateways converged on v12 and the stored v12 source contains the marker. localhost:8084 — login 200 (/auth/me 200), editor rendered, no external resources, no CSP errors.

### 2. Stale gateway eviction on a live stack (10-10)
expected: A dead gateway record whose last heartbeat is older than 15m is absent from the convergence count; a gateway silent between 60s and 15m still shows as "partitioned".
result: pass
notes: gateway_acks holds 7 rows incl. 4 dead records from earlier stacks (last heartbeat 16m–8h old); /gateways and Cluster Topology list only gateway-1/2/3, convergence 3/3 (100%). Stopped gateway-3 for ~80s → listed as "partitioned", convergence 2/3 (67%); restarted afterwards.

### 3. Review-fix retest: draft preservation, policy-ID change, concurrent publish, stale count (WR-01..WR-04, SEC-01)
expected: Unsaved Rego survives tab switches and the 10s refetch; changing the policy ID with unsaved edits keeps them; a publish based on an outdated version is rejected (412) with a "newer version" banner, and "Load latest" lets the operator republish; topology shows how many stale replicas are hidden.
result: pass
notes: Rebuilt distributed stack at 850b88c, driven via Playwright on http://192.170.9.86:8084. WR-01: pasted marker on line 1, switched Topology→Simulator→Studio, waited 12s — marker still line 1. SEC-01: editing base v13; seed re-run published v14 as a second client; UI showed "Active ETag 14 / Editing Based On 13" + banner; publish → 412 with conflict message, v14 not overwritten; "Load latest" → base 14, publish 200 → v15 (clean, no marker). WR-02: unsaved edit kept after changing Policy Identifier. WR-03: topology shows "4 stale replicas hidden", convergence 3/3. Note: the rejected attempt still upserted its draft (POST /policies 201) before publish returned 412 — published snapshots unaffected.

## Summary

total: 3
passed: 3
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps
