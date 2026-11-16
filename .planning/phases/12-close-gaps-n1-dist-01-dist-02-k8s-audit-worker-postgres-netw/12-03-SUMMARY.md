---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
plan: 03
subsystem: telemetry
tags: [prometheus, histogram, grpc, metrics]
requires: []
provides:
  - aegis_http_request_duration_seconds histogram (method, route_id, status_class)
  - production callers for SetConnectedGateways and RecordSnapshotPublish
affects: [internal/telemetry, internal/control]
key-files:
  created:
    - internal/telemetry/metrics_duration_test.go
    - internal/control/server_metrics_test.go
  modified:
    - internal/telemetry/metrics.go
    - internal/telemetry/http_middleware.go
    - internal/control/server.go
decisions:
  - "RecordRequest left unchanged (raw method label) to avoid altering existing series; only the new histogram is constrained."
metrics:
  completed: 2026-11-16
---

# Phase 12 Plan 03: Request-latency histogram and control-plane metrics Summary

Bounded-label request duration histogram observed by the gateway middleware, plus live connected-gateway gauge and snapshot-publish counter wired into the control plane.

## Tasks

| Task | Commit | Notes |
|------|--------|-------|
| 1 histogram + normalization + middleware | 4af4317 | normalizeMethod/statusClass closed sets, route_id "unknown" fallback |
| 2 gauge/counter in control plane | 2dec534 | gauge set under s.mu; counter in BroadcastSnapshot |

## Deviations from Plan

- Tests for Task 1 placed in new file `internal/telemetry/metrics_duration_test.go` instead of `metrics_test.go` (same package, avoids touching existing tests).
- Did not use `prometheus/testutil` (would require go.mod changes); tests use Registry.Gather.
- Left `RecordRequest` method label un-normalized (plan permitted either). Grep of `aegis_http_requests_total` found only tests/integration/operator_telemetry_test.go and planning docs; no deployments or dashboards.
- Actual gauge metric name is `aegis_control_plane_connected_gateways`.

## Verification

`go build ./...`, `go vet`, and `go test -short -race ./internal/telemetry/ ./internal/control/` pass. RED was observed for both tasks before implementation.

## Known Stubs

None.

## Self-Check: PASSED
