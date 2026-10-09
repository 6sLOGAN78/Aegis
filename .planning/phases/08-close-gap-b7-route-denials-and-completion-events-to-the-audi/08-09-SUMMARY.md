---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 09
subsystem: live-smoke
tags: [smoke, compose, audit, dedupe, rotation, lint]
requires: ["08-07", "08-08"]
provides:
  - "scripts/compose-smoke.sh AUDIT-TYPES, DEDUPE and ROTATION sections plus gw_probe/psql_q/wait_q_eq/cursor_field helpers"
  - "tests/compose/smoke_script_test.go hermetic lint (4 tests)"
affects: [08-11, 08-12]
tech-stack:
  added: []
  patterns: ["request-id keyed audit_events assertions", "window guard for suppression keys"]
key-files:
  created:
    - tests/compose/smoke_script_test.go
  modified:
    - scripts/compose-smoke.sh
key-decisions:
  - "The suppression key excludes the path, so a window guard (reads the age of the newest DENIED_DEVELOPER_ADMIN_FORBIDDEN denial row) waits out a previous window instead of using a unique path"
  - "SKIPPED (never FAIL) when the immediate repeat did not return 403; the summary row is then proven only by hermetic tests"
  - "jq runs on the host for cursor parsing because the audit-worker image has no jq"
requirements-completed: []
duration: ~35min
completed: 2026-10-09
---

# Phase 8 Plan 09: Live smoke audit evidence Summary

The smoke script now captures request ids for an allowed request, a policy denial, an immediate identical repeat and an unauthenticated request and asserts the resulting audit_events rows by request_id, then proves dedupe-on-replay per worker and (optionally) multi-segment rotation. Nothing was run against Docker; AUD-03 and AUD-04 are not marked complete (plan 08-12 decides from the live run).

## Tasks and commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Request-id capture and AUDIT-TYPES assertions | 7346fd8 |
| 2 | DEDUPE and ROTATION sections, honest header | 8c5e649 |
| 3 | Hermetic lint of the smoke script | 6e0396b |

## What changed

