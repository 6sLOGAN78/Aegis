# Phase 10: Close gaps B5, B6 - Pattern Map

**Mapped:** 2026-10-11
**Files analyzed:** 22 (12 modified source, 2 new Go tests, 8 modified tests/config/scripts)
**Analogs found:** 22 / 22 (all in-repo; this is a wiring phase and most files are their own analog)

Locked decisions: CTRL-02 mappings come from a config file (`policies/data/seed_roles.json`, env var, read-only compose mount), validated at startup (fail startup if malformed) and carried forward from the active snapshot on publish. OPS-02 `Idempotency-Key` is REQUIRED server-side on state-changing mutations (publish, rollback, quarantine, revoke, create policy/route); seed.sh, tests and compose smoke script are updated in the same plan.

## File Classification

| File | Role | Data Flow | Closest Analog | Match |
|------|------|-----------|----------------|-------|
| `internal/snapshot/client.go` (mod: ack on lease) | client | streaming | itself, Snapshot and Ping cases (lines 215-265) | exact |
| `internal/snapshot/client_test.go` (mod: heartbeat tests) | test | streaming | `internal/control/control_test.go` bufconn harness (lines 20-86) | role-match |
| `internal/control/server.go` (mod: Pong handling) | service (gRPC) | streaming | itself, Ack branch (lines 94-103) | exact |
| `internal/control/control_test.go` (mod: Pong test) | test | streaming | `TestControl_StreamInitialSnapshotAndAck` (line 88) | exact |
| `internal/control/gateway_handler_test.go` (mod) | test | request-response | `TestGatewayConvergence` (lines 21-60) | exact |
| `internal/control/publish_handler.go` (mod: validate, mappings, nil guards) | controller | request-response | `HandleValidatePolicy` in `policy_handler.go` (lines 104-170) | exact |
| `internal/control/publish_handler_test.go` (NEW) | test | request-response | `api_test.go` publish subtest (lines 497-559) + `setupTestHarness` (46-98) | exact |
| `internal/control/identity_mappings.go` (NEW, loader/validator) | utility | file-I/O, transform | `cmd/control-plane/main.go` env/key loading (lines 80-110) | partial |
| `internal/control/api.go` (mod: mappings field, require-key middleware) | config/route | request-response | itself (lines 14-63 struct+ctor; 103-140 routes) | exact |
| `internal/control/idempotency.go` (mod: require key) | middleware | request-response | itself, `IdempotencyMiddleware` (lines 122-189) | exact |
| `internal/control/rollback.go` (verify only) | service | CRUD | itself (lines 66-76 preserve payload) | exact |
| `internal/control/openapi_drift_test.go` (NEW) | test | batch/transform | `concurrency_test.go` table of subtests; chi routes in `api.go` | partial |
| `internal/control/api_test.go`, `concurrency_test.go`, `tests/integration/operator_telemetry_test.go` (mod: add keys) | test | request-response | existing `Idempotency-Key` use at `api_test.go:547`, `operator_telemetry_test.go:320,372` | exact |
| `cmd/control-plane/main.go` (mod: load mappings) | config | file-I/O | itself, env reads (lines 42-110, 243-255) | exact |
| `api/openapi/control-v1.yaml` (mod) | config | n/a | itself; `IdempotencyKey` param (line 573) | exact |
| `pkg/api/control/v1/types.gen.go` (regen) | generated | n/a | `scripts/generate.sh` | n/a |
| `web/dashboard/src/components/PolicyStudio.tsx` (mod) | component | request-response | itself, mutations (lines 127-175) | exact |
| `web/dashboard/src/components/EmergencyQuarantine.tsx` (mod) | component | request-response | itself, mutations (lines 62-120) | exact |
| `web/dashboard/dist/**` (rebuild, commit) | build artifact | n/a | Pitfall 8 | n/a |
| `deployments/compose/seed/seed.sh` (mod) | script | request-response | itself `cp_call` (lines 76-100) | exact |
| `scripts/compose-smoke.sh` (mod) | script | request-response | itself `cp_call` (lines 153-166) | exact |
| `deployments/compose/docker-compose.{mvp,distributed,hardened}.yml` + `tests/compose/compose_test.go` (mod) | config | n/a | mvp `control-plane` service (lines 31-60) and `../certs:/certs:ro` mount style | role-match |

