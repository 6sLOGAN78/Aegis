# Phase 9: Close gaps B9, B10 (jti revocation, SPIFFE quarantine keys) - Pattern Map

**Mapped:** 2026-10-10
**Files analyzed:** 22 new/modified (production + test + script)
**Analogs found:** 22 / 22 (every file modifies an existing file or copies a sibling; two new files have exact sibling analogs)

Most work here is in-place modification, so the "analog" is usually the file itself (the pattern to extend) plus a sibling for the new piece. Line numbers are from the current tree.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `cmd/demo-issuer/main.go` | service (auth issuer) | request-response | itself (`HandleLogin`, `seedDatabase`) | exact (modify) |
| `cmd/demo-issuer/main_test.go` | test | request-response | itself (`TestDemoIssuer`) | exact (modify) |
| `cmd/gateway/main.go` (missing-jti deny + `SetJTI` + fallback literal) | middleware (inline closure) | request-response | itself, lines 344-402 | exact (modify) |
| `cmd/gateway/*_test.go` (new `missing_jti` wiring test + helper test) | test (AST wiring) | transform | `cmd/gateway/wiring_test.go` | exact |
| `internal/audit/governor.go` (reasonTable entry) | config | transform | itself, lines 43-70 | exact (modify) |
| `internal/audit/event.go` (`JTI` field, `Normalize`) | model | transform | itself (`SuppressedCount`, `ErrorCode`, `clipString`) | exact (modify) |
| `internal/audit/logger.go` (`AuditContext.JTI`, `SetJTI`, two builders) | model/utility | event-driven | itself (`SetErrorCode` l.131-139, builders l.187-205, l.394-413) | exact (modify) |
| `internal/audit/worker.go` (21st insert arg) | service | batch | itself (`suppressed` nullable arg) | exact (modify) |
| `internal/audit/worker_test.go`, `spool_group_test.go`, `pipeline_e2e_delivery_test.go`, `event_test.go` | test | batch | themselves (`anyAuditArgs`, `auditArgsWith`) | exact (modify) |
| `migrations/000004_add_audit_jti.sql` | migration | batch | `migrations/000003_add_audit_suppressed_count.sql` | exact |
| `internal/storage/audit_repo.go` (+ `audit_repo_test.go`, `db_test.go`) | model (repo) | CRUD (read) | itself (SELECT at ~l.87) | exact (modify) |
| `internal/identity/spiffe.go` (+ `ParseSPIFFEID`) and `spiffe_test.go` | utility | transform | `ExtractSPIFFEID` in same file; `TestSPIFFE_ExtractValid` | exact |
| `internal/control/quarantine_handler.go` | controller | request-response | itself (`HandleQuarantinePrincipal`, `HandleUnquarantinePrincipal`) | exact (modify) |
| `internal/control/api.go` (routes l.132-138) | route | request-response | itself | exact (probably no change; route `POST /quarantine` already wired to the same handler) |
| `internal/control/api_test.go` | test | request-response | itself, "Principal Emergency Quarantine and Unquarantine" subtest l.200-233 | exact (modify) |
| `api/openapi/control-v1.yaml` + `pkg/api/control/v1/types.gen.go` | config / generated | n/a | existing `/principals/{id}/quarantine` path entry | role-match |
| `web/dashboard/src/api/types.ts` | model (hand-maintained types) | n/a | `QuarantineRecord` / `QuarantineRequest` / `UnquarantineResponse` (~l.134-160) | exact |
| `scripts/compose-smoke.sh` | test (live script) | request-response | itself: A4 section l.650-675, helpers l.100-235 | exact (modify) |
| `tests/compose/smoke_script_test.go` | test (lint) | transform | itself | exact (modify) |
| `tests/compose/compose_test.go` (new issuer-env test) | test | transform | `TestSeedService` l.407-432 | exact |
| `deployments/compose/docker-compose.{mvp,hardened,distributed}.yml` | config | n/a | existing `demo-issuer` env block (mvp l.167-176) | exact (modify) |
| `tests/integration/mvp_test.go:22`, `bypass_test.go:256` | test | request-response | themselves (`loginToken`) | exact (modify) |
| `docs/runbooks/quarantine-and-revocation.md` | doc | n/a | n/a | n/a (text fix) |

