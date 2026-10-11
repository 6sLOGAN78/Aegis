---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 02
subsystem: revocation / storage
tags: [rev-03, postgres, redis, dr]
requires: []
provides:
  - migrations/000005 (principal_quarantines, revoked_tokens)
  - storage.SecurityStateRepo
  - revocation marker gate, ReconstructRevocations, ListRevokedJTIs
  - SECURITY_STATE_MISSING governor outage reason
affects: [11-05, 11-08, 11-09]
key-files:
  created:
    - migrations/000005_create_security_state.sql
    - internal/storage/security_state_repo.go
    - internal/storage/security_state_repo_test.go
    - internal/revocation/security_state_test.go
  modified:
    - internal/revocation/store.go
    - internal/audit/governor.go
    - internal/audit/main_reasons_test.go
decisions:
  - "Marker EXISTS rides in the CheckRevocation pipeline and is evaluated before quarantine results, so a wiped Redis fails closed."
  - "Reconstruct functions chunk at 1000 keys per pipeline with a 2s timeout per chunk."
metrics:
  completed: 2026-10-11
requirements: [REV-03]
---

# Phase 11 Plan 02: Durable security state storage layer Summary

Postgres migration and repo for quarantines and jti revocations, plus a Redis security-state marker gate (ErrSecurityStateMissing), chunked jti/quarantine restore, and the SECURITY_STATE_MISSING governor reason.

## Commits
- d81a831: migration 000005 and SecurityStateRepo
- 9a8f405: revocation store marker gate and jti restore
- 9bbc199: governor reason

## Deviations from Plan
- New revocation tests live in `internal/revocation/security_state_test.go` rather than `store_test.go` (the environment blocked appending to the existing file; the tests run in the same package).
- Worktree base was behind the expected base commit, so it was reset to 54df0b8 at startup as prescribed.

No stubs. No new threat surface beyond the plan's threat model. No new dependencies (google/uuid already in go.mod).

Verification: `go vet ./internal/...` and `go test ./internal/storage ./internal/revocation ./internal/audit ./internal/telemetry ./tests/dr -race -count=1` all pass.

## Self-Check: PASSED
