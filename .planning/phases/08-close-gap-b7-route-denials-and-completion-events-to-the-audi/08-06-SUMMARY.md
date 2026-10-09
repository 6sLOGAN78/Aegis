---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 06
subsystem: audit
tags: [audit, committer, group-commit, fail-closed, shutdown-drain]
requires: ["08-01", "08-02", "08-05"]
provides:
  - "audit.Committer: single-goroutine group-commit writer (one WriteFrames call, one fsync per flush)"
  - "audit.Recorder interface (seven methods) satisfied structurally by *telemetry.Metrics"
  - "fail-closed fault handling for completion loss (queue_full, hard_limit, write_error) with traffic-free recovery"
affects: [08-07]
tech-stack:
  added: []
  patterns: ["one writer goroutine with two bounded never-closed channels", "fault generation counter guarding recovery clears", "retained-batch retry with capped backoff"]
key-files:
  created:
    - internal/audit/committer.go
    - internal/audit/committer_test.go
  modified: []
key-decisions:
  - "Only completion loss sets the write fault; denial and summary drops are counted only, so an unauthenticated flood cannot flip the gateway to 503 (D-12)"
  - "Recovery probe reads the fault generation first and clearFaultIfGen is the only clear path; a fault raised mid-probe survives"
  - "After a successful write-error retry, if the completion queue is over half full the fault is handed to the recovery tick as queue_full instead of reopening the gate"
requirements-completed: []
duration: ~45min
completed: 2026-10-09
---

# Phase 8 Plan 06: Group-commit Committer Summary

One goroutine now owns every denial, completion and summary write. It batches whatever has arrived into a single `DiskSpool.WriteFrames` call, makes `SubmitDenial` durable before it returns, keeps `EnqueueCompletion` non-blocking, trips the D-12 fail-closed gate when completions are lost, recovers without inbound traffic, and drains everything on shutdown.

AUD-03 and AUD-04 are intentionally not marked complete here (plan 08-12 decides from live evidence).

## Commit

| Tasks | Commit |
|-------|--------|
| 1 and 2 (core committer, fault handling, retry, recovery, tests) | 15e4adb |

## Names defined for plan 08-07 (package audit)

Production (internal/audit/committer.go):
- `type Recorder interface` with exactly `RecordAuditWritten(kind string, n int)`, `RecordAuditDropped(kind, reason string, n int)`, `RecordAuditSuppressed(reasonCode string, n int)`, `RecordAuditSuppressorOverflow()`, `ObserveAuditFlush(d time.Duration)`, `SetAuditDegraded(bool)`, `SetAuditQueueDepth(int)`. Verified with a throwaway compile check that `*telemetry.Metrics` satisfies it.
- `type CommitterConfig struct { GroupFlush, CompletionFlush time.Duration; QueueSize, DenialQueueSize, MaxBatchFrames, MaxBatchBytes int; WaiterTimeout, RecoveryInterval time.Duration }`, defaults 2ms / 25ms / 8192 / 1024 / 512 / 512KiB / 2s / 250ms.
- `var ErrCommitterClosed`, plus extra `ErrDenialQueueFull` and `ErrDenialTimeout` (SubmitDenial return values for a full denial queue and a waiter timeout).
- `NewCommitter(spool *DiskSpool, rec Recorder, cfg CommitterConfig) *Committer`, `(*Committer).SubmitDenial(ev) error`, `EnqueueCompletion(ev)`, `EnqueueSummary(ev)`, `QueueDepth() int`, `Shutdown(ctx) error`.
- Metric kinds `"completion"` and `"denial"`; reasons `queue_full`, `hard_limit`, `write_error`, `closed`, `timeout`.
- Unexported seams (same package tests only): `setFault`, `currentFaultGen`, `clearFaultIfGen`, `recoveryProbe`, `setProbeHook`.