## Pattern Assignments

### `cmd/demo-issuer/main.go` (service, request-response)

**Analog:** itself. Keep the structure; replace the body decode and user lookup, add `ID`.

**Current seed + login to modify** (lines 20-29, 94-125): `seedDatabase` is keyed by username with `UserID`/`Roles`. Extend `SeedUser` with a password field loaded from env, do not change the keys.

**Error response style to copy** (lines 87-110): every error is `Content-Type: application/json`, `WriteHeader(code)`, `json.NewEncoder(w).Encode(map[string]string{"error": "CODE"})`.
```go
if !s.checkRateLimit(clientIP) {          // keep BEFORE body decode: every attempt counts (D-04)
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusTooManyRequests)
    _ = json.NewEncoder(w).Encode(map[string]string{"error": "RATE_LIMITED"})
    return
}
```

**Target body handling** (replace l.94-110):
```go
var req struct {
    Username string `json:"username"`
    Password string `json:"password"`
}
dec := json.NewDecoder(r.Body)
dec.DisallowUnknownFields()               // old {"role":..} body -> decode error -> 400
if err := dec.Decode(&req); err != nil || req.Username == "" {
    // 400 INVALID_REQUEST
}
user, ok := seedDatabase[req.Username]
want := user.Password
if !ok { want = dummyPassword }           // compare anyway: no user-enumeration timing
a, b := sha256.Sum256([]byte(want)), sha256.Sum256([]byte(req.Password))
if !ok || subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
    // 401 INVALID_CREDENTIALS (same body for unknown user and wrong password)
}
```
Note: `DisallowUnknownFields` on `{"role":"developer"}` yields a decode error (400), which is the D-01 behavior. Missing password with a valid username falls through to 401.

**jti** (add to `RegisteredClaims` at l.117-123): `ID: uuid.NewString(),` (import `github.com/google/uuid`). Response map (l.135-139) gains `"jti": claims.ID`.

**Env password loading + startup warn** (new, in `main()` near l.168): `os.Getenv("AEGIS_DEMO_PASSWORD_DEVELOPER")` etc.; when empty use demo default and `log.Printf("WARN: default demo password in use for %s", name)` (use the existing `log` import, never print the value). Env var names: `AEGIS_DEMO_PASSWORD_DEVELOPER`, `_FINANCE`, `_APPLICATION_ADMIN`.

---

### `cmd/demo-issuer/main_test.go` (test, request-response)

**Analog:** itself, lines 1-60.

**Setup pattern** (l.18-26):
```go
privKey := ed25519.NewKeyFromSeed(DefaultDeterministicSeed)
server := NewIssuerServer(privKey, "aegis-issuer", "aegis-gateway")
ts := httptest.NewServer(server.Routes())
defer ts.Close()
validator := identity.NewTokenValidator("aegis-issuer", "aegis-gateway", privKey.Public())
```
Change `json.Marshal(map[string]string{"role": tc.role})` to `{"username":..., "password":...}`; replace the `UNKNOWN_ROLE` expectation with 401 `INVALID_CREDENTIALS`; add subtests: old `{"role":..}` -> 400, wrong/missing password -> 401, `claims.ID` is a UUID and unique across two logins (`validator.ValidateBearerToken` returns claims with `ID`), response has `jti` equal to `claims.ID`, 429 after 20 attempts (update body). Run with `-run Jti` for the Wave 0 filter.

---

### `cmd/gateway/main.go` (middleware, request-response)

**Analog:** itself, lines 344-402. Insert the jti guard between `SetPrincipal` (l.364-367) and `CheckRevocation` (l.370), outside the `revStore != nil` guard.

