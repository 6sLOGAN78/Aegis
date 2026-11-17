---
phase: quick-261117-rvo
plan: 01
status: complete
requirements: [GW-01]
commits:
  - 2d22afe test(quick-261117-rvo): failing regression tests
  - 4c0093d fix(quick-261117-rvo): enforce body limit
---

# Quick 261117-rvo: GW-01 1 MiB body limit enforcement

Content-Length pre-check (413 problem+json) on all three ingress handlers, `*http.MaxBytesError` mapped to 413 in both reverse-proxy ErrorHandlers, plus a ModifyResponse drain so an early upstream 2xx cannot mask an oversize chunked body; `body_too_large` added to the closed rejection set.

## Red test output (before fix, commit 2d22afe)

```
--- FAIL: TestBodyLimit_ContentLengthOverLimitRejected (user, workload)
    expected: 413   actual: 200          (also: no application/problem+json, empty JSON body)
--- FAIL: TestBodyLimit_ChunkedOverLimitNeverSucceeds
    user:     expected: 413  actual: 502
    workload: expected: 413  actual: 200
FAIL	aegis/internal/proxy
```
(TestBodyLimit_ExactlyAtLimitForwarded passed, as intended.)

## Changes

- `internal/proxy/server.go`: `writeBodyTooLarge` helper; `ContentLength > MaxBodyBytes` check after header check in NewServer (no rejection recorder), userIngress and workloadIngress (records `body_too_large`).
- `internal/proxy/proxy.go`: both ErrorHandlers map `*http.MaxBytesError` to 413. The race fallback (plan Task 2 step 4) was needed: the workload chunked test returned 200 because the backend replied before the body hit the limit. Added `limitTrackingBody` (mutex-guarded, no-op Close) wrapping the outbound body and `modifyResponseEnforceLimit`, which drains the remainder (bounded by MaxBytesReader) and returns the MaxBytesError so ErrorHandler emits 413.
- `internal/telemetry/metrics.go` and `metrics_test.go`: `body_too_large` in `rejectionReasons`, RecordRejection switch, and drift-guard enumerations.

## Deviations

- [Rule 3] Worktree HEAD was reset to base 14a5a0e per the branch check; PLAN.md copied from the main checkout.
- Race fallback added because the test demonstrated the race (as the plan prescribed).

## Verification

- `go build ./... && go vet ./... && go test -short -count=1 ./...` green (one transient FAIL seen once in an early full run, not reproducible in 8 subsequent runs; failing package not identified).
- `go test -short -race -count=1 ./internal/proxy/ ./cmd/gateway/ ./internal/telemetry/` green.
- `gofmt -l .` clean. Nothing under web/, STATE.md or ROADMAP.md touched.

## Self-Check: PASSED
