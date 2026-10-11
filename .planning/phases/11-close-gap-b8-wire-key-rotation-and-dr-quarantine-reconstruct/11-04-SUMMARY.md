---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 04
subsystem: control-plane gRPC / snapshot client
tags: [mtls, spiffe, grpc, cr-02, ar-01]
requires: [11-01]
provides:
  - control.NewServerTransportCredentials
  - control.SnapshotDistributionServer.SetMetrics
  - snapshot.NewClientTransportCredentials / GatewayIDFromClientCert
affects: [11-08 (gateway entrypoint wiring), 11-10 (tests/chaos, tests/integration migration)]
key-files:
  created:
    - internal/control/tlsconfig.go
    - internal/control/mtls_test.go
    - internal/snapshot/tlsconfig.go
    - internal/snapshot/tlsconfig_test.go
    - internal/snapshot/client_tls_test.go
  modified:
    - internal/control/server.go
    - internal/control/control_test.go
    - internal/snapshot/client.go
decisions:
  - "Metrics are injected via SetMetrics (nil default = no-op) so existing constructors keep compiling; the entrypoint plan must call it."
  - "control_test.go uses a single cert id (gateway-node-1) for most tests; setupTestControlPlaneAs(t, id) provides other ids."
metrics:
  tasks: 2
  completed: 2026-10-12
requirements: [CTRL-03, CTRL-05]
---

# Phase 11 Plan 04: mTLS on the snapshot stream Summary

StreamSnapshots now derives the gateway id only from the verified client cert SPIFFE /gw/<id>; any Ack, Pong or heartbeat id mismatch ends the stream with PermissionDenied and records nothing. The snapshot client has no insecure default.

## Commits
- c9bd624: server-side identity binding, server credential loader, MTLS tests
- d8f5060: client insecure default removal, client credential loaders, static check

## Deviations from Plan
- Client-side tests live in new files `internal/snapshot/client_tls_test.go` (plus `tlsconfig_test.go`) rather than editing `client_test.go`; there were no existing insecure dials in that file to migrate. A recording fake server is used because importing control from package snapshot would be an import cycle.
- `testpki.WriteFiles` signature is `(dir, gatewayIDs...)` (per 11-01), not `(t, dir)`.
- MTLS tests use `insecure.NewCredentials()` in `mtls_test.go` deliberately to prove plaintext rejection (test-only file, excluded by the static check).

## Known follow-ups
- cmd/gateway/main.go still calls NewStreamClient without dial options; it fails closed until Plan 08 wires real creds. tests/chaos and tests/integration callers are migrated by Plan 10.
- The server's SetMetrics must be called by the control-plane entrypoint for rejection counters to be exported.

## Verification
`go build ./...`, `go vet ./cmd/... ./internal/...`, and `go test ./internal/control ./internal/snapshot ./internal/identity -race -count=1` pass.

## Self-Check: PASSED
