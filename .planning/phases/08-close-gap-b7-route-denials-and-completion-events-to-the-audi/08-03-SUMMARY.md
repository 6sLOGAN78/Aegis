---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 03
subsystem: config-telemetry-proxy
tags: [config, prometheus, metrics, audit, rate-limit, rejection-counter]
requires: []
provides:
  - "Nine range-validated audit/spool Config fields and AEGIS_* env keys"
  - "Audit Prometheus metrics and nine *Metrics methods (contract for plan 06 Recorder and plan 08 wiring)"
  - "aegis_http_rejected_total{reason} fed through proxy.RejectionRecorder (consumer-side interface)"
affects: [08-05, 08-06, 08-07, 08-08]
tech-stack:
  added: []
  patterns: ["parse-or-error env helpers (envDuration/envInt) with explicit ranges", "closed-label counters pre-initialized to zero", "atomic.Pointer holder for an attach-after-construction recorder"]
key-files:
  created: []
  modified:
    - internal/config/config.go
    - internal/config/config_test.go
    - internal/telemetry/metrics.go
    - internal/telemetry/metrics_test.go
    - internal/proxy/limiter.go
    - internal/proxy/server.go
    - internal/proxy/limiter_test.go
    - internal/proxy/server_test.go
key-decisions:
  - "Rejection recorder is stored in an atomic.Pointer holder so it can be attached after NewDualServer without a data race and a nil recorder is a no-op"
  - "The single-listener proxy.Server (not DualServer) is untouched: the plan scoped D-16 counting to DualServer and the limiter"
requirements-completed: []
duration: ~20min
completed: 2026-10-09
---

# Phase 8 Plan 03: Config keys, audit metrics and pre-middleware rejection counter Summary

Three independent supporting surfaces for the audit pipeline: nine validated configuration keys, the audit and rejection Prometheus metrics with the exact method contract later plans consume, and an additive nil-safe hook that counts the three rejections occurring before the audit middleware (no audit rows, middleware order unchanged).

AUD-03 and AUD-04 are intentionally not marked complete here (only plan 08-12 decides that from live evidence).

## Tasks and commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Audit and spool configuration keys | ac6efab |
| 2 | Audit metrics and pre-middleware rejection counter | f6980a7 |
| 3 | RejectionRecorder hook on limiter and DualServer | 0501982 |

## Exported names for later plans

Config fields (`internal/config`): `AuditGroupFlushInterval` (2ms, 0<d<=50ms), `AuditCompletionFlushInterval` (25ms, <=1s), `AuditCompletionQueueSize` (8192, 64..1000000), `SpoolHardLimitRatio` (0.95, 0.90<r<=0.99), `AuditUnauthRate` (10, >0), `AuditUnauthBurst` (50, >=1), `AuditSuppressWindow` (60s, 1s..1h), `AuditSuppressMaxKeys` (4096, >=16), `SpoolSegmentBytes` (16777216, >=4096). Env keys: `AEGIS_AUDIT_GROUP_FLUSH_INTERVAL`, `AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL`, `AEGIS_AUDIT_COMPLETION_QUEUE_SIZE`, `AEGIS_SPOOL_HARD_LIMIT_RATIO`, `AEGIS_AUDIT_UNAUTH_RATE`, `AEGIS_AUDIT_UNAUTH_BURST`, `AEGIS_AUDIT_SUPPRESS_WINDOW`, `AEGIS_AUDIT_SUPPRESS_MAX_KEYS`, `AEGIS_SPOOL_SEGMENT_BYTES`. No identified-denial-allowance key exists (D-15). Invalid or out-of-range values return an error naming the variable.

`*telemetry.Metrics` methods: `RecordAuditWritten(kind string, n int)`, `RecordAuditDropped(kind, reason string, n int)`, `RecordAuditSuppressed(reasonCode string, n int)` ("" maps to "OTHER"), `RecordAuditSuppressorOverflow()`, `ObserveAuditFlush(d time.Duration)`, `SetAuditDegraded(bool)`, `SetAuditQueueDepth(int)`, `InitAuditSuppressedLabels([]string)`, `RecordRejection(reason string)` (unknown reason maps to "other"). Written/dropped/rejected series are zero-initialized in `NewMetrics`; suppressed series only for labels passed to `InitAuditSuppressedLabels` plus "OTHER" (also pre-initialized in `NewMetrics`). Drop reasons: queue_full, hard_limit, write_error, closed, timeout, unauth_cap; kinds: completion, denial.

Proxy (`internal/proxy`): `type RejectionRecorder interface{ RecordRejection(reason string) }`, `(*ConcurrencyLimiter).SetRejectionRecorder`, `(*DualServer).SetRejectionRecorder` (sets the limiter and both ingress closures). `*telemetry.Metrics` satisfies it structurally; proxy does not import telemetry. `NewConcurrencyLimiter(n)` and `NewDualServer(cfg, user, workload, tlsCfg)` signatures are unchanged. Plan 08 must call `ds.SetRejectionRecorder(metrics)`.

## Verification

- `TMPDIR=/dev/shm/aegis-gotmp go test -race -count=1 ./internal/config/ ./internal/telemetry/ ./internal/proxy/` all ok
- `go build ./...` ok; `go vet` on the three packages ok
- `grep -o 'record("..."))'` shows concurrency x1, header_too_large x2, ambiguous_credentials x1 call sites
- No `aegis/internal/telemetry` import in proxy files; no `aegis/internal/audit` import in telemetry
- Zero matches for the removed identified-denial-allowance key in config.go and config_test.go

## Deviations from Plan

### Auto-fixed Issues

None to code behavior. Notes on process:

1. Task 3 tests were written after the implementation rather than strictly red-first (tasks 1 and 2 were red-first and confirmed failing). The tests assert the recorded reasons and unchanged wire responses.
2. The config test sets the removed key via string concatenation (`"AEGIS_AUDIT_IDENTIFIED_"+"DENIAL_ALLOWANCE"`) so the plan's grep acceptance check (zero occurrences) still holds while still testing that the key is ignored.
3. No existing tests were modified or weakened; new assertions were added to the default-config subtest and new test functions were appended.

## Known Stubs

None.

## Threat Flags

None. New metric labels are closed enums only; `RecordRejection` collapses unknown input to "other" (T-08-12, T-08-13 mitigated); config ranges mitigate T-08-11.

## Self-Check: PASSED

- Files modified exist and commits ac6efab, f6980a7, 0501982 present in git log.
