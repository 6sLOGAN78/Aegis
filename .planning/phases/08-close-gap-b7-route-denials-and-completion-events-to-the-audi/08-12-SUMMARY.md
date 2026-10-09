---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 12
subsystem: verification
tags: [regression, validation-map, requirements, audit, AUD-03, AUD-04]
requires: ["08-11"]
provides:
  - "Final hermetic regression on HEAD 69f9daf, filled 08-VALIDATION.md, evidence-derived AUD-03 / AUD-04 verdicts"
affects: []
key-files:
  created: []
  modified:
    - .planning/phases/08-close-gap-b7-route-denials-and-completion-events-to-the-audi/08-VALIDATION.md
    - .planning/REQUIREMENTS.md
requirements-completed: []
completed: 2026-10-09
---

# Phase 8 Plan 12: Final regression, validation map, AUD-03 / AUD-04 verdicts

Both requirements meet the plan's mechanical verdict rules and are marked SATISFIED for the MVP profile, with the residual limits listed below. No code was changed in this plan. `requirements-completed` is deliberately left empty in the frontmatter: no `requirements mark-complete` or `phase.complete` command was run; the only edits to REQUIREMENTS.md are the four lines shown below.

## Phase result

### What is now true
- B7 is closed in code and live for the MVP profile. On the rebuilt stack (HEAD d400d0b, whose code is identical to HEAD 69f9daf: `git diff --stat d400d0b HEAD -- cmd internal deployments scripts migrations tests` is empty), the smoke script exited 0 with every AUDIT-TYPES assertion passing: allowed request -> completion row (200, duration_ms > 0) and a decision row (status 0) with distinct event_ids and the same request_id; policy denial -> exactly one denial row (403, deny, FORBIDDEN, duration > 0); the identical repeat produced no row and a suppression summary row with suppressed_count=1 was written; the unauthenticated request produced a denial row (401, anonymous); the A4 quarantine denial landed as a 403 PRINCIPAL_QUARANTINED row.
- Dedupe replay (count unchanged at 25) and rotation (300 requests, segment seq 1 -> 5) passed live; graceful stop exited 0 with 30 of 30 completion rows present. Final database total before teardown: 686 rows = 341 completion + 341 decision + 4 denial.
- The pre-forward path is unchanged per D-09: the `AppendPreForward` body hash equals 94cbd2f and the two call sites in cmd/gateway/main.go have 0 diff lines.

### What is not
- No live failure is unresolved. There was no ENVIRONMENT failure and no Phase 8 defect in the live run (08-11-SUMMARY.md: repair cycles 0). The only environmental note is that `go test` for pre-existing packages was run under `TMPDIR=/dev/shm/aegis-gotmp` as instructed; `go test -race -count=1 ./internal/audit/` also passed once without it (disk `df` 89%, spool gate ratio about 84), so no saturation failure occurred in this plan.
- Only the MVP profile was run live. The hardened and distributed profiles were not run live (lint and `compose config -q` only).
- A crash between the Postgres insert and the cursor save is unit-level evidence only (`TestAuditWorker_RestartRecovery`).
- The shutdown drain of a non-empty queue is proven hermetically only (`TestPipelineShutdown`, `TestE2EShutdownFlushesQueuedRecords`); the live stop check ran sequential, already-finished requests.
- The review-fix fault paths (disk-error recovery, summary carry, failed-first-denial, 503 burst after a disk error), the 502 upstream-error path and the hard-limit band are covered by regression tests, not exercised live.
- The live DEDUPE check does not distinguish a restart-driven replay from the worker's in-process cursor reload (08-11 assumption 10).
- The live-run disk precondition used the spool gate's own ratio (statfs blocks minus bfree, <= 85.0 and >= 10 GB available; orchestrator amendment, commit 7896cac) instead of the earlier flat 80 percent rule. `df` Use% read 88% before and 90% after the build while the gate ratio stayed 83.1 to 84.5. 08-VALIDATION.md now states the amended rule.
- Out of scope and untouched: jti in the demo issuer, percent-encoded SPIFFE quarantine keys, B8, B5/B6, B2, multi-directory audit worker, extracting the gateway pipeline from main(), dashboard display of the new rows, Phase 7 review leftovers.

## Task 1: final regression (HEAD 69f9daf, 2026-10-09)

