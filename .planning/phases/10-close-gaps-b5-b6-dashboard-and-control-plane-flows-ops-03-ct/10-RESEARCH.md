# Phase 10: Close gaps B5, B6 - Dashboard and Control-Plane Flows - Research

**Researched:** 2026-10-11
**Domain:** Go control plane (chi REST + gRPC snapshot stream), Go gateway snapshot client, React/TS dashboard
**Confidence:** HIGH (all findings come from reading the repo code; no new external libraries)

## Summary

Phase 10 is a bug-fix and wiring phase on code that already exists. No new libraries are needed. Every gap has a precise root cause in the current tree, and the original gap text is in `.planning/v1.0-MILESTONE-AUDIT.md` (B5, B6, and the per-requirement `evidence:` lines).

- **B5 (false "partitioned").** The gateway client (`internal/snapshot/client.go`) sends an `Ack` only on stream connect and after snapshot activation. The control plane (`internal/control/server.go`) never sends `Ping` and never reads `GetPong()`. The only periodic traffic is the 10s signed `FreshnessLease` (`lease.go`, wired in `cmd/control-plane/main.go:232`). `HandleListGateways` marks a replica partitioned when `now - lastHeartbeatAt > 60s`, where `lastHeartbeatAt` comes from `ack.AcknowledgedAt` (in-memory) or `gateway_acks.last_heartbeat_at` (DB, set to `NOW()` on each `RecordGatewayAck`). So a healthy idle gateway flips to partitioned after 60s.
- **B6 (publish 404).** `PolicyStudio.tsx:59` defaults `policyId` to `pol_authz_core`. The seed script creates the draft `mvp-authz`. `HandlePublishPolicy` calls `policyRepo.GetDraft(id)` and returns 404 if the draft is missing. The dashboard never POSTs `/control/v1/policies` to save a draft before publishing. `CreateDraft` is already an upsert (`ON CONFLICT (draft_id) DO UPDATE`), so "save draft" is just `POST /policies`.
- **CTRL-01.** `HandlePublishPolicy` never validates Rego. Validation exists only in `HandleValidatePolicy` (`Validator.ValidatePolicyDraft` plus `RunRegoTests`).
- **CTRL-02.** `HandlePublishPolicy` builds a `SnapshotPayload` with `Routes` and one `PolicyModule` and no `IdentityMappings`. `Manager.ValidateAndActivate` reads an empty map. Every publish also drops any mappings from the previous snapshot. A source of truth exists in `policies/data/seed_roles.json` (role to permissions), but nothing loads it.
- **OPS-01.** `api/openapi/control-v1.yaml` already contains most paths. The audit lists some as missing (`auth/*`, `/policies/simulate`, `/snapshots/rollback`, maybe others). The spec has no drift test, so any gap is unguarded.
- **OPS-02.** The `Idempotency-Key` header is optional server-side (`idempotency.go` skips when empty, and the OpenAPI param is `required: false`). The dashboard `ApiClient` supports `idempotencyKey` but no component passes one. ETag/If-Match is enforced on publish and works.

**Primary recommendation:** Do the fixes in this order: (1) gateway ack-on-lease heartbeat plus control-plane Pong handling (B5); (2) dashboard "save draft then publish" with the default policy id aligned to `mvp-authz` (B6); (3) publish-time Rego validation returning 422 (CTRL-01); (4) carry identity mappings into published and rolled-back snapshots, loaded from a configured source (CTRL-02); (5) a Go OpenAPI drift test plus spec fill-in, with `Idempotency-Key` sent by the dashboard and required on mutating operations (OPS-01/02). Every fix gets a Go test that fails before the change.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Gateway liveness heartbeat (B5) | Gateway (snapshot client) | Control plane (gRPC server records it) | Gateway originates the signal. Control plane persists it and derives status. |
| Convergence status computation | API / Backend (`HandleListGateways`) | Browser (renders badge) | Thresholds (60s partition, version lag) live server-side. |
| Draft save, publish, Rego validation | API / Backend | Browser (orchestrates call order) | Server must validate at publish regardless of client behaviour (fail closed). |
| Default policy id | Browser (PolicyStudio) | Seed script (`seed.sh`) | The UI default must match the seeded id. |
| Identity mappings in snapshot | API / Backend (publish) | Database / config (source of mappings) | Mappings are signed into the snapshot, so the control plane assembles them. |
| Idempotency and ETag enforcement | API / Backend | Browser (sends key and If-Match) | Server enforces. Client generates UUID per user action. |
| OpenAPI contract | Repo artifact (`api/openapi`) | Go tests (drift guard) | Spec is the contract. A test compares it to the chi router. |

