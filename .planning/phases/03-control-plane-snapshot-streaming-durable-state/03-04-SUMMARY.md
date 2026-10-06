---
phase: 03-control-plane-snapshot-streaming-durable-state
plan: 04
subsystem: durable-audit-wal
tags: [wal, spool, fsync, non-repudiation, pgx, postgresql, cursor, audit-worker]

requires:
  - phase: 03-control-plane-snapshot-streaming-durable-state
    plan: 01
    provides: "PostgreSQL schema and partitioned audit tables"
  - phase: 03-control-plane-snapshot-streaming-durable-state
    plan: 02
    provides: "Dynamic snapshot manager and streaming client"
  - phase: 03-control-plane-snapshot-streaming-durable-state
    plan: 03
    provides: "Distributed rate limiting and token revocation store"
provides:
  - "Local append-only disk WAL spool with synchronous pre-forward fsync and CRC32 framing"
  - "90% volume capacity saturation circuit breaker halting admission with HTTP 503 (AUDIT_SPOOL_SATURATED)"
  - "Atomic durable cursor checkpoint tracker in wal.cursor"
  - "Asynchronous background worker tailing WAL segments and batch flushing to PostgreSQL with ON CONFLICT DO NOTHING"
  - "Standalone audit-worker daemon binary with graceful signal handling"
affects:
  - "04-operator-dashboard-policy-management-convergence"
  - "05-distributed-resilience-performance-slo"

tech-stack:
  added: []
  patterns:
    - "Pattern 4: Pre-Forward WAL Append with fsync and 90% Saturation Gate"
    - "Binary Framing with Magic Header (0xAE615001), CRC32 Checksum, and Length Prefix"
    - "Atomic File Checkpoint Tracker using Temporary File and Atomic Rename"
    - "At-Least-Once Async Batch Ingestion using pgx.Batch with ON CONFLICT DO NOTHING"

key-files:
  created:
    - internal/audit/spool.go
    - internal/audit/spool_test.go
    - internal/audit/cursor.go
    - internal/audit/worker.go
    - internal/audit/worker_test.go
    - cmd/audit-worker/main.go
    - tests/failure/spool_saturation_test.go
  modified:
    - internal/audit/logger.go
    - cmd/gateway/main.go

key-decisions:
  - "Enforced synchronous pre-forward fsync on local append-only WAL before any upstream proxy dispatch (AUD-01, Invariant 10, ADR-0006)"
  - "Framed WAL records with 4B magic (0xAE615001), 4B CRC32 checksum, and 4B length prefix; worker detects corruption and resynchronizes via magic scanning"
  - "Activated 90% spool saturation circuit breaker halting admissions with HTTP 503 Service Unavailable (AUDIT_SPOOL_SATURATED) before disk capacity is exhausted (AUD-02)"
  - "Persisted WAL offset checkpoints atomically via wal.cursor.tmp -> wal.cursor rename only after successful PostgreSQL commit, guaranteeing at-least-once delivery (AUD-03)"

patterns-established:
  - "Pre-forward durable audit WAL append with mandatory fsync"
  - "90% disk spool saturation fail-closed circuit breaker"
  - "Atomic crash-safe file offset cursor checkpoints"
  - "Decoupled asynchronous batch ingestion with pgx.Batch and database outage resilience"

requirements-completed:
  - AUD-01
  - AUD-02
  - AUD-03

duration: 20min
completed: 2026-10-06
---

# Plan 03-04: Local Append-Only Disk WAL Spool with Pre-Forward fsync, 90% Saturation Gate, and Async Audit Worker Summary

**Local append-only disk WAL spool with synchronous pre-forward fsync, 90% capacity saturation gate, atomic cursor checkpoints, and asynchronous batch worker flushing to PostgreSQL**

## Performance

- **Duration:** ~20 min
- **Tasks:** 2 completed
- **Files created/modified:** 9
- **Tests Passing:** 100% across all unit, failure, and security test suites (`go test -v -race ./...`)

## Accomplishments

- **Append-Only Disk WAL Spool with Pre-Forward fsync and CRC32 Framing (`internal/audit/spool.go`, `cmd/gateway/main.go`)**:
  - Implemented `DiskSpool` managing binary framed WAL segments (`wal-<timestamp>-<seq>.log`) with POSIX `0700` directory and `0600` file permissions (AUD-01).
  - Enforced 12-byte binary framing: `[Magic: 0xAE615001][CRC32: 4B][Length: 4B][Payload: NB][\n: 1B]`.
  - Added thread-safe `(ac *AuditContext) ToCompletionEvent(method, clientIP, snapshotVersion)` for generating structured decision records prior to dispatch.
  - Wired `AppendPreForward` into both user and workload gateway request pipelines: synchronously forces non-volatile persistence via `os.File.Sync()` (`fsync` syscall) **before** proxy dispatch. If disk write or `fsync` fails, upstream backend requests are never dispatched (Invariant 10).
  - Enforced 90% disk saturation safety gate: actively monitors directory quota and underlying filesystem blocks via `syscall.Statfs`; immediately halts request admissions with HTTP 503 Service Unavailable (`AUDIT_SPOOL_SATURATED`), dropping zero audit records (AUD-02).
  - Implemented unit tests in `internal/audit/spool_test.go` covering record writes, CRC32 verification, bit-flip corruption rejection, segment rotation on size threshold, and directory reopen recovery under the race detector.
  - Implemented failure integration test `tests/failure/spool_saturation_test.go` verifying that when spool utilization exceeds 90%, gateway returns HTTP 503 (`AUDIT_SPOOL_SATURATED`) with RFC 7807 problem details, and mock upstream backends receive zero requests.

