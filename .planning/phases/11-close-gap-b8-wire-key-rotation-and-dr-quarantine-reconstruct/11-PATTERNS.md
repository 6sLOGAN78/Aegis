# Phase 11: Close gaps B8, B2, CR-02 - Pattern Map

**Mapped:** 2026-10-11
**Files analyzed:** 33 (new and modified)
**Analogs found:** 31 / 33 (2 have only partial analogs)

Note: `deployments/certs` is regenerated in ONE dedicated commit (D-24). No other task may stage it.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `internal/keyfile/keyfile.go` (new) | utility | file-I/O, transform | `cmd/control-plane/main.go:79-105` (key parsing) | role-match |
| `internal/keyset/reloader.go` (new) | service | batch/polling, event-driven (SIGHUP) | `internal/snapshot/client.go:95-133` (loop + ctx/stop) and `Verifier.SetTrustedKeys` | partial |
| `internal/identity/spiffe_gateway.go` (new) | utility | transform | `internal/identity/spiffe.go:23-64` | exact |
| `internal/pki/pki.go` (+`LoadCA`) | utility | file-I/O | `internal/pki/pki.go:26-66` (`NewCA`) | exact |
| `scripts/certificates/main.go` (modify) | utility/config | file-I/O, batch | itself, steps 2-6 (lines 62-149) | exact |
| `migrations/000005_create_security_state.sql` (new) | migration | CRUD | `migrations/000004_add_audit_jti.sql` | exact |
| `internal/storage/security_state_repo.go` (new) | model/repo | CRUD | `internal/storage/route_repo.go` | exact |
| `internal/storage/security_state_repo_test.go` (new) | test | CRUD | `internal/storage/audit_repo_test.go` (pgxmock) | exact |
| `internal/revocation/store.go` (modify: marker, `ReconstructRevocations`, option) | service | CRUD (Redis) | itself (lines 28-101, 160-188) | exact |
| `internal/control/securitystate.go` (new) | service | batch/event-driven loop | `internal/revocation/store.go:160-188` + `internal/snapshot/client.go` loop | role-match |
| `internal/control/quarantine_handler.go` (modify) | controller | request-response, CRUD | itself (lines 129-200) | exact |
| `internal/control/api.go` (modify: `HandleRevokeToken`, `/livez`, `/readyz`) | controller | request-response | `internal/proxy/probe.go:29-81` for probe shape | role-match |
| `internal/control/server.go` (modify: identity binding) | service (gRPC) | streaming | itself (lines 37-125) | exact |
| `internal/snapshot/client.go` (modify: remove insecure) | service (gRPC client) | streaming | itself (lines 135-162) | exact |
| `cmd/control-plane/main.go` (modify) | config/entrypoint | request-response | itself (lines 42-165, 282-295) | exact |
| `cmd/gateway/main.go` (modify) | entrypoint | request-response | itself (lines 76-105, 398-425, 722-745) | exact |
| `cmd/demo-issuer/main.go` (modify) | entrypoint | request-response | `cmd/control-plane/main.go:79-105` | role-match |
| `internal/proxy/probe.go` (modify) | middleware | request-response | itself (lines 38-81) | exact |
| `internal/audit/governor.go` (+reason) | config | transform | itself (lines 44-48) | exact |
| `internal/telemetry/metrics.go` (+metrics) | utility | pub-sub/metrics | existing `aegis_*` metrics in the same file | exact |
| `tests/chaos/test_helpers.go` (modify to mTLS) | test | streaming | itself (lines 644-703) | exact |
| shared test PKI helper (new, e.g. `internal/testpki`) | test util | file-I/O | `internal/pki/pki.go`, `tests/rotation/rotation_test.go` (inline PKI) | role-match |
| `internal/control/*_test.go` mTLS and write-through tests (new) | test | streaming/CRUD | `internal/control/control_test.go:58,360` | role-match |
| `tests/dr/dr_test.go` (repoint) | test | CRUD | `TestRedisQuarantineReconstruction` (lines 75-135) | exact |
| `tests/rotation/rotation_test.go` (repoint) | test | event-driven | `TestJWTIssuerKeyRotation` (line 101), `TestSnapshotSigningKeyRotation` (line 178) | exact |
| `tests/manifests/manifest_test.go` (extend) | test | file-I/O | itself (lines 13-60) | exact |
| `tests/compose/compose_test.go`, `smoke_script_test.go` (extend) | test | file-I/O | `TestGatewayWiring` (291-310), `TestSmokeScriptCoversJTIAndWorkloadQuarantine` (101-124) | exact |
| `scripts/compose-smoke.sh` (+DR, KEYROT, MTLS) | script | request-response | `=== REV-03 JTI` section (line 792+), `replica_poll` (line 198) | exact |
| `deployments/compose/docker-compose.{mvp,hardened,distributed}.yml` | config | n/a | `docker-compose.hardened.yml:101-111` cert mounts | exact |
| `deployments/kubernetes/base/gateway/{statefulset,configmap}.yaml` | config | n/a | themselves | exact |
| `deployments/kubernetes/base/control-plane/deployment.yaml`, `backends/*.yaml`, `audit-worker/*` (delete), `overlays/production/*` | config | n/a | gateway `statefulset.yaml` | role-match |
| `docs/runbooks/{disaster-recovery,credential-rotation}.md`, `10-SECURITY.md` | docs | n/a | themselves | exact |

