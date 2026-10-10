---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
plan: 03
subsystem: control-api
tags: [openapi, oapi-codegen, audit, quarantine, revocation]
requires: []
provides:
  - control-v1.yaml quarantine/revocation routes
  - AuditEvent.Jti in generated types and audit_repo read path
affects: [audit_repo, dashboard types]
key-files:
  modified:
    - api/openapi/control-v1.yaml
    - pkg/api/control/v1/types.gen.go
    - web/dashboard/src/api/types.ts
    - internal/storage/audit_repo.go
    - internal/storage/audit_repo_test.go
decisions:
  - QuarantineRequest.reason kept required in spec (relaxing makes it *string and breaks handler compile)
metrics:
  tasks: 2
  completed: 2026-10-10
---

# Phase 9 Plan 03: Control API contract and audit jti Summary

Spec now documents GET /principals/quarantine, GET+POST /quarantine, POST /revocations (with encoded-id note on path-form routes); types regenerated with oapi-codegen v2.5.0 (diff-clean); audit list/get return `jti` (NULL omitted).

## Commits
- 99d37b2: spec, types.gen.go, types.ts
- 7aa1733: audit_repo jti + tests

## Deviations from Plan

- QuarantineRequest.reason left required (plan allowed relaxing since handler defaults it, but that would change the generated field to *string and break internal/control/quarantine_handler.go, owned by a parallel plan). Handler still defaults empty reason. Optional `principal_id` added.
- The audit_events table has no `jti` column in any SQL migration in this worktree. The SELECT now references `jti`, so a migration adding `audit_events.jti` (presumably from plan 09-01/09-02) must land before this runs against a real database. pgxmock tests pass.

## Verification
- regenerate-and-diff clean, `go build ./...`, `go vet ./pkg/api/...` pass
- `go test -race -count=1 ./internal/storage/ ./internal/control/` pass

## Self-Check: PASSED
