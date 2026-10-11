---
status: partial
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
source: [10-VERIFICATION.md]
started: 2026-10-11T10:30:00Z
updated: 2026-10-11T10:30:00Z
---

<!-- Re-verification after gap-closure plans 10-09..10-11. Prior UAT round (B5/B6/CR-01/CR-03 pass; cookie + Monaco CSP gaps) is in git history at 9254d41. -->

## Current Test

[awaiting human testing]

## Tests

### 1. Cookie login and Monaco editor over plain HTTP (10-09, 10-11)
expected: On a rebuilt stack, at both http://localhost:8084/dashboard/ and http://<lan-ip>:8084/dashboard/ — login persists (no 401 after the 200 login); Monaco leaves "Loading...", shows the active policy source, and the console shows no CSP violations and no cdn.jsdelivr.net requests; publishing edited Rego succeeds.
result: [pending]

### 2. Stale gateway eviction on a live stack (10-10)
expected: A dead gateway record whose last heartbeat is older than 15m is absent from the convergence count; a gateway silent between 60s and 15m still shows as "partitioned".
result: [pending]

## Summary

total: 2
passed: 0
issues: 0
pending: 2
skipped: 0
blocked: 0

## Gaps