**Existing invalid-token deny to mirror** (l.346-362):
```go
claims, err := tokenValidator.ValidateBearerToken(authHeader)
if err != nil {
    if ac != nil {
        ac.SetCanonicalPath(canonicalPath)
        ac.SetDecision("deny", "UNAUTHORIZED")
        ac.SetErrorCode("UNAUTHORIZED")
    }
    proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
        "https://aegis.local/errors/unauthorized", reqID)
    return
}
if ac != nil {
    ac.SetPrincipal(claims.Subject, "user", claims.Roles)
    ac.SetCanonicalPath(canonicalPath)
    // NEW: ac.SetJTI(claims.ID)
}
```
**New block** (after l.367):
```go
if missingJTI(claims) {
    if ac != nil {
        ac.SetDecision("deny", "TOKEN_MISSING_JTI")
        ac.SetErrorCode("TOKEN_MISSING_JTI")
    }
    proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized",
        "Token is missing a jti claim", "https://aegis.local/errors/unauthorized", reqID)
    return
}
```
The `SetDecision("deny", "<literal>")` must stay a string literal; `TestEveryMainReasonIsClassified` AST-scans for it. `missingJTI` is a tiny pure helper in package main (`func missingJTI(c *identity.UserClaims) bool { return c == nil || c.ID == "" }`) so it is unit testable. Also: fallback event literal at ~l.538 add `JTI: claims.ID`. The workload path (~l.848) has no jti. Revoked-token response (l.387-399) is the 403 template, unchanged.

---

### `cmd/gateway/` new wiring test (test, AST)

**Analog:** `cmd/gateway/wiring_test.go`.

**Helpers to reuse** (l.1-40): `wiringParseMain(t)` returns `(*ast.File, *ast.FuncDecl)`; `wiringSelector(fn, x, name)` matches `x.name` selectors; `wiringCall` holds call plus position. Write a test that walks `main`'s body for `ac.SetDecision` calls whose first arg is `"deny"` and second `"TOKEN_MISSING_JTI"`, and asserts its `Pos()` is less than the `revStore.CheckRevocation` call `Pos()`. Plus a plain table test for `missingJTI` (nil, empty ID, set ID). Run filter: `-run MissingJTI`.

---

### `internal/audit/governor.go` (config)

**Analog:** itself, lines 56-70 (identified class block).
```go
// Identified class: suppressor keyed (principal, route, reason).
"PRINCIPAL_QUARANTINED": classIdentified,
"TOKEN_REVOKED":         classIdentified,
// add:
"TOKEN_MISSING_JTI":     classIdentified,
```
Principal is known (signature validated), so identified is correct. Optionally extend the literal list in `governor_test.go:46` and `main_reasons_test.go:80`.

---

### `internal/audit/event.go` (model)

**Analog:** itself. Struct tail (l.59-64) and `Normalize` (l.76-104).
```go
ErrorCode string `json:"error_code,omitempty"`
EventType string `json:"event_type,omitempty"`
SuppressedCount int `json:"suppressed_count,omitempty"`
// add:
// JTI is the token id of the authenticated user token, empty for workloads/anonymous.
JTI string `json:"jti,omitempty"`   // omitempty keeps old frames/workers compatible (spool uses plain json)
```
In `Normalize()` next to the other clips (l.80-90): `e.JTI = clipString(e.JTI, 128)`. Test in `event_test.go`: round-trip JSON, old frame without `jti` decodes empty, clip at 128 bytes, NUL stripping.

---

### `internal/audit/logger.go` (model, event-driven)

**Analog:** itself. `AuditContext` fields (l.21-40), `SetErrorCode` (l.131-139).
```go
func (ac *AuditContext) SetErrorCode(errCode string) {
	if ac == nil { return }
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.ErrorCode = errCode
}
```
Copy for `SetJTI(jti string)` and add `JTI string` to the struct. Both event builders must copy it: the one ending at l.205 (`ErrorCode: ac.ErrorCode, EventType: EventTypeDecision`) and the other at l.411 (`ErrorCode: ac.ErrorCode`). Add `JTI: ac.JTI,` in each. The slog line at ~l.254 (`"error_code", event.ErrorCode`) may optionally gain `"jti"`; a jti is not a secret but keep log fields minimal.

---

### `internal/audit/worker.go` (service, batch)

**Analog:** itself, lines 78-88 (query) and 121-154 (args).

