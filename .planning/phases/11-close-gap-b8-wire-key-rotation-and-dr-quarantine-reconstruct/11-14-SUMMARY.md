---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 14
subsystem: docs
tags: [runbooks, disaster-recovery, key-rotation, security-record]
requires: [11-09, 11-11, 11-12]
provides:
  - DR runbook matching the shipped Redis reconstruct mechanism
  - Credential-rotation runbook matching the keyset directory mechanism
  - AR-01 / T-10-02 / T-10-07-02 recorded as closed by Phase 11
affects: [11-15]
key-files:
  modified:
    - docs/runbooks/disaster-recovery.md
    - docs/runbooks/credential-rotation.md
    - .planning/phases/10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct/10-SECURITY.md
decisions:
  - "New residual accepted risk numbered AR-08 because AR-02 already exists (idempotency store)."
metrics:
  completed: 2026-10-12
  tasks: 2
---

# Phase 11 Plan 14: Runbooks and AR-01 closure Summary

DR and rotation runbooks rewritten against the code as built (Postgres tables, marker key, keyset directories), and AR-01 closed in the Phase 10 security record with named test evidence.

## Commits
- eb8a95e: runbooks rewrite
- b610918: 10-SECURITY.md AR-01 closure

## What changed
- **DR runbook section 2:** tables `principal_quarantines` / `revoked_tokens` in database `aegis` (fixed from `aegis_control`); Postgres-first write-through with 503 DEPENDENCY_FAILURE semantics; marker `aegis:security-state:generation` (no TTL, written last); gateway SECURITY_STATE_MISSING within ~1-3s (1s poll, 200ms timeout, missing after 3 stale intervals); control plane marker check every 2s and reconstruct with remaining TTLs; 60s SLO; metric `aegis_securitystate_reconstruct_total` (CP metrics port 9092); reconcile every 30s prunes stray quarantine keys only (jti keys expire by TTL); resurrection race and partial-restore window bounded by 30s; Postgres mandatory; GET /control/v1/quarantine returns `.quarantines` (not `.principals`); FLUSHALL drill; manual fallback.
- **Rotation runbook:** keysets are directories (`AEGIS_JWT_KEYSET_DIR`, `AEGIS_SNAPSHOT_KEYSET_DIR`), every regular non-dot file is a key; `AEGIS_KEYSET_RELOAD_INTERVAL` default 10s, clamped 1s-10m, compose 2s, plus SIGHUP; bad or empty reload keeps last good set (`aegis_keyset_reload_total{result="error"}`); separate JWT and snapshot keys; signers load `AEGIS_SIGNING_KEY_PATH` / `AEGIS_ISSUER_PRIVATE_KEY_PATH`; expand/switch/prune with overlap > poll + TTL + 30s; K8s names `gateway-jwt-keyset`, `gateway-snapshot-keyset`, `aegis-control-plane-signing-key`; compose `AEGIS_KEYS_DIR`; mTLS CA/cert rotation explicitly not hot reloaded.
- **10-SECURITY.md:** T-10-02 and T-10-07-02 now `mitigate`, `closed (Phase 11)`; AR-01 carries CLOSED note citing `gatewayIdentityFromContext` (internal/control/server.go), `NewServerTransportCredentials` RequireAndVerifyClientCert, internal/control/mtls_test.go tests, `TestOperatorTelemetrySpoofedGatewayRejected`, `TestCredentials_NoInsecureCredentialsInProductionCode`; audit-trail row added; AR-08 records the sibling-pod cert readability residual.

## Deviations from Plan
1. **[Rule 1 - Bug in plan]** Plan named the static test `TestNoInsecureCredentialsInProductionCode`; the real name is `TestCredentials_NoInsecureCredentialsInProductionCode` (internal/snapshot/tlsconfig_test.go). Used the real name.
2. **[Rule 1]** Plan asked for AR-02 for the residual risk, but AR-02 is already the idempotency-store entry. Used AR-08.
3. **[Rule 1]** Plan cited a "live MTLS section of scripts/compose-smoke.sh"; no such section exists in the file. Not cited. Live smoke evidence is pending and will come from plan 11-15; no live run is claimed here.
4. The runbook step-by-step and file formats (base64 keys via `go run ./scripts/certificates -keypair`) were taken from deployments/keys/README.md. The "republish a snapshot before pruning the snapshot key" advice is precautionary and not verified by a test.

## Verification
Greps: SECURITY_STATE_MISSING count 4 in DR runbook; no `aegis_control`; no `BEGIN` in either runbook; `keyset.pub`/`current.pub` and SIGHUP present in rotation runbook; AR-01 row contains CLOSED; AR-08 present; gatewayIdentityFromContext referenced.

## Known Stubs
None.

## Self-Check: PASSED