## Pattern Assignments

### `internal/identity/spiffe_gateway.go` (utility, transform)

**Analog:** `internal/identity/spiffe.go`

Reuse `ExtractSPIFFEID(cert, "aegis.local")` (line 33) and `ParseSPIFFEID(id, trustDomain)` (line 55). Both route through `canonicalSPIFFE`, so the id comes back canonical (lowercase scheme and host). Sentinel errors are declared as a `var (...)` block (lines 11-16) in the form `errors.New(...)`, so declare `ErrNotGatewayIdentity` the same way.

```go
// spiffe.go:33-48
func ExtractSPIFFEID(peerCert *x509.Certificate, expectedTrustDomain string) (string, error) {
	if peerCert == nil { return "", ErrNoPeerCertificate }
	for _, uri := range peerCert.URIs {
		if uri == nil { continue }
		if strings.EqualFold(uri.Scheme, "spiffe") {
			return canonicalSPIFFE(uri, expectedTrustDomain, true)
		}
	}
	return "", ErrMissingSPIFFEID
}
```

New code: `GatewayIDFromSPIFFE(spiffe)` must `strings.CutPrefix(spiffe, "spiffe://aegis.local/ns/gateway/sa/aegis-gateway/gw/")` and match `^[A-Za-z0-9._-]{1,64}$`. The bare backend id `.../sa/aegis-gateway` must NOT match.

---

### `internal/pki/pki.go` (+`LoadCA`) (utility, file-I/O)

**Analog:** `NewCA` and `EncodeCertPEM` / `EncodeKeyPEM` in the same file.

The `CA` struct is `{Certificate *x509.Certificate; PrivateKey *ecdsa.PrivateKey; CertPool *x509.CertPool}` (lines 19-23). The key is encoded with `x509.MarshalECPrivateKey` and PEM type `"EC PRIVATE KEY"` (lines 156-166). `LoadCA(certPEM, keyPEM []byte) (*CA, error)` must be the exact inverse:

```go
// build with pem.Decode -> x509.ParseCertificate / x509.ParseECPrivateKey,
// then pool := x509.NewCertPool(); pool.AddCert(cert)
return &CA{Certificate: cert, PrivateKey: key, CertPool: pool}, nil
```

Error style: `fmt.Errorf("failed to ...: %w", err)` (see lines 29, 59).

Issuance helpers to reuse unchanged:
- `IssueWorkloadCert(spiffeURIStr)` (line 69) gives ClientAuth only, with the URI SAN. Use it for `cp-client-<id>`.
- `IssueServerCert(cn, dnsNames, ips)` (line 111) gives ServerAuth only. Use it for the control-plane server cert.

---

### `scripts/certificates/main.go` (utility, file-I/O)

**Analog:** itself.

Structure to keep: `writeCertFile` is 0644 and `writeKeyFile` is 0600 (lines 18-24). Each step is issue, `pki.EncodeKeyPEM(cert.PrivateKey.(*ecdsa.PrivateKey))`, write `.crt` and `.key`, then `fmt.Println("  [+] Generated ...")`. Errors use `log.Fatalf("Failed to ...: %v", err)`.

Change 1 (D-24): replace step 1 (`pki.NewCA`, lines 44-60) with "if `root-ca.crt` and `root-ca.key` exist in `outDir`, `pki.LoadCA`; else `NewCA` and write".

Change 2 (D-17): add after step 3.

```go
// per-gateway CP client certs (same helper as lines 80-95)
for _, id := range []string{"gateway","gateway-1","gateway-2","gateway-3","aegis-gateway-0","aegis-gateway-1","aegis-gateway-2"} {
    c, err := ca.IssueWorkloadCert("spiffe://aegis.local/ns/gateway/sa/aegis-gateway/gw/" + id)
    // write cp-client-<id>.crt / .key
}
// CP server cert (same helper as lines 63-77)
ca.IssueServerCert("control-plane", []string{"control-plane","aegis-control-plane","aegis-control-plane.aegis-system.svc","localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
```

