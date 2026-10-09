---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
fixed_at: 2026-10-09T00:00:00Z
review_path: .planning/phases/08-close-gap-b7-route-denials-and-completion-events-to-the-audi/08-REVIEW.md
iteration: 1
findings_in_scope: 8
fixed: 8
skipped: 0
status: all_fixed
---

# Phase 08: Code Review Fix Report

**Fixed at:** 2026-10-09
**Source review:** 08-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 8 (CR-01, WR-01..WR-07; Info items not in scope)
- Fixed: 8
- Skipped: 0
- no_change_needed: 0

Execution note: the work was done on the main working tree of branch `main`, as the
orchestrator instructed (no branch or worktree), one commit per fix, nothing pushed.
For every fix a regression test was written first and run against the unfixed code
(it failed at runtime, or for the new-API cases only after adding the bare seam or
a stub so it compiled), then the fix was made and the test passed.

## Fixed Issues

### CR-01, WR-06, WR-07: WriteFrames failure recovery (one commit)

**Status:** fixed (logic fixes: requires human verification)
**Commit:** 7dc9e65 (single commit naming all three; they are one recovery rule in the same
function, and WR-06 and CR-01 share the failure branch, so they are not separable)
**Files modified:** `internal/audit/spool.go`, `internal/audit/committer.go`,
`internal/audit/spool_group_test.go`, `internal/audit/spool_recovery_test.go` (new)
**Regression tests:**
- CR-01: `TestWriteFramesDriftNeverCutsAcknowledgedFrames`
- WR-06: `TestWriteFramesSyncFailureKeepsPossiblyIngestedBytes`
- WR-07: `TestWriteFramesRotationFailureAfterDurableBatch`
- Recovery rule when rotation also fails: `TestWriteFramesUnrotatableTornTailRefusesAppendUntilRepaired`,
  `TestCommitterTickRepairsUnrotatableSegment`

**Applied fix:** One rule. `currentSize` is no longer trusted to decide where a segment
ends. Before every write `WriteFrames` compares the active file's real size (fstat) with
`currentSize`; any difference (the torn or unsynced tail an unchanged `AppendPreForward`
leaves behind after a failed write or fsync) triggers a rotation first, so nothing is
ever appended behind bytes the spool did not account for. After a failed write or fsync
nothing is truncated, with one exception: if fewer bytes than the first frame of the batch
landed, no reader can have decoded them, so the tail is truncated back to the true base
(kept for tidiness in ENOSPC storms). Anything longer (any complete frame, every fsync
failure) stays where it is and the spool rotates to a fresh segment. This means acknowledged
frames are never cut (CR-01), the tailing worker's cursor can never end up beyond a
rewritten end of file (WR-06), and the retry's duplicates are absorbed by `ON CONFLICT`.
`rotateSegment` now opens the next segment first and retires the old one only on success,
so a failed open leaves a usable active file (WR-07). A rotation failure after a batch that
was written and synced no longer fails the batch (rate-limited log line; the next write
retries the rotation). If a post-failure rotation fails, a `needsRotate` flag makes
`CheckSaturation` report saturated, so the unchanged `AppendPreForward` refuses (fail
closed, D-12) instead of appending behind torn bytes; `WriteFrames` rotates before it
writes, and the new `DiskSpool.RepairSegment()` is called from the committer recovery tick
so allowed traffic does not stay refused until a denial happens to arrive. The
`AppendPreForward` body is untouched (D-09 check below) and `rotateSegment` is shared, so
`AppendPreForward` also benefits from open-first rotation.

CR-01 test reproduction note: `AppendPreForward` has no write/sync seam, so the failed
pre-forward append is simulated by appending stray bytes to the active segment behind the
spool's back, which produces exactly the state the review describes (real size greater
than `currentSize`). The "worker already read it" race in WR-06 is simulated by sampling the
file size inside the injected `syncFn`.

**Existing tests changed (list for review):** `TestWriteFramesFailureTruncates` in
`spool_group_test.go` pinned truncation of complete frames after a write error and
truncation of an fsync-failed batch, which is precisely the WR-06 hazard.
- "write error": now writes only part of the first frame, so the original assertions
  (truncated back, same segment, later frame appended) still hold unchanged.
- New subtest "write error after whole frames landed rotates".
- "sync error": assertions changed from "size equals before" to "bytes kept, new segment".
- "truncate failure rotates": unchanged and passing.

### WR-01: failed first denial opened a suppression window

