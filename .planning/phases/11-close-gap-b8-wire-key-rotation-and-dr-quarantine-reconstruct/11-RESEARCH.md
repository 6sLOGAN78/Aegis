# Phase 11: Close gaps B8, B2, CR-02 - Research

**Researched:** 2026-10-11
**Domain:** Go (grpc-go mTLS + identity binding), keyset hot reload, Postgres/Redis durable security state + DR reconstruction, Kubernetes manifest conformance, docker-compose live smoke
**Confidence:** HIGH on codebase facts (all read directly from the repo); MEDIUM on grpc-go/Kubernetes behaviours taken from training knowledge (tagged `[ASSUMED]`)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **D-01:** New additive migration(s) (`000005`+) add a `principal_quarantines` table (principal_id PK, reason, expires_at, created_at, created_by or similar) and a `revoked_tokens` table (jti PK, expires_at, created_at). The DR runbook already assumes `principal_quarantines` exists. The schema must match the runbook query, or the runbook gets updated.
- **D-02:** Write-through from the control plane: quarantine, unquarantine and jti revoke write Postgres first, then Redis. If Postgres succeeds and Redis fails, the API returns 503 `DEPENDENCY_FAILURE`, the Postgres row stays, and the reconcile pass pushes it to Redis later. Retries are idempotent. Unquarantine deletes from both.
- **D-03:** jti revocations use the same pattern (table, write-through, reconstruct, marker) so a Redis wipe cannot bring a revoked token back to life.
- **D-04:** The control plane runs reconstruction (`ReconstructQuarantines` plus a jti equivalent) at startup, and again whenever it sees that Redis lost state. Detection uses a marker or generation key the control plane writes after a successful restore. Only unexpired rows are restored, with their remaining TTL, not the 24h default. Gateways get no Postgres access, which preserves the "no PG on the gateway" constraint.
- **D-05:** Gateways check the marker key. While it is missing, they **fail closed**: requests get 503 with a new classified reason (for example `SECURITY_STATE_MISSING`, which must be added to the Phase 8 governor reason table so `TestEveryMainReasonIsClassified` passes), and `/readyz` returns 503 with a matching body. Target: restored within 60s (runbook SLO).
- **D-06:** Live proof: `scripts/compose-smoke.sh` on **mvp, hardened and distributed**: quarantine a user and revoke a jti -> `FLUSHALL` Redis -> gateways go 503 or not ready -> they recover -> the same principal is still 403 `PRINCIPAL_QUARANTINED` and the revoked token is still denied, both within 60s and on every replica (poll gateway-1/2/3 directly, as in Phase 9 D-15). Hermetic tests cover the migration, write-through ordering and failure handling, marker detection and reconstruction, and the gateway gate.
- **D-07:** Each key family has its own mounted keyset directory: for example `AEGIS_JWT_KEYSET_DIR` and `AEGIS_SNAPSHOT_KEYSET_DIR`, each holding `*.pub` files. This matches the runbook's `keyset.pub.N` ConfigMap layout. The existing single-key env vars (`AEGIS_ISSUER_PUBLIC_KEY`, `AEGIS_CONTROL_PLANE_PUBLIC_KEY`) remain as a fallback when no dir is set, with a startup warning.
- **D-08:** Hot reload: the gateway re-reads each dir on a short poll interval (around 10s, configurable) and/or on SIGHUP, and swaps the set atomically through `SetTrustedKeys` (snapshot `Verifier`) and `SetPublicKeys` (`TokenValidator`). An empty, unreadable or partly invalid set is rejected and the last good set is kept, with a log line and a metric. No gateway restart is needed for any rotation step.
- **D-09:** Scope: gateway JWT validator and snapshot verifier keysets. Signers load their private keys from files: the demo issuer stops using the hardcoded `DefaultDeterministicSeed` (it reads a key path, with the seed only as a dev fallback that logs a warning), and the control plane keeps `AEGIS_SIGNING_KEY_PATH`. Compose uses **separate** issuer and snapshot signing keys; today both default to the same seed. mTLS CA rotation is unchanged.
- **D-10:** No `kid`. Verifiers try every trusted key, and overlap sets are small. Token and snapshot formats do not change.
- **D-11:** All 3 compose profiles mount keyset dirs, so the drill runs everywhere.
- **D-12:** Live proof: a smoke drill per key family (JWT and snapshot): **expand** (add the new pub to the keyset dir) -> wait for reload -> **switch signer** (restart only the issuer or control plane with the new private key) -> old and new tokens and snapshots both accepted, with no gateway restart -> **prune** the old pub -> old tokens are rejected and new ones still pass. Hermetic tests cover the reloader (good swap, bad set rejected and last-good kept, concurrent verify under `-race`).
- **D-13:** Each gateway has its own **separate control-plane client cert** with URI SAN `spiffe://aegis.local/ns/gateway/sa/aegis-gateway/gw/<id>`, used only for the gRPC stream. The backend-facing `gateway-client` cert and the backends' pinned `defaultGatewaySPIFFE` (`cmd/services/{orders,payments,admin}/main.go`) stay **unchanged**.
- **D-14:** The control plane uses a dedicated **control-plane server cert** (DNS SANs such as `control-plane`, `aegis-control-plane`, `localhost`). The gateway verifies it against `root-ca.crt` with a configured ServerName. The gRPC server requires and verifies client certs against the Aegis root CA. **No plaintext fallback:** the `insecure.NewCredentials()` default in `internal/snapshot/client.go` is removed, and missing cert or key paths are a startup error on both sides.
- **D-15:** The control plane parses the peer's SPIFFE ID (reuse `internal/identity` SPIFFE parsing), accepts only the `/gw/<id>` shape in trust domain `aegis.local`, and checks the GatewayId of **every** Ack, Pong and heartbeat. On a mismatch or a missing or invalid cert identity, it terminates the stream with `codes.PermissionDenied`, records nothing for that ID, and logs plus counts a low-cardinality metric. The gateway's backoff retries, and its lease expires if it keeps failing (fail closed).
- **D-16:** The gateway gets its GatewayId from its own control-plane client cert. If `AEGIS_GATEWAY_ID` is also set and differs, startup fails. The random `gateway-<uuid>` fallback goes away in mTLS mode.
- **D-17:** `scripts/certificates/main.go` generates the control-plane server cert and per-gateway control-plane client certs: `gateway` (mvp/hardened), `gateway-1..3` (distributed) and `aegis-gateway-0..2` (K8s StatefulSet ordinals). All compose profiles run the authenticated stream.
- **D-18:** Tests: `tests/chaos` helpers (`grpc.NewServer()` and the insecure dial in `tests/chaos/test_helpers.go`) and the integration tests move to mTLS with an in-memory test PKI. Add negative tests: plaintext client rejected, wrong-CA client rejected, GatewayId mismatch rejected (PermissionDenied), and a valid client accepted. Update `10-SECURITY.md` AR-01 / T-10-02 status when this closes.
- **D-19:** Proof is **hermetic cross-check tests** in `tests/manifests`, with no cluster: every ConfigMap or env key in the manifests is one the binary actually reads (from `internal/config/config.go` and `os.Getenv` calls), container ports and probe paths match the real listeners and handlers, and audit spools are PVC-backed rather than `emptyDir`. A live kind/minikube bring-up is deferred; no K8s tooling is installed on the host.
- **D-20:** Certs and keysets reach pods through **named Secret and ConfigMap references** (for example the runbook's `gateway-jwt-keyset`, a snapshot keyset ConfigMap, the control-plane server cert Secret, and per-gateway control-plane client cert Secrets). A script or doc creates them from `deployments/certs`. No key material is committed into the manifests or the kustomize output.
- **D-21:** The K8s gateway ID follows the StatefulSet pod ordinal (`aegis-gateway-0` -> cert `.../gw/aegis-gateway-0`). Each pod mounts its own client cert. Research and the planner choose the mechanism.

### Claude's Discretion
- Exact table and column names, the marker key name and format, the reconcile cadence, and the env var names, as long as they are consistent and documented.
- Poll interval default and the reload metric names.
- Reason-code names for missing security state and for gRPC identity rejection, as long as they are classified.
- The per-pod cert mounting mechanism in K8s (D-21).
- Plan breakdown and wave ordering.
- Update `docs/runbooks/disaster-recovery.md` and `docs/runbooks/credential-rotation.md` to describe the real mechanisms instead of the current aspirational steps.

### Deferred Ideas (OUT OF SCOPE)
- Live Kubernetes bring-up (kind/minikube apply + smoke).
- `kid`-based key selection for JWTs and snapshots.
- mTLS CA / workload cert hot reload (third rotation family).
- Full K8S-01 (default-deny NetworkPolicies, topology spread) stays v2.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| REV-03 | Revocation/quarantine survives Redis loss; fail closed until restored | Sections "DR design", migration, marker, gateway gate, reconcile loop |
| AUTH-01 | JWT validation with multi-key rotation, no downtime | "Keyset reload design" (`TokenValidator.SetPublicKeys`), key separation, smoke drill |
| CTRL-02 | Signed snapshots verified against pinned/rotatable control-plane keys | Keyset reload for `snapshot.Verifier.SetTrustedKeys`; CP signer file loading |
| CTRL-03 | Gateway <-> control plane stream (reconnect, lease) | mTLS client dial (remove insecure default), backoff retained, fail-closed lease |
| CTRL-05 | Per-gateway convergence/ack telemetry is trustworthy | GatewayId bound to cert identity, PermissionDenied on mismatch |
</phase_requirements>

## Summary

All of Phase 11's mechanisms already exist as library code with only test callers (`Verifier.AddTrustedKey/SetTrustedKeys`, `TokenValidator.AddPublicKey/SetPublicKeys`, `Store.ReconstructQuarantines`). The work is wiring them into `cmd/gateway`, `cmd/control-plane` and `cmd/demo-issuer`, adding durable Postgres tables, adding mTLS to the gRPC stream, fixing the Kubernetes manifests, and extending the compose smoke. No new third-party Go packages are needed (grpc v1.83.2, pgx v5.10, go-redis v9.22, goose v3.28, miniredis, pgxmock are already in `go.mod`; `fsnotify` is NOT in `go.mod` and should not be added; use polling).

The K8s manifests are in much worse shape than the CONTEXT suggests. The gateway ConfigMap sets three env keys no binary reads (`AEGIS_HTTP_ADDR`, `AEGIS_WORKLOAD_ADDR`, `AEGIS_METRICS_ADDR`); the real workload listener defaults to **9443** while the manifest, Service and NetworkPolicy say **8443**; the control plane has no `/livez` or `/readyz` handler at all (probes would fail); the audit-worker runs as a separate Deployment with an `emptyDir` so it can never see any gateway's WAL; backends and control plane mount no certs and set no env. See the mismatch table below.

**Primary recommendation:** Build in this order: (1) shared `internal/keyfile` + `identity` SPIFFE gateway-ID helpers + `pki.LoadCA`; (2) cert generator additions and one atomic regeneration of `deployments/certs`; (3) gRPC mTLS server/client + identity binding + test PKI migration of chaos/integration tests; (4) migrations + PG repo + write-through + reconcile + marker + gateway gate; (5) keyset reloader + issuer/CP key files + compose wiring; (6) K8s manifest rewrite + source-derived cross-check tests; (7) smoke script sections + runbooks + 10-SECURITY update.

## Project Constraints (from CLAUDE.md)

No `CLAUDE.md` exists in the repo root (verified). `GEMINI.md` carries the GSD project block; its relevant directives: Go for all services; **no synchronous PostgreSQL on the gateway hot path** (so the gateway must never touch PG; the security-state check is Redis-only); fail-closed (503) on Redis outage, lease expiry (>60s) or spool saturation; strict automated tests with negative tests and `-race`. No `.claude/skills` or `.agents/skills` directories were found.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Durable quarantine/revocation source of truth | Database (Postgres) | Control plane | Redis is a cache; PG survives Redis loss |
| Write-through + reconcile + reconstruct | Control plane | Redis | Only CP has PG access (gateways must not) |
| Security-state marker presence check | Gateway (reads Redis) | Redis | Gateway must fail closed without PG |
| Keyset reload (JWT, snapshot) | Gateway | Mounted Secret/ConfigMap/bind dir | Verifiers live in gateway memory |
| Signer key loading | Demo issuer / control plane | Mounted key files | Private keys never reach gateways |
| gRPC stream authentication & GatewayId binding | Control plane (server) | Gateway (client cert) | Server verifies peer cert; identity from cert, not message |
| K8s manifests | Deployment config | -- | Must match binary env/ports/probes |
| Live proof | scripts/compose-smoke.sh | tests/compose lint | Hermetic lint + live smoke |

## Standard Stack

### Core (all already in go.mod; no new packages)
| Library | Version | Purpose | Notes |
|---------|---------|---------|-------|
| google.golang.org/grpc | v1.83.2 [VERIFIED: go.mod] | mTLS via `credentials.NewTLS`, `peer`, `status/codes` | `grpc.DialContext` is deprecated in favour of `grpc.NewClient` [ASSUMED] |
| github.com/jackc/pgx/v5 | v5.10.0 [VERIFIED: go.mod] | PG repo | Repo pattern: `DBPool` interface, tests via `pgxmock/v4` |
| github.com/pressly/goose/v3 | v3.28.0 [VERIFIED: go.mod] | embedded migrations (`migrations/*.sql`, `-- +goose Up/Down`) | Latest is `000004_add_audit_jti.sql` |
| github.com/redis/go-redis/v9 | v9.22.0 [VERIFIED: go.mod] | Redis | miniredis v2.34.0 for hermetic tests |
| crypto/x509, crypto/tls, crypto/ed25519 (stdlib) | go 1.26 | PKI, keysets | Reuse `internal/pki` |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Polling keyset dirs | fsnotify | Not in go.mod; K8s ConfigMap updates swap a `..data` symlink and bind-mounted single files can be replaced by inode; polling by path + content hash is robust on both. **Use polling.** |
| Stream interceptor only | Handler-level check | See pattern 1: handler-level check is fail-closed even if an interceptor is forgotten |

**Installation:** none. **Package Legitimacy Audit:** no external packages are introduced by this phase; slopcheck not required. All packages are pre-existing in `go.mod`. Do NOT add `fsnotify`.

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | slopcheck | Disposition |
|---------|----------|-----|-----------|-------------|-----------|-------------|
| (none new) | -- | -- | -- | -- | n/a | Approved: no installs this phase |

**Packages removed due to slopcheck [SLOP] verdict:** none. **Flagged [SUS]:** none.

## Architecture Patterns

### System Architecture Diagram

```
 operator --HTTP--> Control Plane (api.go quarantine/revoke handlers)
                         |  1) INSERT/UPSERT/DELETE  ---> Postgres (principal_quarantines, revoked_tokens)
                         |  2) SET/DEL w/ remaining TTL -> Redis  --fail--> 503 DEPENDENCY_FAILURE (PG row kept)
                         v
   SecurityState loop (CP, every ~2s):  EXISTS marker? --no--> reconstruct(PG unexpired rows -> Redis, pipelined)
                         |                                      then SET marker (no TTL) = generation id
                         |  every ~30s: reconcile add-missing / prune expired (heals partial-restore, resurrected deletes)
                         v
                       Redis  <-- gateway pipeline: EXISTS quarantine, EXISTS jti, EXISTS marker (same round-trip)
                         |            marker missing -> ErrSecurityStateMissing -> 503 SECURITY_STATE_MISSING
                         |   gateway bg poll 1s -> atomic ready flag -> /readyz 503 SECURITY_STATE_MISSING

 mounted dirs (/keys/jwt, /keys/snapshot or ConfigMaps) -> keyset.Reloader (poll 10s + SIGHUP, sha256 of parsed set)
        -> TokenValidator.SetPublicKeys / Verifier.SetTrustedKeys (atomic; bad/empty set rejected, last-good kept)

 gateway (cert .../gw/<id>) ==mTLS gRPC==> CP gRPC server (RequireAndVerifyClientCert, root CA)
        StreamSnapshots: peer cert -> SPIFFE /gw/<id>; every Ack/Pong/heartbeat GatewayId must == <id>
        else status PermissionDenied, nothing recorded, counter++ ; gateway backoff retries; lease expires -> fail closed
```

### Recommended Project Structure
```
internal/keyfile/        # LoadEd25519Private, ParseEd25519Public (b64 raw / PEM PKIX), shared by gw, CP, issuer
internal/keyset/         # Reloader (dir -> []ed25519.PublicKey), metrics hook, SIGHUP trigger
internal/identity/spiffe_gateway.go   # GatewaySPIFFEID(id), GatewayIDFromSPIFFE(spiffe)
internal/pki/            # + LoadCA(certPEM,keyPEM), IssueControlPlaneServerCert helper (or reuse IssueServerCert)
internal/storage/security_state_repo.go  # quarantines + revoked tokens (DBPool, pgxmock-tested)
internal/control/securitystate.go        # write-through service, reconstruct, reconcile loop, marker
internal/revocation/store.go             # + marker key, ErrSecurityStateMissing, RevokeJTI/Quarantine with explicit TTL already exist
migrations/000005_create_security_state.sql
deployments/keys/ (or deployments/keysets) # dev jwt + snapshot keypairs and keyset dirs
```

### Pattern 1: Peer identity in a bidi stream (answers research Q1)
**What:** In `StreamSnapshots`, `p, ok := peer.FromContext(stream.Context())`; `ti, ok := p.AuthInfo.(credentials.TLSInfo)`; use `ti.State.PeerCertificates[0]` (and `VerifiedChains` non-empty) -> `identity.ExtractSPIFFEID(cert, "aegis.local")` -> `GatewayIDFromSPIFFE`. Return `status.Error(codes.PermissionDenied, ...)` from the handler (or from the Recv goroutine via `errCh`) to terminate the stream. [ASSUMED: standard grpc-go API from training; confirm with `go doc google.golang.org/grpc/credentials.TLSInfo` during implementation]
**Recommendation:** Do the identity extraction **inside the handler** (a helper `gatewayIdentityFromContext(ctx)` in `internal/control`) and compare every received message's `GatewayId` (and nested `Ack.GatewayId`) with it in the Recv loop. Rationale: it fails closed even if someone builds a server without the interceptor or without TLS credentials (no `TLSInfo` -> PermissionDenied/Unauthenticated). Optionally add a `grpc.StreamInterceptor` only for the metric/log, not for the decision. Server credentials: `credentials.NewTLS(&tls.Config{Certificates: [cert], ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: rootPool, MinVersion: tls.VersionTLS13})`. Wrong-CA and missing-cert clients are rejected at handshake (client sees `codes.Unavailable`); plaintext clients fail the handshake; only the ID mismatch yields `PermissionDenied` (tests must assert these distinct error shapes).
**Current handler detail to change** (`internal/control/server.go:95-117`): today an empty `ack.GatewayId` is filled from `req.GatewayId`, and Pong uses `req.GatewayId` blindly. New rule: let `certID` be the identity; if `req.GatewayId != certID` -> deny; if `Ack.GatewayId` is non-empty and `!= certID` -> deny; empty is overwritten with `certID`. Check before any `RecordAck`. Also the client's first `stream.Send` may return `io.EOF` if the server terminates early; the client already ignores Send errors, and the `Recv` error carries the status.
**Client:** remove `insecure` import and the `len(opts)==0` default in `internal/snapshot/client.go:136-139`. `NewStreamClient` should take credentials explicitly (e.g. a `TransportCredentials` argument or require a `grpc.WithTransportCredentials` option) and `runStream` must return an error "no transport credentials configured" when none is supplied; never default to insecure. Migrating `grpc.DialContext` to `grpc.NewClient` is optional; if done, note `NewClient` uses the `dns` resolver by default, so bufconn tests need target `passthrough:///bufnet` with `grpc.WithContextDialer`, and the connection is lazy (stream creation triggers connect). [ASSUMED] Keep `DialContext` to limit blast radius unless the planner wants the cleanup.
**Gateway TLS config:** `tls.Config{Certificates: [cpClientCert], RootCAs: caPool, ServerName: cfg.ControlPlaneServerName, MinVersion: TLS13}`. Default `ServerName` = host part of `AEGIS_CONTROL_PLANE_GRPC_ADDR` (`control-plane` in compose, `aegis-control-plane` in K8s; both are SANs). GatewayId = `GatewayIDFromSPIFFE(leaf URI)` parsed from the gateway's own client cert at startup (`x509.ParseCertificate(cert.Certificate[0])`); if `AEGIS_GATEWAY_ID` is set and differs -> `log.Fatalf`.

### Pattern 2: Keyset reloader (answers Q2)
- Read the directory each tick with `os.ReadDir`; **skip any entry whose name starts with "."** (K8s ConfigMap volumes contain `..data` and `..<timestamp>` entries plus per-key symlinks); read remaining regular files with `os.ReadFile` (follows symlinks). **Do not filter on `*.pub`**: the existing runbook layout is `keyset.pub.0`, `keyset.pub.1` which `*.pub` would miss. Accept every non-dot file (document this; D-07 says `*.pub` but the runbook says `keyset.pub.N`, so the loader must accept both).
- Parse each file: base64 raw 32-byte Ed25519 (the format used by `assertion-ed25519.pub` and `/public-key` of the issuer) or PEM `PUBLIC KEY` (PKIX). Sort by filename, dedupe identical keys, compute `sha256` over concatenated raw keys. If hash unchanged: no-op. If directory unreadable, zero valid keys, or ANY file fails to parse (D-08 "partly invalid"): reject, keep last-good, log, increment failure counter. On success call `SetPublicKeys` / `SetTrustedKeys` once (atomic copy-on-set, both already RWMutex-guarded and copy the slice).
- Trigger: `time.Ticker` (default 10s, `AEGIS_KEYSET_RELOAD_INTERVAL`, bounds e.g. 1s..10m via `envDuration`) plus `signal.Notify(SIGHUP)` for immediate reload. Initial load at startup is synchronous: a configured dir that is empty/invalid is a startup error (fail closed); fallback to env keys only when no dir is configured, with a warning.
- Compose: mount **directories** (never single files; editors/`cp` can replace an inode and break single-file bind mounts). Polling by path handles both.
- Metrics (add to `internal/telemetry/metrics.go`, same style as existing `aegis_*`): `aegis_keyset_reload_total{family="jwt|snapshot",result="ok|error|unchanged"}`, `aegis_keyset_keys{family}` gauge, `aegis_keyset_last_success_timestamp_seconds{family}`. Labels are a closed set (no attacker-controlled cardinality).
- Rotation timing constraint: overlap must exceed poll interval + token TTL (demo tokens live 5 min; runbook says TTL + 30s). Prune only after that.
- Note `VerifyLease` and `VerifySnapshot` both read `trustedKeys` under RLock, so one `SetTrustedKeys` rotates snapshots and leases together.

### Pattern 3: Marker key + gateway gate (answers Q4)
- Key: `aegis:security-state:generation` (no TTL), value = `<RFC3339 time>/<uuid>` generation id. Written by the CP with plain `SET` **after** the restore pipeline succeeds (ordering guarantees the data is present before gateways reopen). `FLUSHALL`/restart-empty removes it.
- **Gateway request path (recommended):** add `EXISTS aegis:security-state:generation` as a third command in the **existing** `CheckRevocation` pipeline (same single round trip, same 200ms timeout, zero extra round trips and zero detection window). If it returns 0 return a new sentinel `revocation.ErrSecurityStateMissing`; both handlers in `cmd/gateway/main.go` (lines ~404 and ~727) map `errors.Is(err, ErrSecurityStateMissing)` -> `ac.SetDecision("deny","SECURITY_STATE_MISSING")`, 503 problem `https://aegis.local/errors/security-state-missing`; other errors keep `DEPENDENCY_OUTAGE_REDIS`. Note the early-return `if quarCmd == nil && jtiCmd == nil` path must still not bypass the marker (both IDs empty never occurs in practice, but check).
- **Readiness path:** background goroutine in the gateway polls the marker every 1s (200ms timeout), stores an `atomic.Bool` + last-success time; stale >3s or error -> not ready. Plumb into `CreateProbeHandler` (add an `isSecurityStateReady func() bool` parameter or functional option; 8 call sites in `internal/proxy/probe_test.go`, plus `cmd/gateway/main_test.go` x2, `tests/chaos/test_helpers.go:896`, `cmd/gateway/main.go:964` need updating; a variadic option keeps churn smaller). Body `SECURITY_STATE_MISSING`, status 503. Expose `aegis_security_state_ready` gauge.
- **Classification:** add `"SECURITY_STATE_MISSING": classOutage` to `reasonTable` in `internal/audit/governor.go` (gateway-wide outage, keyed by reason only) and add it to the `classOutage` list in `TestEveryMainReasonIsClassified`. Keep the `SetDecision("deny", "SECURITY_STATE_MISSING")` as a **string literal in `cmd/gateway/main.go`**: that test AST-parses main.go for literal deny reasons (it requires >=20 found). Also update `metrics.InitAuditSuppressedLabels` (automatic via `ClosedReasonLabels`).
- Opt-in vs default: enabling the marker requirement in `revocation.Store` by default would fail every existing test that seeds Redis via miniredis without the marker (chaos, failure, dr, integration, control api tests). Recommend `revocation.NewStore(client, revocation.WithRequireSecurityMarker())` used only by `cmd/gateway/main.go`, plus a test helper `store.MarkSecurityStateReady(ctx)` for hermetic gate tests. A gateway-wiring test must assert main.go uses the option.
- Partial-restore hazard: Redis RDB/AOF restore may bring back an OLD marker without newer quarantines. The periodic reconcile (re-assert all unexpired PG rows with `SET ... NX`-style add-missing) heals this within one cycle. State the residual window (<= reconcile interval) in the runbook.

### Pattern 4: Write-through, reconstruct and reconcile (answers Q5)
- Migration `000005_create_security_state.sql` (goose Up/Down, `CREATE TABLE IF NOT EXISTS`): 
  `principal_quarantines(principal_id VARCHAR(128) PRIMARY KEY, reason TEXT NOT NULL, created_by VARCHAR(64) NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), expires_at TIMESTAMPTZ NOT NULL)` + `INDEX (expires_at)`; `revoked_tokens(jti VARCHAR(128) PRIMARY KEY, reason TEXT, created_by VARCHAR(64) NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), expires_at TIMESTAMPTZ NOT NULL)` + `INDEX (expires_at)`. Limit 128 matches `maxPrincipalIDLen`. The runbook query `SELECT principal_id, reason, ... expires_at FROM principal_quarantines WHERE expires_at > NOW()` works as written, but the runbook uses database `aegis_control` while compose uses `aegis`; fix the runbook.
- Handlers (`internal/control/quarantine_handler.go` `quarantineCore`, `HandleUnquarantinePrincipal`, `HandleRevokeToken`): repo upsert first (`INSERT ... ON CONFLICT (principal_id) DO UPDATE SET reason, expires_at, created_by`), then Redis with the same TTL. PG failure -> 503 `DEPENDENCY_FAILURE`, no Redis write. Redis failure after PG success -> 503 `DEPENDENCY_FAILURE`, row stays, reconcile pushes later; retry is idempotent. Unquarantine: PG `DELETE` first, then Redis `DEL`. Note `HandleListQuarantines` reads Redis; keep, or read PG (preferable for post-flush consistency; optional).
- `APIServer` currently gets `revStore` only; add a `SecurityStateRepo` dependency (interface, nil allowed for legacy hermetic tests => Redis-only behaviour). `cmd/control-plane/main.go` currently tolerates PG being unavailable ("running with in-memory state"). With durable state that becomes unsafe: recommended policy is **fail startup if PG is unreachable** (K8s/compose restart it; compose already `depends_on: postgres healthy`). Flag as an Open Question for the user.
- Reconstruct (CP, new `SecurityState` component): load unexpired rows, `remaining = expires_at - now` (skip <=0, floor at 1s), pipeline `SET` with TTL for quarantines and `SET revocation:jti:<jti>` for revocations (extend `Store` with a `ReconstructRevocations`; the existing `ReconstructQuarantines` already takes TTL per principal and defaults only if <=0; its 2s timeout is too short for large sets: make timeout scale or chunk 1000 per pipeline), then write marker. Run **synchronously before the HTTP and gRPC listeners start** so a healthy CP implies marker present (gateways `depends_on: control-plane healthy`).
- Loss detection: CP goroutine every 2s `EXISTS marker`; missing -> reconstruct again (log + `aegis_securitystate_reconstruct_total{result}` counter, `aegis_securitystate_last_reconstruct_timestamp_seconds`). Reconcile every 30s: add-missing from PG; delete expired PG rows; **also** delete Redis quarantine keys that have no PG row only after the one-time upgrade backfill (see next bullet), which heals the "reconstruct read row, operator unquarantined, reconstruct SET" resurrection race.
- Upgrade backfill: if marker is absent but Redis already holds `quarantine:principal:*` / `revocation:jti:*` keys (pre-Phase-11 data), import them into PG (expires_at = now + TTL) before the first reconstruct so they are not lost or pruned. After a FLUSHALL Redis is empty so this is a no-op.
- HA: prod overlay has 2 CP replicas; reconstruct/reconcile are idempotent, no lock needed.
- Existing tests to update/point at real code: `tests/dr/dr_test.go` (`TestRedisQuarantineReconstruction`), `internal/control/api_test.go`, `internal/revocation/store_test.go`.

### Pattern 5: Per-pod gateway cert selection in K8s (answers Q7)
**Recommend: one Secret `aegis-gateway-cp-clients` holding `cp-client-aegis-gateway-{0,1,2}.{crt,key}` mounted read-only at `/etc/aegis/cp-client`, selected through Kubernetes dependent env-var expansion** (no init container, no code change): 
```yaml
env:
  - name: POD_NAME
    valueFrom: {fieldRef: {fieldPath: metadata.name}}
  - name: AEGIS_CP_CLIENT_CERT_PATH
    value: /etc/aegis/cp-client/cp-client-$(POD_NAME).crt
  - name: AEGIS_CP_CLIENT_KEY_PATH
    value: /etc/aegis/cp-client/cp-client-$(POD_NAME).key
```
(`$(VAR)` expansion works for earlier-defined env vars [ASSUMED: K8s dependent env vars feature; verify against kubernetes.io docs when writing]). Tradeoff to document: every gateway pod can read all ordinal keys, so a compromised pod could assert a sibling's stream identity (impact limited to convergence telemetry; snapshot integrity is signature-protected). Per-pod isolation (init container + per-ordinal Secrets) is stronger but needs shell tooling in a `FROM alpine` init and per-pod Secret RBAC; deferred. The cross-check test must assert the Secret volume + `$(POD_NAME)` env pattern and that `POD_NAME` is declared before use. Replicas must stay <= the number of generated ordinal certs (3).

### Anti-Patterns to Avoid
- Trusting `GatewayMessage.GatewayId` from the wire; the cert is the only identity source.
- Defaulting to insecure credentials anywhere (keep a grep/AST test: no `insecure.NewCredentials()` in non-test code under `internal/` or `cmd/`).
- Per-request Redis marker GET as a separate round trip; use the existing pipeline.
- Single-file bind mounts for keysets; filtering `*.pub` only; reading `..data` dirs.
- Deleting Redis keys not in PG before the upgrade backfill ran.
- Putting an audit worker in a separate Deployment with an emptyDir (cannot see gateway WAL).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| SPIFFE parsing | regex on SAN | `identity.ExtractSPIFFEID` / `ParseSPIFFEID` + a thin `/ns/gateway/sa/aegis-gateway/gw/<id>` matcher | One canonicalisation already shared with quarantine keys |
| Cert issuance | openssl scripts | `internal/pki` (`IssueWorkloadCert`, `IssueServerCert`) + new `LoadCA` | Pure Go, tests already use it |
| Migrations | ad-hoc DDL at startup | goose embedded `migrations/` | Existing runner `RunMigrationsWithPool` |
| PG mocking | fake DB | `pgxmock/v4` via `storage.DBPool` | Existing pattern |
| Redis mocking | fake client | miniredis (supports FLUSHALL) | Existing pattern |
| Key file parsing | three copies | one `internal/keyfile` | Gateway assertion key, CP signing key, issuer all parse PEM/base64 differently today |
| Manifest env validation | hand-maintained lists only | allowlist **derived from source** (see test design) | Prevents drift |

## Runtime State Inventory

Not a rename phase, but the phase changes persisted/registered state:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | Redis `quarantine:principal:*`, `revocation:jti:*` exist today with no PG copy; new PG tables | One-time backfill Redis->PG when marker absent (see Pattern 4); data migration, not just code edit |
| Live service config | None outside git (compose/K8s files are in git) | None |
| OS-registered state | None | None (verified: no systemd/pm2/launchd references) |
| Secrets/env vars | New env names (below); K8s Secrets/ConfigMaps named in D-20 | Create via script from `deployments/certs`/keys; no key material in manifests |
| Build artifacts | Docker images embed binaries; `policies/` copied into gateway image | Rebuild images (`--build`) |

## B2: Kubernetes mismatch table (answers Q3)

Real env keys read per binary (grep of `os.Getenv`, `internal/config/config.go`, `envDuration/envInt` literals):
- **gateway** (`cmd/gateway/main.go` + `internal/config` + `internal/proxy/router.go`): `AEGIS_PORT`(8080) `AEGIS_WORKLOAD_PORT`(**9443**) `AEGIS_MAX_CONCURRENT` `AEGIS_ROUTES_PATH` `AEGIS_TLS_CERT_PATH` `AEGIS_TLS_KEY_PATH` `AEGIS_CLIENT_CERT_PATH` `AEGIS_CLIENT_KEY_PATH` `AEGIS_WORKLOAD_CA_PATH` `AEGIS_ASSERTION_PRIVATE_KEY_PATH` `AEGIS_ASSERTION_PUBLIC_KEY_PATH` `AEGIS_CONTROL_PLANE_GRPC_ADDR` `AEGIS_REDIS_ADDR` `AEGIS_REDIS_PASSWORD` `AEGIS_SPOOL_DIR` `AEGIS_SPOOL_MAX_BYTES` `AEGIS_SPOOL_HARD_LIMIT_RATIO` `AEGIS_SPOOL_SEGMENT_BYTES` `AEGIS_AUDIT_*` (7 keys) `AEGIS_ISSUER` `AEGIS_AUDIENCE` `AEGIS_ISSUER_PUBLIC_KEY` `AEGIS_CONTROL_PLANE_PUBLIC_KEY` `AEGIS_GATEWAY_ID` `AEGIS_METRICS_PORT`(":9091") `AEGIS_ASSERTION_PRIVATE_KEY` `AEGIS_DRAIN_TIMEOUT` `AEGIS_UPSTREAM_SCHEME`. (`AEGIS_ROUTES_PATH` is read by config but compose's `TestNoDeadEnv` treats it as dead since routes come from the snapshot.)
- **control-plane**: `AEGIS_GRPC_PORT`(:9090) `AEGIS_PORT`/`AEGIS_HTTP_PORT`(:8084) `AEGIS_METRICS_PORT`(:9092) `AEGIS_SIGNING_KEY_PATH` `AEGIS_SIGNING_KEY_SEED` `AEGIS_SIGNING_KEY_ID` `AEGIS_REDIS_ADDR`(default localhost:6379!) `AEGIS_REDIS_PASSWORD` `AEGIS_DB_HOST/PORT/USER/PASSWORD/NAME/SSLMODE` `AEGIS_IDENTITY_MAPPINGS_FILE` `AEGIS_COOKIE_SECURE` `AEGIS_GATEWAY_STALE_AFTER`.
- **audit-worker**: `AEGIS_SPOOL_DIR` `AEGIS_AUDIT_BATCH_SIZE` `AEGIS_AUDIT_FLUSH_INTERVAL_MS` `AEGIS_PRUNE_ARCHIVED` `AEGIS_DB_*`; no HTTP listener, no probes.
- **backends** (orders/payments/admin): `PORT` `TLS_CERT_PATH` `TLS_KEY_PATH` `CA_CERT_PATH` `GATEWAY_ASSERTION_PUBKEY_PATH` `GATEWAY_ASSERTION_PUBKEY`; serve `/health` over **mTLS** (when certs set); plain HTTP otherwise.

| # | Manifest (file) | Current | Reality | Fix |
|---|-----------------|---------|---------|-----|
| 1 | gateway ConfigMap `AEGIS_HTTP_ADDR` | ":8080" | unread; real key `AEGIS_PORT` | replace with `AEGIS_PORT: "8080"` |
| 2 | gateway ConfigMap `AEGIS_WORKLOAD_ADDR` | ":8443" | unread; real `AEGIS_WORKLOAD_PORT` default **9443** | set `AEGIS_WORKLOAD_PORT: "8443"` (keep 8443 in Service/NetworkPolicy/containerPort) OR move everything to 9443; pick 8443 to limit manifest churn but the test must tie them together |
| 3 | gateway ConfigMap `AEGIS_METRICS_ADDR` | ":9091" | unread; real `AEGIS_METRICS_PORT` (":9091" default OK) | `AEGIS_METRICS_PORT: "9091"` |
| 4 | gateway StatefulSet env | `envFrom` not even used; ConfigMap is never referenced by the StatefulSet | none of the config is applied | add `envFrom: configMapRef aegis-gateway-config` |
| 5 | gateway certs/keys | none mounted | gateway needs TLS server cert/key, client cert/key, CA, assertion private key, keysets, CP client cert/CA, `AEGIS_UPSTREAM_SCHEME=https` | named Secret/ConfigMap volumes (D-20); env paths |
| 6 | gateway probes | /livez, /readyz on 8080 | correct (user listener has `CreateProbeHandler`); workload :9443/:8443 has none | keep; test asserts probe port == `AEGIS_PORT` |
| 7 | gateway audit WAL | PVC `wal-spool` at /var/log/aegis/wal | correct | keep; first container must remain the gateway (existing test indexes `containers[0]`) |
| 8 | audit-worker Deployment | `wal-volume: emptyDir`, separate pod, no env | cannot read any gateway WAL; no DB env | **Convert to a sidecar container** in the gateway StatefulSet (appended after `gateway`) mounting the same `wal-spool` PVC (read-write for `wal.cursor`) plus `AEGIS_DB_*` env from a Secret; delete `base/audit-worker/deployment.yaml` or turn it into the sidecar patch; update NetworkPolicy (gateway egress to postgres:5432) and `base/kustomization.yaml`. Mirrors compose (one worker per spool) |
| 9 | control-plane env | none set | needs Redis addr (default localhost!), DB_*, signing key path, identity mappings file, gRPC TLS paths, `AEGIS_COOKIE_SECURE` | ConfigMap + Secret refs, mounts for signing key Secret and server cert Secret |
| 10 | control-plane probes | `/livez`, `/readyz` on 8084 | **not served** by the control plane (only the gateway's `probe.go` implements them); compose healthcheck uses `/control/v1/auth/me` and accepts 401 (k8s httpGet fails on >=400) | add unauthenticated `/livez` (200) and `/readyz` (PG ping + security-state marker present) to the CP router before the SPA/auth routes; then probes are correct. Fallback if descoped: `tcpSocket` on 8084 |
| 11 | control-plane ports | 8084, 9090, 9092 | match defaults | keep; test cross-checks with `AEGIS_*PORT` or defaults parsed from source |
| 12 | control-plane replicas | base 1, prod overlay 2 | fine with idempotent reconcile | keep |
| 13 | backends env/certs/probes | no env, no cert mounts, no probes | need `PORT`, `TLS_*`, `CA_CERT_PATH`, `GATEWAY_ASSERTION_PUBKEY_PATH`; `/health` is behind mTLS | add env + Secret mounts; probes `tcpSocket` (httpGet cannot do client-cert mTLS) |
| 14 | gateway NetworkPolicy egress | to aegis-apps 8443/8081-8083, redis, CP 9090, DNS | missing Postgres 5432 if audit sidecar added | add egress rule |
| 15 | Gateway Services | 8080, 8443, 9091 | consistent with fixes | keep |
| 16 | image refs | `aegis/*:latest` | unchanged | out of scope |

**Cross-check test design (`tests/manifests`)** -- derive truth from source, don't only maintain lists:
1. Per binary, `go/parser` (or regex `"(AEGIS_[A-Z0-9_]+|PORT|TLS_CERT_PATH|TLS_KEY_PATH|CA_CERT_PATH|GATEWAY_ASSERTION_PUBKEY(_PATH)?)"`) over `cmd/<bin>/*.go` (non-test) plus, for the gateway, `internal/config/config.go`, `internal/proxy/router.go` -> set of readable keys. (Regex over string literals is needed because `envDuration("AEGIS_...")` calls hold the literal outside `os.Getenv`.) Reuse the existing `gatewayAllowlist` pattern from `tests/compose/compose_test.go` as the cross-check for the gateway.
2. Parse every manifest document (multi-doc YAML with `yaml.NewDecoder`) incl. ConfigMap `data` keys, container `env[].name`, `envFrom` (resolve ConfigMap keys by name); assert each key is in that container's binary set; assert no env on a pod points to an unread key.
3. Assert ports: gateway containerPort/Service/NetworkPolicy == `AEGIS_PORT` (default 8080) and `AEGIS_WORKLOAD_PORT` as set in the ConfigMap; probes target the container named port that the binary serves `/livez`/`/readyz` on (gateway user port; CP port requires a handler test: grep that `/livez` and `/readyz` registered in `internal/control`); backends use tcpSocket.
4. Assert every `volumeClaimTemplates`/PVC backs the spool mount; no `emptyDir` mounted at `/var/log/aegis/wal` in any workload; audit-worker (sidecar or deployment) mounts the same volume name as the gateway.
5. Assert every `secretName`/`configMap.name` referenced by volumes is in a documented list matching the creation script, and that no manifest contains key material (`BEGIN`, base64 32-byte keys) -- scan raw bytes of `deployments/kubernetes`.
6. Assert the per-pod cert env uses `$(POD_NAME)` with `POD_NAME` fieldRef defined first.
Keep the existing `TestGatewayManifestHardening` assertions green (containers[0] gateway; `/livez`,`/readyz`; wal-spool 2Gi RWO; mounts `/var/log/aegis/wal` and `/tmp`).

## Cert generation (answers Q6)

- `scripts/certificates/main.go` creates a **brand-new root CA on every run** and rewrites every file; all 16 tracked files under `deployments/certs` are currently modified in the working tree (someone ran it, git status shows `M` for all). Implications: (a) never mix certs from different runs; (b) `tests/integration/bypass_test.go` reads `root-ca.crt` and `workload-orders.{crt,key}` from the tracked dir, so the committed set must remain internally consistent; (c) the planner should add a `pki.LoadCA` and make the generator reuse an existing `root-ca.{crt,key}` (idempotent, add-only) so adding the CP certs does not rotate the CA; (d) the working-tree modification should be either reverted or regenerated and committed in **one dedicated commit** ("regenerate dev PKI") so later diffs are small. Do not stage/commit the pre-existing modifications as part of unrelated tasks. These are dev/demo keys already tracked in git; make that explicit in docs ("never reuse").
- New outputs: `control-plane-server.{crt,key}` via `IssueServerCert("control-plane", ["control-plane","aegis-control-plane","aegis-control-plane.aegis-system.svc","localhost"], [127.0.0.1])`; `cp-client-<id>.{crt,key}` via `IssueWorkloadCert("spiffe://aegis.local/ns/gateway/sa/aegis-gateway/gw/<id>")` for ids `gateway`, `gateway-1`, `gateway-2`, `gateway-3`, `aegis-gateway-0`, `aegis-gateway-1`, `aegis-gateway-2`. Also generate separate Ed25519 keypairs `jwt-issuer.{key,pub}` and `snapshot-signer.{key,pub}` (format identical to `assertion-ed25519.*`: base64 of 64-byte private / 32-byte public + newline), keyset dirs (see below). Note `IssueWorkloadCert` has `ExtKeyUsageClientAuth` only (correct for client certs) and `IssueServerCert` has `ServerAuth` only (correct).
- Compose mounting: hardened/mvp/distributed control-plane currently mount **no certs**; add `../certs:/certs:ro`. mvp/hardened gateway uses cert id `gateway` (path `/certs/cp-client-gateway.*`); distributed gateway-N uses `cp-client-gateway-N` and keeps `AEGIS_GATEWAY_ID=gateway-N` (must equal cert id or startup fails). Note `deployments/certs` mount gives every gateway all keys in compose; acceptable for dev.
- gateway `defaultGatewaySPIFFE` for backends stays `spiffe://aegis.local/ns/gateway/sa/aegis-gateway` (no `/gw/` suffix); `ExtractSPIFFEID` only needs a prefix-strict matcher for the CP stream: path must equal `/ns/gateway/sa/aegis-gateway/gw/<id>` with `<id>` matching `[A-Za-z0-9._-]{1,64}` (already compatible with `canonicalSPIFFE`'s allowed characters).

## Keys: issuer, control plane, rotation drill (answers Q8)

- Today: demo issuer hardcodes `DefaultDeterministicSeed` (`cmd/demo-issuer/main.go:216`) and has no key-path env; control plane falls back to the same seed string (`cmd/control-plane/main.go:30`); gateway defaults `controlPlanePubKey` to the issuer pubkey. Add `AEGIS_ISSUER_PRIVATE_KEY_PATH` to the issuer (via `internal/keyfile`; seed fallback logs a warning); control plane keeps `AEGIS_SIGNING_KEY_PATH` (already accepts PEM, raw, base64) -- move its parsing to `internal/keyfile` too. The CP key loader currently silently falls through to the demo seed if the file is unreadable/invalid: when a path is configured and invalid it must be fatal (fail closed), not fall back.
- Layout: `deployments/keys/jwt-current.key`, `deployments/keys/snapshot-current.key` (private; mounted only into issuer / control plane), and `deployments/keys/keyset-jwt/` + `deployments/keys/keyset-snapshot/` (public, mounted into gateways as directories at `/keysets/jwt` and `/keysets/snapshot`). Use a compose variable `${AEGIS_KEYS_DIR:-../keys}` so the smoke can run the drill on a **temp copy** and never mutate tracked files.
- Drill without gateway restart (smoke): (1) generate a new keypair into the temp dir (add `-keypair <dir>/<name>` mode to the certificates tool, or a tiny `scripts/keygen`; host has Go 1.26 but the script already requires docker/curl/jq, so shell out to `go run`; document the Go requirement or generate inside the control-plane container); (2) EXPAND: copy `<new>.pub` into the keyset dir; wait >= poll interval (set `AEGIS_KEYSET_RELOAD_INTERVAL=2s` in compose for the smoke or use SIGHUP via `docker compose kill -s HUP gateway`); (3) SWITCH: replace the signer key file and `dc restart demo-issuer` (or `control-plane` -- which also triggers the reconstruct path, fine); (4) assert old token (minted before) AND new token both return 200; (5) PRUNE: delete the old pub; wait; old token -> 401, new -> 200. For snapshot: after control-plane restart with the new signer, publish a snapshot (or rely on lease renewal every 10s, which is signed by the new key) and confirm gateways stay ready and `aegis_snapshot_active_version`/lease age keeps advancing; after prune, a gateway must still be ready (new key trusted) -- old-key rejection is proven hermetically (tests/rotation). Also verify `aegis_keyset_reload_total{result="ok"}` increments via the gateway metrics port (distributed exposes 9092 for CP only; gateway metrics are in-container: `dc exec gateway curl :9091/metrics`).
- Caution: restarting the control plane under mTLS drops streams; gateways reconnect with backoff (100ms..5s); lease validity is 15s with 10s renewals, so a restart must come back within the lease. Gateways fail closed after 60s lease expiry (existing).

## Smoke script and lint (answers Q9)

- `scripts/compose-smoke.sh` (964 lines): single bash script with sections `READY`, `B4`, `B1`, `AUDIT`, `AUDIT-TYPES`, `DEDUPE`, `ROTATION`(audit segments), `A4`, `REV-03 JTI`, `WORKLOAD QUARANTINE`, `GRACE`; helpers: `dc`, `pass`/`fail`, `op_login`, `cp_call`, `dev_login`, `dev_token`, `replica_poll` (parallel per-replica polling from one t0, 5s limit), `gw_code`, `gw_probe`, `psql_q`, `wait_q_eq`, `GATEWAYS` array (`dc config --services | grep '^gateway'`). Disk pre-flight (statfs ratio of Docker root > 0.85 -> exit 3) is at the top; keep it unchanged.
- Add sections after WORKLOAD QUARANTINE and before GRACE: `DR`, `KEYROT-JWT`, `KEYROT-SNAPSHOT`, `MTLS`. 
  - DR: login + dev token; quarantine a user (`POST /control/v1/quarantine`), revoke a jti; assert PG rows via `psql_q`; `dc exec redis redis-cli FLUSHALL`; `replica_poll`-style loop (limit 60s, record time) until each gateway's `/readyz` is 503 `SECURITY_STATE_MISSING` OR requests return 503 (the window may be ~1-2s, so poll quickly and accept "never observed 200 allowed with the stale state" by also asserting no 200 for the quarantined token during the loop); then wait each replica back to 200 on `/readyz` (<=60s); assert quarantined principal -> 403 `PRINCIPAL_QUARANTINED` and revoked token -> 403 `TOKEN_REVOKED` on **each replica** (reuse `replica_poll` with parameterised limit: it hardcodes 5s today; add a `REPLICA_POLL_LIMIT` variable defaulting to 5). Clean up (unquarantine) at the end so later sections/`cleanup()` are unaffected. Must also not leave Redis marker missing for GRACE.
  - Must not break existing sections: A4/B9/B10 sections create quarantines too, which now also write PG rows (use unquarantine in cleanup as they already do).
- `tests/compose` hermetic lint: `smoke_script_test.go` asserts script syntax (`bash -n`), audit evidence strings, honest header text, idempotency keys, no credential/token echo, JTI+workload section presence. Extend with tests that the new sections exist (`DR`, `KEYROT`, `MTLS` strings, `FLUSHALL`, `SECURITY_STATE_MISSING`, `PermissionDenied` not applicable), that the header documents them, and that no token/private key is echoed. `compose_test.go` has `gatewayAllowlist` and `TestGatewayWiring`/`TestPortsMatchBinaries`: add the new env keys (`AEGIS_JWT_KEYSET_DIR`, `AEGIS_SNAPSHOT_KEYSET_DIR`, `AEGIS_KEYSET_RELOAD_INTERVAL`, `AEGIS_CP_CLIENT_CERT_PATH`, `AEGIS_CP_CLIENT_KEY_PATH`, `AEGIS_CP_CA_PATH`, `AEGIS_CP_SERVER_NAME`) to the allowlist and add assertions: every profile's control-plane has `AEGIS_GRPC_TLS_*` + cert mount; every gateway has keyset dir mounts and a CP client cert whose file name matches its `AEGIS_GATEWAY_ID`; issuer has `AEGIS_ISSUER_PRIVATE_KEY_PATH`; the CP and issuer use different key files.
- `docker` is installed on the host (Docker 29.1.3); `kind`, `kubectl`, `kustomize` are not (verified), consistent with D-19.

## Common Pitfalls

1. **Existing tests break when the marker becomes mandatory.** Make the requirement opt-in (`WithRequireSecurityMarker`) and add a helper that seeds the marker; update `tests/chaos`, `tests/failure`, `tests/dr`, `tests/integration` only where they use the gateway-main wiring.
2. **`*.pub` glob vs `keyset.pub.N` runbook names.** Load all non-dot files; skip `..data` etc.
3. **mTLS test migration surface is large.** `NewStreamClient` callers: `tests/chaos/test_helpers.go:696`, `tests/integration/{operator_telemetry,snapshot_streaming}_test.go`, `internal/control/control_test.go:366`, `internal/snapshot/client_test.go`; servers via `grpc.NewServer()` in those files plus `internal/control/control_test.go:58,360`. Provide one shared helper (e.g. `internal/testpki` or `pki` test helper returning server creds, client creds for an ID, bufconn dialer) to avoid copy-paste. With bufconn + TLS, set `tls.Config.ServerName` on the client (authority is "bufnet").
4. **Reconcile deleting legacy Redis keys** before backfill (see Pattern 4).
5. **Resurrection race** in reconstruct vs unquarantine; reconcile full-sync heals it; document the <=30s window.
6. **`CreateProbeHandler` signature change** touches ~12 call sites.
7. **K8s `readOnlyRootFilesystem: true`:** the sidecar needs a writable WAL volume (PVC, fine) and `/tmp` emptyDir.
8. **Gateway readiness loop hazard:** if `/readyz` depends on the marker and CP never writes it (PG down), the whole fleet stays unready; this is intentional fail-closed, but compose `depends_on`/healthcheck ordering must ensure CP reconstructs before becoming healthy.
9. **Lease/restart interplay in drills** (see above).
10. **`EXISTS` on three keys:** go-redis `Pipeline()` is not transactional but a single round trip; fine. If the marker EXISTS errors with the others succeeding, treat any error as outage.
11. **Prod overlay patch:** `overlays/production` patches containers by name `gateway`/`control-plane`; adding a sidecar must not rename them.
12. **Runbook DB name** `aegis_control` vs actual `aegis`; `psql` snippets and `/control/v1/quarantine` response shape (`.principals` vs `.quarantines`) in the DR runbook are wrong today; fix when updating.

## Code Examples

```go
// Source: grpc-go credentials/peer API [ASSUMED from training; confirm with go doc]
func gatewayIdentity(ctx context.Context) (string, error) {
    p, ok := peer.FromContext(ctx)
    if !ok { return "", status.Error(codes.Unauthenticated, "no peer") }
    ti, ok := p.AuthInfo.(credentials.TLSInfo)
    if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.PeerCertificates) == 0 {
        return "", status.Error(codes.Unauthenticated, "mTLS client certificate required")
    }
    spiffe, err := identity.ExtractSPIFFEID(ti.State.PeerCertificates[0], "aegis.local")
    if err != nil { return "", status.Error(codes.PermissionDenied, "invalid workload identity") }
    id, err := identity.GatewayIDFromSPIFFE(spiffe) // requires /ns/gateway/sa/aegis-gateway/gw/<id>
    if err != nil { return "", status.Error(codes.PermissionDenied, "not a gateway identity") }
    return id, nil
}
```

```sql
-- migrations/000005_create_security_state.sql
-- +goose Up
CREATE TABLE IF NOT EXISTS principal_quarantines (
    principal_id VARCHAR(128) PRIMARY KEY,
    reason TEXT NOT NULL,
    created_by VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_principal_quarantines_expires ON principal_quarantines (expires_at);
CREATE TABLE IF NOT EXISTS revoked_tokens (
    jti VARCHAR(128) PRIMARY KEY,
    reason TEXT,
    created_by VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_revoked_tokens_expires ON revoked_tokens (expires_at);
-- +goose Down
DROP TABLE IF EXISTS revoked_tokens;
DROP TABLE IF EXISTS principal_quarantines;
```

## State of the Art

| Old | Current | Impact |
|-----|---------|--------|
| `grpc.DialContext` | `grpc.NewClient` (lazy, dns resolver default) [ASSUMED] | optional migration; bufconn needs `passthrough:///` |
| insecure gRPC default | mTLS required, no fallback | this phase |
| Single pinned key envs | keyset dirs + reload | this phase |

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | grpc-go `peer.FromContext` + `credentials.TLSInfo` shape and handshake-vs-PermissionDenied behaviours | Pattern 1 | Test assertions on error codes may need adjusting; verify with a quick bufconn spike |
| A2 | `grpc.NewClient` default resolver is dns; lazy connect | Pattern 1 | Only matters if migrating from DialContext |
| A3 | K8s `$(VAR)` dependent env expansion with fieldRef POD_NAME | Pattern 5 | If wrong, use init container for per-pod cert selection |
| A4 | Acceptable to make PG mandatory for the control plane at startup | Pattern 4 | User may want degraded mode; confirm |
| A5 | Dev certs stay tracked in git and are regenerated in one commit | Cert generation | User may prefer generate-on-demand |
| A6 | Choose 8443 as the K8s workload port (vs 9443) | B2 table #2 | Pure choice; tests tie env and ports together either way |

## Open Questions

1. **PG mandatory for control plane?** Currently CP continues without PG. Recommendation: fatal at startup (durable state requires it). Needs user confirmation (A4).
2. **Convert the audit-worker to a gateway sidecar?** Needed for K8s correctness (separate Deployment cannot read per-pod RWO WAL). Recommendation: yes, sidecar appended after the gateway container.
3. **Add unauthenticated `/livez` `/readyz` to the control plane?** Needed for K8s probes; small code change in `internal/control/api.go`. Recommendation: yes.
4. **Keyset file selection:** D-07 says `*.pub`; runbook says `keyset.pub.N`. Recommendation: accept all non-dot files.
5. **Dev cert regeneration commit strategy** (A5).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | build/tests | yes | go1.26.0 | -- |
| Docker + compose | live smoke | yes | Docker 29.1.3 | -- |
| kind/kubectl/kustomize | live K8s | no | -- | hermetic YAML tests only (D-19) |
| Disk headroom | smoke builds | checked by script pre-flight (<=85%) | -- | script exits 3 |
| Postgres/Redis for unit tests | -- | not needed (pgxmock, miniredis) | -- | -- |

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` + testify v1 (require/assert), pgxmock v4.9, miniredis v2.34, `-race` |
| Config file | none (plain `go test`); Makefile targets exist |
| Quick run command | `go test ./internal/... ./cmd/... -race -count=1` |
| Full suite command | `go test ./... -race -count=1` |
| Live | `docker compose -f deployments/compose/docker-compose.<p>.yml up -d --build --wait` then `scripts/compose-smoke.sh <p>` for p in mvp hardened distributed, then `down -v` |

### Phase Requirements -> Test Map
| Req / Decision | Behavior | Test Type | Automated Command | File Exists? |
|---------|----------|-----------|-------------------|-------------|
| REV-03 / D-01 | migration creates both tables; Up/Down | unit | `go test ./internal/storage -run SecurityState -race` | Wave 0 |
| REV-03 / D-02 | PG-first ordering; PG fail -> 503 no Redis write; Redis fail -> 503 row kept; retry idempotent; unquarantine deletes both | unit (pgxmock+miniredis) | `go test ./internal/control -run 'Quarantine\|Revoke\|WriteThrough' -race` | Wave 0 (extend api_test.go) |
| REV-03 / D-03,D-04 | reconstruct restores only unexpired with remaining TTL, then marker; re-run on marker loss; backfill; reconcile heals | unit | `go test ./internal/control ./internal/revocation ./tests/dr -run 'Reconstruct\|Marker\|Reconcile' -race` | partial (tests/dr exists) |
| REV-03 / D-05 | marker missing -> ErrSecurityStateMissing -> 503 SECURITY_STATE_MISSING on both listeners; /readyz 503; recovers when marker set | unit/wiring | `go test ./cmd/gateway ./internal/proxy ./internal/revocation -run 'SecurityState' -race` | Wave 0 |
| D-05 classification | reason in governor table | unit | `go test ./internal/audit -run 'TestEveryMainReasonIsClassified' -race` | exists (extend lists) |
| REV-03 / D-06 | FLUSHALL drill on 3 profiles | live | `scripts/compose-smoke.sh {mvp,hardened,distributed}` (DR section) | Wave 0 |
| AUTH-01,CTRL-02 / D-07,D-08 | reloader: good swap, bad/empty/partial rejected & last-good kept, ConfigMap `..data` layout, hash no-op, SIGHUP, concurrent verify | unit `-race` | `go test ./internal/keyset ./internal/keyfile ./tests/rotation -race` | Wave 0 (rotation tests exist; repoint) |
| D-09 | issuer/CP load keys from files; invalid path fatal; seed fallback warns | unit | `go test ./cmd/demo-issuer ./cmd/control-plane -race` | partial |
| D-10 | no kid; formats unchanged | unit | `go test ./internal/identity ./internal/snapshot -race` | exists |
| D-11,D-12 | keyset mounts all profiles; live expand/switch/prune | lint + live | `go test ./tests/compose -run 'Keyset\|Wiring' ` and smoke KEYROT sections | Wave 0 |
| CTRL-03,CTRL-05 / D-13..D-16 | valid client accepted; plaintext rejected; wrong-CA rejected; ID mismatch -> PermissionDenied on Ack, Pong, heartbeat; nothing recorded; gateway ID from cert; env mismatch fatal; no insecure default | unit (bufconn + in-memory PKI) | `go test ./internal/control ./internal/snapshot ./internal/identity ./cmd/gateway -run 'MTLS\|Identity\|GatewayID' -race` | Wave 0 |
| D-18 | chaos/integration moved to mTLS; no `insecure.NewCredentials` outside tests | integration + static | `go test ./tests/chaos ./tests/integration -race` ; `! grep -rn 'insecure.NewCredentials' --include='*.go' cmd internal` | exists (rewrite) |
| D-17 | generator emits CP server + per-gateway certs, SPIFFE shape, signed by root | unit | `go test ./scripts/certificates ./internal/pki -race` (add test) | Wave 0 |
| B2 / D-19..D-21 | env keys, ports, probes, PVC spools, secret refs, no key material, POD_NAME pattern | hermetic YAML | `go test ./tests/manifests -race` | extend |
| B2 control plane probes | `/livez`,`/readyz` served unauthenticated | unit | `go test ./internal/control -run 'Livez\|Readyz'` | Wave 0 |
| Live all | smoke syntax/sections lint | unit | `go test ./tests/compose -race` | exists (extend) |

### Sampling Rate
- **Per task commit:** `go test ./<touched pkg>/... -race -count=1`
- **Per wave merge:** `go vet ./... && go test ./... -race -count=1`
- **Phase gate:** full suite green, then three live smoke runs (one profile at a time to respect the disk pre-flight; `down -v` between), before `/gsd:verify-work`.

### Wave 0 Gaps
- [ ] `internal/keyfile`, `internal/keyset` packages + tests
- [ ] shared in-memory test PKI helper for mTLS gRPC tests
- [ ] `internal/storage` security-state repo tests (pgxmock), migration up/down check
- [ ] `tests/manifests` source-derived env/port/probe cross-check tests
- [ ] `tests/compose` extensions (new env keys, keyset/cert mounts, smoke sections)

## Security Domain

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | yes | mTLS gRPC with SPIFFE gateway identity; JWT EdDSA multi-key validation |
| V3 Session Management | no (unchanged) | -- |
| V4 Access Control | yes | GatewayId bound to cert identity; PermissionDenied on mismatch |
| V5 Input Validation | yes | strict keyset parsing (reject partial sets), SPIFFE canonicalisation, principal/jti length limits |
| V6 Cryptography | yes | stdlib `crypto/tls` TLS1.3, `crypto/ed25519`; never hand-roll; private keys never to gateways/ConfigMaps |
| V7/V8 Error handling & data protection | yes | fail closed on missing security state; keys in Secrets only, none in manifests |

### Known Threat Patterns
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Spoofed GatewayId / plaintext gRPC (AR-01, T-10-02) | Spoofing | mTLS + cert-bound ID |
| Redis wipe revives revoked/quarantined principals | Elevation | PG source of truth + marker + fail-closed gate |
| Poisoned/empty keyset accepted | Tampering/DoS | reject empty/invalid, keep last good |
| Stale marker after partial Redis restore | Tampering | periodic reconcile of PG into Redis |
| Sibling-pod cert theft in StatefulSet | Spoofing | documented tradeoff; telemetry-only impact |
| Key material in manifests/git | Info disclosure | Secret refs; test scans manifests; dev keys flagged as demo |
| Downgrade to plaintext | Tampering | no insecure code path; static grep test |

## Sources

### Primary (HIGH confidence)
- Repository source read directly: `cmd/{gateway,control-plane,demo-issuer,audit-worker}/main.go`, `internal/{config,control,snapshot,identity,revocation,proxy,pki,storage,audit,telemetry}`, `deployments/{compose,kubernetes}`, `scripts/{certificates,compose-smoke.sh}`, `tests/{manifests,compose,chaos,rotation,dr}`, `migrations/`, `docs/runbooks/{disaster-recovery,credential-rotation}.md`, `10-SECURITY.md`, `go.mod`.

### Secondary / Tertiary (MEDIUM/LOW; training knowledge, tagged [ASSUMED] inline)
- grpc-go credentials/peer/NewClient behaviours; Kubernetes dependent env-var expansion and ConfigMap `..data` symlink semantics. Context7/WebFetch verification was not run in this session; the planner should schedule a short spike/`go doc` check for A1-A3.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH (no new packages; versions from go.mod)
- Architecture: HIGH for repo-derived facts, MEDIUM for grpc/K8s API behaviours
- Pitfalls: HIGH (found by reading call sites and tests)

**Research date:** 2026-10-11
**Valid until:** 2026-11-10