Change 3 (D-09): Ed25519 keypair writer copies lines 135-149. The format is base64 of the 64-byte private key and base64 of the 32-byte public key, each followed by `\n`. Generate separate `jwt-issuer.{key,pub}` and `snapshot-signer.{key,pub}`. Factor lines 136-149 into a `writeEd25519(name)` helper.

---

### `migrations/000005_create_security_state.sql` (migration, CRUD)

**Analog:** `migrations/000004_add_audit_jti.sql`.

```sql
-- +goose Up
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS jti VARCHAR(128);
CREATE INDEX IF NOT EXISTS idx_audit_events_jti ON audit_events (jti) WHERE jti IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_audit_events_jti;
ALTER TABLE audit_events DROP COLUMN IF EXISTS jti;
```

Conventions: `-- +goose Up` and `-- +goose Down` markers, `IF NOT EXISTS` and `IF EXISTS` guards, and Down in reverse order. The full proposed DDL is in RESEARCH.md "Code Examples". Migrations are embedded via `migrations/migrations.go` (`migrations.FS`) and run through `storage.RunMigrations` (`internal/storage/migration.go:17-26`). No registration is needed beyond the filename.

---

### `internal/storage/security_state_repo.go` (repo, CRUD)

**Analog:** `internal/storage/route_repo.go`.

Repo shape (lines 42-50): a struct holding `db DBPool` and a `NewXRepo(db DBPool)` constructor. Record structs carry json tags. Queries live in `const xQuery = ` blocks. Upsert example (lines 52-75):

```go
const upsertServiceQuery = `
INSERT INTO services (id, environment, enabled, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, NOW(), NOW())
ON CONFLICT (id) DO UPDATE SET
    environment = EXCLUDED.environment, ...;`

_, err := r.db.Exec(ctx, upsertServiceQuery, svc.ID, ...)
if err != nil {
    return fmt.Errorf("failed to upsert service %q: %w", svc.ID, err)
}
```

Get-style errors map `pgx.ErrNoRows` to `ErrNotFound` (line 14, `ErrNotFound = errors.New("record not found")`).

Methods:
- `UpsertQuarantine`, `DeleteQuarantine`
- `ListUnexpiredQuarantines` (`WHERE expires_at > NOW()`)
- `UpsertRevokedToken`, `ListUnexpiredRevocations`
- `DeleteExpired`

Idempotent upsert (`ON CONFLICT (principal_id) DO UPDATE SET reason, expires_at, created_by`) is required by D-02.

**Test analog:** `internal/storage/audit_repo_test.go:1-40` uses `pgxmock.NewPool()`, `defer mock.Close()`, `repo := NewXRepo(mock)`, `mock.NewRows([]string{...})`, and testify `require`/`assert`. Imports are `github.com/pashagolub/pgxmock/v4`, `regexp` (for `ExpectExec(regexp.QuoteMeta(...))`) and `testify`.

---

### `internal/revocation/store.go` (service, CRUD on Redis)

**Analog:** itself.

Key naming: `"revocation:jti:"+jti` and `"quarantine:principal:"+id`. Add the constant `SecurityStateMarkerKey = "aegis:security-state:generation"`.

Core to extend, the `CheckRevocation` pipeline (lines 66-101):

```go
ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
defer cancel()
pipe := s.client.Pipeline()
var quarCmd, jtiCmd *redis.IntCmd
if principalID != "" { quarCmd = pipe.Exists(ctx, "quarantine:principal:"+principalID) }
if jti != "" { jtiCmd = pipe.Exists(ctx, "revocation:jti:"+jti) }
if quarCmd == nil && jtiCmd == nil { return false, "", nil }   // MUST NOT bypass the marker when option set
if _, err := pipe.Exec(ctx); err != nil {
    return false, "", fmt.Errorf("%w: %w", ErrRevocationDependency, err)
}
```

Changes:
- Add the opt-in `WithRequireSecurityMarker()` option. `NewStore(client *redis.Client)` (line 22) becomes variadic `NewStore(client, opts ...Option)` so existing callers still compile.
- Add `markerCmd := pipe.Exists(ctx, SecurityStateMarkerKey)` only when the option is on. If `Val()==0`, return `ErrSecurityStateMissing`, declared as `var ErrSecurityStateMissing = errors.New(...)` next to `ErrRevocationDependency` (line 14).
- The early return at line 84 must be skipped when the marker is required.
- `ReconstructRevocations` mirrors `ReconstructQuarantines` (lines 162-188): a pipeline of `pipe.Set(ctx, key, "1", ttl)`, and wrap errors with `fmt.Errorf("failed to reconstruct ...: %w", err)`. Its 2s timeout (line 166) must be chunked (about 1000 per pipeline) or scaled.
- Add `MarkSecurityStateReady(ctx)` (SET marker `<RFC3339>/<uuid>`, no TTL) and `SecurityStateReady(ctx) (bool, error)` for the gateway poller and CP loop.