## Standard Stack

No new dependencies. Use what is in the repo. [VERIFIED: repo read of go.mod and web/dashboard/package.json]

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go | go1.26.0 local toolchain (go.mod says 1.24+) | Control plane and gateway | Existing |
| github.com/go-chi/chi/v5 | existing | REST router (`internal/control/api.go`) | Existing. Offers `chi.Walk` for drift testing |
| google.golang.org/grpc and protobuf | existing | Snapshot stream (`SnapshotDistributionService`) | `HeartbeatPong` and `Ping` already exist in the proto |
| github.com/open-policy-agent/opa (`rego`, `ast`) | existing | Rego compile and tests via `Validator` | Reuse `ValidatePolicyDraft` and `RunRegoTests` |
| pashagolub/pgxmock + testify | existing | Handler tests with mocked DB (`api_test.go`) | Existing pattern |
| React 19.3, @tanstack/react-query 5, Vite 8, TS 5.7 | existing | Dashboard | `tsc -b && vite build` is the only dashboard check |

### Supporting
| Tool | Purpose | When to Use |
|------|---------|-------------|
| `oapi-codegen` (installed at ~/.local/bin) | Regenerates `pkg/api/control/v1/types.gen.go` | Run `scripts/generate.sh` after editing the OpenAPI yaml, so types do not drift |
| `opa test policies/rego policies/tests -v` | Rego unit tests | Part of `make test-quick` |
| A YAML parser for the drift test | Parse `control-v1.yaml` paths and methods | Check `go.mod` first. If `gopkg.in/yaml.v3` or `sigs.k8s.io/yaml` is already an indirect dependency, use it. Otherwise this is the only candidate new dependency (see Package Legitimacy Audit) |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Gateway acks on each lease receipt | Control plane sends `Ping` every N seconds and records `Pong` | Pong path needs server-side Ping loop plus Pong handling. Ack-on-lease needs one change in the client and reuses the 10s cadence. Recommended: ack-on-lease as primary. Also handle `Pong` server-side (small) so both liveness paths work |
| Independent gateway heartbeat ticker goroutine | Send inside the Recv loop | gRPC `ClientStream.SendMsg` is not safe to call concurrently from multiple goroutines. A ticker goroutine would race with the snapshot-ack Send. Send from the Recv loop (or guard Send with a mutex) |

**Installation:** none (no new packages expected).

**Version verification:** not applicable if no new packages are added. If the planner adds a YAML dependency, run `go list -m all | grep -i yaml` first.

## Package Legitimacy Audit

No new external packages are recommended for this phase. The one possible exception is a YAML parser for the OpenAPI drift test.

| Package | Registry | Age | Downloads | Source Repo | slopcheck | Disposition |
|---------|----------|-----|-----------|-------------|-----------|-------------|
| gopkg.in/yaml.v3 (only if not already in go.sum) | Go modules | many yrs | very high | github.com/go-yaml/yaml | not run (Go ecosystem, slopcheck is pip/npm oriented) | [ASSUMED] - planner should check `go.sum` first and gate with a human-verify step if it is a net-new module |

**Packages removed:** none. **Packages flagged [SUS]:** none. slopcheck was not run because no install is planned. Prefer a drift test that avoids a new dependency (see Pattern 5).

## Architecture Patterns

### System Architecture Diagram

