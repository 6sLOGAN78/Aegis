---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
plan: 05
subsystem: gateway
tags: [jwt, revocation, audit]
requires: [09-02]
provides: [jti fail-closed guard, jti in audit events]
affects: [cmd/gateway/main.go, internal/audit/governor.go]
key-files:
  created: [cmd/gateway/missing_jti_test.go]
  modified: [cmd/gateway/main.go, internal/audit/governor.go, internal/audit/governor_test.go]
requirements: [REV-03]
completed: 2026-10-10
---

# Phase 9 Plan 05: Gateway jti guard Summary

User listener now returns 401 TOKEN_MISSING_JTI for validated tokens lacking a jti, before CheckRevocation and outside the revStore guard; the jti is recorded via ac.SetJTI and the fallback event literal.

## Commits
- a3c363c: governor classification (classIdentified), missingJTI helper, tests (helper table + AST ordering + SetJTI wiring)
- see git log: guard + SetJTI + JTI in fallback event

## Deviations
None. ValidateBearerToken untouched. `go test -race ./cmd/gateway/ ./internal/audit/` passes (TMPDIR=/dev/shm).

## Self-Check: PASSED
