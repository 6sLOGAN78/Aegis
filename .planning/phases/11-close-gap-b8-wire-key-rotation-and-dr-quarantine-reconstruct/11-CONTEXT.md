# Phase 11: Close gaps B8, B2, CR-02 (key rotation, DR reconstruction, K8s manifests, authenticated gRPC stream) - Context

**Gathered:** 2026-10-11
**Status:** Ready for planning

<domain>
## Phase Boundary

Code that today has only test callers runs in production, and the snapshot stream is authenticated:

- **B8, rotation:** the gateway loads trusted keysets for JWT validation and snapshot verification and changes them at runtime through `AddTrustedKey`/`SetTrustedKeys` (and the `TokenValidator` equivalents), so signing keys rotate with zero downtime.
- **B8, DR:** quarantines and jti revocations survive a Redis loss. `ReconstructQuarantines` runs in production, and gateways do not serve traffic or report ready until security state is back.
- **B2:** the Kubernetes manifests match the current binaries: container ports, probe paths, env keys, PVC-backed spools, and the new cert and keyset mounts.
- **CR-02 / AR-01:** the control-plane gRPC snapshot server requires mTLS. Each gateway dials with its own client cert, and the GatewayId in Ack/Pong/heartbeats is bound to that cert's identity. Plaintext, wrong-CA and mismatched-ID clients are rejected.

Out of scope: full production K8S-01 (default-deny NetworkPolicies, topology spread) stays in v2. mTLS CA / workload cert hot reload is not part of this phase.

</domain>

<decisions>
## Implementation Decisions

### DR: durable security state (REV-03)
- **D-01:** New additive migration(s) (`000005`+) add a `principal_quarantines` table (principal_id PK, reason, expires_at, created_at, created_by or similar) and a `revoked_tokens` table (jti PK, expires_at, created_at). The DR runbook already assumes `principal_quarantines` exists. The schema must match the runbook query, or the runbook gets updated.
- **D-02:** Write-through from the control plane: quarantine, unquarantine and jti revoke write Postgres first, then Redis. If Postgres succeeds and Redis fails, the API returns 503 `DEPENDENCY_FAILURE`, the Postgres row stays, and the reconcile pass pushes it to Redis later. Retries are idempotent. Unquarantine deletes from both.
- **D-03:** jti revocations use the same pattern (table, write-through, reconstruct, marker) so a Redis wipe cannot bring a revoked token back to life.
- **D-04:** The control plane runs reconstruction (`ReconstructQuarantines` plus a jti equivalent) at startup, and again whenever it sees that Redis lost state. Detection uses a marker or generation key the control plane writes after a successful restore. Only unexpired rows are restored, with their remaining TTL, not the 24h default. Gateways get no Postgres access, which preserves the "no PG on the gateway" constraint.
- **D-05:** Gateways check the marker key. While it is missing, they **fail closed**: requests get 503 with a new classified reason (for example `SECURITY_STATE_MISSING`, which must be added to the Phase 8 governor reason table so `TestEveryMainReasonIsClassified` passes), and `/readyz` returns 503 with a matching body. Target: restored within 60s (runbook SLO).
- **D-06:** Live proof: `scripts/compose-smoke.sh` on **mvp, hardened and distributed**: quarantine a user and revoke a jti → `FLUSHALL` Redis → gateways go 503 or not ready → they recover → the same principal is still 403 `PRINCIPAL_QUARANTINED` and the revoked token is still denied, both within 60s and on every replica (poll gateway-1/2/3 directly, as in Phase 9 D-15). Hermetic tests cover the migration, write-through ordering and failure handling, marker detection and reconstruction, and the gateway gate.

### Keyset loading & rotation (AUTH-01, CTRL-02)
- **D-07:** Each key family has its own mounted keyset directory: for example `AEGIS_JWT_KEYSET_DIR` and `AEGIS_SNAPSHOT_KEYSET_DIR`, each holding `*.pub` files. This matches the runbook's `keyset.pub.N` ConfigMap layout. The existing single-key env vars (`AEGIS_ISSUER_PUBLIC_KEY`, `AEGIS_CONTROL_PLANE_PUBLIC_KEY`) remain as a fallback when no dir is set, with a startup warning.
- **D-08:** Hot reload: the gateway re-reads each dir on a short poll interval (around 10s, configurable) and/or on SIGHUP, and swaps the set atomically through `SetTrustedKeys` (snapshot `Verifier`) and `SetPublicKeys` (`TokenValidator`). An empty, unreadable or partly invalid set is rejected and the last good set is kept, with a log line and a metric. No gateway restart is needed for any rotation step.
- **D-09:** Scope: gateway JWT validator and snapshot verifier keysets. Signers load their private keys from files: the demo issuer stops using the hardcoded `DefaultDeterministicSeed` (it reads a key path, with the seed only as a dev fallback that logs a warning), and the control plane keeps `AEGIS_SIGNING_KEY_PATH`. Compose uses **separate** issuer and snapshot signing keys; today both default to the same seed. mTLS CA rotation is unchanged.
- **D-10:** No `kid`. Verifiers try every trusted key, and overlap sets are small. Token and snapshot formats do not change.
- **D-11:** All 3 compose profiles mount keyset dirs, so the drill runs everywhere.
- **D-12:** Live proof: a smoke drill per key family (JWT and snapshot): **expand** (add the new pub to the keyset dir) → wait for reload → **switch signer** (restart only the issuer or control plane with the new private key) → old and new tokens and snapshots both accepted, with no gateway restart → **prune** the old pub → old tokens are rejected and new ones still pass. Hermetic tests cover the reloader (good swap, bad set rejected and last-good kept, concurrent verify under `-race`).

