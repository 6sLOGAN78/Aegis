---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
reviewed: 2026-10-12T14:01:21Z
depth: standard
files_reviewed: 40
files_reviewed_list:
  - cmd/control-plane/main.go
  - cmd/demo-issuer/main.go
  - cmd/gateway/main.go
  - cmd/gateway/securitystate.go
  - cmd/gateway/wiring.go
  - deployments/compose/docker-compose.distributed.yml
  - deployments/compose/docker-compose.hardened.yml
  - deployments/compose/docker-compose.mvp.yml
  - deployments/kubernetes/base/backends/admin.yaml
  - deployments/kubernetes/base/backends/orders.yaml
  - deployments/kubernetes/base/backends/payments.yaml
  - deployments/kubernetes/base/control-plane/configmap.yaml
  - deployments/kubernetes/base/control-plane/deployment.yaml
  - deployments/kubernetes/base/control-plane/kustomization.yaml
  - deployments/kubernetes/base/gateway/configmap.yaml
  - deployments/kubernetes/base/gateway/networkpolicy.yaml
  - deployments/kubernetes/base/gateway/statefulset.yaml
  - deployments/kubernetes/base/kustomization.yaml
  - deployments/kubernetes/secrets/create-secrets.sh
  - internal/audit/governor.go
  - internal/control/api.go
  - internal/control/quarantine_handler.go
  - internal/control/securitystate.go
  - internal/control/server.go
  - internal/control/tlsconfig.go
  - internal/identity/spiffe_gateway.go
  - internal/keyfile/keyfile.go
  - internal/keyset/reloader.go
  - internal/pki/pki.go
  - internal/proxy/probe.go
  - internal/revocation/store.go
  - internal/snapshot/client.go
  - internal/snapshot/tlsconfig.go
  - internal/storage/security_state_repo.go
  - internal/telemetry/metrics.go
  - internal/testpki/testpki.go
  - migrations/000005_create_security_state.sql
  - scripts/certificates/main.go
  - scripts/compose-smoke.sh
  - tests/chaos/test_helpers.go
findings:
  critical: 1
  warning: 10
  info: 6
  total: 17
status: issues_found
fix_scope: critical_warning
fix_status: all_fixed
fixed: 11
skipped: 0
---

# Phase 11: Code Review Report

**Reviewed:** 2026-10-12T14:01:21Z
**Depth:** standard
**Files Reviewed:** 40
**Status:** issues_found

## Fix Outcomes

Scope: Critical + Warning. Info findings (IN-01..IN-06) were out of scope and are left open.

| Finding | Outcome | Commit |
|---------|---------|--------|
| CR-01 | fixed: `GetActiveSnapshot` requires gateway identity | 341b8d5 |
| WR-01 | fixed: write-through serialised with Reconcile under the SecurityState mutex; regression test; multi-replica gap documented in `internal/control/securitystate.go` | 2ada0e2 |
| WR-02 | fixed: per-command error checks in `ListQuarantined` | f4ba125 |
| WR-03 | fixed: jti <= 128 and ttl_seconds <= 2592000 rejected with 400 before any write; created_by truncated to 64 bytes; OpenAPI schema updated | b598012 |
| WR-04 | fixed: malformed configured gateway public keys and a short `AEGIS_SIGNING_KEY_SEED` are fatal; demo fallback only when unset | 3889925 |
| WR-05 | fixed (fail-safe kept): rejected reloads log the offending file on every attempt, count errors, and set `aegis_keyset_reload_failing`; runbook section 5.1. Partial sets are still never applied. | 4591e4f |
| WR-06 | fixed: DB password passed via a 0600 temp file and `--from-file` | 41e06da |
| WR-07 | fixed (partial by design): dev keys stay tracked per D-24; `AEGIS_CERTS_DIR`/`AEGIS_KEYS_DIR` overrides, a real input guard, and a prominent README warning were added | 4f48ec8 |
| WR-08 | fixed: private keys written via 0600 temp file + rename; kept keys tightened to 0600 | 63ffb18 |
| WR-09 | fixed: half-present pairs derive the missing public key or fail; mismatched pairs fail | b63447c |
| WR-10 | fixed: `KEYS_BACKUP` deleted only after a fully successful restore | a1c58f5 |

## Narrative Findings (AI reviewer)

## Summary

I reviewed the Phase 11 diff (`54df0b8..HEAD`) for the durable security state (Postgres to Redis reconstruct and reconcile), the marker-gated fail-closed revocation check, mTLS identity binding on the control-plane gRPC stream, hot-reloadable keysets, and the secret and PKI tooling.

