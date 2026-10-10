---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
plan: 04
subsystem: control-plane
tags: [quarantine, spiffe, redis, b10]
requires: [09-03]
provides: [identity.ParseSPIFFEID, canonicalPrincipalID, body-form quarantine]
affects: [internal/control, internal/identity]
key-files:
  modified:
    - internal/identity/spiffe.go
    - internal/identity/spiffe_test.go
    - internal/control/quarantine_handler.go
    - internal/control/api_test.go
decisions:
  - "Single-decode enforced via pathPrincipalID: re-escape chi param when RawPath is empty (chi already decoded it)"
metrics:
  tasks: 2
  completed: 2026-10-10
---

# Phase 9 Plan 04: Quarantine principal id decode/validate Summary

Quarantine handlers now PathUnescape once, strictly validate SPIFFE ids via the new `identity.ParseSPIFFEID`, and accept the body form `POST /control/v1/quarantine`, so Redis keys equal the gateway's `quarantine:principal:spiffe://aegis.local/...` check.

## Commits
- 03f643f: feat(09-04): ParseSPIFFEID with tests
- 825ae19: feat(09-04): decode/validate quarantine ids, body form, tests

## Deviations from Plan

**[Rule 1 - Bug] Double-decode when chi has no RawPath.** For `a%2541`, httptest/chi yields an already-decoded `a%41` (RawPath empty), and the plan's PathUnescape would produce `aA`, bypassing the guard. Fix: `pathPrincipalID` re-escapes the param when `r.URL.RawPath == ""`; body ids are escaped before canonicalization so they are treated literally. `%zz` is tested at unit level (`canonicalPrincipalID`) because httptest cannot construct that URL.

## Verification
`TMPDIR=/dev/shm go test -race -count=1 ./internal/control/ ./internal/identity/` passes, go vet clean.

## Known Stubs
None.

## Self-Check: PASSED