- Helpers: `gw_probe TOKEN PATH` (prints `<status> <request_id>` from the X-Request-ID response header via `curl -D`), `valid_rid` (UUID shape check before any id is interpolated into SQL), `psql_q SQL` (SQL on stdin, CR/blank/whitespace trimmed, stderr left visible), `wait_q_eq SQL WANT SECS` (sets `LAST_Q`), `cursor_field WORKER FIELD` (host jq).
- B1: same PASS/FAIL text as before; records ALLOW_ID, a window guard, DENY1 (403 expected) with DENY2 immediately after (no sleep, no PASS/FAIL of its own), then UNAUTH (`invalid.bearer.token`, 401 expected, new PASS line "B7 unauthenticated request returned 401").
- AUDIT-TYPES (between AUDIT and A4): allowed pair (`completion|allow|200|1` and `decision|allow|0|0`, 2 distinct event_ids), one denial row (`denial|deny|403|1|1|FORBIDDEN`, filtered to `suppressed_count is null` because the summary row shares the first denial's request_id), repeat has 0 rows, summary row with `suppressed_count=1` (poll 2x window + 30s), unauthenticated row `denial|deny|401|anonymous`, gateway counters (`written_total{kind="completion"}` > 0, `{kind="denial"}` > 0, `aegis_audit_degraded 0`).
- DEDUPE: per worker, quiesce (3 equal samples), require cursor offset > 0, `rm -f` the cursor, `dc restart`, wait for the cursor to reappear, wait 5s, PASS only if the count is unchanged and the new segment name sorts >= the old one.
- ROTATION: runs only for the mvp profile with SMOKE_ROTATION_REQUESTS > 0, otherwise prints `ROTATION SKIPPED ... (rotation is NOT claimed)`; asserts every 200-response id has a completion row, cursor segment seq advanced by >= 2, and /readyz is 200 afterwards. Prints counts only, never the id list.
- Header rewritten; AUDIT PASS text changed to "(drain wiring per spool; typed rows are asserted in AUDIT-TYPES)". Exit semantics, the single `trap cleanup EXIT`, the Phase 7 behaviour (GRACE without `-t`, healthy wait, cleanup re-login with warnings) are unchanged.
- Lint tests: `TestSmokeScriptSyntax`, `TestSmokeScriptCoversAuditEvidence`, `TestSmokeScriptHonestHeader`, `TestSmokeScriptNoCredentialEcho`.

## Verification (no Docker started)

- `bash -n scripts/compose-smoke.sh`: ok. shellcheck is not installed on this host.
- `TMPDIR=/dev/shm/aegis-gotmp go test -count=1 -v ./tests/compose/ -run TestSmokeScript`: 4 PASS; `go test -count=1 ./tests/compose/`: ok; gofmt and go vet clean.
- gw_probe, valid_rid, psql_q trimming, the segment-seq sed and the metrics awk were exercised against a local Python HTTP server and a fake `docker` shim (header extraction, CR stripping, UUID validation, sequence `000003`, metric value 7 all as expected).
- Acceptance greps: AUDIT-TYPES x1, DEDUPE x1, ROTATION x1, trap x1, stale phrases x0, no added echo of credentials.
- The SQL was read carefully against migrations 000002/000003 (column names event_type, request_id, http_status, duration_ms, reason_code, error_code, principal_kind, suppressed_count, "timestamp") but could not be executed: psql is installed but there is no Postgres server on the host.

## Assumptions about live behaviour NOT verified hermetically (watch these in plan 08-11)

1. SQL executes as written on real Postgres: `text || int` concatenation, `(bool)::int`, `extract(epoch from ...)::int` and the quoted `"timestamp"` column in the window guard.
2. Allowed requests really produce a decision row with `http_status = 0` and `duration_ms = 0` (assertion `decision|allow|0|0`) and a completion row with `http_status = 200`, `duration_ms > 0`, `decision = allow`.
3. A policy denial row has `error_code = FORBIDDEN`, non-empty `reason_code` and `duration_ms > 0`; an unauthenticated denial has `principal_kind = anonymous`, `decision = deny`, `http_status = 401`.
4. The X-Request-ID response header is present on 200, 403 and 401 responses (including the pre-middleware 401), and equals the audit row's request_id.
5. The summary row carries the first denial's request_id (as in governor.summary, which copies the first event and only changes event_id/timestamp/count), so the `suppressed_count=1` query by DENY1_ID is correct; it appears within 2x window + 30s.
6. DENY1 and DENY2 both return 403 (route limit for /api/admin/users is 10 rps, burst 2). If DENY2 is 429 the repeat and summary assertions print SKIPPED.
7. SMOKE_SUPPRESS_WINDOW_SECS matches the stack's AEGIS_AUDIT_SUPPRESS_WINDOW. If the script's value is smaller than the stack's, the window guard may not wait long enough and the first denial can be suppressed (the denial-row assertion would then fail on a re-run).
8. The window guard measures the age of the newest denial row of that reason. A summary row's timestamp is the window end, so the guard is conservative, never too short.
9. Metrics are served at 127.0.0.1:9091 inside the gateway container (default `:9091`) and the `{kind="completion"}` / `{kind="denial"}` series exist with that exact label formatting; `aegis_audit_degraded` renders as `aegis_audit_degraded 0`.
10. DEDUPE: the audit-worker image (alpine) provides `rm` and `cat` (assumption A2). The worker reloads the cursor on every ProcessAvailable, so it may recreate the cursor itself between the `rm` and the restart; the replay is then partly proven by that in-process reload rather than strictly by the restart. Either way the count must stay unchanged. With AEGIS_PRUNE_ARCHIVED=true only the newest segment remains, so replay covers that segment.
11. DEDUPE quiescing assumes no background audit writes: healthcheck traffic, the sweeper (no remaining open window after AUDIT-TYPES d) or the unauthenticated bucket must not add rows during the section. If the SUMMARY-wait was SKIPPED, a late summary row could make DEDUPE report a count change.
12. ROTATION: 300 requests at ~50 rps stay under the /api/orders limit (100 rps, burst 20); with AEGIS_SPOOL_SEGMENT_BYTES=65536 the burst produces at least 2 rotations; the worker cursor tracks the newest segment so the seq delta is visible within 15s; the gateway process is not restarted during the section (the spool seq counter resets on restart).
13. Cursor field names (`segment_file`, `offset`) and the `wal-%020d-%06d.log` naming match internal/audit (read from source, not observed live).

## Deviations from Plan

None. Minor notes: Task 1 and Task 2 were committed separately but the header rewrite (Task 2 item 3) and helper block landed in the Task 1 commit via one editing pass; the lint test also guards the explicit-string requirements from the plan. The hermetic test cannot prove SQL correctness.

## Known Stubs

None.

## Threat Flags

None. The script prints only counts, statuses, request ids (random UUIDs) and segment names; request ids are validated as UUIDs before SQL interpolation (T-08-49).

## Self-Check: PASSED
