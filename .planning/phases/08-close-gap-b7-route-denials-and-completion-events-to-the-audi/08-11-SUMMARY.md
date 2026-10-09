---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 11
subsystem: live-verification
tags: [docker, compose, smoke, audit, dedupe, rotation, live-run]
requires: ["08-10"]
provides:
  - "Live evidence for AUD-04 (typed rows through the real gateway write path) and AUD-03 (dedupe replay, rotation) on the rebuilt MVP stack"
affects: [08-12]
key-files:
  created: []
  modified: []
requirements-completed: []
completed: 2026-10-09
---

# Phase 8 Plan 11: Live MVP run Summary

Status: complete. Every hard check of the smoke script passed on the first run (exit 0), no repair cycle was used, no script or product change was needed. The graceful-stop retention check passed. The stack was torn down and no non-Aegis Docker resource was touched. AUD-03 and AUD-04 are NOT ticked here; plan 08-12 decides.

## Task 1: preconditions, bring-up, smoke

### Step 0 preconditions

| Check | Result |
|-------|--------|
| Approval | 08-10-SUMMARY.md "Teardown approval" records the user's verbatim reply "Approved" (approved) to "Approve the Docker rebuild, live MVP run and teardown described above for plan 08-11?" with its scope. Verified. |
| Code state | `git rev-parse --short HEAD` = `d400d0b`. `git status --short cmd internal deployments scripts migrations` printed nothing (no uncommitted change under those paths). Images were built from this HEAD. |
| Disk before first mutating command (10:31 UTC) | `/dev/nvme0n1p5  183G  152G  22G  88% /` (Docker root `/var/lib/docker` is on the same filesystem). **GATE_RATIO = 83.1**, available to non-root 23.0 GB. Amended precondition (<= 85.0 and >= 10 GB) MET. |
| Ports | `ss -ltn` showed nothing on 8080, 9443, 8085, 8084, 9090, 9091 before and after `down`. |
| Pre-existing compose resources | none (`compose-*` containers and `compose_*` volumes absent). |
| Certs | `deployments/certs/root-ca.crt` and `assertion-ed25519.pub` present; generation skipped. |

### Mutating Docker commands executed (all name `deployments/compose/docker-compose.mvp.yml`)

1. `docker compose -f deployments/compose/docker-compose.mvp.yml down -v --remove-orphans` (clean slate; nothing to remove) - exit 0
2. `AEGIS_SPOOL_SEGMENT_BYTES=65536 AEGIS_AUDIT_SUPPRESS_WINDOW=5s docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build --wait` - exit 0 on the first try (full from-scratch build and pulls, no network retry needed)
3. Inside the smoke script (as designed): `exec`, `restart audit-worker`, `stop gateway`, `up -d --wait gateway`
4. Graceful-stop check: `stop gateway`, `up -d --wait gateway`
5. Final `docker compose -f deployments/compose/docker-compose.mvp.yml down -v --remove-orphans`

No prune, `rmi` or other command was run. The only other Docker commands were read-only (`ps`, `logs`, `volume ls`, `inspect`, `info`).

### Bring-up verification

- `up -d --build --wait` exit 0; seed `Exited (0)`; gateway, control-plane, postgres, redis healthy.
- `exec -T gateway printenv AEGIS_SPOOL_SEGMENT_BYTES AEGIS_AUDIT_SUPPRESS_WINDOW` printed `65536` and `5s`.
- `select count(*) from information_schema.columns where table_name='audit_events' and column_name='suppressed_count'` printed `1` (migration 000003 applied).
- Disk after the build: `/dev/nvme0n1p5 183G 154G 19G 90% /`, **GATE_RATIO = 84.5**, available 20.4 GB. Note: `df` Use% reads 90% but the spool gate ratio (statfs blocks minus bfree) was 84.5, so the gate was not tripped. After smoke 84.3, before teardown 84.3, after teardown 84.2 (`df`: 154G used, 20G avail, 89%). Images added roughly 2 GB. No saturation symptom (`AUDIT_SPOOL_SATURATED`, /readyz 503) occurred at any time.

