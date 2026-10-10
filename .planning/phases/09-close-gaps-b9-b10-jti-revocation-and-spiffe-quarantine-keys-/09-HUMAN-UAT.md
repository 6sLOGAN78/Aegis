---
status: partial
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
source: [09-VERIFICATION.md]
started: 2026-10-11T00:00:00Z
updated: 2026-10-11T00:00:00Z
---

## Current Test

[awaiting human testing]

## Tests

### 1. Live smoke re-run on rebuilt images (after the code-review fixes)
expected: With the Docker root statfs ratio at or below 0.85, for each of mvp, hardened and distributed run `docker compose -f deployments/compose/docker-compose.<p>.yml down -v`, then `up -d --build --wait`, then `bash scripts/compose-smoke.sh <p>`. Every REV-03 JTI and WORKLOAD QUARANTINE line PASSes, each replica's `denied after` is at most 5s, and the audit jti assertions PASS. The 09-08 run used images from HEAD 7671781, before fixes CR-01, WR-01, WR-02 and WR-06 (815cf71..4cec512), which touch these exact paths.
result: [pending]

### 2. Decide what to do with the distributed AUDIT-TYPES suppression failures
expected: Either fix the harness, so that on multi-gateway profiles DENY1 and DENY2 go to one replica directly (as replica_poll does) and distributed exits 0, or accept the two failures as a known Phase 8 test-harness limit. The cause is that HAProxy round-robins the requests while governor suppression is per gateway process. This is not a Phase 9 regression.
result: [pending]

### 3. Integration tests against a live mvp stack
expected: With the mvp stack up, `go test -count=1 ./tests/integration/ -run 'TestMVPEndToEnd|TestBackendBypassPrevention'` passes. The tests now use the credentialed issuer login and have only been compile-checked.
result: [pending]

## Summary

total: 3
passed: 0
issues: 0
pending: 3
skipped: 0
blocked: 0

## Gaps
