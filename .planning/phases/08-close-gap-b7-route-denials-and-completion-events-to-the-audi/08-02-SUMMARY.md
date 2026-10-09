---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 02
subsystem: audit
tags: [audit, spool, wal, group-commit, hard-limit, fail-closed]
requires: []
provides:
  - "DiskSpool.WriteFrames: one Write and one fsync per batch, gated by a hard limit (default 0.95)"
  - "frameEvent: frame builder identical in layout to AppendPreForward, no normalization"
  - "DiskSpoolConfig.HardLimitRatio and StatfsFunc (injectable statfs)"
  - "write-fault flag (SetWriteFault / ClearWriteFault / WriteFaulted) that makes CheckSaturation fail allowed traffic closed"
affects: [08-05, 08-06, 08-07]
tech-stack:
  added: []
  patterns: ["group commit under the existing spool mutex", "truncate-or-rotate recovery after a failed batch write", "injected statfs and quota so tests never depend on host disk usage"]
key-files:
  created:
    - internal/audit/spool_group_test.go
  modified:
    - internal/audit/spool.go
key-decisions:
  - "WriteFrames calls checkHardLimit only, never CheckSaturation, so the 0.90 gate and the fault flag cannot block the 90-95 percent headroom path (D-11, D-12)"
  - "Rotation happens after the whole batch is written and synced, so no frame straddles two segments and a segment may exceed MaxSegmentBytes by at most one batch (D-07, D-08)"
  - "AppendPreForward body is byte-identical to 94cbd2f (D-09); it fails closed on the fault flag only through CheckSaturation"
requirements-completed: []
duration: ~25min
completed: 2026-10-09
---

# Phase 8 Plan 02: Spool group commit, hard limit and write-fault gate Summary

The disk spool now has a second write path for denial and completion records: `WriteFrames` appends a batch of pre-built CRC frames with one write and one fsync, gated by a hard limit above the 0.90 admission gate. A write-fault flag turns the unchanged `AppendPreForward` gate into the fail-closed signal required by D-12.

AUD-03 and AUD-04 are intentionally not marked complete here (only plan 08-12 decides that from live evidence).

## Tasks and commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Injectable statfs, hard-limit predicate, configurable ratio, write-fault gate | 868dbf0 |
| 2 | WriteFrames group-commit writer with failure recovery | 2d11498 |

## Names defined for later plans (package audit, internal/audit/spool.go)

- `DiskSpoolConfig.HardLimitRatio float64` (0 means 0.95; must be > 0.90 and <= 0.99, else `NewDiskSpool` returns an error)
- `DiskSpoolConfig.StatfsFunc func(path string) (used, total uint64, err error)` (nil means real `syscall.Statfs`)
- `var ErrHardLimit = errors.New("audit spool at hard limit")`
- `func frameEvent(ev *CompletionEvent) ([]byte, error)` (package-private; callers call `ev.Normalize()` first)
- `func (s *DiskSpool) WriteFrames(frames [][]byte) error` (empty batch returns nil; closed spool errors; hard limit returns `ErrHardLimit` and writes nothing)
- `func (s *DiskSpool) HardLimitExceeded() (bool, error)`
- `func (s *DiskSpool) SetWriteFault(reason string)`, `ClearWriteFault()`, `WriteFaulted() (bool, string)`
- Unexported seams `writeFn func(*os.File, []byte) (int, error)` and `syncFn func(*os.File) error` on DiskSpool, used only by WriteFrames
- Test helpers in spool_group_test.go (do not redeclare): `fixedStatfs`, `newGroupSpool`, `groupSegments`, `readSegmentFrames`, `mustFrame`

## Verification

All `go test` runs used `TMPDIR=/dev/shm/aegis-gotmp` (host root fs near 90 percent full); the directory was removed afterwards.

- `go test -race -count=1 ./internal/audit/ ./tests/failure/ ./tests/chaos/ ./tests/dr/` : all ok
- New tests: TestHardLimitBand (statfs and quota subtests), TestWriteFaultGate, TestHardLimitRatioValidation, TestFrameEvent, TestWriteFramesClosedAndEmpty, TestGroupCommit, TestGroupCommitRotation (consumed end to end by an AuditWorker over pgxmock), TestWriteFramesHardLimit, TestWriteFramesFailureTruncates (write error, fsync error, truncate failure leading to rotation). `-count=5` on the group commit and WriteFrames tests was stable.
- `go vet ./internal/audit/` and `go build ./...` : ok
- D-09: sha256 of the `AppendPreForward` body equals the hash from `git show 94cbd2f:internal/audit/spool.go` (8bd6714e...).
- WriteFrames contains 0 references to CheckSaturation and 1 to checkHardLimit.
- Every NewDiskSpool call in spool_group_test.go sets VolumeQuotaBytes and StatfsFunc (shared helper `newGroupSpool` plus explicit calls).

## Deviations from Plan

- The first commit (868dbf0) was initially written with the `Co-Authored-By: Claude Opus 5.5` trailer from the orchestrator rules file, then amended (own, just-made commit, not yet used by anything) to the `Claude Sonnet 5.5` trailer given by the harness attribution instruction. Commit 2d11498 uses the Sonnet trailer. Earlier phase 8 commits carry the Opus trailer.
- Otherwise none; the plan executed as written. No existing test was changed.

## Known Stubs

None.

## Threat Flags

None. No new network, auth or file-access surface beyond the planned batched writer.

## Self-Check: PASSED

- internal/audit/spool.go and internal/audit/spool_group_test.go exist.
- Commits 868dbf0 and 2d11498 present in git log.