### Smoke run: `SMOKE_SUPPRESS_WINDOW_SECS=5 SMOKE_ROTATION_REQUESTS=300 bash scripts/compose-smoke.sh mvp --stop-check`

Exit code 0, one run, no AUDIT-TYPES SKIPPED, no ROTATION SKIPPED. Verbatim output:

```
=== Aegis compose smoke: profile=mvp gateway=gateway ===
=== READY ===
PASS: READY gateway /readyz returned 200
=== B4: control plane reaches Redis ===
PASS: B4 quarantine POST returned 200
PASS: B4 quarantine DELETE returned 200 (probe removed)
=== B1: gateway authorizes with seeded policy ===
PASS: B1 developer GET /api/orders returned 200
PASS: B1 developer GET /api/admin/users returned 403
PASS: B7 unauthenticated request returned 401
=== AUDIT: per-spool drain (B3) ===
PASS: AUDIT audit_events count grew above baseline 3
PASS: AUDIT audit-worker wal.cursor advanced (drain wiring per spool; typed rows are asserted in AUDIT-TYPES)
=== AUDIT-TYPES: typed rows for allowed, denied, suppressed and unauthenticated requests (B7 / AUD-04) ===
PASS: AUDIT-TYPES allowed request has a completion row (200, duration_ms>0) and a decision row (status 0) with distinct event_ids
PASS: AUDIT-TYPES denied request has exactly one denial row (403, deny, reason set, duration_ms>0, FORBIDDEN)
PASS: AUDIT-TYPES identical repeat denial within the window produced no row (suppressed)
PASS: AUDIT-TYPES suppression summary row written with suppressed_count=1 (migration 000003 and sweeper live)
PASS: AUDIT-TYPES unauthenticated request has a denial row (401, anonymous)
PASS: AUDIT-TYPES gateway counter aegis_audit_records_written_total{kind=completion} is 11
PASS: AUDIT-TYPES gateway counter aegis_audit_records_written_total{kind=denial} is 3
PASS: AUDIT-TYPES aegis_audit_degraded 0
=== DEDUPE: delete the cursor, restart the worker, row count must not change (AUD-03) ===
PASS: DEDUPE audit-worker cursor deleted, worker restarted, replay absorbed (count unchanged at 25)
=== ROTATION: multi-segment delivery (AUD-03) ===
ROTATION sent 300 requests: 300 with 200 and a request id, 0 non-200, 0 200 without request id
PASS: ROTATION 300 requests delivered across >=2 segment rotations (seq 1 -> 5)
=== A4: quarantined principal denied (assumption) ===
A4 RESULT: PASS (HTTP 403)
=== GRACE: gateway stops with exit code 0 ===
PASS: GRACE gateway exited 0 on stop
=== Summary: 0 hard failure(s) ===
```

Of the 13 live-behaviour assumptions listed in 08-09-SUMMARY.md, the run confirmed 1-9 and 12-13 (SQL executes on real Postgres; decision row has status 0 and duration 0; completion row 200 with duration > 0; denial FORBIDDEN; 401 anonymous; X-Request-ID present on 200, 403 and 401 and equal to the row's request_id; summary row carries the first denial's request_id with suppressed_count=1; DENY2 returned 403; metrics at 127.0.0.1:9091 with the expected labels; cursor fields `segment_file` and `offset` and segment naming as read from source; rotation advanced seq 1 -> 5). Assumptions 10 and 11 (DEDUPE: `rm` and `cat` exist in the worker image, quiesce holds) also held: the cursor was deleted, the worker restarted, the cursor came back and the count stayed at 25. Whether the worker recreated the cursor from the restart or by an in-process reload was not distinguished.

Classification: no failure, so no ENVIRONMENT failure and no Phase 8 defect. Repair cycles used: 0. Script edits: none (`scripts/compose-smoke.sh` and `tests/compose/smoke_script_test.go` untouched). Go source untouched.

## Task 2: graceful-stop retention, findings, teardown