Append column and placeholder at the end only (existing tests override index 1 and 19):
```go
http_status, duration_ms, client_ip, error_code, suppressed_count, jti
) VALUES ( ... $19, $20, $21 )
```
Nullable arg pattern to copy (l.121-124):
```go
var suppressed any
if e.SuppressedCount > 0 {
    suppressed = e.SuppressedCount
}
```
becomes
```go
var jti any
if e.JTI != "" { jti = e.JTI }   // e.Normalize() already ran at l.92
```
and `batch.Queue(query, ..., suppressed, jti)`.

**Test fallout** (`worker_test.go:20-26`):
```go
func anyAuditArgs() []any {
	args := make([]any, 20)          // -> 21
	for i := 0; i < 20; i++ { ... }  // -> 21
```
`auditArgsWith` (~l.322) must also grow by one; `spool_group_test.go:361` and `pipeline_e2e_delivery_test.go:75` use these helpers, so verify after the change. Add a test: event with JTI passes it as arg 21, empty JTI passes nil.

---

### `migrations/000004_add_audit_jti.sql` (migration)

**Analog:** `migrations/000003_add_audit_suppressed_count.sql` (whole file, 5 lines):
```sql
-- +goose Up
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS suppressed_count INT;

-- +goose Down
ALTER TABLE audit_events DROP COLUMN IF EXISTS suppressed_count;
```
New file: same shape with `jti VARCHAR(128)` (nullable); optional partial index `idx_audit_events_jti ... WHERE jti IS NOT NULL` with matching `DROP INDEX IF EXISTS` in Down (before the column drop). Picked up automatically by `//go:embed *.sql` in `migrations/migrations.go`. In `internal/storage/db_test.go:60-65` add `assert.Contains(t, fileNames, "000004_add_audit_jti.sql")`; the loop below it already enforces Up/Down markers.

---

### `internal/storage/audit_repo.go` (repo, CRUD read)

**Analog:** itself. List SELECT (l.~87-92):
```go
SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind,
       roles, service_id, route_id, http_method, request_path, decision, reason_code,
       snapshot_version, http_status, duration_ms
FROM audit_events
```
Append `, jti` in this query and in `getAuditEventQuery`, add a `*string` scan target in both scan blocks, and map to the generated `Jti *string` field after `oapi-codegen`. Update `audit_repo_test.go` pgxmock `NewRows` column lists (add `jti`) and row values. `internal/control/audit_handler.go` needs no change.

---

### `internal/identity/spiffe.go` (utility, transform)

**Analog:** `ExtractSPIFFEID` in the same file (l.17-41) and `TestSPIFFE_ExtractValid` table style in `spiffe_test.go` (l.20-60).

**Imports to extend** (l.3-8): add `net/url`. Reuse existing `ErrInvalidTrustDomain`, `ErrMalformedSPIFFEURI` (l.10-15).

**Checks to share** (l.29-37): scheme EqualFold `spiffe`, host EqualFold trust domain wrapped as `fmt.Errorf("%w: got %q, expected %q", ErrInvalidTrustDomain, ...)`, path not `""`/`"/"`.

**New function** (RESEARCH Code Examples is the shape; additionally enforce: no `//` or `.`/`..` segments, path chars `[A-Za-z0-9._/-]`, total length <= 128, lowercase host, return canonical string):
```go
func ParseSPIFFEID(id, trustDomain string) (string, error) {
    u, err := url.Parse(id)
    if err != nil || u.Scheme != "spiffe" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
        return "", ErrMalformedSPIFFEURI
    }
    if !strings.EqualFold(u.Host, trustDomain) { return "", fmt.Errorf("%w: ...", ErrInvalidTrustDomain) }
    ...
    u.Host = strings.ToLower(u.Host)
    return u.String(), nil
}
```
Caution from the existing test (l.34-38): `ExtractSPIFFEID` preserves host case (`SPIFFE://AEGIS.LOCAL/...` yields `spiffe://AEGIS.LOCAL/...`... actually `uri.String()` keeps whatever the cert has). Real certs use lowercase `aegis.local`, so lowercasing in the parser keeps byte equality with the gateway key. Do not change `ExtractSPIFFEID` output. Table test cases: valid `spiffe://aegis.local/workload/orders`, wrong domain, `%zz`, query/fragment/port/userinfo, `..`, empty/`/` path, over-length, uppercase host canonicalized.

---