---

### `internal/control/securitystate.go` (service, event-driven loop)

**Analog:** loop and shutdown style from `internal/snapshot/client.go:95-133` (select on `ctx.Done()` and `stopCh`, then sleep). Redis restore calls come from `revocation.Store`.

Pattern: `Run(ctx)` does a synchronous `Reconstruct` before the listeners start. It then uses a `time.NewTicker(2s)` to `EXISTS marker` and re-reconstructs on a miss, and a 30s reconcile ticker (add missing PG rows, delete expired). Order inside reconstruct: SET quarantines and revocations with remaining TTL (skip `<=0`, floor 1s), and only then SET the marker. It takes the repo as an interface so hermetic tests can use pgxmock or a fake, plus miniredis.

---

### `internal/control/quarantine_handler.go` and `HandleRevokeToken` in `api.go` (controller, request-response)

**Analog:** itself.

Current Redis-only write in `quarantineCore` (lines 144-149):

```go
if s.revStore != nil {
    if err := s.revStore.QuarantinePrincipal(r.Context(), principalID, req.Reason, ttl); err != nil {
        writeErrorResponse(w, r, http.StatusServiceUnavailable, "DEPENDENCY_FAILURE", "Failed to propagate quarantine to Redis: "+err.Error())
        return
    }
}
```

Target (D-02):
1. When `s.securityRepo != nil` (nil allowed so legacy hermetic tests keep working), `UpsertQuarantine` first. On error, 503 `DEPENDENCY_FAILURE` ("Failed to persist quarantine"), and no Redis write.
2. Then `revStore.QuarantinePrincipal` with the same TTL. On error, 503 `DEPENDENCY_FAILURE`, and the PG row is kept (reconcile heals it).
3. `HandleUnquarantinePrincipal` (lines 179-184) does PG `DELETE` first, then `RemoveQuarantine`.
4. `HandleRevokeToken` (line 196+) does the same with `revoked_tokens`.

Reuse unchanged:
- `canonicalPrincipalID` (lines 31-61), `maxPrincipalIDLen = 128` (matches the VARCHAR(128) columns).
- Actor from session: `GetSessionFromContext(r.Context())`, defaulting to `"sec-ops"` (lines 139-142). Use it for `created_by`.
- Error helper: `writeErrorResponse(w, r, status, code, msg)`.

---

### `internal/control/server.go` (gRPC service, streaming)

**Analog:** itself.

Replace the blind-trust Recv loop (lines 86-122). Current behavior to change:

```go
if ack := req.GetAck(); ack != nil {
    if ack.GatewayId == "" && req.GatewayId != "" { ack.GatewayId = req.GatewayId }
    ...
    _ = s.ackTracker.RecordAck(stream.Context(), ack)
}
if pong := req.GetPong(); pong != nil && s.ackTracker != nil && req.GatewayId != "" { ... }
```

Pattern to apply (RESEARCH Pattern 1):
- At the top of `StreamSnapshots`, call `certID, err := gatewayIdentityFromContext(stream.Context())`. It uses `peer.FromContext`, `p.AuthInfo.(credentials.TLSInfo)`, requires `len(ti.State.VerifiedChains) > 0`, then `identity.ExtractSPIFFEID` and `GatewayIDFromSPIFFE`.
- Return `status.Error(codes.PermissionDenied, ...)`. The file already imports `codes` and `status` (lines 10-11, used at line 133: `status.Errorf(codes.NotFound, ...)`).
- In the Recv goroutine, before any `RecordAck`: if `req.GatewayId != certID`, or `ack.GatewayId != "" && ack.GatewayId != certID`, then `errCh <- status.Error(codes.PermissionDenied, ...)`, increment the metric, and return. The goroutine already terminates the stream via `errCh <- err; return` (line 90), and the final `return <-errCh` (line 124) propagates it.
- An empty `ack.GatewayId` is overwritten with `certID`, not `req.GatewayId`.
- Keep the `Rejected`-not-upgraded logic for Pong (lines 114-117).

---

### `internal/snapshot/client.go` (gRPC client, streaming)