What holds up:
- The marker check sits in the same Redis pipeline as the quarantine and jti checks.
- Reconstruct writes the marker last.
- Keyset application is atomic under the reloader and consumer mutexes.
- The stream path derives the gateway id only from the verified peer certificate.

The main problem: identity binding was added to `StreamSnapshots` only. The sibling unary RPC `GetActiveSnapshot` lets any client certificate issued by the shared root CA (for example a workload certificate) read the full active snapshot. Other findings:
- An unquarantine-then-requarantine race with Reconcile can silently undo a fresh quarantine. This is the fail-open direction, which the existing comment does not document.
- The redis.Nil filter in `ListQuarantined` can hide real per-command errors.
- The handlers do not validate input against the new VARCHAR limits and do not guard against TTL overflow.
- The fail-open demo-key fallbacks are still in the binaries.
- Secret material is handled loosely in the scripts: a DB password in argv, file modes that are never corrected, and a backup deleted after a failed restore.

Known and tracked items are not repeated here: Postgres TLS, the DEDUPE flake, the 30s resurrection and partial-restore windows, and stray jti keys being left to expire by TTL.

## Critical Issues

### CR-01: `GetActiveSnapshot` has no gateway identity binding, so any CA-issued client cert can read the full snapshot

**Outcome:** fixed in 341b8d5

**File:** `internal/control/server.go:189-198` (with `cmd/control-plane/main.go:347`)
**Issue:** Phase 11 added `gatewayIdentityFromContext` and calls it only in `StreamSnapshots` (line 85). `GetActiveSnapshot` is registered on the same gRPC server with no interceptor, so it only needs the transport-level `RequireAndVerifyClientCert` against `AEGIS_GRPC_CLIENT_CA_PATH`. In every profile, that CA is the shared `root-ca.crt`, which also issues `workload-orders.crt` (`spiffe://aegis.local/workload/orders`, ClientAuth EKU) and `gateway-client.crt`. A workload pod, or anyone holding a workload key, can therefore call `GetActiveSnapshot` and receive every route, Rego policy module and identity mapping. The `/gw/<id>` restriction the phase claims (CTRL-03/CTRL-05, D-14) is bypassed through the unary RPC. `aegis_grpc_identity_rejections_total` never counts these calls.
**Fix:** Enforce identity on every RPC with server interceptors so new RPCs cannot skip it:
```go
func (s *SnapshotDistributionServer) GetActiveSnapshot(ctx context.Context, req *snapshotv1.GetActiveSnapshotRequest) (*snapshotv1.SnapshotEnvelope, error) {
	if _, reason, err := gatewayIdentityFromContext(ctx); err != nil {
		s.countRejection(reason)
		return nil, err
	}
	...
}
```
A stronger option is to add `grpc.ChainUnaryInterceptor` and `grpc.ChainStreamInterceptor` in `cmd/control-plane/main.go` that run `gatewayIdentityFromContext` for all methods. A separate client CA for `/gw/` certs would also stop non-gateway leaves from completing the handshake.

## Warnings

### WR-01: Reconcile prune can delete a freshly re-applied quarantine (fail-open window not covered by the documented trade-off)

**Outcome:** fixed in 2ada0e2 (serialised in-process; multi-replica control planes still need a cross-replica guard, see the securitystate.go header)

**File:** `internal/control/securitystate.go:229-271`
**Issue:** Reconcile takes the Redis snapshot (`redisQ`) first, then lists PG rows at line 255, then deletes every key in `redisQ` that has no row. Consider this interleaving:
1. Redis snapshot contains K.
2. An operator unquarantines K, deleting the PG row and the Redis key.
3. Reconcile lists PG, and K is absent.
4. An operator quarantines K again. The PG upsert and Redis SET both succeed and the API returns 200.
5. Reconcile calls `RemoveQuarantine(K)` and deletes the fresh key.

K then stays unquarantined until the next reconcile, up to 30s. The header comment only documents the opposite, fail-closed resurrection race. Here a principal the operator just quarantined, with a 200 response, keeps passing through the gateways.
**Fix:** Before deleting a stray key, re-check the PG row for that principal, for example with `GetQuarantine(ctx, id)` or a `SELECT 1 ... WHERE principal_id=$1 AND expires_at>NOW()`. Alternatively, delete atomically only if the value or generation still matches the snapshot: store a per-write nonce as the Redis value and use a Lua compare-and-delete. At minimum, document this fail-open direction next to the resurrection note.

### WR-02: `ListQuarantined` treats a whole-pipeline `redis.Nil` as success, which can hide real errors and turn TTL into 0

