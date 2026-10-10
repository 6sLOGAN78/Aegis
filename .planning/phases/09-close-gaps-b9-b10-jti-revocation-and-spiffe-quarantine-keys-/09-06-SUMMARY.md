---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
plan: 06
subsystem: tests, docs
tags: [integration-tests, runbook, jti, quarantine]
requires: [09-01]
provides:
  - integration login helpers using username/password
  - corrected quarantine and revocation runbook
affects: [tests/integration, docs/runbooks]
key-files:
  modified:
    - tests/integration/mvp_test.go
    - tests/integration/bypass_test.go
    - docs/runbooks/quarantine-and-revocation.md
decisions:
  - demoPassword helper honors AEGIS_DEMO_PASSWORD_<ROLE> override, else compose defaults
metrics:
  completed: 2026-10-10
requirements: [AUTH-04, OPS-01]
---

# Phase 9 Plan 06: Integration callers and runbook Summary

Integration login helpers now post {username,password} (via a `demoPassword` helper), and the quarantine/revocation runbook documents the real session-cookie + CSRF API, encoded SPIFFE paths, expires_at, jti revocation and TOKEN_MISSING_JTI.

## Tasks

1. Integration login helpers: commit 2319efe. `go vet ./tests/integration/` passes; no `{"role":...}` bodies remain. Tests were not run (need Docker), per plan.
2. Runbook: sections 3, 4, 6 rewritten (cookie + X-CSRF-Token, path/body/unquarantine forms with `%2F` encoding, `expires_at`, `/control/v1/revocations` with jti/reason/ttl_seconds, issuer login body, `TOKEN_MISSING_JTI`, aegis.local-only SPIFFE ids, 5s convergence per ADR-0005). Committed separately.

## Deviations from Plan

None. The spool-saturation environment note was not relevant: these tests were only compile-checked, as the plan specifies.

Note: the body-form quarantine (`principal_id` in body) is documented as specified; its handler support is owned by other plans in this phase.

## Self-Check: PASSED
