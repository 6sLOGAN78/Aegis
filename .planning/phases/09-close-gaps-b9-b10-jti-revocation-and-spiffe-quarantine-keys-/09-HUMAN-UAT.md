---
status: complete
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
source: [09-VERIFICATION.md]
started: 2026-10-11T00:00:00Z
updated: 2026-10-11T03:25:00Z
---

## Current Test

[testing complete]

## Tests

### 1. Live smoke re-run on rebuilt images (after the code-review fixes)
expected: With the Docker root statfs ratio at or below 0.85, for each of mvp, hardened and distributed run `docker compose -f deployments/compose/docker-compose.<p>.yml down -v`, then `up -d --build --wait`, then `bash scripts/compose-smoke.sh <p>`. Every REV-03 JTI and WORKLOAD QUARANTINE line PASSes, each replica's `denied after` is at most 5s, and the audit jti assertions PASS. The 09-08 run used images from HEAD 7671781, before fixes CR-01, WR-01, WR-02 and WR-06 (815cf71..4cec512), which touch these exact paths.
result: pass
evidence: "2026-10-11 03:14-03:21, HEAD d752bc4, images rebuilt 03:15 after the review fixes: smoke exit 0 with 0 hard failures on mvp, hardened and distributed; every REV-03 JTI and WORKLOAD QUARANTINE section PASS; per-replica deny 0.13-0.17s (distributed gateway-1/2/3); audit jti assertions PASS."

### 2. Decide what to do with the distributed AUDIT-TYPES suppression failures
expected: Either fix the harness, so that on multi-gateway profiles DENY1 and DENY2 go to one replica directly (as replica_poll does) and distributed exits 0, or accept the two failures as a known Phase 8 test-harness limit. The cause is that HAProxy round-robins the requests while governor suppression is per gateway process. This is not a Phase 9 regression.
result: pass
evidence: "Fixed the harness (d752bc4): AUDIT-TYPES DENY1/DENY2 now go to one replica via gw_probe_one. Distributed smoke exit 0; suppressed repeat and suppression summary (suppressed_count=1) PASS on all three profiles."

### 3. Integration tests against a live mvp stack
expected: With the mvp stack up, `go test -count=1 ./tests/integration/ -run 'TestMVPEndToEnd|TestBackendBypassPrevention'` passes. The tests now use the credentialed issuer login and have only been compile-checked.
result: pass
evidence: "On the live mvp stack (same run): go test ./tests/integration -run 'TestMVPEndToEnd|TestBackendBypassPrevention' ok; both PASS."

## Summary

total: 3
passed: 3
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps
