---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
plan: 02
subsystem: audit
tags: [audit, wal, committer, worker, spool]
requires: []
provides:
  - "Completion queue overflow sheds without faulting (D-12a)"
  - "Worker cursor/prune only on explicit EOF; poison frames skipped; torn tail retried"
  - "DiskSpool.SetBytesWrittenHook for RecordSpoolBytes (plan 05)"
key-files:
  modified:
    - internal/audit/committer.go
    - internal/audit/committer_test.go
    - internal/audit/pipeline_e2e_delivery_test.go
    - internal/audit/worker.go
    - internal/audit/spool.go
  created:
    - internal/audit/worker_readerr_test.go
    - internal/audit/spool_bytes_test.go
decisions:
  - "D-12a (Phase 12, user 2026-11-16): completion-queue overflow sheds + counts + logs and never faults/unreadies the replica; amends Phase 8 D-12. Pre-forward row, 0.90 gate, 0.95 hard limit and write-error faults remain fail-closed. (For the orchestrator to record in STATE.md.)"
metrics:
  tasks: 3
  completed: 2026-11-16
---

# Phase 12 Plan 02: Audit pipeline hardening Summary

Completion-queue overflow is now shed, counted and logged without setting a fault (D-12a); the worker no longer advances the cursor or prunes on read errors; DiskSpool gained a bytes-written hook.

## Commits
- 4ab06ee: Task 1, D-12a overflow without fault
- 1903d60: Task 2, worker read-error hardening
- (Task 3 hook commit follows in git log: "feat(12-02): add DiskSpool.SetBytesWrittenHook")

## What changed
- committer.go: `EnqueueCompletion` only logs (rate-limited, no event fields) on overflow; the drop is counted by `enqueueAsync` as before. `clearWriteErrorFault` no longer re-sets queue_full. `setFault(dropHardLimit|dropWriteError)` call count unchanged at 3.
- Tests rewritten (not deleted): `TestCommitterQueueFullShedsWithoutFault`, `TestE2EAllowedNotDelayedByStuckDisk` now assert shed + counted + not faulted + not saturated + never degraded; new `TestCommitterHardFaultsStillSaturate` proves hard_limit and write_error still saturate; the e2e test still exercises the fail-closed pre-forward refusal using an explicit hard_limit fault.
- worker.go: `processFile` returns `(count, offset, reachedEOF, err)`. Cursor advance and prune on a historical segment only when err==nil and reachedEOF. CRC-valid bad-JSON frames (new `ErrInvalidRecordJSON` from `ReadFramedRecord`) are skipped and counted (`SkippedFrames()`). Torn tail (`io.ErrUnexpectedEOF`, now also wrapped by the partial-payload error, still `ErrCorruptedRecord`) on the newest segment retries without advancing; on a historical segment it ends the segment with a warning. `scanToNextMagic` now returns real I/O errors instead of mapping them to EOF.
- Read-error injection seam: unexported package var `readFramedRecord` (defaults to `ReadFramedRecord`); Test A overrides it, so it is hermetic and root-safe.
- spool.go: `SetBytesWrittenHook(func(int))` via `atomic.Pointer`, invoked after successful `WriteFrames` and `AppendPreForward`; no telemetry import, `Recorder` interface untouched.

## Deviations from Plan
- Worker tests live in new `worker_readerr_test.go` rather than `worker_test.go` (avoids editing shared file; same package).
- `recoverable()` still accepts a queue_full reason: `SetWriteFault("queue_full")` is still used directly by spool-level and fault-generation tests; it is unreachable from production paths now. Hard_limit recovery is byte-identical.
- `spool_recovery_test.go` had no queue_full overflow assertion, so was not modified.
- Test A RED-before-change was not separately demonstrated (seam did not exist pre-change); the old code `break`s on read errors and would have advanced the cursor.
- STATE.md not edited per orchestrator instruction; D-12a text above is for the orchestrator to record.

## Verification
`go build ./... && go vet ./internal/audit/ && go test -short -race ./internal/audit/ -count=1` all pass.

## Self-Check: PASSED