| Gate | Result |
|------|--------|
| `go build ./...` | pass |
| `go vet ./...` | pass |
| phase-scoped gofmt gate against 94cbd2f | prints nothing (pass); the 17 unrelated unformatted files untouched |
| `go test -race -count=1 $(go list ./... \| grep -v /tests/integration)` under `TMPDIR=/dev/shm/aegis-gotmp` | exit 0, 26 packages `ok`, no failure. `benchmarks` passed inside the full suite (3.51s) and again in isolation (`-run TestPolicyEngine_LatencyBudget`, ok), so the flake did not occur |
| `go test -race -count=3 ./internal/audit/` | ok (10.0s) |
| `go test -count=1 ./tests/compose/ ./cmd/gateway/` | ok, ok |
| 27 named validation tests (`-run`, verbose) | all `--- PASS`, 0 FAIL, 0 SKIP |
| `docker compose -f ... config -q` for mvp, hardened, distributed | all three exit 0 |
| `bash -n scripts/compose-smoke.sh` | ok |
| `opa test policies/rego policies/tests` | PASS 8/8 |
| D-09 `AppendPreForward` body sha256 vs `git show 94cbd2f:internal/audit/spool.go` | identical (`8bd6714e...3211f`) |
| `git diff -U0 94cbd2f -- cmd/gateway/main.go \| grep '^[+-]' \| grep -c AppendPreForward` | 0; call sites still at main.go lines 556 and 866 |
| `docker ps -a \| grep -c '^compose-'`, `docker volume ls \| grep -c '^compose_'` | 0 and 0 |

The fix commits after 08-09 (7dc9e65, 94da2bc, 677281a, 9a0ecac, eb91e6a, 05f1dcf) changed internal/audit and internal/config after the 08-10 hermetic gate; this regression ran on top of them and is green, and the live run exercised the same code.

### Validation map
08-VALIDATION.md: 26 rows (24 original rows with real task IDs, plan and wave, plus 2 added rows: live graceful stop and compose/wiring lint), all `green`, no TBD or pending cells. Name mapping: no test is named exactly `TestNormalize`; coverage is the `TestNormalize_*` family plus `TestAuditWorker_NormalizesPoison` and `TestE2EPoisonNormalized`. `TestE2E` is a family of 12 tests (`TestE2EAllowedProducesDecisionAndCompletion`, `TestE2EWorkerDeliversAllKinds`, `TestE2ESuppressionAndSummary`, `TestE2EShutdownFlushesQueuedRecords`, and so on). All other row names exist exactly. Wave 0 files verified with `test -f`; `wave_0_complete: true`, `nyquist_compliant: true` (explained under the table), `status: complete`. A "Residual evidence gaps" section was added.

## Task 2: Requirement verdicts

| Requirement | Verdict | Evidence relied on (verbatim from 08-11-SUMMARY.md unless noted) |
|-------------|---------|----------------------------------------------------------------------|
| AUD-04 | SATISFIED (MVP profile) | `PASS: AUDIT-TYPES allowed request has a completion row (200, duration_ms>0) and a decision row (status 0) with distinct event_ids`; Postgres rows show the pair with the same request id bf5a1e60 (completion allow 200, 7.761 ms; decision allow 0, 0 ms; different event ids 0bd480e4 / 48a8d5a7); `PASS: AUDIT-TYPES denied request has exactly one denial row (403, deny, reason set, duration_ms>0, FORBIDDEN)`; `PASS: AUDIT-TYPES unauthenticated request has a denial row (401, anonymous)`; `PASS: AUDIT-TYPES gateway counter aegis_audit_records_written_total{kind=completion} is 11`. Hermetic, run in this plan: `TestWrapUpstreamErrorHandler`, `TestE2E*` (all PASS). |
| AUD-03 | SATISFIED (MVP profile, with limits) | Delivery of all kinds: allowed pair line above (decision and completion), denial line above, `PASS: AUDIT-TYPES suppression summary row written with suppressed_count=1 (migration 000003 and sweeper live)` (a PASS, not SKIPPED); `PASS: DEDUPE audit-worker cursor deleted, worker restarted, replay absorbed (count unchanged at 25)`; `PASS: ROTATION 300 requests delivered across >=2 segment rotations (seq 1 -> 5)` (not SKIPPED; 300 of 300 returned 200). Smoke exit 0, 0 hard failures. Hermetic: `TestE2EWorkerDeliversAllKinds`, `TestE2ESuppressionAndSummary` PASS. |