Shared test helpers (committer_test.go; do not redeclare): `newTestSpool(t, ratio)`, `ratioControl` (`Set`, `statfs`), `newTestSpoolControlled(t, ratio)`, `readAllEvents(t, dir)`, `fakeRecorder` with `written`, `dropped`, `suppressed`, `overflows`, `flushes`, `degraded`, `everDegraded`, `lastQueueDepth`, and `newFakeRecorder()`. Extras also taken (additional names): `fakeRecorder.degradedFalseCalls()`, `startCommitter`, `blockSync`, `routeIDs`, `eventually`, `denialEvent`.

Behaviors plan 07 should know:
- Any event passed in is copied and `Normalize`d before framing; the caller's struct is not mutated.
- `SubmitDenial` can return after the waiter timeout while the frame is later still written (it is then counted as both a timeout drop and a written record). Documented, not prevented.
- A write-error fault is set right after the failed batch's denial waiters are released, so a caller that sees the error may briefly observe no fault yet.
- Because `AppendPreForward` takes the spool mutex before checking saturation, it blocks while an fsync is in progress; this is existing D-09 behavior.

## Verification

All `go test` runs used `TMPDIR=/dev/shm/aegis-gotmp` (removed afterwards).

- `go test -race -count=1 -v ./internal/audit/ -run TestCommitter`: 14 top-level tests all PASS (8 Task 1 including a canceled-context shutdown test, 6 Task 2) plus 2 subtests.
- Flake checks: `-count=5`, `-count=20` (30s) and `-count=5 -cpu 1,2` of the committer tests all passed with zero failures. `-count=3` of the whole `./internal/audit/` package passed. Observed flake rate: 0 failures in about 70 runs.
- `go vet ./internal/audit/` and `gofmt -l internal/audit` are clean.
- Acceptance greps: no `close(c.denialCh/completionCh)`, no `CheckSaturation` and no telemetry import in committer.go; `ClearWriteFault` appears once (inside `clearFaultIfGen`); `faultGen` 5 times; `SubmitDenial` contains no `setFault`.
- D-09: sha256 of the `AppendPreForward` body equals the hash from `git show 94cbd2f:internal/audit/spool.go` (8bd6714e...).

## Deviations from Plan

1. [Process] The two tasks were implemented and committed as one commit; both edit the same two files and were written together. Tests were written in the same pass as the implementation, not captured as red first. The first run did expose one test mistake (see 4).
2. [Rule 2 - design addition] After a successful write-error retry, the committer checks the completion queue: above half full it re-labels the fault `queue_full` for the recovery tick rather than clearing immediately. The plan only said "clear on success"; this closes a window where the gate would reopen with a full queue.
3. [Design choice] On the batch deadline the collector sweeps whatever is already in both channels (up to the caps) before flushing, so batches are deterministic under test and deadline firing never splits an already-arrived burst.
4. [Test adjustment] The plan's queue-full test asked for an `AppendPreForward` check while the fsync is blocked. `AppendPreForward` locks the spool mutex before consulting saturation, so it blocks behind the held fsync. The test asserts `CheckSaturation()==true` during the block and `AppendPreForward` succeeding after recovery; the refusal of `AppendPreForward` while faulted is covered by plan 08-02's `TestWriteFaultGate` and by the hard-limit-band test here (statfs 0.92).
5. [Test adjustment] The generation-race test drives `recoveryProbe()` directly with a 1h tick for deterministic assertions, and a second subtest uses the real ticker with `Eventually`. Queue-full tests set `MaxBatchFrames` 4 so the committer cannot absorb the queue into an in-memory batch.
6. No existing test was changed or weakened; no file outside `files_modified` was touched.

## Known Stubs

None.

## Threat Flags

None. No new network, auth or file-access surface beyond the planned single writer.

## Self-Check: PASSED

- internal/audit/committer.go and internal/audit/committer_test.go exist.
- Commit 15e4adb exists on main.