### Graceful-stop loss check (D-10 live)

Gateway healthy; developer token minted via the demo issuer (not printed); 30 sequential `GET /api/orders` through 127.0.0.1:8080, request ids captured from X-Request-ID (scratchpad file, not in the repo).

| Item | Result |
|------|--------|
| 200 responses with request id | 30 of 30 |
| `docker compose ... stop gateway` | exit 0 |
| `docker inspect -f '{{.State.ExitCode}}'` of the gateway container | 0 |
| `count(distinct request_id)` of completion rows for those 30 ids | 30 (equal to the number of 200 ids, polled within 30 s) |
| `up -d --wait gateway` afterwards | exit 0 |

Strongest proof that a non-empty queue is drained at shutdown remains the hermetic TestPipelineShutdown and TestE2EShutdownFlushesQueuedRecords. The requests were sequential and finished before the stop, so this live check shows that stop and restart lose nothing, not that a full queue is flushed at the instant of SIGTERM.

### Log and metrics findings (read after the stack's last gateway restart)

- `logs gateway | grep -c 'audit pipeline shutdown'` = 0 and `grep -c 'AuditCommitter'` = 0: no shutdown error or committer error lines. (The log is short; the gateway emits `audit_completion` JSON lines. The shutdown path logs nothing at INFO on success, so 0 is not by itself evidence of a clean drain; the exit code 0 and the row counts are.)
- `logs audit-worker | grep -c 'batch flush warning'` = 0; no warn/error/corrupt lines in the worker log.
- Gateway metrics (read from the gateway after the second restart, so in-process counters were reset): every `aegis_audit_records_dropped_total` series is 0 (none non-zero), `aegis_audit_degraded 0`, all `aegis_audit_records_suppressed_total` and `aegis_http_rejected_total` series 0, `aegis_audit_records_written_total` 0 (fresh process). The pre-restart counters are recorded in the smoke PASS lines: written_total completion 11, denial 3, degraded 0.
- Final database totals before teardown: 686 rows = 341 completion + 341 decision + 4 denial (every completion has its decision pair, no loss).

### Representative audit_events rows (as seen in Postgres, request ids shortened)

Allowed request, decision + completion pair (request `bf5a1e60`):

```
 event_type | decision | http_status | duration_ms |       reason_code        | error_code | principal_kind | suppressed_count |   rid    |   eid
 completion | allow    |         200 |       7.761 | ALLOWED_DEVELOPER_ORDERS |            | user           |                  | bf5a1e60 | 0bd480e4
 decision   | allow    |           0 |           0 | ALLOWED_DEVELOPER_ORDERS |            | user           |                  | bf5a1e60 | 48a8d5a7
```

Denial rows (all four `denial` rows in the database), in timestamp order:

```
 event_type | decision | http_status | duration_ms |           reason_code            |      error_code       | principal_kind | suppressed_count |   rid
 denial     | deny     |         403 |       0.938 | DENIED_DEVELOPER_ADMIN_FORBIDDEN | FORBIDDEN             | user           |                  | 24f8d1ba   <- policy denial (DENY1)
 denial     | deny     |         401 |        0.07 | UNAUTHORIZED                     | UNAUTHORIZED          | anonymous      |                  | 74e33e63   <- anonymous 401 denial
 denial     | deny     |         403 |           0 | DENIED_DEVELOPER_ADMIN_FORBIDDEN | FORBIDDEN             | user           |                1 | 24f8d1ba   <- summary row (same request id as DENY1, suppressed_count=1)
 denial     | deny     |         403 |       0.386 | PRINCIPAL_QUARANTINED            | PRINCIPAL_QUARANTINED | user           |                  | 6baa2a3b   <- A4 quarantine denial
```

The identical repeat (DENY2) produced no row; only the summary row accounts for it.

### Teardown

