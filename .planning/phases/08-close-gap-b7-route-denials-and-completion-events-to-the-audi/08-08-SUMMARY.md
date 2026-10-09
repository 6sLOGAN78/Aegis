---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 08
subsystem: gateway-wiring
tags: [gateway, audit, wiring, compose, shutdown-ordering, ast-guard]
requires: ["08-03", "08-05", "08-07"]
provides:
  - "Gateway binary routes denials and completions through audit.Pipeline on both listeners (B7 closed in code)"
  - "Upstream errors classified on both reverse proxies via audit.WrapUpstreamErrorHandler"
  - "MVP compose knobs AEGIS_SPOOL_SEGMENT_BYTES and AEGIS_AUDIT_SUPPRESS_WINDOW (shell-overridable, production defaults)"
  - "Source-level AST guard over main() wiring"
affects: [08-09, 08-10, 08-11, 08-12]
tech-stack:
  added: []
  patterns: ["go/parser guard for an inline-closure main()", "compose variable with production default for live-proof overrides"]
key-files:
  created:
    - cmd/gateway/wiring_test.go
  modified:
    - cmd/gateway/main.go
    - deployments/compose/docker-compose.mvp.yml
    - deployments/compose/docker-compose.hardened.yml
    - deployments/compose/docker-compose.distributed.yml
    - tests/compose/compose_test.go
key-decisions:
  - "Pipeline settings are read straight from the validated config fields; main.go does no env parsing for them"
  - "Only the MVP gateway exposes the two live-proof knobs; defaults equal the binary defaults and are asserted by TestGatewayAuditKnobsDefaultSafe"
requirements-completed: []
duration: ~25min
completed: 2026-10-09
---

# Phase 8 Plan 08: Gateway wiring and compose knobs Summary

The real gateway binary now builds an `audit.Pipeline` after the metrics registry, attaches it with `audit.WithSink` to both audit middleware instances, wraps both reverse-proxy ErrorHandlers, counts the three pre-middleware rejections, publishes the queue-depth gauge, and shuts the pipeline down between the HTTP drain and the spool close. AUD-03 and AUD-04 are intentionally not marked complete here (plan 08-12 decides from live evidence).

## Tasks and commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Wire the pipeline into cmd/gateway/main.go | 2119df9 |
| 2 | Compose knobs, allowlist, grace-period lint | 9fdf2d3 |
| 3 | Source-level wiring guard for main() | 758a118 |

## What changed

- main.go: `DiskSpoolConfig` gets `MaxSegmentBytes` and `HardLimitRatio` from config; `auditPipeline := audit.NewPipeline(diskSpool, metrics, ...)` placed right after `telemetry.NewMetrics()` followed by `metrics.InitAuditSuppressedLabels(audit.ClosedReasonLabels())`; `metrics.SetAuditQueueDepth(auditPipeline.QueueDepth())` in the existing 1s gauge ticker; `rp.ErrorHandler = audit.WrapUpstreamErrorHandler(rp.ErrorHandler)` on both proxies; `audit.WithSink(auditPipeline)` on both middleware constructions; `dualServer.SetRejectionRecorder(metrics)`; `auditPipeline.Shutdown` with its own 5s context directly after `dualServer.Shutdown` and before stream/metrics/redis shutdown and `diskSpool.Close`. Net diff is wiring only; handlers were not restructured.
- D-09: the two `diskSpool.AppendPreForward(` call sites and their saturation/write-error handling are untouched. `git diff -U0 94cbd2f -- cmd/gateway/main.go` has zero changed lines mentioning AppendPreForward; `grep -c` is 2 in both the base commit and now. spool.go was not touched.
- Compose: MVP gateway env gains `AEGIS_SPOOL_SEGMENT_BYTES=${AEGIS_SPOOL_SEGMENT_BYTES:-16777216}` and `AEGIS_AUDIT_SUPPRESS_WINDOW=${AEGIS_AUDIT_SUPPRESS_WINDOW:-60s}`. Hardened and distributed set neither. The `stop_grace_period` comment names the 5s audit pipeline budget in all three files (45s unchanged).
- compose_test.go: allowlist gains the nine audit/spool keys; `TestGatewayStopGracePeriod` now requires drain + 10s; new `TestGatewayAuditKnobsDefaultSafe`.
- wiring_test.go: six tests (`TestWiringAuditMiddlewareHasSink`, `TestWiringUpstreamErrorHandlerWrapped`, `TestWiringAppendPreForwardUntouched`, `TestWiringShutdownOrder`, `TestWiringPipelineAfterMetrics`, `TestWiringRejectionRecorder`) using exact-count assertions (2 middleware calls, 2 wrapped handlers, 2 AppendPreForward sites).

## Verification

All `go test` runs used `TMPDIR=/dev/shm/aegis-gotmp` (removed afterwards).

- `go build ./...` and `go vet ./cmd/... ./internal/... ./tests/compose/`: ok; `gofmt -l cmd/gateway tests/compose`: empty.
- `go test -race -count=1 ./cmd/... ./tests/compose/ ./internal/audit/ ./internal/config/ ./internal/telemetry/ ./internal/proxy/ ./tests/failure/ ./tests/security/ ./tests/chaos/ ./tests/dr/`: all ok (cmd/gateway runs 8 tests: 2 existing + 6 new, all pass).
- `docker compose -f deployments/compose/docker-compose.{mvp,hardened,distributed}.yml config -q`: exit 0 for each (no container started).
- Resolved MVP config with `AEGIS_SPOOL_SEGMENT_BYTES=65536 AEGIS_AUDIT_SUPPRESS_WINDOW=5s` shows `"65536"` and `5s`; with no override shows `"16777216"` and `60s`.
- Shutdown order by line in main.go: dualServer.Shutdown (970), auditPipeline.Shutdown (977), diskSpool.Close (993); `audit.NewPipeline` (134) follows `telemetry.NewMetrics()` (130).

## Notes for later plans

- 08-09 / 08-10 / 08-11 can shrink segments and the suppression window from the shell: `AEGIS_SPOOL_SEGMENT_BYTES=65536 AEGIS_AUDIT_SUPPRESS_WINDOW=5s docker compose -f deployments/compose/docker-compose.mvp.yml up ...` (MVP profile only). Config range: segment bytes >= 4096; suppress window 1s..1h.
- The distributed profile had no stop_grace_period comment before; one was added above each of the three gateways.
- Not covered by hermetic tests here: the live behavior of the built binary (belongs to plan 08-11).

## Deviations from Plan

1. [Process] The wiring tests (Task 3) were written after Task 1 rather than red-first; the negative case (remove one `WithSink`) was not exercised against a scratch copy, as the plan states it is not required. The AST assertions use exact counts.
2. [Minor] The distributed compose file had no stop_grace_period comment to update; comments were added above its three gateway grace periods so all three files carry the same text.
3. No existing test was changed or weakened. The only existing assertion that was tightened is the grace-period lint (drain+5s to drain+10s), as the plan directs; all three profiles still satisfy it with 45s.

## Known Stubs

None.

## Threat Flags

None. No new network endpoint; the new compose variables default to production values (T-08-42 asserted by test), shutdown ordering is test-guarded (T-08-43), AppendPreForward sites unchanged (T-08-44), both listeners require a sink (T-08-45).

## Self-Check: PASSED

- cmd/gateway/main.go, cmd/gateway/wiring_test.go, the three compose files and tests/compose/compose_test.go exist and are modified/created as listed.
- Commits 2119df9, 9fdf2d3, 758a118 present in git log.
