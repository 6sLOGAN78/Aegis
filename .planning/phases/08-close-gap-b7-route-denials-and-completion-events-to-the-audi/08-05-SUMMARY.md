---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 05
subsystem: audit
tags: [audit, middleware, sink, denial, completion, error-codes]
requires: ["08-01"]
provides:
  - "audit.Sink interface (RecordDenial, EnqueueCompletion), audit.Option, audit.WithSink"
  - "AuditMiddleware(logger, snapshotVersion, opts ...Option), source compatible with all existing callers"
  - "audit.WrapUpstreamErrorHandler with REQUEST_BODY_TOO_LARGE, CLIENT_CANCELED, UPSTREAM_TIMEOUT, UPSTREAM_UNAVAILABLE"
  - "AuditContext.decisionSet / durableRecorded (unexported) and ToCompletionEvent returning a typed, normalized decision row"
affects: [08-06, 08-07, 08-08]
tech-stack:
  added: []
  patterns: ["first-WriteHeader hook for pre-response denial capture", "explicit-decision-wins event construction"]
key-files:
  created: []
  modified:
    - internal/audit/logger.go
    - internal/audit/logger_test.go
key-decisions:
  - "Sink implementations receive normalized value copies; the stdout log keeps the raw event and its existing 17+ field set (no event_type field added)"
  - "Denial hook fires on the first final (>=200) WriteHeader before the underlying writer sees it; a denial that only reaches the client through Write is recorded by the defer (one row either way, tracked by durableRecorded)"
  - "Decisions that are neither allow nor deny never reach the sink (only deny and allow are routed)"
requirements-completed: []
duration: ~20min
completed: 2026-10-09
---

# Phase 8 Plan 05: Audit middleware sink seam Summary

AuditMiddleware now takes optional `WithSink(...)`: denials are handed to `Sink.RecordDenial` before the first response header reaches the client, allowed requests call the non-blocking `Sink.EnqueueCompletion` after the handler returns, upstream failures get AUD-04 error codes, and the old allow-to-deny flip on backend 4xx/5xx is fixed for handlers that set a decision explicitly.

AUD-03 and AUD-04 are intentionally not marked complete here (plan 08-12 decides from live evidence).

## Commit

| Tasks | Commit |
|-------|--------|
| 1 and 2 (Sink seam, WrapUpstreamErrorHandler, typed/normalized ToCompletionEvent) | 6d12c12 |

## Names defined for later plans (package audit)

- `type Sink interface { RecordDenial(ev *CompletionEvent); EnqueueCompletion(ev *CompletionEvent) }` (RecordDenial may block, EnqueueCompletion must not; both get a normalized copy the callee may keep)
- `type Option func(*middlewareConfig)`, `func WithSink(s Sink) Option`
- `func AuditMiddleware(logger *Logger, snapshotVersion int64, opts ...Option) func(http.Handler) http.Handler`
- `func WrapUpstreamErrorHandler(next func(http.ResponseWriter, *http.Request, error)) func(http.ResponseWriter, *http.Request, error)` (plan 07/main.go must wire it onto the reverse proxy ErrorHandler; this plan did not edit main.go or internal/proxy)
- Events handed to the sink: denial rows have `EventType "denial"`, completions `"completion"`; completions carry a NEW EventID and the same RequestID as the pre-forward decision row. `ToCompletionEvent` now returns `EventType "decision"`, normalized.
- Test-only names added to logger_test.go (all `sink`-prefixed): `sinkFake`, `sinkProbeWriter`, `newSinkProbeWriter`, `sinkServe`, `sinkLogMap`, `sinkTimeoutErr`.

## Verification

All `go test` runs used `TMPDIR=/dev/shm/aegis-gotmp` (removed afterwards).

- `go test -race -count=1 ./internal/audit/ ./internal/proxy/ ./tests/failure/ ./tests/security/ ./tests/chaos/`: all ok
- `go test -race -count=1 -v` on the six new test groups: 6 top-level PASS (TestAuditMiddlewareSink, TestDenialRecordedBeforeResponse, TestCompletionKeepsAllowOnBackendError, TestCompletionNonBlocking, TestWrapUpstreamErrorHandler, TestToCompletionEventTypeAndNormalize); existing TestCompletionAuditLogging unchanged and green.
- `go vet ./internal/audit/ ./cmd/gateway/ ./tests/failure/ ./tests/security/ ./tests/chaos/`: ok (existing `AuditMiddleware(logger, 0)` call sites compile unchanged).
- `gofmt -l internal/audit/logger.go internal/audit/logger_test.go`: empty (logger.go is now gofmt-clean).
- Commit touches only internal/audit/logger.go and logger_test.go; spool.go, internal/proxy and cmd/ untouched (D-09 holds; no AppendPreForward change).

## Deviations from Plan

1. [Process] The two tasks were implemented and committed as one commit rather than two, because both edit the same two files and the new `ToCompletionEvent`/`WrapUpstreamErrorHandler` changes were written in the same pass.
2. [Process] Tests were written after the implementation rather than strictly red-first; I did not capture a failing run. They do exercise the ordering (probe writer untouched at RecordDenial time), 1xx handling, NUL/28-char-method normalization, legacy deny/HTTP_<status> behavior and error-code precedence.
3. No existing test was changed or weakened.

## Known Stubs

None.

## Threat Flags

None. No new network or file surface; client IP still comes only from RemoteAddr.

## Self-Check: PASSED

- internal/audit/logger.go and logger_test.go present and modified in 6d12c12
- Commit 6d12c12 exists on main