- **Persistent Cursor Tracker and Asynchronous PostgreSQL Ingestion Worker (`internal/audit/cursor.go`, `internal/audit/worker.go`, `cmd/audit-worker/main.go`)**:
  - Implemented `CursorTracker` recording `wal.cursor` state (`SegmentFile`, `Offset`, `UpdatedAt`) using temporary files and atomic `fsync` + `os.Rename` to guarantee crash-safe checkpoints.
  - Implemented `AuditWorker` discovering WAL segments in chronological order, validating magic header and CRC32 checksums, and buffering records into configurable batches.
  - Implemented corruption recovery: automatically scans forward for the binary magic header (`0xAE615001`) to resynchronize on corrupted segments without losing clean records.
  - Flushed batches to PostgreSQL `audit_events` partitioned table using `pgx.Batch`:
    `INSERT INTO audit_events (...) VALUES (...) ON CONFLICT (event_date, event_id) DO NOTHING`
  - Advanced persistent cursor offset checkpoint **only after** PostgreSQL batch commits successfully, guaranteeing at-least-once delivery (AUD-03).
  - Built standalone `cmd/audit-worker/main.go` daemon with environment variable configuration and graceful SIGINT/SIGTERM handling.
  - Implemented unit tests in `internal/audit/worker_test.go` using `pgxmock/v4` verifying batch flushing, idempotent deduplication, cursor retention on DB errors, worker restart recovery, and corruption tolerance.

## Task Commits

Each task was committed atomically and pushed to `main`:

1. **Task 1: Pre-forward append-only disk WAL spool with fsync, CRC32, and 90% saturation gate** - `9f2ab88` (feat)
2. **Task 2: Persistent cursor tracking and async audit ingestion worker** - `8f194d9` (feat)

## Files Created/Modified

- `internal/audit/logger.go` - Added `ToCompletionEvent` method to `AuditContext`
- `internal/audit/spool.go` - Append-only disk WAL spool with pre-forward `fsync`, CRC32, and 90% saturation gate
- `internal/audit/spool_test.go` - Unit tests for disk spool under race detection
- `cmd/gateway/main.go` - Gateway pipeline integration with pre-forward WAL append and 503 saturation gate
- `tests/failure/spool_saturation_test.go` - Failure test verifying HTTP 503 and zero upstream calls on spool saturation
- `internal/audit/cursor.go` - Durable checkpoint tracker for `wal.cursor` with atomic rename
- `internal/audit/worker.go` - Asynchronous background worker tailing WAL and batching to PostgreSQL via `pgx.Batch`
- `internal/audit/worker_test.go` - In-memory PostgreSQL batch unit tests with `pgxmock`
- `cmd/audit-worker/main.go` - Standalone daemon binary connecting to PostgreSQL and running `AuditWorker`

## Decisions Made

- Enforced synchronous `os.File.Sync()` (`fsync` syscall) on local append-only WAL before any upstream proxy dispatch, strictly upholding Invariant 10 and ADR-0006 while isolating request latency from central PostgreSQL availability.
- Used binary framing with 4-byte magic (`0xAE615001`), IEEE CRC32 checksum, and length prefix; equipped worker with byte-scanning resynchronization so corrupted bytes in a segment do not prevent reading subsequent clean records.
- Activated 90% spool saturation circuit breaker halting admissions with HTTP 503 Service Unavailable (`AUDIT_SPOOL_SATURATED`) before disk capacity is exhausted, preventing host OS crashes during prolonged database outages.
- Persisted WAL offset checkpoints atomically via `wal.cursor.tmp` -> `wal.cursor` rename only after successful PostgreSQL commit, guaranteeing at-least-once delivery and surviving database outages without loss.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated pgxmock batch expectations in worker_test.go**
- **Found during:** Task 2 (Audit worker unit tests)
- **Issue:** `pgxmock` requires explicit argument matchers (`WithArgs`) on `ExpectExec` within `ExpectBatch()`, failing with "expected 0, but got 19 arguments".
- **Fix:** Added `anyAuditArgs()` helper providing 19 `pgxmock.AnyArg()` matchers for each batch execution expectation.
- **Files modified:** `internal/audit/worker_test.go`
- **Verification:** All 5 `TestAuditWorker` tests passed cleanly under `-race`.
- **Committed in:** `8f194d9` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 bug).
**Impact on plan:** Minor test setup correction for `pgxmock`. No architectural or production code impact.

## Next Phase Readiness

- Plan 03-04 completes all durable state and audit logging requirements for Phase 3 (AUD-01, AUD-02, AUD-03).
- Phase 3 is now fully complete (control plane persistence, snapshot streaming, freshness leases, Redis rate limiting/revocation, and durable WAL spool).
- Ready for Phase 4: Operator Dashboard, Policy Management, and Live Convergence.

---
*Phase: 03-control-plane-snapshot-streaming-durable-state*
*Plan: 04*
*Completed: 2026-10-06*
