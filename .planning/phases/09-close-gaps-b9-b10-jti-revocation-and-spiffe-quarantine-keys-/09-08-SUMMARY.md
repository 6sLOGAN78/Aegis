---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
plan: 08
subsystem: testing
tags: [compose-smoke, live, jti-revocation, spiffe-quarantine]
requires: [09-06, 09-07]
provides:
  - "Live evidence on mvp, hardened and distributed for REV-03 JTI revoke, workload SPIFFE quarantine (encoded path and body form) and jti-bearing audit rows"
key-files:
  modified: []
requirements-completed: [REV-03, AUTH-04, OPS-01]
duration: 40min
completed: 2026-10-10
---

# Phase 9 Plan 08: Live smoke on three profiles Summary

All Phase 9 live sections passed on all three compose profiles. On distributed, every replica denied within 0.55 s. Distributed also had 2 hard failures, but both are in the pre-existing Phase 8 AUDIT-TYPES suppression assertions, which assume a single gateway. They are not Phase 9 regressions.

## Tasks

1. **Disk checkpoint (human-action).** The statfs ratio was 0.8309 at the start, under the 0.85 gate. The operator approved the run. After the mvp run plus the hardened and distributed image builds, the ratio was 0.851–0.853, so the DISK pre-flight stopped hardened and distributed correctly with exit 3. With operator approval, `docker builder prune -f` freed 2.99 GB of build cache (ratio 0.8401). Hardened and distributed were then re-run without `--build`, on images built from HEAD 7671781 minutes earlier.
2. **Live smoke runs.** Each profile was taken down with `down -v`, brought up with `up -d --wait`, smoked, and taken down with `down -v` again.

| Profile | Smoke exit | Phase 9 sections | Per-replica deny times |
|---|---|---|---|
| mvp | 0 (0 hard failures) | all PASS | jti 0.27 s; workload path 0.13 s; unquarantine 0.12 s; body form 0.15 s |
| hardened | 0 (0 hard failures) | all PASS | jti 0.14 s; workload path 0.18 s; unquarantine 0.19 s; body form 0.18 s |
| distributed | 1 (2 hard failures, see below) | all PASS | jti 0.20 / 0.36 / 0.55 s; workload path 0.19 / 0.33 / 0.49 s; unquarantine 0.21 / 0.37 / 0.54 s; body form 0.16 / 0.32 / 0.49 s (gateway-1/2/3) |

The audit assertions passed on every profile:
- TOKEN_REVOKED denial rows carry the jti (1 row on mvp and hardened, 3 on distributed);
- the earlier allowed request carries the jti (2, 2 and 6 rows).

The body-form `POST /control/v1/quarantine` returned 200 on every profile. The deny times include `docker exec` latency.

## Deviations from Plan

- **Distributed exits 1, not 0.** The two failures:
  - `FAIL: AUDIT-TYPES suppressed repeat (expected 0 rows for the repeat request id, got 1)`
  - `FAIL: AUDIT-TYPES suppression summary (no row with suppressed_count=1 ...)`

  Root cause: `gw_probe` sends DENY1 and DENY2 to `GATEWAY_URL`, which on distributed is HAProxy with `balance roundrobin`. The governor's suppression state is per gateway process, so the identical repeat lands on another replica and is recorded as a new first denial. That is why the repeat has a row and the first gateway writes no summary. This Phase 8 section had only ever run live on single-gateway mvp. Phase 9 changed neither the governor nor this section.

  The fix belongs to the test harness: on multi-gateway profiles, send DENY1 and DENY2 to one replica directly, the way `replica_poll` does. Per the plan, the script was not weakened to make it pass.
- **Hermetic suite.** `go build`, `go vet` and `go test $(go list ./... | grep -v /tests/integration)` pass, with all 26 packages ok. The first full run had one timing flake: `benchmarks` `TestPolicyEngine_LatencyBudget` p99 at 3.1 ms against a 2 ms budget under parallel package load. Run alone it passed 3 of 3 times. Phase 9 touched no policy or benchmark code.
- **Cert files.** The orchestrator ran `make certs` before the first run, which regenerated the 16 tracked files under `deployments/certs/`. The live runs used that regenerated set consistently. After teardown, the committed versions were restored with `git checkout -- deployments/certs`. No cert change is committed.
- **Not re-run:** `tests/integration` (TestMVPEndToEnd, TestBackendBypassPrevention), which require Docker.

## Verification

Logs: `smoke-{mvp,hardened,distributed}.log`, `up-*.log` and `logs-*.log` in the session scratchpad.

Recheck commands:
- `grep -E 'FAIL|denied after' smoke-*.log`
- `bash scripts/compose-smoke.sh <profile>` on a running stack

## Self-Check: PASSED with one finding (distributed AUDIT-TYPES suppression assertions assume a single gateway)