**Analog:** itself (lines 135-162).

Remove:

```go
opts := append([]grpc.DialOption{}, c.dialOpts...)
if len(opts) == 0 {
    opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))   // DELETE (and the insecure import)
}
```

Replace with a check that returns `errors.New("no transport credentials configured")` when no `WithTransportCredentials` was supplied. `NewStreamClient(addr, gatewayID, mgr, verifier, dialOpts...)` keeps its signature (see its call at `tests/chaos/test_helpers.go:696-703`). The failure feeds the existing backoff loop (lines 107-131). Keep `grpc.DialContext` unless the planner opts into `NewClient` (needs `passthrough:///bufnet`).

Add a static test: `! grep -rn 'insecure.NewCredentials' --include='*.go' cmd internal` excluding `_test.go`.

---

### `cmd/control-plane/main.go` (entrypoint)

**Analog:** itself.

- gRPC server (line 288): `grpcServer := grpc.NewServer()` becomes `grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cpServerCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS13})))`. Missing cert, key or CA path is `log.Fatalf`.
- PG (lines 162-164): currently `log.Printf("Notice: PostgreSQL unavailable ... (running with in-memory state)")`. Per D-22, change to `log.Fatalf`.
- Signing key (lines 79-105): currently falls through silently on an invalid path. Change to fatal when a path is configured but invalid. Move the PEM, raw and base64 parsing to `internal/keyfile`.
- Env style: `os.Getenv("AEGIS_...")` with an explicit default. Add `AEGIS_GRPC_TLS_CERT_PATH`, `AEGIS_GRPC_TLS_KEY_PATH` and `AEGIS_GRPC_CLIENT_CA_PATH` (or consistent names, and document them).
- Run reconstruct synchronously before the listeners at about line 282.

---

### `cmd/gateway/main.go` (entrypoint)

**Analog:** itself.

Key loading to replace (lines 81-100). The pattern today:

```go
pubKey := ed25519.PublicKey(ed25519.NewKeyFromSeed(defaultDemoSeed).Public().(ed25519.PublicKey))
if pubKeyB64 := os.Getenv("AEGIS_ISSUER_PUBLIC_KEY"); pubKeyB64 != "" { ... }
controlPlanePubKey := pubKey   // defaults to the ISSUER key; D-09 separates them
...
snapVerifier := snapshot.NewVerifier(controlPlanePubKey)
```

New: when `AEGIS_JWT_KEYSET_DIR` or `AEGIS_SNAPSHOT_KEYSET_DIR` is set, do a synchronous initial load (an invalid or empty set is fatal). Start a `keyset.Reloader` per family bound to `validator.SetPublicKeys` and `snapVerifier.SetTrustedKeys`. Otherwise fall back to the env vars with a `log.Printf("Warning: ...")` (existing style, lines 86 and 96).

Revocation error mapping, both handlers (about lines 404-418 and 727-741). Copy this block and add the new branch before it:

```go
revoked, reason, err := revStore.CheckRevocation(r.Context(), claims.Subject, claims.ID)
if err != nil {
    if ac != nil {
        ac.SetDecision("deny", "DEPENDENCY_OUTAGE_REDIS")
        ac.SetErrorCode("DEPENDENCY_OUTAGE_REDIS")
    }
    proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
        "Authorization dependency check unavailable; failing closed.",
        "https://aegis.local/errors/dependency-unavailable", reqID)
    return
}
```

New branch: `if errors.Is(err, revocation.ErrSecurityStateMissing) { ac.SetDecision("deny", "SECURITY_STATE_MISSING") ... "https://aegis.local/errors/security-state-missing" }`. Keep the reason as a string literal (the AST test requires literals in `main.go`).

The store is constructed with `revocation.NewStore(client, revocation.WithRequireSecurityMarker())`; add a wiring test asserting it.

Gateway ID: parse the leaf of the CP client cert (`x509.ParseCertificate(cert.Certificate[0])`), then `ExtractSPIFFEID` and `GatewayIDFromSPIFFE`. If `AEGIS_GATEWAY_ID` is set and differs, `log.Fatalf`. Drop the `gateway-<uuid>` fallback.

---

### `internal/proxy/probe.go` (middleware, request-response)

**Analog:** itself (lines 38-81). Add one more readiness check using the same shape:

```go
if isSpoolSaturated != nil && isSpoolSaturated() {
    http.Error(w, "SPOOL_SATURATED", http.StatusServiceUnavailable)
    return
}
```

