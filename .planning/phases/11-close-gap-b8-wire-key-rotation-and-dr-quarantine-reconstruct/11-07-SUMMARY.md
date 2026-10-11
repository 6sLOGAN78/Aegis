---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 07
subsystem: control-plane
tags: [mtls, grpc, postgres, security-state, keyfile]
requires: ["11-01", "11-04", "11-05"]
provides:
  - "control-plane binary with mTLS gRPC, mandatory Postgres, synchronous SecurityState reconstruct, keyfile signing key"
key-files:
  modified:
    - cmd/control-plane/main.go
    - cmd/control-plane/main_test.go
decisions:
  - "loadSigningKey usedSeed is true only for the demo seed; AEGIS_SIGNING_KEY_SEED keeps working silently as before"
  - "No AEGIS_ALLOW_NO_POSTGRES dev flag was kept; fallback removed entirely"
metrics:
  tasks: 2
  completed: 2026-10-12
---

# Phase 11 Plan 07: Control-plane entrypoint wiring Summary

Control-plane main now refuses to start without mTLS material, Postgres, or a valid configured signing key, and restores security state before serving.

## What changed
- Helpers `loadGRPCCredentials`, `loadSigningKey`, `requirePostgres` (generic) with unit tests (env names AEGIS_GRPC_TLS_CERT_PATH / _KEY_PATH / AEGIS_GRPC_CLIENT_CA_PATH).
- Signing key via `keyfile.LoadEd25519Private`; configured-but-invalid path is fatal, demo seed only when nothing is configured (warned).
- Postgres failure and migration failure are fatal; "in-memory state" fallback removed.
- `SecurityState` built with `WithPinger(pool.Ping)` and `WithSecurityStateMetrics`; `SetSecurityRepo`/`SetSecurityState` on the API; `Reconstruct` runs synchronously (30s timeout, fatal on failure) before HTTP and gRPC listeners; `go Run(runCtx)`, cancelled on shutdown.
- `distServer.SetMetrics(metrics)` so identity-rejection counters export.
- gRPC server uses `grpc.Creds`; creds are loaded before any listener starts.
- Source-level wiring test `TestMainWiring` asserts creds, SecurityState, SetSecurityState/Repo, SetMetrics, Reconstruct-before-listeners; /readyz wiring is therefore guarded.

## Deviations from Plan
None of substance. Note: worktree base was corrected via reset to 36b23f2 at start as instructed.

## Verification
`go build ./...`, `go vet ./cmd/control-plane`, `go test ./cmd/control-plane ./internal/control -race -count=1` all pass. No live run (belongs to 11-15).

## Commits
- 1279d4f: helpers (Task 1)
- 375b4ce: wiring (Task 2)

## Self-Check: PASSED