## Pattern Assignments

### `internal/snapshot/client.go` (client, streaming) - B5 ack-on-lease

**Analog:** itself. The Snapshot case builds an ack; the Lease case (lines 240-248) is where to add one.

Current Lease case to change (lines 240-248):
```go
case *snapshotv1.ControlPlaneMessage_Lease:
    lease := p.Lease
    var expectedVer int64 = 0
    if active := c.manager.Active(); active != nil {
        expectedVer = active.Version
    }
    if err := c.verifier.VerifyLease(lease, expectedVer); err == nil {
        c.manager.RecordLeaseRenewal()
    }
```
Add, inside the `err == nil` branch, a `stream.Send` of `GatewayMessage_Ack` mirroring the Ping case Send shape (lines 256-265) and the Snapshot ack fields (lines 218-238). Declare `lastStatus := snapshotv1.AckStatus_ACK_STATUS_ACTIVATED` and `lastErr := ""` before the `for` loop (near line 182 `var receivedFirst bool`). In the Snapshot case set them from `actErr` (REJECTED plus `actErr.Error()`, else ACTIVATED and "").
```go
_ = stream.Send(&snapshotv1.GatewayMessage{
    GatewayId: c.gatewayID,
    Timestamp: timestamppb.Now(),
    Payload: &snapshotv1.GatewayMessage_Ack{Ack: &snapshotv1.SnapshotAck{
        GatewayId: c.gatewayID, ActiveVersion: expectedVer,
        Status: lastStatus, ErrorMessage: lastErr,
        AcknowledgedAt: timestamppb.Now(),
    }},
})
```
Constraints: send only from the Recv loop (single goroutine, no mutex, no ticker). Do not ack when VerifyLease fails. Existing error style: sends are `_ = stream.Send(...)` (ignore error; Recv surfaces failure).

### `internal/control/server.go` (gRPC service, streaming) - Pong handling

**Analog:** itself, Recv goroutine (lines 86-107):
```go
if ack := req.GetAck(); ack != nil {
    if ack.GatewayId == "" && req.GatewayId != "" { ack.GatewayId = req.GatewayId }
    if ack.AcknowledgedAt == nil { ack.AcknowledgedAt = timestamppb.Now() }
    if s.ackTracker != nil { _ = s.ackTracker.RecordAck(stream.Context(), ack) }
}
```
Add a sibling `if pong := req.GetPong(); pong != nil { ... }` using the same `ackTracker != nil` guard. Per RESEARCH Pattern 2, look up last status via `AckTracker.GetReplicaStatuses()` and do not upgrade REJECTED to ACTIVATED. Stamp `AcknowledgedAt: timestamppb.Now()` (server time, Pitfall 3).

### `internal/control/publish_handler.go` (controller, request-response) - CTRL-01, CTRL-02, nil guards

**Analog for validation:** `internal/control/policy_handler.go` `HandleValidatePolicy` lines 126 and 141:
```go
if err := s.validator.ValidatePolicyDraft(regoSource); err != nil { ... }
if strings.Contains(regoSource, "test_") {
    summary, testErr := s.validator.RunRegoTests(r.Context(), regoSource, regoSource)
    ... valid := testErr == nil && summary.Failed == 0
```
Mirror exactly (same `RunRegoTests(src, src)` args). Insert after `GetDraft` (publish_handler.go line 37), before routes load (line 39). Return with `writeErrorResponse(w, r, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", ...)`; check the `UnprocessableEntity` response name in the yaml for the code string.

**Error helper pattern** (already used throughout, lines 29, 35, 42, 83):
```go
writeErrorResponse(w, r, http.StatusNotFound, "NOT_FOUND", "Policy draft not found")
```
Nil guard (Pitfall 6): before line 33 add `if s.policyRepo == nil || s.routeRepo == nil || s.snapshotRepo == nil { writeErrorResponse(w, r, 503, "SERVICE_UNAVAILABLE", ...) ; return }`. The existing `HandleValidatePolicy` already uses `s.policyRepo != nil` (line ~113).
Line 100 leaks DB error to the client (`"Failed to persist snapshot: "+err.Error()`): replace with generic message, log the error.
Line 111 `_ = s.policyRepo.UpdateDraft(...)`: keep non-fatal but log failure.

