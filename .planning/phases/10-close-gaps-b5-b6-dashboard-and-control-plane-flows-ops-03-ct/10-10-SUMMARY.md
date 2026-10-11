---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 10
subsystem: control
tags: [gateways, stale-eviction, convergence]
requires: []
provides:
  - "Stale gateway filter in HandleListGateways (AEGIS_GATEWAY_STALE_AFTER, default 15m)"
affects: [dashboard convergence denominator]
key-files:
  modified: [internal/control/gateway_handler.go]
  created: [internal/control/gateway_handler_stale_test.go]
decisions:
  - "Stale records hidden from list only, not deleted from storage"
  - "Rejected replicas exempt from stale filter (fail-closed)"
requirements-completed: [OPS-01, OPS-02]
completed: 2026-10-11
---

# Phase 10 Plan 10: Stale Gateway Eviction Summary

Gateways silent longer than AEGIS_GATEWAY_STALE_AFTER (default 15m, values <=60s or invalid fall back) are omitted from the gateway list, so dead records no longer inflate the convergence denominator; the 60s partitioned rule and ACK_STATUS_REJECTED visibility are unchanged.

## Tasks
1. Stale filter plus tests - commit 7ca6ebd. Pure helpers `gatewayStaleAfter()` and `isStaleReplica()` with table tests for all boundary cases. `go test ./internal/control/ -count=1` passes.

## Deviations from Plan
- Handler-level test not added: constructing the full server was not cheap; the filter is covered through the extracted pure functions (plan marked it optional).
- The worktree started on a different base than the expected commit; reset to 0087394 per the startup check (clean tree, no work lost).

## Self-Check: PASSED