Residual limits that apply to both verdicts: only the MVP profile was run live; hardened and distributed were not. For AUD-03 additionally: crash between insert and cursor save is unit-level only (`TestAuditWorker_RestartRecovery`); the DEDUPE check does not tell a restart-driven replay apart from an in-process cursor reload; shutdown-drain-under-load is hermetic only; review-fix fault paths are regression-tested, not exercised live. These are limits on strength of evidence, not failed or skipped assertions; if the orchestrator or user holds AUD-03 to a stricter standard (for example needing the crash case or the other profiles live), AUD-03 is the one to revert.

### REQUIREMENTS.md edits (untracked file, not staged, not committed)
Only these four lines changed (verified with `diff` against a copy taken first; nothing else differs):

| Line | Before | After |
|------|--------|-------|
| 53 | `- [ ] **AUD-03**: Asynchronous audit worker delivers spooled records to PostgreSQL with at-least-once batching and deduplication` | `- [x] **AUD-03**: ...` (rest unchanged) |
| 54 | `- [ ] **AUD-04**: Completion audit events record backend HTTP status, request duration, and error codes` | `- [x] **AUD-04**: ...` (rest unchanged) |
| 136 | `\| AUD-03 \| Phase 3 \| Gap closure \|` | `\| AUD-03 \| Phase 3 \| Complete \|` |
| 137 | `\| AUD-04 \| Phase 1 \| Gap closure \|` | `\| AUD-04 \| Phase 1 \| Complete \|` |

## Reconciliation for the orchestrator

Every plan in this phase lists AUD-03 and AUD-04 in its frontmatter, so `gsd-sdk query phase.complete` will auto-tick both. The boxes legitimately ticked are exactly the ones marked SATISFIED above: **AUD-03 and AUD-04 may both stay ticked** (checkbox `[x]` and traceability row `Complete`), as of this plan's edit. If phase completion or the verifier finds either not earned, revert that requirement's checkbox to `[ ]` and its traceability row to `Gap closure` before committing. REQUIREMENTS.md is untracked user work: this plan did not stage or commit it, and the orchestrator should decide how to commit it. ROADMAP progress is updated through `roadmap.update-plan-progress` below.

## Carried tech debt (not fixed)

Info findings of 08-REVIEW.md, none fixed (08-REVIEW-FIX.md fixed CR-01 and WR-01..WR-07 only):
- IN-01 `internal/audit/logger.go` panic and hijack paths record a fabricated HTTP status (200 for an aborted request or a 101 upgrade).
- IN-02 expensive work (UUID, Normalize, stdout log line, event build for allowed responses) happens before the unauthenticated cap.
- IN-03 `internal/audit/committer.go`: `SubmitDenial` timeout counts a dropped denial that is later still written and counted as written (double count).
- IN-04 `cmd/gateway/main.go`: `AEGIS_DRAIN_TIMEOUT` unbounded; a stuck fsync can make `spool.Close()` block until the 45s grace period (exit 137).
- IN-05 `scripts/compose-smoke.sh`: ROTATION does not fail on non-200 responses (it was 0 in the live run, so the PASS was not weak this time); the suppressed-repeat check does not also assert the suppression counter; `TestSmokeScript*` are substring lints.
- IN-06 `internal/telemetry/metrics.go`: `RecordAuditSuppressed`/`Dropped`/`Written` trust callers for the closed label set.

## Deviations from Plan

- [Rule 3 - bookkeeping] The 08-VALIDATION.md map was extended from 24 to 26 rows (added live graceful stop and compose/wiring lint) so that tasks 08-02, 08-03, 09-03 and 11-02 appear with real statuses. No existing row was removed or weakened.
- The 80 percent live-disk rule in 08-VALIDATION.md's environment note was replaced by the spool-gate-ratio rule (matches the orchestrator amendment, commit 7896cac).
- `nyquist_compliant` and `wave_0_complete` were set to true; every row has an automated command and a recorded passing result, and all Wave 0 files exist.
- Otherwise none. No code or test was changed; no Docker mutating command was run; no REQUIREMENTS.md line other than the four was touched.

## Known Stubs

None.

## Threat Flags

None.

## Self-Check: PASSED

- 08-VALIDATION.md has 0 `| TBD |` and 0 `⬜ pending` cells and contains `Residual evidence gaps`, `TestAuditWorker_RestartRecovery`, `not run live`.
- REQUIREMENTS.md diff against the pre-edit copy shows exactly lines 53, 54, 136, 137; file remains untracked.
- Regression results above were observed in this session on HEAD 69f9daf; `/dev/shm/aegis-gotmp` removed after the runs.
