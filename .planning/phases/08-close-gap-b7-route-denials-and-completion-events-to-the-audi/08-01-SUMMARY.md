---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 01
subsystem: audit
tags: [audit, worker, postgres, migration, normalization, openapi]
requires: []
provides:
  - "audit.EventTypeDecision/EventTypeCompletion/EventTypeDenial constants and audit.ValidEventType"
  - "CompletionEvent.EventType and CompletionEvent.SuppressedCount (omitempty JSON)"
  - "(*CompletionEvent).Normalize() chokepoint (NUL strip, UTF-8 repair, column-width clip, 2048-byte path cap)"
  - "AuditWorker 20-argument insert with typed event_type and nullable suppressed_count"
  - "migration 000003 (audit_events.suppressed_count INT NULL)"
  - "denial value in OpenAPI / generated Go / dashboard event_type contract"
affects: [08-02, 08-03, audit pipeline producers]
tech-stack:
  added: []
  patterns: ["normalize-before-insert chokepoint in the worker", "direct WAL frame writes in tests to avoid statfs gate"]
key-files:
  created:
    - internal/audit/event_test.go
    - migrations/000003_add_audit_suppressed_count.sql
  modified:
    - internal/audit/event.go
    - internal/audit/worker.go
    - internal/audit/worker_test.go
    - tests/dr/dr_test.go
    - internal/storage/db_test.go
    - api/openapi/control-v1.yaml
    - pkg/api/control/v1/types.gen.go
    - web/dashboard/src/api/types.ts
key-decisions:
  - "Explicit event_type allowlist; untyped or unknown values fall back to the legacy status-based guess (D-03)"
  - "Normalize is applied in the worker on a per-event value copy, so spool bytes are never altered (D-18)"
  - "Non-UUID event_id/request_id are replaced with fresh UUIDs so one hostile record cannot wedge a batch"
requirements-completed: []
duration: ~20min
completed: 2026-10-09
---

# Phase 8 Plan 01: Audit record type, normalization and worker hardening Summary

Explicit `event_type` (decision/completion/denial) and `suppressed_count` on `CompletionEvent`, a `Normalize()` chokepoint applied by the worker before every insert, a 20-argument typed insert, additive migration 000003, and `denial` added to the three event_type contract surfaces.

AUD-03 and AUD-04 are intentionally not marked complete here (only plan 08-12 decides that from live evidence).

## Tasks and commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Event type fields, constants, Normalize chokepoint | 12b8c47 |
| 2 | Worker typed insert, normalization, suppressed_count, migration 000003 | 385598e |
| 3 | `denial` in OpenAPI, generated Go, dashboard TS union | d191f3d |

## Names defined for later plans

- `audit.EventTypeDecision`, `audit.EventTypeCompletion`, `audit.EventTypeDenial` (string consts)
- `audit.ValidEventType(s string) bool`
- `audit.MaxCanonicalPathBytes = 2048`
- `CompletionEvent.EventType string` (`json:"event_type,omitempty"`), `CompletionEvent.SuppressedCount int` (`json:"suppressed_count,omitempty"`)
- `(*CompletionEvent).Normalize()` nil-safe, idempotent, in place
- Generated Go constant `controlv1.Denial AuditEventEventType = "denial"` (no existing `Denial` identifier, no generator target, so hand-edited in generated style)
- Test helpers in package audit (worker_test.go): `writeWorkerTestFrames(t, dir, events...) string`, `argMatcher`, `auditArgsWith`, `runWorkerOnce`

## Verification

All `go test` runs used `TMPDIR=/dev/shm/aegis-gotmp` (host root fs near 90% full); the directory was removed afterwards.

- `go test -race -count=1 ./internal/audit/ ./internal/storage/ ./tests/dr/` : ok
- New tests (TestNormalize*, TestEventJSON*, TestValidEventType, TestAuditWorker_EventType/NormalizesPoison/SuppressedCount/InsertStatementShape) : pass; new worker tests do not use NewDiskSpool/AppendPreForward.
- `go build ./...`, `go vet ./internal/audit/ ./internal/storage/ ./pkg/...` : ok; `gofmt -l pkg/api/control/v1/` empty.
- Full suite `go test -race -count=1 $(go list ./... | grep -v /tests/integration)`: everything ok except `benchmarks/TestPolicyEngine_LatencyBudget` (p50 280us vs 200us budget under parallel load). Re-run in isolation: ok. Timing-sensitive, unrelated to this plan.
- `grep -rn 'make(\[\]any, 19)' --include='*_test.go' .` prints nothing; the only audit-insert expectations are in internal/audit/worker_test.go and tests/dr/dr_test.go (both updated).

## Deviations from Plan

- Existing tests changed only as the plan prescribed: `anyAuditArgs()` in `internal/audit/worker_test.go` and `tests/dr/dr_test.go` went from 19 to 20 arguments because the insert legitimately gained a column. No assertions were weakened or removed.
- `TestAuditWorker_InsertStatementShape` (source-text check for `suppressed_count`, `$19, $20` and the `ON CONFLICT` clause) was added because pgxmock does not inspect SQL text; the plan's behavior 6 required it.
- Pre-existing, out of scope: `gofmt -l internal/audit` flags `logger.go`; left alone.
- `.planning/STATE.md` already carried uncommitted orchestrator edits before this plan started; they are included in the final docs commit together with this plan's state updates.

## Known Stubs

None.

## Threat Flags

None. No new network, auth or file-access surface; migration is additive and nullable.

## Self-Check: PASSED

- Files exist: event.go, event_test.go, worker.go, migrations/000003_add_audit_suppressed_count.sql, and the three contract files.
- Commits 12b8c47, 385598e, d191f3d present in git log.