### `internal/control/quarantine_handler.go` (controller, request-response)

**Analog:** itself. Imports (l.3-11) gain `net/url`, `strings`, `aegis/internal/identity`.

**Current handler shape to refactor** (l.20-67): `chi.URLParam(r,"id")` -> empty 400 -> decode `controlv1.QuarantineRequest` -> default reason -> ttl from `ExpiresAt` -> actor from `GetSessionFromContext` -> `s.revStore.QuarantinePrincipal(ctx, id, reason, ttl)` -> 503 `DEPENDENCY_FAILURE` on error -> `controlv1.QuarantineRecord` JSON. Error helper: `writeErrorResponse(w, r, status, "CODE", "message")`.

**Shared id resolution to add** (one function used by quarantine, unquarantine, body form):
```go
func canonicalPrincipalID(raw string) (string, error) {
    id, err := url.PathUnescape(raw)          // NOT QueryUnescape ('+' -> ' ')
    if err != nil { return "", err }
    // reject: empty, len>128, control chars, '%' (double-decode guard: a%2541), 
    if strings.HasPrefix(id, "spiffe://") {
        return identity.ParseSPIFFEID(id, "aegis.local")   // 400 on error
    }
    return id, nil                                          // user ids pass through
}
```
**Flow in `HandleQuarantinePrincipal`:** `raw := chi.URLParam(r,"id")`. Decode the body first (needed for body form). If `raw != ""` use it; else use `req.PrincipalId` (new optional field on `QuarantineRequest`); if both present and canonicalized values differ -> 400; neither -> 400 "Principal ID is required". The route `pr.With(RequireRole("sec-ops")).Post("/quarantine", s.HandleQuarantinePrincipal)` already exists (`api.go:137`), so no router change; the "always 400" is because `{id}` is empty. `HandleUnquarantinePrincipal` (l.70-91): same decode + validate, no body.

`RevokeTokenRequest` (l.13-17) is a hand-written struct; if the OpenAPI addition generates a type of the same name, delete the local struct (name collision).

---

### `internal/control/api_test.go` (test, request-response)

**Analog:** itself, lines 200-233 ("Principal Emergency Quarantine and Unquarantine"). Pattern: cookie + `X-CSRF-Token`, `httptest.NewRequest(POST, "/control/v1/principals/evil-user/quarantine", body)`, `handler.ServeHTTP`, assert `controlv1.QuarantineRecord`, then verify Redis directly:
```go
val, err := h.rdb.Get(t.Context(), "quarantine:principal:evil-user").Result()
```
New subtests with the same scaffold: path `/control/v1/principals/spiffe:%2F%2Faegis.local%2Fworkload%2Forders/quarantine` and the `encodeURIComponent` form `spiffe%3A%2F%2F...` -> key `quarantine:principal:spiffe://aegis.local/workload/orders`; wrong trust domain -> 400; `%zz` -> 400; `a%2541` -> 400; `POST /control/v1/quarantine` with `{"principal_id": "...", "reason": "x"}` -> 200 same key; body+path mismatch -> 400; DELETE with encoded path removes the key. Note `httptest.NewRequest` sets `RawPath` only when the target contains escapes, which matches the real server.

---

### `api/openapi/control-v1.yaml` and `pkg/api/control/v1/types.gen.go` (config / generated)

**Analog:** existing `/control/v1/principals/{id}/quarantine` post/delete entries in the spec (paths at l.15-432). Add `GET /principals/quarantine`, `GET /quarantine` (`{quarantines: [QuarantineRecord]}`), `POST /quarantine`, `POST /revocations`; add optional `principal_id` to `QuarantineRequest`; add `jti: string` to `AuditEvent` (l.850-904). Document the encoded-id requirement in the path parameter description. Regenerate via the exact command in `scripts/generate.sh`:
`oapi-codegen -generate types -package controlv1 api/openapi/control-v1.yaml > pkg/api/control/v1/types.gen.go` (v2.5.0, regeneration is currently byte-identical so the diff is only intended changes). Check: `oapi-codegen ... | diff - pkg/api/control/v1/types.gen.go`.

### `web/dashboard/src/api/types.ts` (hand-maintained)