```
 Operator (browser)                         Control plane (cmd/control-plane)                    Gateway (cmd/gateway)
 ------------------                         ---------------------------------                    ---------------------
 PolicyStudio
   |  1. POST /control/v1/policies  (save draft, upsert; Idempotency-Key)
   |------------------------------------> HandleCreatePolicyDraft -> policy_drafts (PG)
   |  2. POST /policies/{id}/validate ---> HandleValidatePolicy (UX feedback only)
   |  3. POST /policies/{id}/publish  ---> HandlePublishPolicy
   |      If-Match: "<active ver>"           a. RequireIfMatch (412 on mismatch)
   |      Idempotency-Key: <uuid>            b. GetDraft (404 if absent)
   |                                         c. NEW: ValidatePolicyDraft + RunRegoTests -> 422 on fail
   |                                         d. routes from RouteRepo + NEW identity mappings (carry forward / config)
   |                                         e. Ed25519 sign -> SaveSnapshot -> BroadcastSnapshot
   |                                                         |  gRPC stream: Snapshot
   |                                                         v
   |                                                   Gateway StreamClient.Recv loop
   |                                                     ValidateAndActivate (atomic swap)
   |                                                     Send Ack(ACTIVATED, ver)
   |                                         <------------- Ack  -> AckTracker.RecordAck -> gateway_acks (last_heartbeat_at=NOW())
   |                                         LeaseGenerator every 10s -> BroadcastLease
   |                                                         |  gRPC stream: Lease
   |                                                         v
   |                                                   Gateway: VerifyLease -> RecordLeaseRenewal
   |                                                     NEW: Send Ack(last status, active ver)   <== B5 fix
   |                                         <------------- Ack (every ~10s) refreshes last_heartbeat_at
   |  4. GET /control/v1/gateways (poll 5s) -> HandleListGateways
   |      healthy / degraded (ver lag) / partitioned (hb > 60s or REJECTED)
```

### Recommended Project Structure (touch points only)
```
internal/snapshot/client.go          # B5: ack on valid lease (preserve last ack status)
internal/control/server.go           # B5: also handle req.GetPong() -> RecordAck
internal/control/publish_handler.go  # CTRL-01 validation, CTRL-02 identity mappings
internal/control/rollback.go         # confirm it preserves payload.IdentityMappings (it unmarshals the old payload)
internal/control/idempotency.go      # OPS-02 require key on mutating ops (policy decision, see Open Questions)
cmd/control-plane/main.go            # CTRL-02 load mappings source (env path), pass to APIServer
web/dashboard/src/components/PolicyStudio.tsx   # B6 default id, save-draft-before-publish, idempotencyKey
web/dashboard/src/components/EmergencyQuarantine.tsx  # OPS-02: pass idempotencyKey on mutations
api/openapi/control-v1.yaml          # OPS-01 add missing paths, 422 on publish
internal/control/openapi_drift_test.go  # NEW drift guard
```

### Pattern 1: Heartbeat ack from the Recv loop (B5)
**What:** In the `ControlPlaneMessage_Lease` case, after `VerifyLease` succeeds, send a `GatewayMessage_Ack` with `ActiveVersion = manager.Active().Version`, `AcknowledgedAt = now`, and the status of the most recent snapshot outcome. Track `lastStatus` and `lastErr` locals in the loop. A rejected snapshot must keep reporting `ACK_STATUS_REJECTED` until a later activation succeeds, otherwise the heartbeat would mask a failure.
**When to use:** Always. The send is inside the single Recv goroutine, so it needs no extra locking.
**Example (existing sibling code in the same file, `Ping` case):**
```go
// Source: internal/snapshot/client.go (current Ping case, pattern to mirror)
_ = stream.Send(&snapshotv1.GatewayMessage{
    GatewayId: c.gatewayID,
    Timestamp: timestamppb.Now(),
    Payload:   &snapshotv1.GatewayMessage_Ack{Ack: &snapshotv1.SnapshotAck{
        GatewayId: c.gatewayID, ActiveVersion: currVer,
        Status: lastStatus, ErrorMessage: lastErr,
        AcknowledgedAt: timestamppb.Now(),
    }},
})
```
Should the lease fail verification, do not ack. A gateway that cannot verify leases is legitimately unhealthy and should age into partitioned.

### Pattern 2: Handle Pong server-side (B5, belt and braces)
In `StreamSnapshots` Recv goroutine, add `if pong := req.GetPong(); pong != nil { RecordAck(&SnapshotAck{GatewayId: req.GatewayId, ActiveVersion: pong.ActiveVersion, Status: ACTIVATED, AcknowledgedAt: now}) }`. Only record a Pong as ACTIVATED if the gateway's last known status is not REJECTED. Look up the last status from `AckTracker.GetReplicaStatuses()`.

