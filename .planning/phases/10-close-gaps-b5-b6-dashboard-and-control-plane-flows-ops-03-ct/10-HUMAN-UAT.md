---
status: partial
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
source: [10-VERIFICATION.md]
started: 2026-10-11T06:25:23Z
updated: 2026-10-11T06:25:23Z
---

## Current Test

[awaiting human testing]

## Tests

### 1. Compose smoke with idle window and control-plane restart (B5 / CR-01)
expected: `make compose-smoke` passes including the 65s idle check; after restarting the control plane, gateways reconnect and stay "converged" (never flip to "partitioned").
result: [pending]

### 2. Dashboard end-to-end over plain HTTP (B6 / CR-03 / OPS-02 / OPS-03)
expected: From both http://localhost and http://<host-ip>:8084, Policy Studio renders; author → validate → publish succeeds; convergence view shows the new version; quarantine/revoke/rollback succeed with no crypto.randomUUID errors in the console.
result: [pending]

## Summary

total: 2
passed: 0
issues: 0
pending: 2
skipped: 0
blocked: 0

## Gaps
