---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 09
subsystem: gateway
tags: [revocation, fail-closed, readiness, REV-03]
requires: [11-02, 11-08]
provides:
  - "Gateway 503 SECURITY_STATE_MISSING on user + workload listeners and /readyz while Redis marker is absent"
affects: [cmd/gateway, internal/proxy]
key-files:
  created: [cmd/gateway/securitystate.go, cmd/gateway/securitystate_test.go, internal/proxy/probe_securitystate_test.go]
  modified: [cmd/gateway/main.go, internal/proxy/probe.go]
decisions:
  - "Probe check is a trailing variadic ProbeOption so existing call sites are unchanged."
  - "Poller Missing() is true before first poll, on error, when not ready, or when last success is older than 3 intervals."
requirements-completed: [REV-03]
duration: 15min
completed: 2026-10-12
---

# Phase 11 Plan 09: Gateway security-state gate Summary

Gateway builds its revocation store with `WithRequireSecurityMarker`, maps `ErrSecurityStateMissing` to 503 `SECURITY_STATE_MISSING` (problem type security-state-missing) in both CheckRevocation branches, and /readyz reports 503 via a 1s marker poller (`proxy.WithSecurityState`) that recovers automatically.

## Commits
- 6bc91ff: probe option + poller
- d31cf3b: main.go wiring + tests

## Verification
`go test ./cmd/gateway ./internal/audit ./internal/proxy ./internal/revocation -race -count=1 -p 1` passes; `go build ./...` and `go vet ./tests/chaos` clean (TestEveryMainReasonIsClassified included).

## Deviations from Plan
- Behavior tests exercise the real revocation Store with miniredis (never authorizes/revokes while marker missing; PRINCIPAL_QUARANTINED only once marker present) plus a source-level wiring test, rather than invoking the full main.go handlers, which are closures inside main() and not constructible in tests. Probe tests are in a new file (probe_securitystate_test.go) so existing probe_test.go is untouched.

## Known Stubs
None.

## Self-Check: PASSED