**Outcome:** fixed in f4ba125

**File:** `internal/revocation/store.go:199-220`
**Issue:** `pipe.Exec` returns the first failed command's error. If an early `GET` returns `redis.Nil` because a key expired between SCAN and GET, any later command failure (connection reset, timeout, OOM) is hidden. Those commands report `Val()==0`, so `TTL` is 0, not -2, and the entry is returned with `TTL: 0`. In `backfillFromRedis` (securitystate.go:197-199), `ttl <= 0` becomes 24h. A long-running quarantine (for example 30 days) is then written to Postgres with a 24h expiry, and that PG row becomes the source of truth. The quarantine is silently shortened. The comment on `backfillFromRedis` says TTL read errors are "never swallowed", which is not true on this path.
**Fix:** Check each command's error:
```go
_, _ = pipe.Exec(ctx)
for i := range keys {
	if err := ttlCmds[i].Err(); err != nil {
		return nil, fmt.Errorf("failed to read quarantine ttl: %w", err)
	}
	if err := getCmds[i].Err(); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("failed to read quarantine reason: %w", err)
	}
	...
}
```
Also, in backfill, skip `ttl == 0` instead of promoting it to 24h. Keep the 24h promotion for -1 (persistent) only.

### WR-03: Revocation and quarantine inputs are not validated against the new schema limits; TTL can overflow

**Outcome:** fixed in b598012

**File:** `internal/control/quarantine_handler.go:221-254`, `internal/control/quarantine_handler.go:140-151`, `migrations/000005_create_security_state.sql:4,11,13`
**Issue:**
- `revoked_tokens.jti` is `VARCHAR(128)`, but `HandleRevokeToken` only checks `JTI == ""`. Any jti longer than 128 characters now fails the PG write and returns a misleading `503 DEPENDENCY_FAILURE`, so the token cannot be revoked. Before this phase the Redis-only path accepted it.
- `created_by VARCHAR(64)` is filled from `sess.Username` with no bound. A long operator username makes every quarantine and revocation by that operator return 503.
- `time.Duration(req.TTLSeconds) * time.Second` overflows int64 for `ttl_seconds` above about 9.2e9. The overflow produces a negative TTL, so PG stores an `expires_at` in the past (pruned immediately by `DeleteExpired`), while `RevokeJTI` silently substitutes 24h. The API returns 200, but the revocation is not durable and does not survive a Redis loss.

**Fix:** Validate before any write: `len(req.JTI) <= 128` (400 otherwise), clamp `TTLSeconds` to a sane maximum (for example 30 days or the max token lifetime) and reject values above it, and truncate or validate `actor` to 64 characters, or widen the column to `TEXT`. Also return 400, not 503, for constraint violations.

### WR-04: Single-key fallbacks still fail open to the publicly known demo key on a parse error

**Outcome:** fixed in 3889925

**File:** `cmd/gateway/main.go:83-100`, `cmd/control-plane/main.go:63-68`
**Issue:** Phase 11 introduced `internal/keyfile`, whose contract is "never falls back to a default key", and D-09 (a configured key that fails to load is an error). Both binaries still have these paths:
- Gateway: a malformed `AEGIS_ISSUER_PUBLIC_KEY` or `AEGIS_CONTROL_PLANE_PUBLIC_KEY` logs a warning and trusts `defaultDemoSeed`, whose seed is in the source tree. An operator typo then makes the gateway accept JWTs, and snapshots, signed by anyone who reads the repo.
- Control plane: a configured `AEGIS_SIGNING_KEY_SEED` shorter than 32 bytes silently falls through to the demo seed. The log then says "AEGIS_SIGNING_KEY_PATH not set", which hides the misconfiguration. That contradicts the `loadSigningKey` doc ("usedSeed is true only when ... nothing was configured").

These paths are dormant only when the keyset dirs and the key path are set, as in the shipped compose and k8s profiles.
**Fix:** Parse with `keyfile.ParseEd25519Public` and `log.Fatalf` on error when the variable is set. In `loadSigningKey`, return an error when `AEGIS_SIGNING_KEY_SEED` is set but shorter than `ed25519.SeedSize`.

### WR-05: Keyset "keep last good" policy can keep a removed (compromised) key trusted indefinitely

**Outcome:** fixed in 4591e4f (made loud and observable; the keep-last-good semantics are intentionally unchanged)