**Analog:** `QuarantineRecord`/`QuarantineRequest`/`UnquarantineResponse` blocks (~l.134-160); mirror their style for `RevokeTokenRequest`, `RevokeTokenResponse`, `QuarantineListResponse`, and `jti?: string` on `AuditEvent`. `EmergencyQuarantine.tsx` keeps `encodeURIComponent` (l.68, l.113); no change. `web/dashboard/dist/` is tracked and embedded: skip `AuditStream.tsx` changes unless rebuilding dist.

---

### `scripts/compose-smoke.sh` (live script)

**Analog:** itself. Helpers (l.100-235) and the A4 section (l.650-675).

**Helpers to reuse:** `dc`, `pass`/`fail` (`fail "label" "why"` increments `FAILS`), `op_login` + `cp_call METHOD PATH [BODY]` (prints status code only), `valid_rid`, `psql_q`, `wait_q_eq SQL WANT SECS`, `GW` (`gateway` or `gateway-1`), `cleanup` + the single `trap cleanup EXIT` (l.239).

**`dev_token` to change** (l.141-146):
```bash
dev_token() {
  dc exec -T "${GW}" curl -s -X POST -H 'Content-Type: application/json' \
    -d '{"role":"developer"}' http://demo-issuer:8085/login 2>/dev/null \
    | jq -r '.access_token // empty' 2>/dev/null || true
}
```
Use `jq -n --arg u developer --arg p "${SMOKE_DEV_PASSWORD}" '{username:$u,password:$p}'` for the body (defaulted near l.67 beside `SMOKE_PASSWORD`). Add a sibling `dev_login` that returns both `.access_token` and `.jti`; never echo the token or password.

**A4 section template** (l.650-675): token + `op_login`, `cp_call POST .../quarantine`, poll loop `for _ in $(seq 1 10)` on `gw_code`, then `cp_call DELETE ... >/dev/null || true`. Copy this skeleton for the new sections but replace the 1s `seq` polling with the per-replica loop from RESEARCH (`mapfile -t GATEWAYS < <(dc config --services | grep '^gateway')`, `date +%s.%N`, `awk` elapsed, `dc exec -T "$g" curl ...`, print `REV-03 jti revoke: ${g} denied after ${el}s`, fail if > 5). Update the A4 stale wording at l.656 and l.670 ("do not fix in this phase").

**New sections** (headers must match lint strings): `=== DISK ...` pre-flight (after tool checks, before compose use; statfs ratio via `docker info -f '{{.DockerRootDir}}'` and `stat -f -c '%b %f'`, exit early above 0.85, `DISK SKIPPED: cannot measure` otherwise, never `df`), `=== REV-03 JTI ...`, `=== WORKLOAD QUARANTINE ...` (encoded id `jq -rn --arg v 'spiffe://aegis.local/workload/orders' '$v|@uri'`, mTLS curl with `/certs/workload-orders.{crt,key}` + `/certs/root-ca.crt` to `https://127.0.0.1:9443/api/payments`, expect 200 then 403 `PRINCIPAL_QUARANTINED` then 200), body form probe. Add the spiffe id(s) to `cleanup` (l.226-237 pattern: `cp_call DELETE /control/v1/principals/<id>/quarantine >/dev/null || echo "WARN: ..." >&2`) using the encoded path. Audit assertion: `valid_rid`-style guard on the jti before SQL, `wait_q_eq "select count(*) ... where jti='${JTI}' ..."`; remember the 60s suppression window (`SMOKE_SUPPRESS_WINDOW_SECS`), use a `>=1` check and also assert the allowed decision/completion row.

---

### `tests/compose/smoke_script_test.go` (lint test)

**Analog:** itself. Pattern (l.37-57 `TestSmokeScriptCoversAuditEvidence`): `readSmokeScript(t)` then `assert.Contains(t, script, want, ...)` over a `required` slice. Add: `"=== REV-03 JTI"`, `"=== WORKLOAD QUARANTINE"`, `"=== DISK"`, `"TOKEN_REVOKED"`, `"PRINCIPAL_QUARANTINED"`, `"workload-orders"`, `"@uri"`, `"denied after"`, `"stat -f"`, `"DISK SKIPPED"`. Keep `TestSmokeScriptHonestHeader` satisfied: exactly one `^trap cleanup EXIT`, header still contains `REV-03` and `TestAuditWorker_RestartRecovery`, and do not reintroduce the two forbidden phrases. Credential-echo regex (l.~77, `SMOKE_PASSWORD|CSRF|SESSION`): extend to `SMOKE_PASSWORD|SMOKE_DEV_PASSWORD|CSRF|SESSION|access_token`.

