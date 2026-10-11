---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 08
subsystem: gateway
tags: [keyset, rotation, mtls, grpc, gateway-identity]
requires: ["11-01", "11-04", "11-06"]
provides:
  - "cmd/gateway/wiring.go: setupKeysets, resolveGatewayIdentity"
  - "Gateway hot-reloads JWT and snapshot trusted keys from mounted dirs"
  - "Snapshot stream uses mTLS creds; gateway id bound to client cert"
affects: ["11-09 (also edits cmd/gateway/main.go)"]
key-files:
  created:
    - cmd/gateway/wiring.go
    - cmd/gateway/wiring_keyset_test.go
    - cmd/gateway/wiring_identity_test.go
  modified:
    - cmd/gateway/main.go
decisions:
  - "Fallback single keys remain constructed in main; setupKeysets overrides them when a dir is set and warns when not."
  - "AEGIS_CONTROL_PLANE_GRPC_ADDR for ServerName default is taken from cfg (which carries the localhost:9090 default) via a getenv wrapper in main."
metrics:
  tasks: 2
  completed: 2026-10-12
---

# Phase 11 Plan 08: Gateway key rotation and mTLS stream wiring Summary

Gateway now loads JWT and snapshot keys from AEGIS_JWT_KEYSET_DIR / AEGIS_SNAPSHOT_KEYSET_DIR with interval/SIGHUP reload (last good set kept), and dials the control plane with its own cert-derived identity over TLS 1.3 mTLS with no plaintext path.

## Tasks

| Task | Commit | Notes |
|------|--------|-------|
| 1 Keyset setup | feat commit (wiring.go + main.go + keyset tests) | setupKeysets; fail-closed startup; env fallback warnings |
| 2 Cert identity | feat commit (shared with task 1) + test commit | resolveGatewayIdentity; gateway-<uuid> removed |

## Deviations from Plan

- Tests live in new files `wiring_keyset_test.go` and `wiring_identity_test.go` instead of appending to the existing `wiring_test.go` (which is an AST-guard file with its own imports). Test names still match the plan's `-run` patterns.
- Tasks 1 and 2 implementation landed in a single feat commit because both helpers share wiring.go and the main.go edit; tests for Task 2 are a separate commit.
- `metrics` and `tokenValidator` construction moved earlier in main() so the keysets can be wired before the stream starts.
- internal/config/config.go not modified: the new env vars are read via os.Getenv in wiring.go (no config struct pattern required).
- The worktree base was reset to 36b23f2 per the branch check (it started at an older commit).

## Verification

`go build ./...`, `go vet ./cmd/gateway`, `go test ./cmd/gateway ./internal/keyset ./internal/snapshot -race -count=1` all pass. No `insecure` or `gateway-`+uuid in non-test gateway code.

## Known Stubs

None.

## Self-Check: PASSED
