---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
verified: 2026-10-12T00:00:00Z
status: human_needed
score: 5/5 must-haves verified
overrides_applied: 0
human_verification:
  - test: "Apply the Kubernetes manifests to a real cluster (kubectl apply -k, create-secrets.sh) and confirm pods go Ready"
    expected: "Gateway StatefulSet, audit sidecar, control plane and backends start; probes pass; PVC binds"
    why_human: "Live K8s bring-up deferred by D-19; kubectl not installed. Only hermetic manifest cross-checks exist."
---

# Phase 11 Verification Report

**Goal:** Wire key rotation (B8), DR quarantine reconstruct, K8s manifests (B2), and CR-02 mTLS for the control-plane gRPC stream.
**Mode:** initial verification. SUMMARY claims were not trusted; code and tests were checked directly.

## Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Gateway loads trusted keysets via SetTrustedKeys (rotation without downtime) | VERIFIED | `cmd/gateway/wiring.go:62` builds `keyset.New("snapshot", dir, interval, verifier.SetTrustedKeys)`. The reloader is in `internal/keyset/reloader.go`. `tests/rotation` runs the expand, switch, prune sequence and passes. |
| 2 | ReconstructQuarantines runs at startup before readiness | VERIFIED | `cmd/control-plane/main.go:312` calls `securityState.Reconstruct` synchronously and `log.Fatalf`s on failure, before listeners start. It calls `store.ReconstructQuarantines` (`internal/control/securitystate.go:193`). The gateway has a fail-closed `/readyz` gate (`secPoller.Missing`, `cmd/gateway/main.go:977`). `tests/dr` passes. Live DR/KEYROT PASS on gateway-1/2/3 per orchestrator smoke. |
| 3 | K8s manifests match the real binaries (ports, probes, PVC spool, configmap keys) | VERIFIED (hermetic) | The StatefulSet declares ports 8080, 8443 and 9091 and probes `/livez` and `/readyz`. The only emptyDir is `/tmp`. The WAL spool comes from `volumeClaimTemplates` (`wal-spool`). `tests/manifests` derives env keys from Go source and passes. Live bring-up is not done (see human item). |
| 4 | gRPC snapshot server requires mTLS against the Aegis CA; gateways dial with a client cert | VERIFIED | `internal/control/tlsconfig.go:34-35` sets `RequireAndVerifyClientCert` with `ClientCAs`. `cmd/control-plane/main.go:350` uses `grpc.Creds(grpcCreds)`. `cmd/gateway/wiring.go:101` resolves the identity and credentials. `internal/snapshot/tlsconfig.go` is the client side. Tests use a foreign-CA client (`internal/testpki`). Live MTLS PASS reported. |
| 5 | GatewayId in Ack/Pong/heartbeat is bound to the cert identity; mismatch is rejected | VERIFIED | `internal/control/server.go:65,69` derive the identity from the SPIFFE ID. `server.go:154` returns PermissionDenied on mismatch. `internal/identity/spiffe_gateway.go` does the parsing. `go test ./internal/control ./internal/identity` passes. |

Compose profiles use the authenticated stream: `tests/compose` passes. The orchestrator reports live smoke results for mvp, hardened and distributed.

## Behavioral Spot-Checks (run by verifier)

`go build ./...` is clean. These suites pass with `-count=1 -p 1`:
- `internal/control`, `internal/keyset`, `internal/snapshot`, `internal/revocation`, `internal/identity`
- `cmd/*` and `scripts/certificates`
- `tests/{chaos,compose,dr,failure,integration,manifests,rotation,security}`

## Requirements Coverage

| ID | Status | Evidence |
|----|--------|----------|
| REV-03 | SATISFIED | Durable security state, reconstruct at startup, reconcile loop, gateway fail-closed readiness. DR test passes. |
| AUTH-01 | SATISFIED | Keyset reload into the TokenValidator, rotation tests, separate JWT and snapshot keys. |
| CTRL-02 | SATISFIED | The snapshot Verifier keyset is rotatable via `SetTrustedKeys` in production wiring. |
| CTRL-03 | SATISFIED | The stream runs over mTLS, with a plaintext or wrong-CA client rejected in tests. |
| CTRL-05 | SATISFIED | Ack, Pong and heartbeat GatewayId are bound to the cert, with a mismatch rejected. |

All five IDs are marked complete or gap-closure in REQUIREMENTS.md (CTRL-02, CTRL-05 and REV-03 are listed as "Gap closure" in the traceability table). No orphaned IDs were found.

## Anti-Patterns and Known Gaps (non-blocking, tracked)

- **TBD/FIXME/XXX markers:** not scanned file by file. This is a warning-level gap.
- **`TestPolicyEngine_LatencyBudget`:** fails only under `-race`, and also on pre-fix commits. It is not a phase regression.
- **Distributed audit DEDUPE flake:** pre-existing, tracked in `.planning/todos/pending/distributed-dedupe-flake.md`.
- **Postgres TLS in cluster:** plaintext Postgres, tracked in a todo and out of scope.
- **WR-01 serialisation:** per-process only. A multi-replica control plane can lose a fresh quarantine for up to the 30s reconcile interval. This is documented in the header of `securitystate.go`.
- **11-REVIEW.md:** 1 critical and 10 warnings, all fixed (`fixed: 11`, `skipped: 0`).

## Human Verification Required

1. **Live Kubernetes deployment.** Apply the kustomize base on a real cluster and confirm the pods become Ready with the PVC bound and the probes passing. Live bring-up was deliberately deferred (D-19), so manifest correctness is verified only by hermetic cross-checks.

## Gaps Summary

No blocking gaps. All five truths are verified in code and by hermetic tests, and the orchestrator reports live compose evidence. The status is `human_needed` only because live Kubernetes validation was deferred.