**File:** `internal/keyset/reloader.go:80-121,146-160`
**Issue:** Any unreadable or unparseable file makes `readDir` fail the whole reload, and the previous set is kept. This includes editor backups such as `next.pub~`, a `README`, a half-written file, or a non-key entry added to the ConfigMap. If an operator prunes a compromised key and the same ConfigMap update (or a later one) contains one bad entry, every gateway keeps trusting the compromised key. The only signals are a log line and `aegis_keyset_reload_total{result="error"}`. The emergency-revocation step of the rotation runbook fails open without any visible failure.
**Fix:** Treat removals separately from parse failures. For example, apply the intersection of the last good set and the currently present valid files when the reload is partial, so a key whose file was deleted is always dropped. Alternatively, ignore files that do not match an explicit pattern such as `*.pub` or `keyset.pub.*`, and make `/readyz` or an alert fire when the reload error state persists.

### WR-06: Database password passed on the kubectl command line

**Outcome:** fixed in 41e06da

**File:** `deployments/kubernetes/secrets/create-secrets.sh:66-70`
**Issue:** `--from-literal=AEGIS_DB_PASSWORD="$AEGIS_DB_PASSWORD"` puts the password in kubectl's argv. Any local user can read it through `ps` or `/proc/<pid>/cmdline`, and audit or exec loggers can record it.
**Fix:** Pass the value through a file descriptor:
```bash
kubectl -n "$NS" create secret generic aegis-db-credentials \
  --from-env-file=<(printf 'AEGIS_DB_USER=%s\nAEGIS_DB_PASSWORD=%s\n' "$AEGIS_DB_USER" "$AEGIS_DB_PASSWORD") \
  --dry-run=client -o yaml | kubectl apply -f -
```
`printf` is a bash builtin, so the value never appears in an argv.

### WR-07: New private keys committed to git and the only Kubernetes provisioning path packages them

**Outcome:** fixed in 4f48ec8 (committed dev keys intentionally remain tracked per D-24; untracking and .gitignore were not applied)

**File:** `deployments/kubernetes/secrets/create-secrets.sh:12-14,40-64`, `deployments/keys/jwt-current.key`, `deployments/keys/snapshot-current.key`, `deployments/certs/cp-client-*.key`, `deployments/certs/control-plane-server.key`
**Issue:** This phase commits the JWT signing key, the snapshot signing key, all control-plane client keys and the control-plane server key. The repo has no `.gitignore`. `root-ca.key` was already tracked before this phase. `create-secrets.sh` hard-codes `CERTS`/`KEYS` to these tracked directories and has no override, so every cluster built with the base kustomization trusts keys that anyone with repo read access holds. With them, someone can forge user JWTs and signed snapshots, and mint `/gw/` client certs from the committed root CA. The "DEV KEYS ONLY" banner is the only guard. The root-CA guard (lines 20-24) greps the script's own text, so it can never detect a real packaging mistake made elsewhere.
**Fix:** Add `CERTS_DIR`/`KEYS_DIR` overrides and refuse to run against the tracked defaults unless `AEGIS_ALLOW_DEV_KEYS=1` is set. Untrack the `*.key` files and add a `.gitignore`. Have `scripts/certificates` generate the keys locally instead. Replace the self-grep guard with a real check, for example asserting that no generated object contains the root CA key bytes.

### WR-08: Generator writes keys with 0600 but never fixes the mode of existing files

**Outcome:** fixed in 63ffb18

**File:** `scripts/certificates/main.go:30-32,35-47`
**Issue:** `os.WriteFile(path, data, 0600)` applies the mode only when it creates the file. All `*.key` files are tracked in git and are checked out as 0664 (confirmed on disk: `deployments/certs/*.key` and `deployments/keys/*.key` are `rw-rw-r--`). Re-running the generator overwrites the contents and leaves them group- and world-readable. Compose then bind-mounts them read-only, keeping the same modes.
**Fix:** After writing, call `os.Chmod(path, 0o600)`, or write to a temp file created with 0600 and `os.Rename` it into place. Also make `writeTLSPair` and `writeEd25519` use this helper.

### WR-09: `writeKeys` skips regeneration when only one half of a pair exists, leaving a mismatched or missing public key

**Outcome:** fixed in b63447c

**File:** `scripts/certificates/main.go:210-219`
**Issue:** `if fileExists(k) || fileExists(pb) { keep }`. If `jwt-current.key` exists but `keyset-jwt/current.pub` was deleted, nothing is written. The gateway keyset dir is then empty, so the gateway fails at startup with "keyset dir contains no keys". If only the `.pub` survives and the private key was lost, the issuer has no key at all. In both cases the generator prints "Keeping existing", which hides the inconsistency.
**Fix:** If the private key exists, derive the public key from it (`priv.Public()`) and rewrite `current.pub` when it is missing or does not match. If only the public key exists, fail with an explicit error.

