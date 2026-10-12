---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 13
subsystem: compose-smoke
tags: [smoke, dr, key-rotation, mtls]
requires: ["11-09", "11-12"]
provides: ["DR, KEYROT-JWT, KEYROT-SNAPSHOT, MTLS smoke sections", "REPLICA_POLL_LIMIT parameter", "KEYS_BACKUP restore trap"]
key-files:
  modified: [scripts/compose-smoke.sh, tests/compose/smoke_script_test.go]
requirements: [REV-03, AUTH-01, CTRL-02, CTRL-03, CTRL-05]
metrics:
  completed: 2026-10-12
---

# Phase 11 Plan 13: Smoke DR, KEYROT and MTLS sections Summary

The live smoke script now has DR, KEYROT-JWT, KEYROT-SNAPSHOT and MTLS sections, guarded by hermetic lint tests. The sections have not been run live; that is plan 11-15.

## Commits
- 2b912e3: feat(11-13): add DR, KEYROT and MTLS sections to compose smoke (Tasks 1 and 2 together, as the plan specifies, because the lint tests fail until the script lands)

## What was built
- `replica_poll` limit is now `REPLICA_POLL_LIMIT` (default 5). DR raises it to 60 and resets it afterwards.
- DR:
  - Quarantines the orders workload and revokes a developer jti, then checks the Postgres rows.
  - Runs `redis-cli FLUSHALL`, then polls one gateway for the fail-closed window. It records "observed fail-closed" or "recovered before first poll" and fails if any quarantined or revoked request returns 200.
  - Requires every replica to recover within 60s, then 403 PRINCIPAL_QUARANTINED and 403 TOKEN_REVOKED on every replica.
  - Requires `aegis_securitystate_reconstruct_total{result="ok"}` to increase.
- KEYROT-JWT and KEYROT-SNAPSHOT:
  - Mint a pair with `go run ./scripts/certificates -keypair`, add `next.pub`, and wait for the per-gateway reload metric.
  - Rewrite the private key in place and restart only demo-issuer or control-plane.
  - Prune the old pub. The JWT section asserts old and new tokens are both accepted after the switch, then old is 401 and new is 200 after the prune.
  - The snapshot section asserts gateways stay ready for 25s after the prune and the operator list shows every replica healthy.
  - Both assert gateway container StartedAt is unchanged.
- MTLS: plaintext and client-cert-less connections to control-plane:9090 are refused. A positive control with the gateway's `cp-client-<gw>` cert shows the refusals are meaningful. It also checks the `/control/v1/gateways` ids against the profile's gateway names and that `aegis_grpc_identity_rejections_total` is exposed.
- The exit trap calls `restore_keys`, which restores from `KEYS_BACKUP`, restarts any switched service and waits for gateway readiness.

## Deviations from Plan
- The DR quarantine target is the orders workload (SPIFFE id) instead of usr_developer_01. The revoked developer token and a quarantined developer would otherwise return an ambiguous denial code. This keeps the two assertions independent.
- The gateways list id field is read as `gateway_id // gatewayId // id` because the JSON field name could not be confirmed from the source.

## Verification
`bash -n scripts/compose-smoke.sh` and `go test ./tests/compose -race -count=1` pass. No Docker commands were run.

## Known Stubs
None.

## Self-Check: PASSED