**Status:** fixed (logic fix: requires human verification)
**Commit:** 94da2bc
**Files modified:** `internal/audit/governor.go`, `internal/audit/pipeline.go`,
`internal/audit/governor_forget_test.go` (new)
**Regression tests:** `TestPipelineFailedFirstDenialDoesNotMaskRepeats`,
`TestPipelineFailedUnauthDenialRefundsToken`, `TestGovernorForgetClearsEmptyWindow`,
`TestGovernorForgetKeepsConcurrentRepeatsCounted`,
`TestGovernorUnwrittenWindowSummaryDoesNotPointAtMissingRow`,
`TestGovernorForgetIgnoresStaleAdmission`, `TestGovernorForgetOverflowAndBucket`

**Applied fix:** `Admission` now carries an unexported reference (kind, key, window epoch)
and `Governor.Forget(Admission)` undoes a `Record` admission. `RecordDenial` calls it on any
`SubmitDenial` error (queue full, hard limit, timeout, write error, closed). An unauthenticated
bucket token is refunded (capped at the burst). A window with no counted repeats is deleted, so
the next identical denial is recorded. If concurrent repeats were already counted against the
unwritten first event (SubmitDenial can block up to the 2s waiter timeout), the window is marked
unwritten: the next occurrence is recorded in its place, the lost denial is added to the
suppressed count, and if no further occurrence comes the summary carries the count plus one and
a fresh request id instead of the id of a row that was never written. The epoch makes a late
`Forget` a no-op once the window has been replaced. The success path is unchanged (D-07, D-15).
Note: on `ErrDenialTimeout` the frame may still be written later, so a repeat can be recorded
as well; that errs towards recording rather than masking.

### WR-02: suppression summaries dropped on a full queue

**Status:** fixed
**Commit:** 677281a
**Files modified:** `internal/audit/committer.go`, `internal/audit/pipeline.go`,
`internal/audit/pipeline_summary_test.go` (new)
**Regression tests:** `TestPipelineSummaryCarriedWhenQueueFull`,
`TestPipelineSummaryCarryBoundedAndCounted`, `TestPipelineShutdownWaitsForQueueRoomForSummaries`,
`TestPipelineShutdownCountsSummariesLostToDeadline`

**Applied fix:** New `Committer.TryEnqueueSummary` returns false (without counting a drop)
when the queue is full. `sweepOnce` keeps the summaries the queue had no room for and retries
them first on the next sweep. The carry is bounded at 4x the suppression key table (`maxCarry`),
and what exceeds it is dropped oldest-first and counted as `denial/queue_full`. The shutdown
flush waits for queue room in 2 ms steps while the committer is still draining, until the
shutdown context ends, then counts whatever is still unsent as dropped. Not done: a config
cross-check between the completion queue size and the key table. The carry removes the failure
mode, and the existing config tests pin a queue size of 64 with the default 4096 keys as valid,
so that guard would have changed accepted configurations for no extra safety.

### WR-03: D-07 hole on the Write and Flush paths

**Status:** fixed
**Commit:** 9a0ecac
**Files modified:** `internal/audit/logger.go`, `internal/audit/logger_test.go`
**Regression test:** `TestDenialRecordedBeforeImplicitHeader` (subtests: Write, Flush, 1xx then Write,
allowed Write, no sink)

**Applied fix:** `Write` and `Flush` go through the same `commitHeader` step as `WriteHeader`, so
the denial is recorded before the first response byte on every path, the event carries status 200
for an implicit header, and the misleading comment is replaced. With no sink the underlying writer
still performs its own implicit `WriteHeader(200)` (no extra explicit call is added), 1xx handling is
unchanged, and `Flush` only commits the header when the underlying writer can actually flush.

### WR-04: D-18 normalization gaps

**Status:** fixed
**Commit:** eb91e6a
**Files modified:** `internal/audit/event.go`, `internal/audit/worker.go`, `internal/audit/logger.go`,
and tests in `event_test.go`, `worker_test.go`, `logger_test.go`
**Regression tests:** `TestNormalize_PrincipalRoles`, `TestNormalize_IDs`,
`TestAuditWorker_NormalizesRolesAndLenientUUIDs`, `TestAuditWorker_BoundsRoles`,
`TestAuditMiddlewareRequestIDHeader`

**Applied fix:** `Normalize` strips NUL and invalid UTF-8 from `PrincipalRoles` and caps them at
64 roles of 128 bytes into a fresh slice (the caller's slice is never mutated, which matters
because the committer normalizes a shallow copy). `EventID` and `RequestID` are rewritten to
canonical UUID text when they parse (`urn:uuid:`, braced, bare hex, upper case) and otherwise only
length-bounded to 64 bytes without touching their bytes. The worker sends the canonical text of a
parsed UUID (`uuid.Parse` accepts `urn:uuid:`, which Postgres rejects) and a fresh UUID otherwise.
`AuditMiddleware` trusts an inbound `X-Request-ID` only if it parses and is 36 bytes, else it
generates one. Not done (stated in the review as longer term): quarantining a poison event in
`ProcessBatch`.