→ add `if securityStateMissing != nil && securityStateMissing() { http.Error(w, "SECURITY_STATE_MISSING", http.StatusServiceUnavailable); return }`. Prefer a variadic functional option to limit churn (about 12 call sites: `probe_test.go` x8, `cmd/gateway/main_test.go` x2, `tests/chaos/test_helpers.go:896`, `cmd/gateway/main.go:964`). Nil checks (`x != nil &&`) are the convention. The gateway-side flag is an `atomic.Bool` like `DrainingState` (lines 10-27), updated by a 1s Redis poller with a 200ms timeout.

The control plane gets an unauthenticated `/livez` (200 `OK\n`) and `/readyz` (PG ping and marker present, otherwise 503) in `internal/control/api.go`, mirroring the `Content-Type: text/plain; charset=utf-8` plus `WriteHeader` plus body pattern at lines 47-51. Register them before the SPA and auth routes.

---

### `internal/audit/governor.go` (config)

**Analog:** the `reasonTable` outage block (lines 44-48). Add:

```go
"SECURITY_STATE_MISSING": classOutage,
```

Also add it to the `classOutage` list in `TestEveryMainReasonIsClassified` (`main_reasons_test.go`). `metrics.InitAuditSuppressedLabels` picks it up through `ClosedReasonLabels`.

---

### `tests/chaos/test_helpers.go` (test, streaming)

**Analog:** itself, `StartTestControlPlane` (lines 644-688) and `spawnNode` (lines 690-703).

Today: `grpcServer := grpc.NewServer()` (line 664) and `grpc.WithTransportCredentials(insecure.NewCredentials())` (line 702), with `grpc.WithContextDialer(c.ControlPlane.Dialer)` over bufconn (`bufconn.Listen(2*1024*1024)`, line 663).

Migrate to: `grpc.NewServer(grpc.Creds(testpki.ServerCreds()))` and `grpc.WithTransportCredentials(testpki.ClientCreds(gatewayID))`. Client `tls.Config.ServerName` must be set explicitly (the authority is "bufnet"). The server cert needs a matching DNS SAN. Per-node client certs use the SPIFFE `/gw/<gatewayID>` shape. The severed-dialer logic (lines 680-685) stays.

Shared helper (new package) builds an in-memory CA with `pki.NewCA`, `IssueServerCert` and `IssueWorkloadCert`, then returns `credentials.NewTLS` for each side. Callers to migrate: `tests/chaos/test_helpers.go:696`, `tests/integration/{operator_telemetry,snapshot_streaming}_test.go`, `internal/control/control_test.go:58,360,366`, `internal/snapshot/client_test.go`.

Negative tests required (D-18): plaintext rejected, wrong-CA rejected (client sees `codes.Unavailable`), ID mismatch gives `PermissionDenied`, valid accepted.

---

### `tests/manifests/manifest_test.go` (test, file-I/O)

**Analog:** itself (lines 13-60).

```go
func loadYAML(t *testing.T, relPath string) map[string]interface{} { ... yaml.Unmarshal ... }
manifestPath := filepath.Join("..", "..", "deployments", "kubernetes", "base", "gateway", "statefulset.yaml")
containers := podSpec["containers"].([]interface{})
gatewayContainer := containers[0].(map[string]interface{})   // keep gateway at index 0; sidecar goes after
```

Imports: `gopkg.in/yaml.v3`, `testify/assert`, `testify/require`. Note that `loadYAML` handles a single document. New cross-check tests need a multi-doc decoder (`yaml.NewDecoder`).

Source-derived key set: regex over non-test `cmd/<bin>/*.go`, `internal/config/config.go` and `internal/proxy/router.go` for `AEGIS_[A-Z0-9_]+` (and `PORT`, `TLS_CERT_PATH`, etc. for backends). Reuse the `gatewayAllowlist` idea from `tests/compose/compose_test.go`.

Existing assertions to keep green: StatefulSet `replicas == 3`, `runAsUser 10001`, `readOnlyRootFilesystem`, `/livez` and `/readyz`, `wal-spool` 2Gi RWO, mounts at `/var/log/aegis/wal` and `/tmp`.

---

### `tests/compose/compose_test.go` and `smoke_script_test.go` (test, file-I/O)

**Analog:** `TestGatewayWiring` (compose_test.go 291-310) iterates `profiles`, calls `loadCompose(t, profile)`, `gatewayNames(services)`, `envMap(services[name])`, and asserts `gatewayAllowlist[key]` for every `AEGIS_*` env. Add the new keys to `gatewayAllowlist`: `AEGIS_JWT_KEYSET_DIR`, `AEGIS_SNAPSHOT_KEYSET_DIR`, `AEGIS_KEYSET_RELOAD_INTERVAL`, `AEGIS_CP_CLIENT_CERT_PATH`, `AEGIS_CP_CLIENT_KEY_PATH`, `AEGIS_CP_CA_PATH`, `AEGIS_CP_SERVER_NAME`. `TestGatewayWiring` also pins `AEGIS_CONTROL_PLANE_GRPC_ADDR == "control-plane:9090"` (keep).