- `docker compose -f deployments/compose/docker-compose.mvp.yml down -v --remove-orphans` completed (network, both volumes removed). The shell exit code printed afterwards belonged to a `tail` pipe, so the proof of success is the verification below.
- `docker ps -a | grep -c '^compose-'` = 0; `docker volume ls | grep -c '^compose_'` = 0.
- devrag-stack names recorded in 08-10-SUMMARY.md are unchanged and still present: containers devrag-stack-app-1, -es01-1, -init-1 (exited 0), -mailpit-1, -minio-1, -mysql-1, -redis-1 (the same seven that were running or exited at the start; uptimes continued) and volumes devrag-stack_esdata01, _minio_data, _mysql_data, _redis_data.
- Final disk: `/dev/nvme0n1p5 183G 154G 20G 89% /`, GATE_RATIO 84.2.
- The Aegis images and build cache remain on disk (about 2 GB); they are not removed since removing images is outside the approved scope.

## Requirement evidence (input to plan 08-12; no requirement is ticked here)

Build: HEAD d400d0b, MVP profile, `AEGIS_SPOOL_SEGMENT_BYTES=65536`, `AEGIS_AUDIT_SUPPRESS_WINDOW=5s`.

### AUD-04 (route denials and completion events reach audit_events with type, status, duration)

Passed live:
- `AUDIT-TYPES allowed request has a completion row (200, duration_ms>0) and a decision row (status 0) with distinct event_ids`
- `AUDIT-TYPES denied request has exactly one denial row (403, deny, reason set, duration_ms>0, FORBIDDEN)`
- `AUDIT-TYPES unauthenticated request has a denial row (401, anonymous)`
- `AUDIT-TYPES identical repeat denial within the window produced no row (suppressed)` and `AUDIT-TYPES suppression summary row written with suppressed_count=1`
- `AUDIT-TYPES gateway counter ...{kind=completion} is 11`, `...{kind=denial} is 3`, `aegis_audit_degraded 0`
- `B7 unauthenticated request returned 401`; A4 quarantine denial also landed as a 403 `PRINCIPAL_QUARANTINED` denial row
- Graceful stop: exit 0, 30 of 30 completion rows present
- The rows above were seen directly in Postgres.

Skipped: none. Failed: none. Not run: none for the MVP profile.

### AUD-03 (every record kind delivered, dedupe on replay, multi-segment rotation)

Passed live:
- Delivery of every kind: completion, decision, denial (policy and anonymous), and the suppression summary row all reached Postgres; 686 rows = 341 + 341 + 4, completion/decision counts equal.
- `DEDUPE audit-worker cursor deleted, worker restarted, replay absorbed (count unchanged at 25)`
- `ROTATION 300 requests delivered across >=2 segment rotations (seq 1 -> 5)`; B - A = 4, which is >= 2; 300 of 300 completion rows present; /readyz 200 afterwards.
- `AUDIT audit-worker wal.cursor advanced`; migration 000003 column `suppressed_count` present.

Skipped: none. Failed: none.

Not covered (stated plainly): hardened and distributed profiles were not run live in this phase. A crash between the Postgres insert and the cursor save is unit-level evidence only (TestAuditWorker_RestartRecovery). A non-empty queue drained at shutdown is proven by hermetic tests (TestPipelineShutdown, TestE2EShutdownFlushesQueuedRecords), not by the live stop check. The DEDUPE test does not distinguish a restart-driven replay from the worker's in-process cursor reload (assumption 10). The fault-injection paths from 08-REVIEW-FIX (write/fsync failure recovery, 503 burst after a disk error) were not exercised live.

## Deviations from Plan

None - plan executed as written. The one wrinkle: the amended disk gate used GATE_RATIO and not `df` Use% (88% before, 90% after the build), per the orchestrator amendment; the gateway's own gate stayed at 84-85 throughout.

## Known Stubs

None.

## Threat Flags

None. No token was printed; the request-id scratch file lives in the session scratchpad, not the repository.

## Self-Check: PASSED

- 08-11-SUMMARY.md written; no source file modified; no per-task code commit was needed (no fix).
- Teardown verified: 0 `compose-*` containers, 0 `compose_*` volumes, devrag-stack unchanged.