### gRPC mTLS & identity binding (CR-02, CTRL-03, CTRL-05)
- **D-13:** Each gateway has its own **separate control-plane client cert** with URI SAN `spiffe://aegis.local/ns/gateway/sa/aegis-gateway/gw/<id>`, used only for the gRPC stream. The backend-facing `gateway-client` cert and the backends' pinned `defaultGatewaySPIFFE` (`cmd/services/{orders,payments,admin}/main.go`) stay **unchanged**.
- **D-14:** The control plane uses a dedicated **control-plane server cert** (DNS SANs such as `control-plane`, `aegis-control-plane`, `localhost`). The gateway verifies it against `root-ca.crt` with a configured ServerName. The gRPC server requires and verifies client certs against the Aegis root CA. **No plaintext fallback:** the `insecure.NewCredentials()` default in `internal/snapshot/client.go` is removed, and missing cert or key paths are a startup error on both sides.
- **D-15:** The control plane parses the peer's SPIFFE ID (reuse `internal/identity` SPIFFE parsing), accepts only the `/gw/<id>` shape in trust domain `aegis.local`, and checks the GatewayId of **every** Ack, Pong and heartbeat. On a mismatch or a missing or invalid cert identity, it terminates the stream with `codes.PermissionDenied`, records nothing for that ID, and logs plus counts a low-cardinality metric. The gateway's backoff retries, and its lease expires if it keeps failing (fail closed).
- **D-16:** The gateway gets its GatewayId from its own control-plane client cert. If `AEGIS_GATEWAY_ID` is also set and differs, startup fails. The random `gateway-<uuid>` fallback goes away in mTLS mode.
- **D-17:** `scripts/certificates/main.go` generates the control-plane server cert and per-gateway control-plane client certs: `gateway` (mvp/hardened), `gateway-1..3` (distributed) and `aegis-gateway-0..2` (K8s StatefulSet ordinals). All compose profiles run the authenticated stream.
- **D-18:** Tests: `tests/chaos` helpers (`grpc.NewServer()` and the insecure dial in `tests/chaos/test_helpers.go`) and the integration tests move to mTLS with an in-memory test PKI. Add negative tests: plaintext client rejected, wrong-CA client rejected, GatewayId mismatch rejected (PermissionDenied), and a valid client accepted. Update `10-SECURITY.md` AR-01 / T-10-02 status when this closes.