Smoke lint pattern (smoke_script_test.go 101-124): `_, script := readSmokeScript(t)`, then a `required := []string{...}` loop of `assert.Contains(t, script, want, "smoke script must contain %q", want)`. Add `"=== DR"`, `"=== KEYROT"`, `"=== MTLS"`, `"FLUSHALL"`, `"SECURITY_STATE_MISSING"`. Keep `TestSmokeScriptNoCredentialEcho` and `TestSmokeScriptNoTokenValueEcho` passing.

---

### `scripts/compose-smoke.sh` (script)

**Analog:** the `REV-03 JTI` section (line 792+) and `replica_poll` (line 198).

- `replica_poll LABEL WANT_STATUS WANT_BODY curl-args...` runs one background subshell per gateway in `GATEWAYS`, polling `dc exec -T "${g}" curl -s -w '\n%{http_code}' "$@"`, with a hardcoded 5s limit (`awk ... e > 5`). Introduce `REPLICA_POLL_LIMIT` (default 5) and use 60 for DR.
- Helpers to reuse: `dc`, `pass`, `fail`, `op_login`, `cp_call`, `dev_login` (returns `"<token> <jti>"`), `psql_q`, `wait_q_ge` (line ~794), `valid_rid`.
- Section header form: `echo "=== NAME: description ==="`. Insert the new `DR`, `KEYROT-JWT`, `KEYROT-SNAPSHOT` and `MTLS` sections after `WORKLOAD QUARANTINE` (line 876) and before `GATEWAYS` (line 945).
- Setup form to copy: `cp_call DELETE /control/v1/principals/usr_developer_01/quarantine >/dev/null || true` before and after (cleanup is mandatory).
- Keep the disk pre-flight (line 76) unchanged. Never echo token values.

---

### Kubernetes manifests

**Analog:** `deployments/kubernetes/base/gateway/{statefulset,configmap}.yaml`.

Facts from the files read:
- The ConfigMap `aegis-gateway-config` (namespace `aegis-system`) currently has `AEGIS_HTTP_ADDR`, `AEGIS_WORKLOAD_ADDR` and `AEGIS_METRICS_ADDR` (all unread by the binary). Replace with `AEGIS_PORT`, `AEGIS_WORKLOAD_PORT` and `AEGIS_METRICS_PORT`.
- The StatefulSet never references the ConfigMap, so add `envFrom: - configMapRef: {name: aegis-gateway-config}`.
- Probes already target `/livez` and `/readyz` on 8080.
- The PVC template `wal-spool` (2Gi RWO) and the `tmp` emptyDir are fine.
- Per-pod cert env (Pattern 5): `POD_NAME` via `fieldRef metadata.name` declared BEFORE `AEGIS_CP_CLIENT_CERT_PATH: /etc/aegis/cp-client/cp-client-$(POD_NAME).crt`.
- The audit worker becomes a second container (after `gateway`) in this StatefulSet mounting `wal-spool`. Delete `base/audit-worker/deployment.yaml` and update `base/kustomization.yaml` and the gateway NetworkPolicy (egress to postgres 5432).
- Overlays patch containers by name (`gateway`, `control-plane`), so do not rename them.
- Certs and keysets come only via named Secret and ConfigMap volume refs. No key material in the manifests.

Full mismatch table: RESEARCH.md "B2: Kubernetes mismatch table" (16 rows).

---

### `internal/keyfile` and `internal/keyset` (new)

**keyfile analog:** `cmd/control-plane/main.go:80-94`, which handles PEM block, raw 64 bytes, and base64 of 64 bytes. `keyfile` generalizes this to public keys: base64 raw 32 bytes (format of `assertion-ed25519.pub`) or PEM PKIX. It must return errors, not silently fall through.

```go
data, _ := os.ReadFile(keyPath)
if block, _ := pem.Decode(data); block != nil { data = block.Bytes }
if len(data) == ed25519.PrivateKeySize { ... }
else if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); err == nil && len(b) == ed25519.PrivateKeySize { ... }
```