### Pattern 3: Save-then-publish in the dashboard (B6)
`publishMutation` should first `POST /control/v1/policies` with `{policy_id, name, source_rego}` (upsert), then `POST .../publish`. Change the initial state to `useState('mvp-authz')`. Better: add a "Save draft" button using the same POST, and have publish call save first so the published bytes always equal the editor content. Validate the new draft source before publish on the server (Pattern 4), not only in the UI.
Also compute `If-Match` from the active snapshot version, not from `versions[0]`. `HandlePublishPolicy` compares against `distServer.GetActiveSnapshotInternal().Version`. `/policies/{id}/versions` returns all snapshots newest first, so `versions[0].snapshot_version` matches in the normal case. The fallback `1` when the list is empty is wrong if the server's active version is 0 or other. Handle 412 by reading the `ETag` response header and offering a retry (the seed script already does this).

### Pattern 4: Publish-time validation (CTRL-01)
After `GetDraft` and before assembling the payload:
```go
if err := s.validator.ValidatePolicyDraft(draft.SourceRego); err != nil { 422 UNPROCESSABLE_ENTITY }
if strings.Contains(draft.SourceRego, "test_") {
    sum, err := s.validator.RunRegoTests(ctx, draft.SourceRego, draft.SourceRego)
    if sum == nil || err != nil || sum.Failed > 0 { 422 }
}
```
Use the existing `UnprocessableEntity` response in the OpenAPI spec (add `'422'` to the publish operation). Do not call `GetDraft` with a nil `policyRepo` (the audit notes repos are nil when Postgres is down). Add a nil guard that returns 503 rather than panicking. Rollback re-publishes a historical signed snapshot and does not need re-validation.
Gateway-side consideration: `Manager.ValidateAndActivate` calls `policy.NewEngine` and rejects bad Rego anyway, but that happens after broadcast. Publish-time validation prevents a bad snapshot from ever being signed, persisted, and monotonic-versioned.

### Pattern 5: OpenAPI drift test (OPS-01)
Walk the chi router with `chi.Walk(s.router, ...)`, normalise `{id}` params, and compare the set of `METHOD path` against `paths:` in `api/openapi/control-v1.yaml`. Fail on any router route missing from the spec (and optionally the reverse). SPA routes (`RegisterSPARoutes`) must be excluded because they are not under `/control/v1`. To avoid a new dependency, a minimal indent-based scan of the yaml (paths start with two spaces and `/control/v1`, methods are four-space `get:`/`post:`/`delete:`) is workable, or use a YAML module already present. Also assert each mutating (POST/DELETE) operation references `IdempotencyKey`, and that publish/rollback reference `IfMatch`.
Missing from the spec according to the audit: `auth/*` (login, logout, me), `/policies/simulate`, `/snapshots/rollback`, and possibly `/audit/events` alias, `GET /principals/quarantine`. Add them, or deliberately declare aliases as excluded in the test with a comment. After editing the yaml, run `scripts/generate.sh` and commit `types.gen.go`.

### Pattern 6: Identity mappings at publish (CTRL-02)
Two coupled changes:
1. **Carry forward.** Unmarshal the active snapshot's payload (`distServer.GetActiveSnapshotInternal().Payload`) and copy its `IdentityMappings` into the new payload, so a publish never silently erases mappings. `rollback.go` already unmarshals the target payload, so confirm it preserves them.
2. **Source of truth.** The seed data exists in `policies/data/seed_roles.json` (role to `[]permission`). Recommended: control plane loads a mappings file at startup from an env var (for example `AEGIS_IDENTITY_MAPPINGS_FILE`, default unset, deterministic sort by role), stores it on `APIServer`, and publish uses it when set, otherwise carries forward. Compose configs must mount the file into the control-plane container (the policy dir is already mounted for seed in `deployments/compose/seed/`; check the three compose profiles). Do not invent a new DB table in this phase.
Sort roles so the signed payload is deterministic. Validate that each mapping has a non-empty role and permissions with no empty strings, reject on load.
Note: Phase 11 also lists CTRL-02 (key loading on the gateway side, B8). Scope here is only the publish-side `IdentityMappings` content.

### Pattern 7: Idempotency (OPS-02)
Dashboard: generate `crypto.randomUUID()` once per user action (per modal open or button click, reused on retry of the same action) and pass `idempotencyKey` to `apiClient.post` for publish, rollback, create policy, quarantine, unquarantine, and revoke. Server: either keep optional (and document) or require the header on mutating `/control/v1` operations other than login/logout. If required, update `scripts` and `deployments/compose/seed/seed.sh` (which calls routes, policies, publish with no key), plus tests in `tests/` and `internal/control/*_test.go` that POST without a key. The idempotent middleware also caches error responses (it caches whatever status the handler produced), so a 412 or 422 is replayed for the same key. The dashboard must therefore mint a fresh key for each new attempt after a failure that the user fixes (for example after correcting Rego), or users will see a stale cached 422.

