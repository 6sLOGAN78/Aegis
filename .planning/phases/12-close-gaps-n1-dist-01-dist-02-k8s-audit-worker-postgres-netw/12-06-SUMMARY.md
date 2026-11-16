---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
plan: 06
subsystem: deployments
tags: [drain, graceful-shutdown, haproxy, kubernetes, compose, DIST-01]
requires: [12-04]
provides:
  - K8s gateway termination grace 70s with preStop sleep 5 and drain propagation delay 10s
  - Compose distributed gateway grace 60s with propagation delay 5s
  - HAProxy :9443 workload servers track the :8080 /readyz check
affects: [deployments/kubernetes/base/gateway, deployments/compose]
key-files:
  created:
    - tests/manifests/drain_grace_test.go
    - tests/compose/drain_grace_test.go
  modified:
    - deployments/kubernetes/base/gateway/statefulset.yaml
    - deployments/kubernetes/base/gateway/configmap.yaml
    - deployments/compose/docker-compose.distributed.yml
    - deployments/compose/haproxy/haproxy.cfg
    - tests/compose/compose_test.go
requirements-completed: [DIST-01]
metrics:
  tasks: 2
  completed: 2026-11-16
---

# Phase 12 Plan 06: Drain grace budget and HAProxy tracking Summary

Termination grace periods sized for the two-phase drain (K8s 70s, compose 60s) with arithmetic-asserting tests, a K8s preStop sleep, and HAProxy workload servers that follow the :8080 /readyz check.

## Tasks

| Task | Commit | Notes |
|------|--------|-------|
| 1 K8s drain budget | 759726f | grace 70, preStop `sh -c "sleep 5"`, configmap delay 10s; 5+10+30+10=55 <= 70 |
| 2 Compose + HAProxy | bbefdb0 | gateway-1..3 grace 60s, delay 5s; `server ... track aegis_http_back/gateway-N` (own `check` removed) |

Both tests were confirmed red before the edits and green after. `go test -short ./tests/...` passes.

## Deviations from Plan

**1. [Rule 3 - Blocking] Added AEGIS_DRAIN_PROPAGATION_DELAY to compose gatewayAllowlist**
- The existing env-allowlist lint in tests/compose/compose_test.go would reject the new env var.
- Files: tests/compose/compose_test.go (committed in bbefdb0).

## Notes

- `haproxy` binary is not installed, so haproxy.cfg syntax was verified by review and the string test only (assumption A1). Docker was not started.
- The worktree base was reset to 9d91c86 per the branch-check instruction (it started on an older base).

## Self-Check: PASSED
