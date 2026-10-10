---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
plan: 07
subsystem: testing
tags: [compose-smoke, jti-revocation, spiffe-quarantine, lint]
requires: [09-01, 09-02, 09-03, 09-04, 09-05]
provides:
  - "compose-smoke.sh DISK pre-flight, REV-03 JTI and WORKLOAD QUARANTINE sections with per-replica timing"
  - "hermetic lint for the new sections"
key-files:
  modified:
    - scripts/compose-smoke.sh
    - tests/compose/smoke_script_test.go
requirements-completed: [REV-03, AUTH-04, OPS-01]
duration: 15min
completed: 2026-10-10
---

# Phase 9 Plan 07: Smoke script jti revoke and workload quarantine Summary

Live smoke now proves per-replica jti revocation, workload SPIFFE quarantine (encoded path and body form) on :9443 and jti-bearing audit rows, guarded by a statfs disk pre-flight and hermetic lint. The live run is plan 09-08 (not run here).

## Tasks
1. Disk pre-flight (docker info root + stat -f ratio, exit 3 above 0.85, `DISK SKIPPED: cannot measure`), SMOKE_DEV_PASSWORD, `dev_login`/`dev_token` with jq-built {username,password}, GATEWAYS mapfile and `replica_poll` helper (5s limit, prints `denied after Xs`). Commit 7699bb2.
2. Sections `=== REV-03 JTI revoke`, `=== WORKLOAD QUARANTINE (:9443)`, audit jti assertions (>=1), cleanup releases the workload id via the encoded path, header/A4 wording updated. Commit 937eae6.
3. Lint: TestSmokeScriptCoversJTIAndWorkloadQuarantine, TestSmokeScriptNoTokenValueEcho, SMOKE_DEV_PASSWORD added to credential-echo regex. Commit: see git log (test(09-07)).

## Deviations from Plan
None of substance. New sections are placed after A4 and before GRACE so the A4 developer quarantine is already released. The recovery poll (expects 200) reuses the helper and so also prints "denied after" in its label wording. shellcheck was not available; only `bash -n` and Go lint were run. No docker commands were executed.

## Verification
`bash -n scripts/compose-smoke.sh`; `go test -count=1 ./tests/compose/` pass.

## Self-Check: PASSED
