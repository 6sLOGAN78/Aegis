---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 12
subsystem: deployments/compose
tags: [compose, mtls, keyset, key-rotation]
requires: [11-03, 11-06, 11-07, 11-08]
provides: ["mvp/hardened/distributed compose profiles wired for authenticated gRPC stream, keyset dirs, separated signing keys, AEGIS_KEYS_DIR override"]
key-files:
  modified:
    - deployments/compose/docker-compose.mvp.yml
    - deployments/compose/docker-compose.hardened.yml
    - deployments/compose/docker-compose.distributed.yml
    - tests/compose/compose_test.go
requirements-completed: [REV-03, AUTH-01, CTRL-02, CTRL-03, CTRL-05]
duration: ~15min
completed: 2026-10-12
---

# Phase 11 Plan 12: Compose wiring for mTLS stream and keysets Summary

All three compose profiles now run the authenticated gRPC stream with keyset directories and separate JWT and snapshot signing keys, backed by five new hermetic lint tests.

## Accomplishments
- Control plane: AEGIS_GRPC_TLS_CERT_PATH/KEY_PATH, AEGIS_GRPC_CLIENT_CA_PATH, AEGIS_SIGNING_KEY_PATH, mounts for certs and snapshot-current.key.
- Gateways (gateway in mvp/hardened, gateway-1..3 in distributed): AEGIS_CP_* client cert envs matching the gateway id, keyset dir envs, reload interval 2s, read-only keyset directory mounts. No private keys mounted into gateways.
- demo-issuer: AEGIS_ISSUER_PRIVATE_KEY_PATH plus jwt-current.key mount.
- All key mounts use `${AEGIS_KEYS_DIR:-../keys}`.
- Tests added: TestControlPlaneMTLSWiring, TestGatewayCPClientCertMatchesGatewayID, TestGatewayKeysetDirsMounted, TestSigningKeysSeparated, TestKeysDirOverridable; allowlist extended; volumeMounts helper made safe for `${VAR:-default}` sources.

## Task Commits
Tasks 1 and 2 were committed together (single commit, so no red-only commit): `feat(11-12)` commit on the worktree branch (see git log).

## Verification
- `go test ./tests/compose -race -count=1` passes.
- `docker compose -f ... config -q` passes for all three profiles (render only, no containers started).

## Deviations from Plan
- Worktree base was not 2ac069f at start; reset to it per the branch check (no work lost).
- The plan said to remove AEGIS_ISSUER_PUBLIC_KEY / AEGIS_CONTROL_PLANE_PUBLIC_KEY / AEGIS_SIGNING_KEY_SEED envs; none were present in the compose files, so nothing to remove. Those two public-key names remain in the gateway allowlist (pre-existing).
- Gateways already depended on control-plane healthy; no change needed.

## Known Stubs
None.

## Self-Check: PASSED
