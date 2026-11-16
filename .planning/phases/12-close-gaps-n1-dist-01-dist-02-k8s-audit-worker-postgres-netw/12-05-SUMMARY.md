---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
plan: 05
subsystem: telemetry
tags: [metrics, prometheus, gateway, ast-guard]
requires: [12-02, 12-03, 12-04]
provides:
  - gateway call sites for policy, rate-limit and spool-bytes recorders
  - TestRecorderCallers AST guard
affects: [cmd/gateway, internal/telemetry]
key-files:
  created:
    - cmd/gateway/metrics_wiring.go
    - cmd/gateway/metrics_wiring_test.go
    - internal/telemetry/callers_test.go
  modified:
    - cmd/gateway/main.go
requirements: [DIST-02]
metrics:
  completed: 2026-11-16
---

# Phase 12 Plan 05: Gateway recorder wiring and dead-recorder guard Summary

Policy eval/decision, rate-limit rejection and spool-byte recorders now have production callers in the gateway, with reason labels bounded by the audit closed set and an AST guard that fails on any dead `*Metrics` recorder.

## Tasks

1. RED guard (cf56407): `TestRecorderCallers` reflects over exported `*Metrics` methods (empty allowlist, documented) and walks non-test Go files for selector uses, including method values. On the pre-wiring tree it failed, naming: RecordPolicyDecision, RecordPolicyEval, RecordRateLimitRejection, RecordSpoolBytes.
2. Wiring (d1950f0): `policyReasonLabel` (closed set else OTHER) plus unit test; `RecordPolicyEval` timed around both `Evaluate` calls (before error handling); `RecordPolicyDecision` on deny and allow paths for both listeners; `RecordRateLimitRejection(route.RouteID)` in both rate-limit deny branches; `diskSpool.SetBytesWrittenHook(metrics.RecordSpoolBytes)` after spool creation. Guard now green.

## Verification

go build ./..., go vet (gateway, telemetry), `go test -short -race ./cmd/gateway/` and the guard all pass.

## Deviations from Plan

None. Added a small `decisionReasonOrAllowed` helper so the allow label matches the audit allow reason default.

## Threat Flags

None. Labels are decision, closed-set reason and route_id only.

## Self-Check: PASSED