**Identity mappings (carry-forward).** Payload assembly at lines 68-79. Add `IdentityMappings`:
```go
payload := &snapshotv1.SnapshotPayload{
    Version: nextVersion, Routes: routeDefs,
    PolicyModules: ...,
    IdentityMappings: s.resolveIdentityMappings(), // configured set if non-empty, else carry forward
}
```
Carry forward by unmarshalling the active envelope payload, same technique as `rollback.go` lines 66-69:
```go
var payload snapshotv1.SnapshotPayload
if err := proto.Unmarshal(targetEnv.Payload, &payload); err != nil { ... }
```
Use `s.distServer.GetActiveSnapshotInternal().Payload` (publish_handler.go lines 16-19 show the nil-safe access pattern). Proto type: `snapshotv1.IdentityMapping{Role string, Permissions []string}` (`pkg/api/snapshot/v1/snapshot.pb.go:739`). Sort by role for deterministic signing.

Precedence decision for planner: config-file set is authoritative when non-empty, else carry forward (per locked decision "loaded at startup, validated, carried forward on each publish").

### `internal/control/identity_mappings.go` (NEW; utility, file-I/O)

**Analog:** `cmd/control-plane/main.go` startup env loading (lines 80-110: `os.Getenv("AEGIS_SIGNING_KEY_PATH")` then read file, fatal on error). No loader exists, so this is a partial match; copy style: env var read, `os.ReadFile`, return error, `main` does `log.Fatalf` on failure (fail startup on malformed). Input format is `policies/data/seed_roles.json`: `{"developer": ["orders.read","payments.read"], ...}` i.e. `map[string][]string`. Validation: non-empty role, non-empty permission list, no empty strings. Output `[]*snapshotv1.IdentityMapping` sorted by role. Add a unit test next to it (table style as in `validator_test.go`).

### `internal/control/api.go` (route/config) - APIServer wiring, require-key

Struct and ctor (lines 14-63) take repos positionally; `setupTestHarness` (`api_test.go:70-82`) and `tests/integration/operator_telemetry_test.go:148` call `NewAPIServer(...)` with 11 positional args. Prefer a setter or functional option (for example `server.SetIdentityMappings([]*snapshotv1.IdentityMapping)`) over a new positional arg, to avoid breaking those callers. If a positional arg is added, update both callers.

Routes (lines 103-140): the protected group applies `pr.Use(s.idempStore.IdempotencyMiddleware)` to ALL routes including GETs and `auth/logout`:
```go
pr.Use(s.sessionMgr.SessionMiddleware)
pr.Use(s.idempStore.IdempotencyMiddleware)
```
For "require" semantic, do NOT make the middleware globally strict. Use per-route `pr.With(RequireIdempotencyKey, ...)` or a method+path-aware wrapper covering: `POST /routes`, `POST /policies`, `POST /policies/{id}/publish`, `POST /policies/{id}/rollback`, `POST /snapshots/rollback`, `POST /principals/{id}/quarantine`, `DELETE /principals/{id}/quarantine`, `POST /quarantine`, `POST /revocations`. Exempt: `auth/login` (outside group), `auth/logout`, validate, simulate (read-like POSTs). Keep route-level `RequireRole` pattern: `pr.With(RequireRole("sec-ops")).Post("/policies", ...)`.

### `internal/control/idempotency.go` (middleware) - require key

**Analog:** itself, lines 124-129:
```go
key := r.Header.Get("Idempotency-Key")
if key == "" {
    next.ServeHTTP(w, r)
    return
}
```
Add a sibling `RequireIdempotencyKey(next http.Handler) http.Handler` (missing header returns `writeErrorResponse(w, r, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")`). Error code style matches line 133 `"INVALID_IDEMPOTENCY_KEY"`. Order matters: the require check must run before `IdempotencyMiddleware`, and 412/422 get cached under the key (Pitfall 4) so dashboard mints a fresh key per attempt. Response-caching of failures is existing behavior (lines 171-178); leave.

### `internal/control/rollback.go` (verify only)