### Anti-Patterns to Avoid
- **Ticker goroutine sending on the gRPC stream** alongside the Recv-loop sends: concurrent `Send` on one stream is a data race. Send from the Recv loop.
- **Heartbeat overwriting REJECTED:** a periodic ack that always says ACTIVATED hides a rejected snapshot. Echo the last real status.
- **Relying on UI-only validation:** the Validate button is advisory. Enforcement lives in publish.
- **Validating against the wrong module set:** `RunRegoTests(policy, policy)` is how the validate handler calls it. Mirror that exactly so publish and validate agree.
- **Adding a second default policy id constant:** use one constant for the UI default and keep it equal to `AEGIS_SEED_POLICY_ID` default (`mvp-authz`).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Rego syntax and test checking | A new parser or regex checks | `Validator.ValidatePolicyDraft` and `RunRegoTests` | Already OPA-backed and tested (`validator_test.go`) |
| Route enumeration for drift test | Manual route lists | `chi.Walk` | Cannot go stale |
| OpenAPI Go types | Hand-edited types | `scripts/generate.sh` (oapi-codegen) | Types come from the spec |
| Optimistic concurrency | New version scheme | Existing `RequireIfMatch` and `FormatETag` | Already enforced on publish and rollback |
| Idempotent replay | New cache | Existing `IdempotencyStore` | Adjust policy, do not rewrite |
| Heartbeat transport | New RPC or HTTP endpoint | Existing `GatewayMessage.Ack` or `Pong` on the stream | Proto and persistence already exist |
| Snapshot signing | Custom crypto | `snapshot.Signer.SignSnapshot` | Ed25519, monotonic |

**Key insight:** Every needed mechanism already exists and is orphaned (Ping/Pong, idempotency client option, Rego validators, IdentityMapping proto). The phase is wiring, so favour small diffs and tests that prove the wiring end to end.

## Common Pitfalls

### Pitfall 1: Concurrent gRPC stream Send
**What goes wrong:** Heartbeat goroutine and Recv-loop ack both call `stream.Send`, causing a race or a corrupted stream.
**How to avoid:** Send only from the Recv loop (lease case), or guard `Send` with a mutex. Run `go test -race`.
**Warning signs:** `-race` failures in `internal/snapshot` or `tests/chaos`.

### Pitfall 2: Heartbeat masks rejection
**What goes wrong:** Periodic ack reports ACTIVATED after a snapshot was REJECTED, so the dashboard shows healthy.
**How to avoid:** Persist `lastStatus` and `lastErr` in the client loop. Add a test: reject a bad snapshot, then deliver a lease, and confirm the tracker still shows REJECTED.

### Pitfall 3: In-memory vs DB heartbeat merge in `HandleListGateways`
**What goes wrong:** Merge picks in-memory data only if `AcknowledgedAt` is newer than the DB `last_heartbeat_at` (set by `NOW()` on the server). Clock differences between gateway-reported `AcknowledgedAt` and server `NOW()` can make the DB record look newer and hide in-memory updates, or the reverse. Also the default `activeVersion := 1` when no distServer is set.
**How to avoid:** In the server's Recv path the code already fills `AcknowledgedAt = now` only when nil. Prefer stamping server time on receipt for heartbeat purposes. Test with a gateway whose reported time is skewed.

### Pitfall 4: Idempotency cache replays failures
**What goes wrong:** Same `Idempotency-Key` reused after fixing input returns the cached 4xx.
**How to avoid:** New UUID per attempt that follows a user edit. Only reuse the key for network retries of the same request body.

### Pitfall 5: Making Idempotency-Key required breaks seed and tests
**What goes wrong:** `seed.sh`, compose smoke, `tests/**`, and `api_test.go` POST without the header and start getting 400.
**How to avoid:** If required server-side, update all callers in the same plan and grep for `/control/v1` POST/DELETE callers. Alternatively require only on publish, rollback, quarantine, revoke.

