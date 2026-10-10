---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
plan: 02
subsystem: audit
tags: [audit, jti, migration, wal, pgx]
requires: []
provides:
  - CompletionEvent.JTI and AuditContext.SetJTI
  - audit_events.jti column (migration 000004)
  - 21-arg worker insert with jti last
affects: [09-03 (read side), revocation denial rows]
tech-stack:
  added: []
  patterns: [append-only omitempty WAL field, nullable bound param]
key-files:
  created: [migrations/000004_add_audit_jti.sql, internal/audit/jti_test.go]
  modified: [internal/audit/event.go, internal/audit/logger.go, internal/audit/worker.go, internal/audit/worker_test.go, internal/storage/db_test.go, tests/dr/dr_test.go]
key-decisions:
  - "jti appended as last column/$21 so existing index-based test overrides stay valid"
requirements-completed: [REV-03]
duration: 15min
completed: 2026-10-10
---

# Phase 9 Plan 02: JTI audit write path Summary

jti now flows token -> AuditContext.SetJTI -> CompletionEvent (clipped to 128) -> worker insert as bound arg $21 (NULL when empty) -> new nullable `audit_events.jti` column with partial index.

## Tasks
1. JTI on event and context: commit 1 (feat(09-02): add JTI to CompletionEvent and AuditContext)
2. Migration 000004 and worker insert: commit 2 (feat(09-02): persist jti via migration 000004 ...)

## Deviations from Plan
- Tests for both tasks were placed in a new `internal/audit/jti_test.go` rather than appended to event_test.go / logger_test.go (tooling constraint on appending); coverage is the same.
- Worktree base was behind the expected base commit; fast-forwarded via `git reset --hard` on the clean worktree per the startup check.

## Issues Encountered
- Host disk is 95% full, so the spool saturation check (ErrSpoolSaturated) fails pre-existing spool/worker/DR tests under the default temp dir. Verified green with `TMPDIR=/dev/shm go test -race -count=1 ./internal/audit/ ./internal/storage/ ./tests/dr/`. Not caused by this plan.
- gofmt reports unrelated pre-existing files; left untouched.

## Known Stubs
None.

## Self-Check: PASSED
