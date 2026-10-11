---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 03
subsystem: dev PKI
tags: [pki, certs, ed25519, keysets]
requires: ["11-01"]
provides:
  - scripts/certificates run(outDir, keysDir) with CA reuse, -keys-dir, -keypair
  - deployments/certs control-plane-server.* and cp-client-<id>.* (7 ids)
  - deployments/keys dev JWT and snapshot keys plus keyset directories
key-files:
  created:
    - scripts/certificates/main_test.go
    - deployments/keys/
  modified:
    - scripts/certificates/main.go
    - deployments/certs/
metrics:
  tasks: 3
  completed: 2026-10-12
requirements: [CTRL-03, CTRL-05, AUTH-01, CTRL-02]
---

# Phase 11 Plan 03: Dev PKI and signing keys Summary

Generator now reuses the existing root CA via pki.LoadCA, emits a control-plane server cert and seven per-gateway `/gw/<id>` client certs, and writes separate Ed25519 JWT and snapshot keypairs and keysets.

## Commits
- be24164: generator code and tests
- 16cbd7e: deployments/certs regeneration (only deployments/certs staged)
- b0daa7e: deployments/keys dev signing keys and keyset directories

## Deviations from Plan
- The worktree was clean at start (no 16 pre-existing uncommitted cert modifications), so the committed root CA was reused. Root CA bytes are unchanged; 14 leaf files modified, 16 added.
- Task 3 generated keys directly into deployments/keys via `-keypair` rather than a scratch dir (sandbox blocked complex shell). deployments/certs untouched by that commit.

## Verification
`go test ./scripts/certificates ./internal/pki -race` and `go test ./tests/integration -run Bypass -race` pass; every cert verifies against root-ca.crt via openssl; 14 cp-client files; pub key files are 45 bytes; keys are 0600 and distinct.

## Self-Check: PASSED