### Pitfall 6: Publish with nil repos
**What goes wrong:** `policyRepo`, `routeRepo`, or `snapshotRepo` is nil when Postgres is unavailable, so publish panics.
**How to avoid:** Guard and return 503 with the existing error response helper before using repos.

### Pitfall 7: Publish rewrites the draft status after a failed persist
`UpdateDraft` result is ignored (`_ =`). With Phase 10's draft-save flow, keep this non-fatal, but log failures.

### Pitfall 8: Embedded dashboard assets
The SPA is served from `web/embed.go` (embeds `web/dashboard/dist`). Dashboard source changes do not take effect until `npm run build` regenerates `dist/` and the control-plane image rebuilds. `dist/` is checked in (a `dist/assets/index-*.js` file contains the old `pol_authz_core`), so the plan must rebuild and commit `dist/` (or confirm the Docker build runs it).

## Code Examples

### Existing validation entry points to reuse
```go
// Source: internal/control/validator.go
func (v *Validator) ValidatePolicyDraft(sourceRego string) error
func (v *Validator) RunRegoTests(ctx context.Context, policyRego, testRego string) (*TestSummary, error)
```

### Existing save-draft handler (upsert)
```go
// Source: internal/control/policy_handler.go HandleCreatePolicyDraft; storage/policy_repo.go createDraftQuery
// POST /control/v1/policies {policy_id, name, source_rego}  -> 201, ON CONFLICT (draft_id) DO UPDATE
```

### Dashboard idempotency option (already supported, unused)
```ts
// Source: web/dashboard/src/api/client.ts
apiClient.post(path, body, { ifMatch: currentETag, idempotencyKey: crypto.randomUUID() })
```