### WR-10: Smoke `restore_keys` deletes the backup even when the restore failed

**Outcome:** fixed in a1c58f5

**File:** `scripts/compose-smoke.sh:385-422`
**Issue:** Each restore step only prints `WARN: could not restore ...` on failure. Line 420 then runs `rm -rf "${KEYS_BACKUP}"` unconditionally. If a restore fails (disk full, permissions, or a `cp` glob failure after `rm -f "${KEYS_DIR}/${fam}"/*` already emptied the keyset dir), the only copy of the original keys is destroyed. The stack is left with the rotation-drill keys, or an empty keyset dir that fails gateways closed at the next restart. When `AEGIS_KEYS_DIR` points outside the git tree, this cannot be recovered.
**Fix:** Track failures in a local flag. Delete `KEYS_BACKUP` only if every restore step succeeded. Otherwise keep it and print its path: `echo "keys backup retained at ${KEYS_BACKUP}" >&2`.

## Info

### IN-01: Quarantine and unquarantine report 503 after the durable write already succeeded

**File:** `internal/control/quarantine_handler.go:145-161,190-202`
**Issue:** Postgres is written first. If the Redis write then fails, the operator gets `503 ... Failed to propagate ...`, but Reconcile applies the change within 30s anyway. An operator who reads the 503 as "not done" may take unnecessary or conflicting action. This matters most for unquarantine, which still takes effect.
**Fix:** Return a distinct code or body such as `202 PERSISTED_PENDING_PROPAGATION` when the PG write succeeded and only Redis failed.

### IN-02: `aegis_security_state_ready` help text describes the control plane but only the gateway sets it

**File:** `internal/telemetry/metrics.go:202-207`, `cmd/gateway/securitystate.go:40-46`
**Issue:** The help text says "control plane security state has been reconstructed", but the control plane never sets this gauge, so it always reads 0 there. Separately, `SecurityState.Run` (securitystate.go:291) counts reconcile errors under `aegis_securitystate_reconstruct_total{result="error"}`, while successful reconciles are not counted. The error ratio is therefore misleading.
**Fix:** Correct the help text, or set the gauge in the control plane from `Ready()`. Use a separate `reconcile_total` counter.

### IN-03: Compose control-plane healthchecks do not use the new `/readyz`

**File:** `deployments/compose/docker-compose.mvp.yml:62`, `docker-compose.hardened.yml`, `docker-compose.distributed.yml:104`
**Issue:** The healthcheck still probes `/control/v1/auth/me` and accepts any response, so compose reports the control plane healthy while the security state is missing. Kubernetes already uses `/readyz`.
**Fix:** `test: ["CMD-SHELL", "curl -sf http://127.0.0.1:8084/readyz"]`.

### IN-04: Raw 64-byte Ed25519 private keys are accepted without checking the embedded public half

**File:** `internal/keyfile/keyfile.go:72-74,93-95`
**Issue:** `ed25519.Sign` uses `priv[32:]` as the public key inside the signature hash. A 64-byte key whose second half does not match its seed produces signatures that no gateway can verify. The failure shows up as rejected snapshots or tokens instead of a load error.
**Fix:** In `fromRaw`, for 64-byte input, check `bytes.Equal(ed25519.NewKeyFromSeed(raw[:32])[32:], raw[32:])` and return an error if they differ.

### IN-05: Gateway pod NetworkPolicy now allows Postgres egress for the gateway container too

**File:** `deployments/kubernetes/base/gateway/networkpolicy.yaml:58-65`
**Issue:** NetworkPolicy works at pod level, so moving the audit worker into a sidecar also gives the internet-facing gateway container a network path to Postgres. That widens the blast radius of a gateway compromise, even though the credentials are only in the sidecar's env.
**Fix:** Document this as an accepted trade-off of D-23, or move the WAL drain back to a separate pod that reads through a shared volume or a forwarding endpoint.

### IN-06: mTLS material (server cert, gateway cp-client cert) is loaded once with no reload

**File:** `internal/control/tlsconfig.go:20`, `internal/snapshot/tlsconfig.go:22`
**Issue:** JWT and snapshot keys are hot-reloadable, but TLS certificates are read only at startup. Rotating them requires restarting every gateway and the control plane, which is inconsistent with the zero-restart rotation the phase aims for.
**Fix:** Use `tls.Config.GetCertificate` / `GetClientCertificate` (and `GetConfigForClient` for the CA pool) backed by a file watcher similar to `keyset.Reloader`, or document the restart requirement in the credential-rotation runbook.

---

_Reviewed: 2026-10-12T14:01:21Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