Lines 66-76 unmarshal the historical payload and re-sign the same struct, so `IdentityMappings` already persists. Add a test asserting this (in `publish_handler_test.go` or a rollback subtest modelled on `api_test.go:561-` "Monotonic Rollback" which builds `v1Payload`). Note rollback does not run through `distServer.BroadcastSnapshot` here; check `rollback_handler.go` for broadcast.

### `internal/control/publish_handler_test.go` (NEW; test, request-response)

**Analog:** `api_test.go` publish subtest lines 497-559 and `setupTestHarness` lines 46-98. Copy:
```go
h := setupTestHarness(t)
defer h.mockDB.Close()
handler := h.server.Handler()
cookie, csrfToken := loginOperator(t, handler, "admin", "admin-secret")
...
h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT draft_id, package_name, module_name, source_rego, status, created_by, created_at, updated_at FROM policy_drafts WHERE draft_id = $1")).
    WithArgs("policy-authz").WillReturnRows(draftRows)
h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT route_id, service_id, http_method, path_template")).WillReturnRows(...)
h.mockDB.ExpectExec(regexp.QuoteMeta("UPDATE policy_drafts")).WithArgs(...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
req.AddCookie(cookie); req.Header.Set("X-CSRF-Token", csrfToken)
req.Header.Set("If-Match", `"0"`); req.Header.Set("Idempotency-Key", uuid.NewString())
```
Tests to write: 404 unknown id; 422 invalid Rego (assert no `INSERT` expectation consumed: `h.mockDB.ExpectationsWereMet()`); 200 with mappings (unmarshal `distServer.GetActiveSnapshotInternal().Payload` and assert `IdentityMappings`); second publish preserves; 400 when `Idempotency-Key` missing. Existing tests omitting the key must be updated (`api_test.go:500,508` are expected-412 cases; they now need a key header or will hit 400 first depending on middleware order, so decide order: If-Match check lives inside handler, key check in middleware, so add keys there).

### `internal/control/control_test.go` / `internal/snapshot/client_test.go` (tests, streaming)

**Analog:** `setupTestControlPlane` (`control_test.go:20-86`, bufconn plus real `SnapshotDistributionServer`, signer, verifier, `ackTracker`). Note `control_test.go` is package `control_test` (external) so it can import `aegis/internal/snapshot`. `client_test.go` is in package `snapshot`; a heartbeat test there importing `internal/control` risks an import cycle (control imports snapshot). Put the end-to-end heartbeat test (StreamClient plus distServer plus `BroadcastLease`) in `internal/control/control_test.go` (or `tests/chaos`), and keep only a pure unit test in `client_test.go`. Pong test: open stream via `client.StreamSnapshots`, `Send(&snapshotv1.GatewayMessage{Payload: &snapshotv1.GatewayMessage_Pong{...}})`, then assert via `ackTracker.GetReplicaStatuses()` (see `TestControl_StreamInitialSnapshotAndAck` for the polling/assert style).

### `internal/control/gateway_handler_test.go` (test)

**Analog:** `TestGatewayConvergence` lines 21-60: `h.distServer.AckTracker().RecordAck(ctx, &snapshotv1.SnapshotAck{GatewayId, ActiveVersion, Status, AcknowledgedAt: timestamppb.New(now.Add(-2*time.Second))})` then GET `/control/v1/gateways` and assert status. Add case: ack at `-50s` (healthy), repeated heartbeat refresh keeps healthy, REJECTED stays partitioned.

### `internal/control/openapi_drift_test.go` (NEW; test)

No close analog. Use `chi.Walk(router, func(method, route string, h http.Handler, mw ...func(http.Handler) http.Handler) error)` on `h.server.Handler()` (type is `http.Handler`; may need `h.server.router` since the test is in package `control`, `router chi.Router` is accessible). Skip routes not prefixed `/control/v1` (SPA routes from `RegisterSPARoutes`). Spec parse: `gopkg.in/yaml.v3 v3.0.1` is already an indirect dependency in `go.mod:66` (and `go.yaml.in/yaml/v3` at line 59), so importing it directly needs only a `go mod tidy` promotion (no new module). Normalise chi `{id}` params against spec `{id}`. Assert POST/DELETE operations (excluding login/logout/validate/simulate if spec omits them) reference `#/components/parameters/IdempotencyKey`, and publish/rollback reference `IfMatch`. Spec param currently `required: false` at `control-v1.yaml:576`; change to `required: true` and re-run `scripts/generate.sh`.