### chi route walk for the drift test
```go
// Source: go-chi/chi v5 docs (chi.Walk)
chi.Walk(router, func(method, route string, h http.Handler, mw ...func(http.Handler) http.Handler) error { ... })
```
`[ASSUMED]` signature from training knowledge. The planner should verify against the vendored chi version with `go doc github.com/go-chi/chi/v5.Walk`.

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| Ack only on connect/activation | Ack on each verified lease (about 10s) | Replica `last_heartbeat_at` stays fresh; partition threshold of 60s becomes meaningful |
| Validation only on `/validate` | Validation enforced at publish | Invalid Rego never gets signed or versioned |
| Snapshot with no identity mappings | Mappings carried and sourced from config | Gateway `ActiveState.IdentityMappings` populated |

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Source of identity mappings should be a file loaded via env var from `policies/data/seed_roles.json` plus carry-forward | Pattern 6 | User may prefer a DB table or API-managed mappings. Needs confirmation before planning |
| A2 | Make `Idempotency-Key` required only on mutating state-changing operations (publish, rollback, quarantine, revoke, create policy/route) rather than globally | Pattern 7 | Over-strict breaks seed and tests. Under-strict leaves OPS-02 "partial" |
| A3 | `chi.Walk` callback signature | Code Examples | Compile error only, trivial to fix |
| A4 | Whether `gopkg.in/yaml.v3` is already in go.sum | Package Audit | If absent, a new module needs approval |
| A5 | Which compose profiles' control-plane service need a mount for the mappings file | Pattern 6 | Mappings silently empty in some profiles. Verify in `deployments/compose/*.yml` |
| A6 | The exact set of spec-missing paths (audit says auth/*, /policies/simulate, /snapshots/rollback, GET /principals/quarantine, /quarantine, /revocations, /audit/events). Current yaml already lists some of those, so the audit evidence may be partly stale | Pattern 5 | The drift test will reveal the true set |

## Open Questions

1. **Where do identity mappings come from?**
   - Known: proto and gateway consumption exist. `seed_roles.json` has the data. Rego in `authz.rego` currently uses roles directly, not permissions.
   - Unclear: whether the mapping is operator-editable (API) or static config for now.
   - Recommendation: static config file plus carry-forward (A1). Confirm with the user in plan review.
2. **Should `Idempotency-Key` be mandatory?** Roadmap says "OpenAPI drift / idempotency". Recommendation: dashboard always sends it. Server requires it on publish and rollback at minimum, with seed.sh and tests updated (A2).
3. **Ping vs ack-on-lease for B5.** The roadmap allows either. Recommendation: ack-on-lease plus Pong handling.
4. **Does the 60s partition threshold need to stay?** Keep as is. Leases arrive every 10s, so six missed leases is a real partition.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | all Go work, tests | yes | 1.26.0 | none needed |
| Node | dashboard build | yes | v22.23.3 | none needed |
| opa CLI | `make test-quick` Rego tests | yes | in ~/.local/bin | none needed |
| oapi-codegen | regenerate types | yes | in ~/.local/bin | none needed |
| protoc | only if proto changes (not expected) | yes | in ~/.local/bin | none needed |
| Docker / compose | optional live e2e (`make compose-smoke`) | not probed | | In-process Go tests cover logic |

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` + testify + pgxmock; in-process gRPC with `tests/chaos` TestCluster; no dashboard unit test framework |
| Config file | none (Makefile targets) |
| Quick run command | `go test -race ./internal/control/... ./internal/snapshot/...` |
| Full suite command | `make test-full` (`go test -race ./... && opa test policies/rego policies/tests -v`) |
| Dashboard check | `cd web/dashboard && npm run build` (`tsc -b && vite build`) |
| Baseline | `go test ./internal/control ./internal/snapshot` passes today |

### Phase Requirements to Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CTRL-05 / OPS-03 (B5) | Client sends Ack on verified lease; tracker `AcknowledgedAt` advances without a snapshot; rejected status is preserved | unit/in-process | `go test -race ./internal/snapshot -run Heartbeat` | Wave 0 (new test in `client_test.go`) |
| CTRL-05 / OPS-03 (B5) | Control plane records Pong | unit | `go test -race ./internal/control -run Pong` | Wave 0 (`control_test.go`) |
| OPS-03 (B5) | `HandleListGateways` reports healthy when heartbeat is recent, partitioned when >60s, and an idle gateway stays healthy across lease ticks | unit | `go test ./internal/control -run TestGatewayConvergence` | extend `gateway_handler_test.go` |
| OPS-03 (B6) | save draft (POST /policies) then publish returns 200 for id `mvp-authz` and 404 for unknown id | unit (pgxmock) | `go test ./internal/control -run TestPublish` | Wave 0 (`publish_handler_test.go`) |
| OPS-03 (B6) | Dashboard default id equals seeded id; build passes | build + grep | `cd web/dashboard && npm run build` and `grep -n "mvp-authz" web/dashboard/src/components/PolicyStudio.tsx` | manual-check |
| CTRL-01 | Publish with invalid Rego or failing `test_` rule returns 422 and does not sign, persist, or broadcast | unit | `go test ./internal/control -run TestPublishValidation` | Wave 0 |
| CTRL-02 | Published snapshot payload contains configured IdentityMappings; second publish and rollback preserve them; gateway `ActiveState.IdentityMappings` populated | unit + in-process | `go test -race ./internal/control ./internal/snapshot -run IdentityMappings` | Wave 0 |
| OPS-01 | Every router route has an OpenAPI operation, and POST/DELETE ops reference IdempotencyKey | unit (drift) | `go test ./internal/control -run TestOpenAPIDrift` | Wave 0 (`openapi_drift_test.go`) |
| OPS-02 | Replay with same key returns cached response; missing key policy per decision; publish without If-Match returns 412 | unit | `go test ./internal/control -run "Idempot|IfMatch"` | partly exists in `concurrency_test.go` |
| OPS-02 (dashboard) | Dashboard passes `idempotencyKey` on mutations | build + grep | `grep -rn idempotencyKey web/dashboard/src/components` | manual-check |
| End-to-end flow | author, validate, publish, swap, converged in compose | integration (optional, needs Docker) | `make compose-smoke PROFILE=mvp` | exists; may need an added check that `/gateways` stays healthy past 60s |

### Sampling Rate
- **Per task commit:** `go test -race ./internal/control/... ./internal/snapshot/...`
- **Per wave merge:** `make test-full` plus `npm run build` for dashboard touches
- **Phase gate:** full suite green, plus a live-profile check that a gateway left idle for more than 60s still shows `healthy` (UAT step)

### Wave 0 Gaps
- [ ] `internal/control/publish_handler_test.go` - B6, CTRL-01, CTRL-02 publish behaviours (reuse `setupTestHarness` in `api_test.go`)
- [ ] `internal/control/openapi_drift_test.go` - OPS-01
- [ ] heartbeat and rejection-preservation tests in `internal/snapshot/client_test.go`
- [ ] Pong test in `internal/control/control_test.go`
- [ ] No dashboard unit test runner exists. Do not add one in this phase; use build plus grep checks and the live UAT step.

## Security Domain

### Applicable ASVS Categories (L1)

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no change | Existing session login |
| V3 Session Management | yes (unchanged) | Existing session cookie plus CSRF token. New dashboard calls must keep sending `X-CSRF-Token` via `ApiClient` |
| V4 Access Control | yes | `RequireRole("sec-ops")` already on publish and create policy. Keep it on any new endpoints. Do not expose draft save to viewers |
| V5 Input Validation | yes | Server-side Rego validation at publish (422), `policy_id` format validation on create (currently unvalidated, add `validIDRegex`-style check), mapping validation on load |
| V6 Cryptography | yes | Ed25519 snapshot signing remains via `snapshot.Signer`. Never sign unvalidated policy. Do not hand-roll |
| V7 Logging/Errors | yes | Do not leak DB errors to clients. `SaveSnapshot` failure currently returns `err.Error()` to the client, so replace with a generic message |
| V13 API | yes | Idempotency and ETag enforcement. OpenAPI contract tested |

### Known Threat Patterns

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Publishing an invalid or malicious Rego policy that weakens authorization | Tampering / Elevation | Compile and test at publish. Role gate on sec-ops. Gateway also precompiles before swap (defense in depth) |
| Over-permissive identity mappings published from tampered config | Elevation | Validate mapping file at startup, fail startup on malformed content, file mounted read-only |
| Heartbeat spoofing: a client claims another gateway id | Spoofing | Gateway stream is authenticated at the gRPC layer. Out of scope to redesign. Note that `GatewayId` is client-asserted in the ack |
| Heartbeat masks a REJECTED or stale gateway | Repudiation / Info | Echo last real status. Convergence compares `active_version` to control-plane version |
| Replay or duplicate publish | Tampering | If-Match plus Idempotency-Key. Monotonic version |
| CSRF on publish and save-draft | Tampering | Existing CSRF token on mutating requests |
| Idempotency cache memory growth (in-memory, 24h, no eviction) | Denial of Service | Note as tech debt. The store never evicts expired entries, only on lookup. Optional cleanup |
| Stale cached 4xx replay | Availability | Fresh key per attempt |

## Sources

### Primary (HIGH confidence - repo reads)
- `.planning/v1.0-MILESTONE-AUDIT.md` - B5, B6 and per-requirement evidence
- `.planning/ROADMAP.md` Phase 10 and 11 scope
- `internal/snapshot/client.go`, `manager.go`; `internal/control/server.go`, `lease.go`, `publish_handler.go`, `policy_handler.go`, `gateway_handler.go`, `ack.go`, `idempotency.go`, `etag.go`, `api.go`, `validator.go`; `internal/storage/policy_repo.go`, `snapshot_repo.go`
- `web/dashboard/src/components/PolicyStudio.tsx`, `src/api/client.ts`
- `deployments/compose/seed/seed.sh`, `policies/data/seed_roles.json`
- `api/openapi/control-v1.yaml`, `scripts/generate.sh`, `Makefile`
- Local run: `go test ./internal/control ./internal/snapshot` passes

### Secondary / Tertiary
- None. No external web research was needed. chi `Walk` signature and gRPC concurrent-Send rule are from training knowledge ([ASSUMED]); verify with `go doc`.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH (no new deps, repo-verified)
- Architecture and root causes: HIGH (read the exact code paths)
- Identity mapping source decision: MEDIUM (design choice needing user confirmation)
- Idempotency strictness: MEDIUM (policy choice)
- Pitfalls: HIGH for repo-derived items, MEDIUM for gRPC concurrency (training knowledge)

**Research date:** 2026-10-11
**Valid until:** 30 days (stable internal code)

## Project Constraints (from CLAUDE.md)

No `./CLAUDE.md` exists. `./GEMINI.md` carries the project brief. Directives that apply:
- Fail-closed semantics: never produce implicit authorization on dependency failure. Publish must not sign unvalidated policy.
- No synchronous Postgres or OPA network calls on the gateway authorization hot path. Heartbeat acks happen on the control stream, off the request path.
- Verification: negative tests, race detector (`-race`), fault injection.
- Go for services, React/TypeScript for the dashboard.
- Project skills in `.agents/skills/` are GSD workflow skills only (no coding-pattern rules).