**Existing test changed:** `TestNormalize_IdempotentNilSafeAndUntouched` pinned
`PrincipalRoles` as left alone (`[]string{"r\x00"}`); it now expects `[]string{"r"}`. Its
`EventID` and `RequestID` assertions are unchanged and still pass.

### WR-05: unbounded audit config

**Status:** fixed
**Commit:** 05f1dcf
**Files modified:** `internal/config/config.go`, `internal/config/config_test.go`
**Regression test:** the extended `TestAuditConfigOverrides` table (invalid and boundary rows)

**Applied fix:** Defaults unchanged. `AEGIS_AUDIT_UNAUTH_RATE` must be finite and between 0.001
and 1000 (Inf, NaN and 1e308 are rejected by name). `AEGIS_AUDIT_UNAUTH_BURST` is limited to
1..10000 (was MaxInt32). `AEGIS_AUDIT_SUPPRESS_MAX_KEYS` to 16..100000 (was MaxInt32; each entry
holds an event copy, so 100k is a few hundred MB at the very worst). `AEGIS_AUDIT_COMPLETION_QUEUE_SIZE`
to 64..100000 (was 1000000, about 1 GB). Errors name the variable. No compose file sets any of
these variables.

**Existing test changed:** the boundary row `AEGIS_AUDIT_COMPLETION_QUEUE_SIZE=1000000` (valid)
became `100000`; `1000001` stays as an invalid row and `100001` was added.

## Verification

All run with `TMPDIR=/dev/shm/aegis-gotmp` (removed afterwards).

| Check | Result |
|-------|--------|
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `gofmt -l internal/audit internal/config` | no output |
| D-09 `AppendPreForward` body vs 94cbd2f | `D09_OK` (byte identical) |
| `diskSpool.AppendPreForward(` call sites in cmd/gateway/main.go | 2, file not modified |
| `go test -race -count=1 $(go list ./... \| grep -v /tests/integration)` | all packages ok except `benchmarks` |
| `benchmarks/TestPolicyEngine_LatencyBudget` | failed inside the full parallel suite (287.6us vs 200us budget, both runs); passes in isolation (`go test -race -count=1 -run TestPolicyEngine_LatencyBudget ./benchmarks/` -> ok). Timing-sensitive, the tolerated case. |
| `go test -race -count=5 ./internal/audit/` | ok (15.1s) |

## Notes for the live-run executor

- Segment layout under failure changed. After an fsync failure or a write error that landed a
  complete frame, the old segment is kept (it may end in a torn frame) and a new segment is
  started. Expect extra `wal-*.log` files and "corrupted WAL record ... seeking resync" log lines
  from the worker for that tail; the worker moves on to the next segment. Duplicates after an fsync
  failure are absorbed by `ON CONFLICT (event_date, event_id)`.
- While a failed write could not be rotated away from, `CheckSaturation` reports saturated, so
  allowed requests get 503 `AUDIT_SPOOL_SATURATED` until the committer recovery tick
  (`RecoveryInterval`, 250 ms) rotates. A short 503 burst right after a disk error is expected
  and is the fail-closed behaviour, not a new bug.
- A residual the fix cannot close without editing `AppendPreForward`: that function can still
  append behind its own torn tail if it fails and a second pre-forward append follows before any
  `WriteFrames`. `WriteFrames` now repairs that on its next call; the worker's magic resync handles
  the rest. Unchanged from before this phase.
- Smoke evidence that matters after WR-01: a denial lost to the hard limit or a write error is now
  recorded again on the next identical denial, so a run that injects disk pressure will see more
  denial rows (and fewer `aegis_audit_records_suppressed_total` increments) than before. Summary
  rows for windows whose first row was lost carry a fresh `request_id` that matches no row.
- WR-02: during a stalled committer, `aegis_audit_records_dropped_total{kind="denial",reason="queue_full"}`
  now only moves when a summary is really lost (carry cap exceeded or shutdown deadline hit), not
  on every full-queue sweep. Summaries may now land one sweep interval (window/4, at least 1 s) later.
- WR-03/WR-04: a client-supplied `X-Request-ID` that is not a canonical UUID is now replaced in both
  the response header and the audit row. Anything in the smoke script that sends a custom request id
  and expects it echoed back must use a canonical UUID (none of the repo's tests or compose files do).
- WR-05: any deployment setting the four variables above outside the new ranges now fails at start
  with an error naming the variable. No compose file in the repo sets them.
- Not fixed (Info, out of scope): IN-01..IN-06.

---

_Fixed: 2026-10-09_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