### Kubernetes manifests (B2)
- **D-19:** Proof is **hermetic cross-check tests** in `tests/manifests`, with no cluster: every ConfigMap or env key in the manifests is one the binary actually reads (from `internal/config/config.go` and `os.Getenv` calls), container ports and probe paths match the real listeners and handlers (for example the workload listener port vs `:8443`, and `/livez` vs what the probe handler serves on that port), and audit spools are PVC-backed rather than `emptyDir`. A live kind/minikube bring-up is deferred; no K8s tooling is installed on the host.
- **D-20:** Certs and keysets reach pods through **named Secret and ConfigMap references** (for example the runbook's `gateway-jwt-keyset`, a snapshot keyset ConfigMap, the control-plane server cert Secret, and per-gateway control-plane client cert Secrets). A script or doc creates them from `deployments/certs`. No key material is committed into the manifests or the kustomize output.
- **D-21:** The K8s gateway ID follows the StatefulSet pod ordinal (`aegis-gateway-0` → cert `.../gw/aegis-gateway-0`). Each pod mounts its own client cert. Research and the planner choose the mechanism (for example a projected volume of all ordinal certs plus selection by hostname, or an init container).

### Claude's Discretion
- Exact table and column names, the marker key name and format, the reconcile cadence, and the env var names, as long as they are consistent and documented.
- Poll interval default and the reload metric names.
- Reason-code names for missing security state and for gRPC identity rejection, as long as they are classified.
- The per-pod cert mounting mechanism in K8s (D-21).
- Plan breakdown and wave ordering.
- Update `docs/runbooks/disaster-recovery.md` and `docs/runbooks/credential-rotation.md` to describe the real mechanisms (table, marker, keyset dirs, env names) instead of the current aspirational steps.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Gap definitions
- `.planning/ROADMAP.md` (Phase 11 entry): scope for B8, B2 and CR-02
- `.planning/v1.0-MILESTONE-AUDIT.md`: B2, B8 evidence (line refs for statefulset/configmap and orphaned callers)
- `.planning/phases/10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct/10-SECURITY.md`: AR-01 / T-10-02 / T-10-07-02 (CR-02 definition, to be closed here)
- `.planning/REQUIREMENTS.md`: REV-03, AUTH-01, CTRL-02, CTRL-03, CTRL-05

### Runbooks (the procedures this phase makes real)
- `docs/runbooks/disaster-recovery.md`: §2 Redis State Reconstruction (assumes the `principal_quarantines` table; 60s SLO; gateways not ready until restored)
- `docs/runbooks/credential-rotation.md`: 3-phase expand/switch/prune, `gateway-jwt-keyset` ConfigMap with `keyset.pub.N`, TTL + 30s overlap rule

### Prior phase decisions that constrain this one
- `.planning/phases/09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys-/09-CONTEXT.md`: smoke on 3 profiles, per-replica polling (D-15), SPIFFE validation reuse (D-12), disk pre-flight (D-17)
- `.planning/phases/08-close-gap-b7-route-denials-and-completion-events-to-the-audi/08-CONTEXT.md`: every new deny reason must be classified in the governor table

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/snapshot/verifier.go:47,54`: `AddTrustedKey` / `SetTrustedKeys` (RWMutex, copy-on-set), ready for the reloader.
- `internal/identity/jwt.go:55,62`: `TokenValidator.AddPublicKey` / `SetPublicKeys`.
- `internal/revocation/store.go:115-185`: `ListQuarantines` (SCAN `quarantine:principal:*`) and `ReconstructQuarantines` (pipelined SET with TTL, 2s timeout).
- `internal/identity` SPIFFE parsing (`ParseSPIFFEID`, already used by `internal/control/quarantine_handler.go:54`).
- `internal/pki/pki.go` and `scripts/certificates/main.go`: pure Go cert generation, to extend with control-plane server and per-gateway client certs.
- `migrations/` (embedded Goose, latest `000004_add_audit_jti.sql`).
- `tests/rotation/rotation_test.go`, `tests/dr/dr_test.go`: existing hermetic drills to point at the real loader and reconstruct path.
- `tests/manifests/manifest_test.go`: existing manifest test suite to extend.

### Established Patterns
- Gateway key loading is env-only today: `cmd/gateway/main.go:81-98`. `controlPlanePubKey` defaults to the **issuer** key, and the demo issuer (`cmd/demo-issuer/main.go:215`) and control plane (`cmd/control-plane/main.go:96`) share the same demo seed. Key separation is part of D-09.
- Readiness checks live in `internal/proxy/probe.go` (`CreateProbeHandler`: drain, lease, snapshot, spool). The security-state check plugs in here.
- Fail-closed Redis behavior: a 200ms timeout leads to 503 (Phase 3). The marker check should reuse the same Redis client and timeout discipline.
- Quarantine and revoke handlers: `internal/control/quarantine_handler.go` (`quarantineCore`), `HandleRevokeToken` in `internal/control/api.go`. The write-through goes here.
- gRPC: server at `cmd/control-plane/main.go:288` (`grpc.NewServer()`); Ack/Pong GatewayId handling in `internal/control/server.go:95-117`; client dial in `internal/snapshot/client.go:136-152` (insecure default).
- Compose cert wiring: `deployments/compose/docker-compose.hardened.yml:101-111` (`../certs:/certs:ro`, `AEGIS_*_PATH` envs); `AEGIS_GATEWAY_ID=gateway-1..3` in distributed.

### Integration Points
- K8s: `deployments/kubernetes/base/gateway/{statefulset,configmap}.yaml` (`AEGIS_WORKLOAD_ADDR: ":8443"`, container port 8443, `/livez` probes), `base/control-plane/deployment.yaml`, `base/audit-worker/deployment.yaml`, `overlays/production/*`.
- `scripts/compose-smoke.sh` plus its hermetic lint in `tests/compose`: new DR, rotation and mTLS sections.
- Backends pin `spiffe://aegis.local/ns/gateway/sa/aegis-gateway` (`cmd/services/*/main.go:18`). Do not change it.

</code_context>

<specifics>
## Specific Ideas

- The gateway's control-plane identity uses the path `/gw/<id>` under the existing gateway service account SPIFFE ID, so it can't be confused with the backend-facing identity.
- The DR smoke deliberately uses `FLUSHALL` to simulate a full Redis wipe while the stack runs, not only a Redis restart.
- Rotation must be proven with **no gateway restart**. Only the signer restarts.

</specifics>

<deferred>
## Deferred Ideas

- Live Kubernetes bring-up (kind/minikube apply + smoke). Needs tooling and disk; the hermetic cross-checks are the bar for this phase.
- `kid`-based key selection for JWTs and snapshots.
- mTLS CA / workload cert hot reload (third rotation family).
- Full K8S-01 (default-deny NetworkPolicies, topology spread) stays v2.

</deferred>

---

*Phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct*
*Context gathered: 2026-10-11*