### `tests/compose/compose_test.go` (new issuer env test)

**Analog:** `TestSeedService` (l.407-432):
```go
for _, profile := range profiles {
    t.Run(profile, func(t *testing.T) {
        services, _ := loadCompose(t, profile)
        ...
        env := envMap(seed)
        assert.Contains(t, env, "AEGIS_SEED_USER", ...)
```
New `TestDemoIssuerCredentials`: `services["demo-issuer"]`, `envMap`, assert the three `AEGIS_DEMO_PASSWORD_*` keys exist and `PORT=8085` unchanged. Leave `TestPortsMatchBinaries` alone.

### `deployments/compose/docker-compose.{mvp,hardened,distributed}.yml`

**Analog:** mvp l.167-176:
```yaml
  demo-issuer:
    ...
    ports:
      - "8085:8085"
    environment:
      - PORT=8085
```
Add `- AEGIS_DEMO_PASSWORD_DEVELOPER=...`, `_FINANCE`, `_APPLICATION_ADMIN` list entries in each profile (hardened ~l.169, distributed analogous; envMap handles list form). Do not change ports.

### `tests/integration/mvp_test.go:22` and `bypass_test.go:256`

Update `loginToken(t, client, role)` to post `{"username":role,"password":<demo default>}`. These need Docker; run only live, never in the hermetic suite.

## Shared Patterns

### Fail-closed deny + reason classification
**Source:** `cmd/gateway/main.go:346-362, 387-399` and `internal/audit/governor.go:43-70`
**Apply to:** the missing-jti deny. Any new `ac.SetDecision("deny", "<LITERAL>")` needs the literal in `reasonTable` or `TestEveryMainReasonIsClassified` fails.

### Error responses
- Control plane: `writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "message")` (`quarantine_handler.go:23`); Redis failure -> 503 `DEPENDENCY_FAILURE`.
- Gateway: `proxy.WriteProblemDetails(w, status, title, detail, "https://aegis.local/errors/<kind>", reqID)`.
- Issuer: JSON `{"error":"CODE"}` with explicit `Content-Type`.

### Additive audit column across layers
**Source order:** `event.go` field + `Normalize` -> `logger.go` context/builders -> `worker.go` last arg (nullable `any`) -> migration `00000N` -> `audit_repo.go` SELECT/scan -> OpenAPI `AuditEvent` -> `types.ts`. Always append, never insert mid-list. Spool JSON is permissive (`json.Marshal`/`Unmarshal`, no `DisallowUnknownFields`), so `omitempty` gives rolling-upgrade safety.

### Canonical id agreement
Control plane writes `quarantine:principal:<decoded canonical id>`; gateway checks `ExtractSPIFFEID(cert, "aegis.local")` output (`cmd/gateway/main.go:693`). Only `url.PathUnescape` (not `QueryUnescape`), strict allowlist after decode, lowercase host.

### Smoke-script hygiene
Never echo tokens/passwords/CSRF/session; `valid_rid`-guard anything interpolated into SQL; exactly one `trap cleanup EXIT`; cleanup releases every quarantine the script created.

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| (none) | | | Per-replica propagation loop and disk pre-flight have no existing equivalent in `compose-smoke.sh`; use the RESEARCH "Code Examples" bash and Q8 formula, styled with the existing `pass`/`fail` helpers. `ParseSPIFFEID` (string form) is new but sits beside `ExtractSPIFFEID`. |

## Metadata

**Analog search scope:** `cmd/demo-issuer`, `cmd/gateway`, `internal/{audit,control,identity,storage}`, `migrations`, `api/openapi`, `scripts`, `tests/compose`, `deployments/compose`
**Files scanned:** ~25 read (several partial)
**Pattern extraction date:** 2026-10-10