### `cmd/control-plane/main.go` (config) - load mappings

**Analog:** itself. Env style (lines 42-55, 80-110):
```go
grpcPort := os.Getenv("AEGIS_GRPC_PORT")
...
if keyPath := os.Getenv("AEGIS_SIGNING_KEY_PATH"); keyPath != "" { ... }
```
Add near step 9 (line ~236, before `control.NewAPIServer` at 243): read `AEGIS_IDENTITY_MAPPINGS_FILE` (name from RESEARCH), if set call the loader, `log.Fatalf` on error, then `apiServer.SetIdentityMappings(...)`. If unset: carry-forward only.

### Compose files and `tests/compose/compose_test.go` (config)

Control-plane service in `docker-compose.mvp.yml` (lines 31-60) has NO `volumes:` block and the `control-plane` image build stage copies `COPY policies ./policies` (Dockerfile lines 23 and 63), so the file is already inside the image at `/app/policies/data/seed_roles.json` in at least one stage (verify WORKDIR for the control-plane target). Locked decision says read-only compose mount: add to each of the three profiles' `control-plane` service (mvp:31, distributed:66, hardened:29), mirroring the existing mount style `- ../certs:/certs:ro`:
```yaml
    environment:
      - AEGIS_IDENTITY_MAPPINGS_FILE=/etc/aegis/seed_roles.json
    volumes:
      - ../../policies/data/seed_roles.json:/etc/aegis/seed_roles.json:ro
```
(paths relative to `deployments/compose/`). Hardened profile may use `read_only: true`; check it. IMPORTANT: `tests/compose/compose_test.go` (around line 55-70) has an allowlist map of known `AEGIS_*` env vars (`"AEGIS_UPSTREAM_SCHEME": true, ...`); add the new variable or that test fails. Also check `tests/manifests` for K8s env checks.

### `web/dashboard/src/components/PolicyStudio.tsx` (component) - B6

**Analog:** itself. Line 59: `useState('pol_authz_core')` becomes `'mvp-authz'` (use a single exported constant). `publishMutation` (lines 127-135) becomes save-then-publish:
```tsx
mutationFn: async () => {
  setPublishError(null);
  await apiClient.post('/control/v1/policies', { policy_id: policyId, name: 'authz.rego', source_rego: regoSource }, { idempotencyKey: crypto.randomUUID() });
  return apiClient.post<PolicyPublishResponse>(`/control/v1/policies/${policyId}/publish`,
    { comment: ... }, { ifMatch: currentETag, idempotencyKey: publishKeyRef.current });
}
```
Seed uses `name: "authz.rego"` (`seed.sh` policy step), match it. Variable name for the editor content: check in file (not extracted here). Idempotency key lifecycle: keep a `useRef` key minted on modal open and re-minted on `onError` for 4xx (Pitfall 4). Add 422 branch in `onError` alongside the existing 412 branch (lines ~146-153) showing `err.message`. Rollback mutation (lines 159-170) also passes `idempotencyKey`. `ApiClient` already supports `idempotencyKey` (`api/client.ts:18,47-49`).

### `web/dashboard/src/components/EmergencyQuarantine.tsx` (component)

Three mutations to add `{ idempotencyKey: crypto.randomUUID() }` as the third arg: `quarantineMutation` (`apiClient.post(url, body)` at lines 67-73), `revokeMutation` (line 92), `unquarantineMutation` (`apiClient.delete(url)` at line 113; `delete` takes options as second arg: `apiClient.delete(url, { idempotencyKey: crypto.randomUUID() })`). Generate inside `mutationFn` so each click gets a fresh key.

### `deployments/compose/seed/seed.sh` and `scripts/compose-smoke.sh` (scripts)