**keyset reloader contract** (no direct analog; behavior from D-08/D-25 and RESEARCH Pattern 2):
- `os.ReadDir`, skip names starting with `.`, parse every other regular file.
- Reject the whole set if any file fails to parse, the set is empty, or the dir is unreadable. Keep the last good set.
- Hash the sorted raw keys with sha256 for a no-op when unchanged.
- Apply with `Verifier.SetTrustedKeys([]ed25519.PublicKey)` (`internal/snapshot/verifier.go:54`, RWMutex, copies the slice) or `TokenValidator.SetPublicKeys([]crypto.PublicKey)` (`internal/identity/jwt.go:62`, also copies).
- Triggers: `time.Ticker` (default 10s) plus `signal.Notify(SIGHUP)`. A constructor-injected `apply func([]ed25519.PublicKey)` keeps it testable.
- Tests: `-race`, concurrent `VerifySnapshot` / `ValidateBearerToken` during swaps, and a ConfigMap layout fixture (`..data` dir plus symlinks).

---

## Shared Patterns

### Fail-closed with 503 and classified reason
**Source:** `cmd/gateway/main.go:404-418` and `internal/audit/governor.go:44-48`.
**Apply to:** the gateway security-state gate, the `/readyz` body, and the new governor reason. Every new deny reason must be a string literal in `main.go` and present in `reasonTable`.

### Control-plane error responses
**Source:** `internal/control/quarantine_handler.go:145-146`.
**Apply to:** all new write-through failures.
```go
writeErrorResponse(w, r, http.StatusServiceUnavailable, "DEPENDENCY_FAILURE", "Failed to ...: "+err.Error())
```

### Repo and DBPool injection with pgxmock tests
**Source:** `internal/storage/route_repo.go:42-75` and `internal/storage/audit_repo_test.go:20-40`.
**Apply to:** `security_state_repo.go` and its tests, and control-plane tests that need PG.

### Redis hermetic tests
**Source:** `tests/dr/dr_test.go:75-135`.
```go
mr := miniredis.RunT(t)
rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
store := revocation.NewStore(rdb)
...
mr.FlushAll()
```
**Apply to:** marker tests, reconstruct tests, and the gateway gate tests. Existing tests seed no marker, so the marker requirement MUST be opt-in; hermetic gate tests call `store.MarkSecurityStateReady`.

### SPIFFE parsing
**Source:** `internal/identity/spiffe.go:33,55`, already used by `internal/control/quarantine_handler.go:54`.
**Apply to:** gRPC identity binding, the cert generator tests, and the gateway ID derivation.

### Env var reading
**Source:** `cmd/control-plane/main.go:42-70` (`os.Getenv(...)` plus default) and `envDuration` / `envInt` helpers in the gateway.
**Apply to:** `AEGIS_KEYSET_RELOAD_INTERVAL` (bounded 1s..10m) and all new env names. Every new env key must be added to the compose and K8s allowlists.

### Metrics
**Source:** `internal/telemetry/metrics.go` (existing `aegis_*` naming with a closed label set).
**Apply to:** `aegis_keyset_reload_total{family,result}`, `aegis_keyset_keys{family}`, `aegis_security_state_ready`, `aegis_securitystate_reconstruct_total{result}`, and the gRPC identity-rejection counter. Labels must be low-cardinality (never the attacker-supplied gateway id).

### Cert files
**Source:** `scripts/certificates/main.go:18-24` (0644 certs, 0600 keys) and `deployments/compose/docker-compose.hardened.yml:101-111` (`../certs:/certs:ro`, `AEGIS_*_PATH` envs).
**Apply to:** control-plane and gateway compose services. The control plane currently mounts no certs.

## No Analog Found

| File | Role | Data Flow | Reason |
|---|---|---|---|
| `internal/keyset/reloader.go` | service | polling and signal-driven | No hot-reload or SIGHUP code exists. Use the `internal/snapshot/client.go` loop and shutdown idiom and the RESEARCH Pattern 2 spec. |
| Control-plane `/livez` and `/readyz` handlers | controller | request-response | The only existing probes are the gateway's in `internal/proxy/probe.go`. Reuse its response shape. |

## Metadata

**Analog search scope:** `internal/{control,snapshot,identity,revocation,pki,storage,proxy,audit}`, `cmd/{gateway,control-plane}`, `scripts/`, `migrations/`, `tests/{chaos,dr,rotation,manifests,compose}`, `deployments/kubernetes/base/gateway`.
**Files scanned:** about 30 read in full or in part. Line numbers were taken from reads at mapping time.
**Pattern extraction date:** 2026-10-11
**Assumptions still unverified:** grpc-go `peer`/`TLSInfo` shapes, `NewClient` vs `DialContext` behavior, K8s `$(POD_NAME)` expansion (RESEARCH A1-A3). The planner should schedule a short spike.
