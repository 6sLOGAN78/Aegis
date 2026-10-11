---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 01
subsystem: foundations
tags: [spiffe, pki, keyfile, testpki, metrics, mtls]
requires: []
provides:
  - identity.GatewaySPIFFEID / GatewayIDFromSPIFFE / ErrNotGatewayIdentity
  - pki.LoadCA
  - keyfile.ParseEd25519Public/Private, LoadEd25519Public/Private
  - testpki (New, ServerCreds, ClientCreds, ClientCredsForSPIFFE, ForeignClientCreds, NoCertClientCreds, WriteFiles)
  - telemetry keyset, security-state and gRPC identity rejection metrics
affects: [later phase 11 plans]
tech-stack:
  added: []
  patterns: [closed-label pre-initialised metrics, strict id regex]
key-files:
  created:
    - internal/identity/spiffe_gateway.go
    - internal/identity/spiffe_gateway_test.go
    - internal/pki/loadca_test.go
    - internal/keyfile/keyfile.go
    - internal/keyfile/keyfile_test.go
    - internal/testpki/testpki.go
    - internal/testpki/testpki_test.go
  modified:
    - internal/pki/pki.go
    - internal/telemetry/metrics.go
    - internal/telemetry/metrics_test.go
decisions:
  - "testpki stores testing.TB in the PKI so credential helpers keep the plan's signatures (ClientCreds(gatewayID))."
  - "WriteFiles(dir, gatewayIDs...) writes server.crt/key, ca.crt and cp-client-<id>.crt/key."
metrics:
  tasks: 3
  completed: 2026-10-11
requirements: [CTRL-03, CTRL-05, AUTH-01, CTRL-02]
---

# Phase 11 Plan 01: Shared foundations Summary

Gateway SPIFFE helpers, pki.LoadCA, a single Ed25519 keyfile parser, a shared in-memory mTLS test PKI, and seven new closed-label metrics.

## Spike outcome (A1 / A2)

- A1 confirmed: on a bufconn TLS server the handler sees `peer.AuthInfo.(credentials.TLSInfo)` with `VerifiedChains > 0`; `ExtractSPIFFEID` + `GatewayIDFromSPIFFE` yields the gateway id. The bare backend-facing id extracts but is not a gateway identity. Plaintext, foreign-CA and no-cert clients all surface as `codes.Unavailable` on the RPC.
- A2 confirmed: both `grpc.DialContext` with `WithContextDialer` and `grpc.NewClient("passthrough:///bufnet", ...)` work with TLS creds when ServerName is set.

## Commits

- 4aff4b5: gateway SPIFFE helpers, pki.LoadCA, keyfile
- 967bb96: testpki and spike
- 9801eaa: metrics

## Deviations from Plan

None material. Note: LoadCA tests live in `internal/pki/loadca_test.go` (new file) rather than appended to pki_test.go. Metrics are exposed as struct fields only (no Record* helpers), per the plan's exported names.

## Verification

`go vet` and `go test -race -count=1` pass for identity, pki, keyfile, testpki, telemetry; `go build ./...` passes. No `insecure` in testpki.go.

## Self-Check: PASSED
