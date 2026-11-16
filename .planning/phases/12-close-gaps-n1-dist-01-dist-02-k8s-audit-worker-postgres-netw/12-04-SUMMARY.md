---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
plan: 04
subsystem: gateway
tags: [drain, graceful-shutdown, DIST-01]
requires: []
provides: [twoPhaseDrain, drainAwareHandler, parseDrainPropagationDelay]
affects: [cmd/gateway/main.go]
key-files:
  created: [cmd/gateway/drain.go, cmd/gateway/drain_test.go]
  modified: [cmd/gateway/main.go, cmd/gateway/wiring_test.go]
decisions:
  - "AEGIS_DRAIN_PROPAGATION_DELAY code default 0; invalid/negative falls back to 0"
metrics:
  completed: 2026-11-16
---

# Phase 12 Plan 04: Two-phase gateway drain Summary

Two-phase SIGTERM drain: readiness fails immediately, gateway keeps serving for AEGIS_DRAIN_PROPAGATION_DELAY (second signal skips it), then Shutdown drains in-flight; workload responses carry Connection: close while draining.

## Tasks

1. Task 1 (drain.go + drain_test.go): committed together (RED step folded into one commit; tests pass with -race).
2. Task 2 (main.go wiring + AST guard TestWiringTwoPhaseDrain asserting main() calls twoPhaseDrain and drainAwareHandler).

## Deviations from Plan

**[Rule 3 - Blocking] TestWiringShutdownOrder adapted.** It required a direct dualServer.Shutdown call in main(); now Shutdown is passed as a method value to twoPhaseDrain, so the test uses the twoPhaseDrain call as the HTTP-drain position. Ordering assertions (drain, audit pipeline, spool close) are unchanged. File: cmd/gateway/wiring_test.go.

Also: the worktree HEAD was reset to the plan base 11b3ca5 per the startup check.

## Verification

`go build ./...`, `go vet ./cmd/gateway/`, `go test -short -race ./cmd/gateway/` all pass.

## Self-Check: PASSED