seed.sh `cp_call` (lines 76-100) forwards `"$@"` extra curl args, so add a header per call: `-H "Idempotency-Key: $(cat /proc/sys/kernel/random/uuid)"` (alpine busybox has /proc; or `uuidgen` is not guaranteed in the alpine image, check Dockerfile seed stage at line 75 for installed packages, which include jq and curl). Cleanest: inside `cp_call` generate `_idem="$(cat /proc/sys/kernel/random/uuid)"` for POST/DELETE and add `-H "Idempotency-Key: $_idem"`. IMPORTANT: the 412 retry (seed.sh lines ~172-180) calls `cp_call` twice for publish; it must use a NEW key on retry because the 412 is cached under the first key (Pitfall 4). Generating inside `cp_call` per invocation handles this.

`compose-smoke.sh` `cp_call` (lines 153-166) has two curl branches; add the same header to both. Calls using it: quarantine POST/DELETE lines ~342-384; `dc exec ... curl` run inside the container, so uuid via `cat /proc/sys/kernel/random/uuid` on the host side (bash) or `$(uuidgen)`/`/proc` expression evaluated locally before the exec. Add a smoke check that `/gateways` stays healthy after >60s idle (RESEARCH validation table).

### Other tests needing key additions

`grep` shows direct `/control/v1` mutating callers: `internal/control/api_test.go` (many POSTs, only publish at line 547 sets a key), `internal/control/concurrency_test.go`, `internal/control/gateway_handler_test.go` (`TestConvergenceAndQuarantine` posts quarantines), `tests/integration/operator_telemetry_test.go` (lines 320, 372 already pass keys; verify other calls), `tests/compose/compose_test.go` and `smoke_script_test.go` (script text assertions). Add a shared test helper (e.g. `newMutatingRequest(method, path, body)` that sets a UUID key) in `api_test.go` to avoid repetition.

## Shared Patterns

### Error response helper
**Source:** `internal/control/publish_handler.go` lines 29, 35, 42 (`writeErrorResponse`)
**Apply to:** all new handler/middleware error paths
```go
writeErrorResponse(w, r, http.StatusNotFound, "NOT_FOUND", "Policy draft not found")
```
Never include `err.Error()` of DB or internal errors in client messages.

### RBAC on routes
**Source:** `internal/control/api.go` lines 112-140
**Apply to:** any new route
```go
pr.With(RequireRole("sec-ops")).Post("/policies/{id}/publish", s.HandlePublishPolicy)
```

### Optimistic concurrency
**Source:** `publish_handler.go` lines 22-25 (`RequireIfMatch(w, r, activeVersion)`), `etag.go` `FormatETag`. Unchanged; keep ordering: If-Match check before draft load.

### Test harness
**Source:** `internal/control/api_test.go` `setupTestHarness` / `loginOperator` (lines 46-115); pgxmock expectations with `regexp.QuoteMeta`; testify `assert`/`require`. External-package gRPC tests: `control_test.go` bufconn harness.

### Dashboard request options
**Source:** `web/dashboard/src/api/client.ts` lines 16-20, 40-50: `{ ifMatch?, idempotencyKey? }`; CSRF is attached automatically for POST/PUT/DELETE/PATCH.

### Single-goroutine gRPC Send
**Source:** `internal/snapshot/client.go` Recv loop: all `stream.Send` calls occur in the Recv goroutine. Keep it that way; run `go test -race`.

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/control/identity_mappings.go` | utility | file-I/O | No existing config-file loader/validator; only env/key loading in main.go (style reference only) |
| `internal/control/openapi_drift_test.go` | test | transform | No existing spec-versus-router test; use `chi.Walk` plus `gopkg.in/yaml.v3` (already indirect in go.mod) |

## Metadata

**Analog search scope:** `internal/control`, `internal/snapshot`, `cmd/control-plane`, `web/dashboard/src`, `deployments/compose`, `scripts`, `tests`, `api/openapi`
**Files scanned:** about 25 read or grepped
**Pattern extraction date:** 2026-10-11
**Gotchas for planner:** (1) `tests/compose/compose_test.go` env-var allowlist must include the new mappings env var; (2) seed.sh publish retry must mint a new Idempotency-Key (412 is cached per key); (3) `NewAPIServer` has 11 positional args used by `api_test.go:70` and `tests/integration/operator_telemetry_test.go:148`, so prefer a setter; (4) snapshot heartbeat end-to-end test cannot live in package `snapshot` if it imports `internal/control` (cycle), put it in `control_test` package; (5) rebuild and commit `web/dashboard/dist` (Pitfall 8).
