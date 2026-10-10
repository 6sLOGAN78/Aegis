# Phase 9: Close gaps B9, B10 (jti revocation and SPIFFE quarantine keys) - Research

**Researched:** 2026-10-10
**Domain:** Go services (chi v5 control plane, JWT gateway, audit pipeline), Postgres migrations, OpenAPI codegen, bash compose smoke
**Confidence:** HIGH (everything below was read in the repo or reproduced locally; items tagged otherwise are marked)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **D-01:** The demo issuer takes a username and password, for example `{"username":"developer","password":"..."}`, for the seeded users `developer`, `finance` and `application-admin`. A wrong or missing password returns 401. The old `{"role":...}` body is no longer accepted: it returns 400 and has no compatibility path.
- **D-02:** Seeded passwords come from env vars, one per user (for example `AEGIS_DEMO_PASSWORD_DEVELOPER`), with demo defaults. The issuer logs a warning at startup for each default in use. The compose files set the variables explicitly. Compare passwords in constant time.
- **D-03:** Update every caller in this phase: `scripts/compose-smoke.sh` (around line 141), `tests/integration/*`, the seed script if it calls the issuer, and any docs or runbooks that show the login body.
- **D-04:** Keep the existing per-IP throttling (`checkRateLimit`). Failed logins count like any other attempt. No per-username lockout.
- **D-05:** The issuer sets `RegisteredClaims.ID = uuid.NewString()` (random UUID v4) on every token. `github.com/google/uuid` is already a dependency.
- **D-06:** The issuer's login response includes `jti` next to `access_token`.
- **D-07:** The gateway records the token's jti in the audit event, so the decision, denial and completion rows carry it. This needs a new event field (normalized like the other ids), a new migration `000004` adding a nullable `jti` column to `audit_events`, the worker insert, and the audit read API. The dashboard audit stream should show it if that is a small change; otherwise the API alone is enough.
- **D-08:** Revocation TTL keeps the existing 24h default with the `ttl_seconds` override (`HandleRevokeToken`). No change.
- **D-09:** The user listener denies tokens without a jti, fail closed. Use the same deny path and status as other invalid-token rejections, with a distinct reason code (for example `TOKEN_MISSING_JTI`). The reason code must be classified in the Phase 8 governor reason table, or `TestEveryMainReasonIsClassified` fails.
- **D-10:** The server decodes the path `{id}` with `url.PathUnescape` in both `HandleQuarantinePrincipal` and `HandleUnquarantinePrincipal`. The dashboard keeps using `encodeURIComponent`. The researcher must confirm how chi matches an encoded `%2F` in a path parameter (RawPath routing) and that `/principals/{id}/quarantine` matches for a full SPIFFE ID.
- **D-11:** `POST /control/v1/quarantine` works with `principal_id` in the JSON body. It goes to a handler (or shared core) that runs the same validation and Redis write as the path form.
- **D-12:** After decoding, an id starting with `spiffe://` must parse as a valid SPIFFE ID in trust domain `aegis.local`, or the request returns 400. Reuse the gateway's SPIFFE parsing (`internal/identity`) if it fits. User ids pass through unchanged. The same rule applies to unquarantine and to the body form.
- **D-13:** Add the routes this phase touches to `api/openapi/control-v1.yaml`: `GET /principals/quarantine`, `GET /quarantine`, `POST /quarantine` (body form), `POST /revocations`, and the path-form quarantine routes if they are missing. Regenerate the Go and TS types the repo generates from it (`types.gen.go`, `web/dashboard/src/api/types.ts`). The other missing routes (auth/*, simulate, rollback, audit/events) are out of scope.
- **D-14:** Extend `scripts/compose-smoke.sh` to run the new checks live on **mvp, hardened and distributed**.
- **D-15:** Propagation is measured per replica. After a revoke or quarantine, poll each gateway directly (gateway-1/2/3 on distributed, not through HAProxy) until it denies. The check passes only if every replica denies within 5s, and it prints the elapsed time for each.
- **D-16:** Live flows the smoke must prove: (1) JTI revoke: log in -> 200 with the token -> revoke its jti -> the same token is denied with `TOKEN_REVOKED` -> a freshly issued token still gets 200. (2) Workload quarantine on :9443: quarantine a backend's SPIFFE ID using the dashboard-style encoded path -> its mTLS call is denied (`PRINCIPAL_QUARANTINED`) -> unquarantine -> 200 again. (3) Body-form quarantine: `POST /quarantine {principal_id}` quarantines exactly like the path form. (4) Audit row carries jti: the denial row for the revoked request shows the revoked jti.
- **D-17:** The smoke script runs a disk pre-flight check. If the filesystem holding the Docker volumes is above about 85%, it exits early with a clear message instead of failing on readiness later (the spool's 90% gate measures the whole filesystem). Freeing disk space stays with the operator. The host is at 87.4% right now, so the live runs need space freed first.
- **D-18:** Hermetic tests must cover: issuer credential checks and jti presence; a gateway deny when the jti is missing; handler decoding and SPIFFE validation; the body-form route; the worker and migration persisting jti. Extend the hermetic smoke-script lint in `tests/compose` to cover the new sections.

### Claude's Discretion
- Exact env var names, the reason-code names (as long as they are classified), and whether the path and body handlers share one core function.
- Whether the dashboard audit stream shows the jti column (D-07), depending on effort.
- The exact 401 vs 403 split for revoked tokens and tokens without a jti. Follow the existing gateway conventions for invalid tokens.

### Deferred Ideas (OUT OF SCOPE)
- Per-username login lockout for the demo issuer (D-04 keeps per-IP only).
- Dashboard feature to paste a JWT and extract its jti.
- Revocation TTL derived from the token's remaining lifetime.
- The rest of the OpenAPI drift: auth/*, /policies/simulate, /snapshots/rollback, /audit/events (B5, B6).
- `ReconstructQuarantines` startup wiring and rotation keyset loading (B8).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| REV-03 | Revocation/quarantine denied on every gateway replica within 5s | Issuer jti (D-05/06), SPIFFE decode (D-10/12), per-replica live poll (section "Smoke design") |
| AUTH-04 | Demo issuer requires seeded credentials | Issuer rewrite (section "Issuer"), caller list |
| OPS-01 (quarantine/revocation routes only) | Control API contract documents the routes | OpenAPI additions + codegen (section "OpenAPI") |
</phase_requirements>

## Summary

No new external packages are needed: `github.com/google/uuid v1.6.0`, `golang-jwt/jwt/v5 v5.3.1`, `go-chi/chi/v5 v5.2.1`, `miniredis v2.34.0`, `pgxmock/v4` are already in go.mod [VERIFIED: go.mod]. The work is wiring: issuer rewrite, one gateway check, one audit column through five layers, one handler decode/validate core, OpenAPI additions, and a smoke extension.

The B10 root cause is confirmed. chi routes on `RawPath` when set and `chi.URLParam` returns the still-percent-encoded segment, so the handler stores `spiffe:%2F%2Faegis.local%2F...` while the gateway checks `spiffe://aegis.local/...`. `url.PathUnescape` in the handler fixes it (reproduced locally, see Q1). The body-form route has no `{id}` so it hits the "Principal ID is required" 400 every time.

**Primary recommendation:** Add one shared `decodeAndValidatePrincipalID` + `quarantineCore` in `internal/control/quarantine_handler.go`, a string-form SPIFFE parser in `internal/identity`, `JTI` plumbed as the last (21st) insert arg, `TOKEN_MISSING_JTI` as 401 / `classIdentified`, and a per-replica smoke using `docker compose exec <gw> curl` against `127.0.0.1:8080` and `https://127.0.0.1:9443` with `/certs/workload-orders.*`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Credential check + jti issuance | Demo issuer (API) | — | Only the issuer mints tokens |
| Missing-jti deny, revocation check | Gateway user listener (API) | Redis | Fail-closed enforcement point (ADR-0005) |
| Quarantine/revoke write + id canonicalization | Control plane REST (API) | Redis | One canonical key form written here |
| Workload quarantine enforcement | Gateway :9443 | Redis | SPIFFE id from client cert |
| jti persistence | Audit worker -> Postgres | Gateway spool | Durable audit |
| jti display | Control API read + dashboard | — | Optional UI |
| Propagation proof | compose-smoke.sh | — | Live evidence |

## Standard Stack

No additions. Use existing: chi v5.2.1, golang-jwt v5.3.1, google/uuid v1.6.0, pgx v5 + pgxmock/v4, goose (embedded migrations), oapi-codegen v2.5.0 (installed at `~/.local/bin/oapi-codegen`, version-matched to the header in `types.gen.go`). [VERIFIED: go.mod, `oapi-codegen -version`]

### Package Legitimacy Audit
No new external packages. slopcheck not required. Packages removed: none. Packages flagged: none.

## Findings by Open Question

### Q1. chi routing with `%2F` in `{id}` (D-10)
- chi v5.2.1 `mux.go:451-458`: `routePath` = `r.URL.RawPath` if non-empty, else `r.URL.Path`. [VERIFIED: module source]
- `chi.URLParam` (`context.go:10-15,100-107`) returns the stored value verbatim, i.e. still encoded when routed from RawPath.
- Reproduced with a scratch test using the same `Route("/control/v1")` + `{id}` shape:
  - `/principals/spiffe:%2F%2Faegis.local%2Fns%2Fdefault%2Fsa%2Forders/quarantine` -> 200, param `spiffe:%2F%2Faegis.local%2F...`, `PathUnescape` yields `spiffe://aegis.local/ns/default/sa/orders`.
  - `encodeURIComponent` form `spiffe%3A%2F%2F...` -> 200, same result.
  - Unencoded `spiffe://aegis.local/ns/x/quarantine` -> **404** (slashes split the segment). So the dashboard encoding is mandatory; the body form is the escape hatch for unencoded ids.
- **Double-decode edge:** `/principals/a%2541/quarantine` -> `RawPath==""`, param is already decoded `a%41`; a blind `PathUnescape` yields `aA`. Mitigation: after unescape, reject ids that fail a strict allowlist (see Q4) or contain `%`, control chars, or exceed 128 bytes (audit `principal_id` is VARCHAR(128), `event.go` clips to 128). SPIFFE ids never legitimately contain `%`.
- `internal/control/api.go` has no `StripSlashes`/`CleanPath`/`RedirectSlashes` [VERIFIED: grep]; middleware is RequestID, RealIP, Logger, Recoverer, Timeout, SecurityHeaders. Nothing rewrites paths.
- Proxies: the dashboard is served by the control plane itself (`internal/control/spa.go`) and `apiClient` uses relative `fetch(path)` (`client.ts`), so no proxy sits in the path. HAProxy only fronts gateways (:8080/:9443, `haproxy.cfg`). The Vite dev proxy (`vite.config.ts`, `/control/v1` -> :8084) forwards the raw path [ASSUMED: http-proxy does not re-encode; dev-only, not on the proof path].
- Dashboard call sites to keep: `EmergencyQuarantine.tsx:68` (POST) and `:113` (DELETE), both `encodeURIComponent`.

### Q2. Gateway user-listener JWT path (D-09)
- `cmd/gateway/main.go:346` `tokenValidator.ValidateBearerToken`; failure -> `SetDecision("deny","UNAUTHORIZED")`, `SetErrorCode("UNAUTHORIZED")`, **401** problem+json (:346-363). On success `SetPrincipal(claims.Subject,"user",claims.Roles)` (:365), then `revStore.CheckRevocation(ctx, claims.Subject, claims.ID)` (:371). Redis error -> 503 `DEPENDENCY_OUTAGE_REDIS`; revoked -> **403** with `reason` as `detail` (`TOKEN_REVOKED` / `PRINCIPAL_QUARANTINED`).
- `internal/identity/jwt.go`: `UserClaims` embeds `RegisteredClaims`; the validator does not require `jti`. Do **not** enforce jti inside `ValidateBearerToken`: many tests mint jti-less tokens (`tests/security/jwt_negative_test.go`, `tests/rotation/rotation_test.go`, `internal/identity/jwt_test.go`) and would break.
- `CheckRevocation` skips the jti lookup when `jti==""` (`store.go` ~line 80), which is why a jti-less token silently bypasses revocation. The gateway check must precede it.
- **Recommended:** immediately after `SetPrincipal` and before `CheckRevocation`:
  `ac.SetDecision("deny","TOKEN_MISSING_JTI"); ac.SetErrorCode("TOKEN_MISSING_JTI")`, **401** "Unauthorized" with the unauthorized problem type (matches "same path/status as invalid tokens"). Put it before the `revStore != nil` guard so it applies even without Redis.
- Governor: `internal/audit/governor.go:43-70` `reasonTable`. Add `"TOKEN_MISSING_JTI": classIdentified` (principal is known because the signature validated; suppressor keyed principal+route+reason). `TestEveryMainReasonIsClassified` (`internal/audit/main_reasons_test.go`) AST-scans `main.go` for `SetDecision("deny", <literal>)` and fails on unclassified literals; also requires >=20 literals (currently satisfied). `ReasonLabel`/`ClosedReasonLabels` derive from the table, so metrics labels follow automatically; no test hard-codes the table length [VERIFIED: grep].
- **Hermetic testability problem:** the handler is an inline closure in `main()`; `cmd/gateway/*_test.go` are AST wiring tests. Extract a tiny pure helper in package main (e.g. `func missingJTI(c *identity.UserClaims) bool`) and test it directly, plus an AST test asserting `main.go` contains `SetDecision("deny","TOKEN_MISSING_JTI")` located before the `CheckRevocation` call (pattern: `wiring_test.go`). The classification drift guard then covers the table entry for free.

### Q3. Audit jti plumbing (D-07)
Touch points (all verified):
1. `internal/audit/event.go:34-62` `CompletionEvent`: add `JTI string \`json:"jti,omitempty"\``; in `Normalize()` add `e.JTI = clipString(e.JTI, 128)`.
2. `internal/audit/logger.go:20-38` `AuditContext`: add `JTI` field + `SetJTI(jti string)` (mutex pattern like `SetPrincipal`). Set it next to `SetPrincipal` at `main.go:365`. Both builders must copy it: `ToCompletionEvent` (`logger.go:187-205`) and the completion builder (`logger.go:394-413`).
3. Fallback literals when `ac==nil`: `main.go:538` (user; add `JTI: claims.ID`); `main.go:848` is the workload path (no jti).
4. Governor summary rows copy `*first` wholesale (`governor.go:376`), so a summary carries the first denial's jti automatically.
5. Worker `internal/audit/worker.go:78-88` column list has 20 columns; **append `jti` as the 21st** (`$21`) so existing index-based overrides stay valid (`pipeline_e2e_delivery_test.go:75` overrides index 1 and 19). Pass `nil` when empty (same pattern as `suppressed`) so rows are NULL.
6. Frame compatibility: spool uses plain `json.Marshal` / `json.Unmarshal` (`spool.go:322,365,619`), no `DisallowUnknownFields` [VERIFIED]. Old frames decode with empty jti (NULL); an old worker reading new frames ignores the field. Safe rolling upgrade. Use `omitempty`.
7. Migration: `migrations/000004_add_audit_jti.sql`, goose format (tests require `-- +goose Up` and `-- +goose Down`, `db_test.go:55-77`):
   ```sql
   -- +goose Up
   ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS jti VARCHAR(128);
   CREATE INDEX IF NOT EXISTS idx_audit_events_jti ON audit_events (jti) WHERE jti IS NOT NULL;
   -- +goose Down
   DROP INDEX IF EXISTS idx_audit_events_jti;
   ALTER TABLE audit_events DROP COLUMN IF EXISTS jti;
   ```
   (Index optional/discretionary. 000003 uses the same ADD COLUMN on the partitioned parent.) Embedded via `migrations/migrations.go` `//go:embed *.sql`; applied by the control plane at startup (`cmd/control-plane/main.go:167`, via `storage.RunMigrationsWithPool`). Audit workers `depends_on: control-plane service_healthy` in compose, so the column exists before they insert. Caveat: a migration failure is only logged as a warning (`main.go:168`), after which the worker's batch would fail on the missing column; the smoke already sees this for 000003.
8. Read API: `internal/storage/audit_repo.go` two SELECTs (list at ~line 87, `getAuditEventQuery`) and two scan blocks; add `jti` and scan into `*string`. OpenAPI `AuditEvent` (`control-v1.yaml:850-904`) add optional `jti: string`; regenerated Go field is `Jti *string`. Handler (`audit_handler.go`) needs no change. `suppressed_count` is not exposed by the read API today, so adding `jti` is the only column change.
9. Dashboard: `web/dashboard/src/api/types.ts` `AuditEvent` add `jti?: string`; `AuditStream.tsx` shows `reason_code` at :326 and in the drawer at :419, so a drawer row is a ~3 line change. **Caveat:** `web/dashboard/dist/` is tracked and embedded (`web/embed.go`); any TS change needs `npm run build` and a dist commit. Recommend API-only unless the build is cheap.

Tests that WILL break and must be updated:
- `internal/audit/worker_test.go:20-26` `anyAuditArgs()` hard-codes 20 -> 21; `auditArgsWith` (:322) follows.
- `internal/audit/spool_group_test.go:361`, `pipeline_e2e_delivery_test.go:75` use these helpers (verify after change).
- `internal/storage/audit_repo_test.go` mock row columns (16) must gain `jti`; scan count changes.
- `internal/storage/db_test.go:63-65` add `000004_...` to the filename assertions.

### Q4. SPIFFE parsing reuse (D-12)
`internal/identity/spiffe.go` has only `ExtractSPIFFEID(*x509.Certificate, trustDomain)`. It parses `cert.URIs` and checks scheme, host (EqualFold), non-empty path. There is **no string-form parser**. Add `ParseSPIFFEID(id, trustDomain string) (string, error)` there (reusing `ErrInvalidTrustDomain`, `ErrMalformedSPIFFEURI`) and refactor `ExtractSPIFFEID` to call a shared check. Rules: `url.Parse`; scheme exactly `spiffe`; host == trust domain; non-empty, non-`/` path; no userinfo, port, query, fragment; no `//` or `.`/`..` segments; path chars limited to `[A-Za-z0-9._-/]`; total length <= 128. Return the canonical string. Note gateway comparison uses `uri.String()` of the cert URI and `EqualFold` on host, so lower-case the host when canonicalizing to guarantee byte equality with `ExtractSPIFFEID` output. Existing certs: `spiffe://aegis.local/workload/orders`, `spiffe://aegis.local/ns/gateway/sa/aegis-gateway`. Unit tests: table in `spiffe_test.go` style.

### Control handler design (D-10, D-11, D-12)
- `HandleQuarantinePrincipal`: `raw := chi.URLParam(r,"id")`; if `raw==""` -> take `principal_id` from body (body form); else `PathUnescape(raw)`, error -> 400. Shared `canonicalPrincipalID(id)`: reject empty, >128 bytes, control chars, `%`; if `strings.HasPrefix(id,"spiffe://")` -> `identity.ParseSPIFFEID(id,"aegis.local")` else 400; else pass through.
- Body-form needs a new field: `controlv1.QuarantineRequest` has only `reason`, `expires_at`, `actor` (`types.gen.go:232`) and `reason` is `required`. Add a separate schema `QuarantinePrincipalRequest` (`allOf` QuarantineRequest + `principal_id`) or add optional `principal_id` to `QuarantineRequest`; the simpler option is the optional field. If both path id and body `principal_id` are present and differ -> 400.
- `HandleUnquarantinePrincipal`: same decode + validation. Existing handler tests at `internal/control/api_test.go:200-235` use plain ids and a real miniredis `h.rdb`; add cases for encoded SPIFFE, invalid trust domain (400), malformed `%zz` (400), body form, and assert the Redis key `quarantine:principal:spiffe://aegis.local/workload/orders`.
- `ListQuarantined` returns keys with the prefix trimmed, so it already shows decoded ids once written correctly.
- Runbook `docs/runbooks/quarantine-and-revocation.md` section 3 shows a `Bearer` management token and `ttl_seconds` for `POST /control/v1/quarantine`, which does not match the real API (session cookie + CSRF, `expires_at`). Fix the doc as part of D-03/D-13 while touching it; also document the encoded path form.

### Q5. OpenAPI codegen (D-13)
- Spec: `api/openapi/control-v1.yaml` (paths at lines 15-432; only `/control/v1/principals/{id}/quarantine` post/delete exist for this area). Config `api/openapi/oapi-codegen.yaml` (types only). Generation: `scripts/generate.sh` runs `oapi-codegen -generate types -package controlv1 api/openapi/control-v1.yaml > pkg/api/control/v1/types.gen.go`, then `go mod tidy` and `go vet ./pkg/api/...`. There is **no Makefile target**. oapi-codegen v2.5.0 is installed and regenerating today produces a byte-identical file (`diff` empty) [VERIFIED], so new output will contain only intended changes.
- `web/dashboard/src/api/types.ts` is **hand-maintained** (no generator, no openapi-typescript in package.json); update it by hand, mirroring `QuarantineRecord`/`QuarantineRequest`/`UnquarantineResponse` (lines ~134-160) and add `RevokeTokenRequest`, `RevokeTokenResponse`, `QuarantineListResponse`.
- Add paths: `GET /control/v1/principals/quarantine` and `GET /control/v1/quarantine` (response `{quarantines: [QuarantineRecord]}`), `POST /control/v1/quarantine` (body `principal_id`), `POST /control/v1/revocations` (`{jti, reason?, ttl_seconds?}` -> `{status, jti, revoked_at}`; matches `HandleRevokeToken`). Note `RevokeTokenRequest` is currently a hand-written struct in `quarantine_handler.go:13`; either keep it or switch to the generated type (avoid a duplicate name collision with the generated `RevokeTokenRequest`; if generated, delete the local struct).
- Also document the encoded-id requirement in the path-parameter description.

### Q6. Demo issuer and callers (AUTH-04)
- `cmd/demo-issuer/main.go`: `seedDatabase` map (:25-29), `HandleLogin` (:80-140), `checkRateLimit` (20/min/IP, :56-76; called before body decode, so every attempt counts, which satisfies D-04), env only `PORT`. Claims lack `ID`.
- Rewrite: decode `{username,password}` with `DisallowUnknownFields`; empty `username` (which includes the old `{"role":..}` body) -> 400 `INVALID_REQUEST`; unknown user or wrong/missing password -> 401 `INVALID_CREDENTIALS`; success: `ID: uuid.NewString()`, response adds `"jti"`. Constant-time: compare `sha256` digests with `crypto/subtle.ConstantTimeCompare`, and for unknown users compare against a dummy digest so timing does not reveal user existence. Passwords: `AEGIS_DEMO_PASSWORD_DEVELOPER`, `_FINANCE`, `_APPLICATION_ADMIN` with demo defaults and a `log.Printf` warning per default in use (never print the value).
- Callers of the login body (grep `8085|demo-issuer|"role"`, excluding .planning): `scripts/compose-smoke.sh:141` (`dev_token`), `tests/integration/mvp_test.go:22` (`loginToken(t, client, role)`), `tests/integration/bypass_test.go:256`, `cmd/demo-issuer/main_test.go` (several subtests incl. rate-limit and unknown-role -> `UNKNOWN_ROLE`). `deployments/compose/seed/seed.sh` does **not** call the issuer. Docs: no runbook shows the login body; `.planning/phases/01-.../01-UAT.md` (untracked) does, and `GEMINI.md`/`spec.md` only mention port 8085. No k6/benchmark caller. `tests/chaos`, `tests/failure`, `tests/rotation`, `tests/security` mint their own JWTs and are unaffected.
- Compose: all three profiles have `demo-issuer` with only `PORT=8085` (mvp :174-175, hardened :169-170, distributed analogous). Add the three `AEGIS_DEMO_PASSWORD_*` vars to each; extend `tests/compose/compose_test.go` (pattern: `TestSeedService`, `envMap`) with a test that every profile sets them. `TestPortsMatchBinaries` expects `8085:8085` published on mvp/hardened; do not change ports. The issuer image has no curl, so the smoke calls it from a gateway container (existing `dev_token` pattern).
- Password values must also reach the smoke script: use `SMOKE_DEV_PASSWORD` defaulting to the compose demo value, and add it to the credential-echo lint regex (`smoke_script_test.go` currently matches `SMOKE_PASSWORD|CSRF|SESSION` only).

### Q7. compose-smoke.sh and live reachability
- Existing helpers: `dc` (docker compose -f), `op_login`/`cp_call` (exec in `control-plane`, explicit `Cookie`/`X-CSRF-Token`, status code only), `dev_token` (exec in `${GW}`), `gw_code`/`gw_probe` (host curl to `127.0.0.1:8080`, which on distributed is HAProxy), `valid_rid`, `psql_q`, `wait_q_eq`, service discovery via `dc config --services | grep '^audit-worker'`. `GW` is `gateway` (mvp/hardened) or `gateway-1` (distributed). Cleanup trap releases `usr_developer_01` and `smoke-b4-probe` (exactly one `trap cleanup EXIT`, enforced by lint).
- **Per-replica addressing:** `mapfile -t GATEWAYS < <(dc config --services | grep '^gateway')` gives `gateway` or `gateway-1..3`. Drive each with `dc exec -T "$g" curl ... http://127.0.0.1:8080/api/orders`. The gateway image installs curl (`Dockerfile`, gateway target) and mounts `../certs:/certs:ro` on every gateway in all profiles [VERIFIED].
- **Workload mTLS from inside a gateway container:** certs tracked in `deployments/certs/`: `workload-orders.crt/.key` has SAN `URI:spiffe://aegis.local/workload/orders` (clientAuth); `root-ca.crt`; `gateway-server.crt` SANs include `localhost` and `127.0.0.1`, so verification works against 127.0.0.1. Command:
  `dc exec -T "$g" curl -s -w '\n%{http_code}' --cacert /certs/root-ca.crt --cert /certs/workload-orders.crt --key /certs/workload-orders.key -X POST -H 'Content-Type: application/json' -d '{"amount":100.0,"currency":"USD"}' https://127.0.0.1:9443/api/payments`
  Baseline is 200 containing `processed` (`bypass_test.go:226-237`); this identity is the one rego allows (`authz.rego:107-122`). The quarantine check runs before route/policy, so a quarantined id returns 403 with detail `PRINCIPAL_QUARANTINED` (problem+json `detail`, `internal/proxy/errors.go`).
- Encoded path like the dashboard: `ENC="$(jq -rn --arg v 'spiffe://aegis.local/workload/orders' '$v|@uri')"` -> `spiffe%3A%2F%2Faegis.local%2Fworkload%2Forders`; `cp_call POST "/control/v1/principals/${ENC}/quarantine" '{"reason":"compose-smoke-b10"}'`; DELETE likewise. Body form: `cp_call POST /control/v1/quarantine '{"principal_id":"spiffe://aegis.local/workload/orders","reason":"compose-smoke-b10-body"}'` (use `jq -n --arg` to build bodies). The control plane is reachable only via `exec control-plane` on mvp (not published); same helper works everywhere. No `Idempotency-Key` is required.
- Revocation: login via issuer (`dc exec -T "$GW" curl ... http://demo-issuer:8085/login`) and read both `.access_token` and `.jti` with jq (no JWT parsing in bash; never print the token). Revoke: `cp_call POST /control/v1/revocations` body `{"jti":"...","reason":"compose-smoke","ttl_seconds":300}`. Validate the jti with `valid_rid` before any SQL interpolation. Denied request: 403 with `TOKEN_REVOKED` in the body.
- **Propagation timing (D-15):** record `t0=$(date +%s.%N)` right after the control call returns 200; for each gateway loop `curl` every ~0.2s until the deny is seen or `elapsed>5`; print `REV-03 jti revoke: gateway-N denied after X.Xs` and FAIL if any replica exceeds 5s. `docker compose exec` adds ~0.2-0.5s overhead per poll, so state in the output that the figure includes exec latency (a conservative upper bound). Redis is the shared store and gateways check per request (no cache), so expect sub-second.
- **Audit suppression pitfall for D-16(4):** denials are governed by the Phase 8 suppressor keyed (principal, route, reason) with a 60s window (`SMOKE_SUPPRESS_WINDOW_SECS`). Repeated polling means only the first `TOKEN_REVOKED` denial per gateway is recorded, and a re-run within the window records none for the new jti. Assert with `wait_q_eq "select count(*) from audit_events where jti='${JTI}' and event_type='denial' and reason_code='TOKEN_REVOKED'" ...` expecting >=1 (use a `>=1` helper, not `=1`, since several replicas each record one), and reuse the existing WINDOW GUARD approach (wait until the latest `TOKEN_REVOKED` denial is older than the window) before the revoke. Alternatively assert on the jti-carrying `decision`/`completion` row of the earlier 200 request, which is not suppressed (allowed traffic is never governed), as a second proof; recommended to assert both.
- Section ordering: run the new sections before or after A4, ensuring `usr_developer_01` is not left quarantined (quarantine check precedes the jti check, so it would mask `TOKEN_REVOKED`). Add the new SPIFFE id and the body-form probe id to `cleanup` (DELETE with encoded path), and use a short `ttl_seconds`/`expires_at` as backstop.
- A4 (section 5) currently prints "do not fix in this phase" wording; update it to reflect B9/B10 closure. The header comment says "REV-03 stays open"; update honestly after the live runs, keeping the string `REV-03` (lint) and not introducing the forbidden strings.
- **Hermetic lint** (`tests/compose/smoke_script_test.go`): `TestSmokeScriptSyntax` (`bash -n`), `TestSmokeScriptCoversAuditEvidence` (required substring list), `TestSmokeScriptHonestHeader` (exactly one `^trap cleanup EXIT`; forbids two stale phrases), `TestSmokeScriptNoCredentialEcho` (no echo/printf line mentioning `SMOKE_PASSWORD|CSRF|SESSION`). Add a new test/strings for: `=== REV-03 JTI`, `=== WORKLOAD QUARANTINE`, `=== DISK`, `TOKEN_REVOKED`, `PRINCIPAL_QUARANTINED`, `workload-orders`, `%2F` or `@uri`, `denied after`, `docker info`/`stat -f`; and extend the credential regex with the new password var and `access_token`.

### Q8. Disk pre-flight (D-17)
- Spool ratio: `internal/audit/spool.go:175-189` `statfsRatio` = `(Blocks - Bfree) / Blocks` from `syscall.Statfs(SpoolDir)`; saturation at `>= 0.90` (:204), hard limit 0.95. This is the **whole filesystem** of the spool volume (a Docker named volume under the daemon root).
- Smoke measurement on the same basis: `root="$(docker info -f '{{.DockerRootDir}}')"` (here `/var/lib/docker`), then `read -r b f < <(stat -f -c '%b %f' "$root")` and `ratio=$(awk -v b=$b -v f=$f 'BEGIN{printf "%.4f",(b-f)/b}')`. Locally this gives 0.8782 while `df` Use% shows 93% (df uses used/(used+avail) so it reads higher); the gate must use the statfs formula, not `df`. Fail early with a clear message when `ratio > 0.85` (print the ratio, never free space itself). If the daemon is remote or `stat` cannot read the root (permission, Docker Desktop VM), print `DISK SKIPPED: cannot measure` and continue rather than failing. Run it right after tool checks and before any compose interaction. Phase 8's VALIDATION notes the same 85% threshold plus `>=10 GB` available; optionally also check `%a*%S`.
- Current state at research time: ratio 0.878 (> 0.85), so live runs require the operator to free space first.

### Q9. Existing tests that assert the old behavior
Will break (must be updated in this phase):
- `cmd/demo-issuer/main_test.go` (all `{"role":..}` bodies, `UNKNOWN_ROLE` expectation, rate-limit subtest body).
- `tests/integration/mvp_test.go:22` `loginToken`, `tests/integration/bypass_test.go:256` (Docker-dependent; run only with a live stack, never in the hermetic suite).
- Worker/repo/migration tests listed in Q3.
Not broken: gateway-handler tests (handler is inline in `main()`; wiring tests are AST-based), jti-less JWT tests in `tests/security`, `tests/rotation`, `internal/identity` (validator unchanged), `tests/chaos`/`tests/failure` (own handlers; most already set `ID`). `TestEveryMainReasonIsClassified` stays green once `TOKEN_MISSING_JTI` is in the table. `compose_test.go` publishes/exposes checks are unaffected by env additions.

## Architecture Patterns

### Data flow
```
issuer /login (user+pw, constant-time) -> JWT{jti=uuid}
  -> gateway :8080: validate sig -> SetPrincipal+SetJTI -> no jti? 401 TOKEN_MISSING_JTI
       -> Redis CheckRevocation(sub, jti) -> 403 TOKEN_REVOKED / PRINCIPAL_QUARANTINED
       -> audit event{jti} -> spool(WAL, json) -> worker -> audit_events.jti
operator -> control plane (session+CSRF)
  POST /revocations{jti}          -> Redis revocation:jti:<jti>
  POST /principals/{enc id}/quarantine | POST /quarantine{principal_id}
       -> PathUnescape -> validate (spiffe://aegis.local/...) -> Redis quarantine:principal:<canonical id>
workload mTLS :9443 -> ExtractSPIFFEID(cert) == canonical key -> 403 PRINCIPAL_QUARANTINED
```

### Anti-patterns
- Enforcing jti inside `ValidateBearerToken` (breaks many tests; also the workload assertion path).
- Decoding with `url.QueryUnescape` (turns `+` into space); use `PathUnescape`.
- Interpolating unvalidated values into SQL in the smoke script (reuse `valid_rid`).
- Measuring the disk gate with `df` Use%.
- Inserting the jti column mid-list in the worker insert (shifts override indices in tests).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Password compare | `==` / bytes.Equal | `crypto/subtle.ConstantTimeCompare` on SHA-256 digests | timing + length leak |
| UUID | custom random | `uuid.NewString()` | already in go.mod |
| Percent-decoding | manual `%XX` loop | `url.PathUnescape` | correct escapes |
| SPIFFE parse | regex-only | `net/url` + strict checks in `internal/identity` | must match cert-side canonical form |
| Migrations | ad-hoc DDL at startup | goose file in `migrations/` | embedded, tested |
| Go types from spec | hand-written structs | `oapi-codegen` (v2.5.0) | keeps spec and code in sync |

## Common Pitfalls

1. **Double-decoding** when `RawPath` is empty (`a%2541`): enforce the id allowlist after unescape (Q1).
2. **Unencoded SPIFFE path 404s**, not 400: smoke and docs must show the encoded form or the body form.
3. **Suppressed denial rows** hide the jti in re-runs (Q7); guard on the window.
4. **Quarantine masks revocation** (`CheckRevocation` reports quarantine first): make sure `usr_developer_01` is released before the jti flow.
5. **Worker arg count** is asserted by pgxmock (20 -> 21); forgetting a call site fails tests at runtime.
6. **dist/ is tracked**: dashboard TS edits need a rebuild or the embedded SPA is stale.
7. **Trust domain case**: gateway uses `EqualFold` on host; canonicalize to lowercase so Redis keys are byte-identical.
8. **Control-plane migration failure is non-fatal**; check logs when jti rows are missing.
9. **Disk gate** at 0.878 now; live proof is blocked until the operator frees space.
10. **Issuer defaults warn but start**: ensure compose sets the vars explicitly so the warning does not fire in the proof runs.

## Code Examples

```go
// internal/identity/spiffe.go (new), shape only
func ParseSPIFFEID(id, trustDomain string) (string, error) {
    u, err := url.Parse(id)
    if err != nil || u.Scheme != "spiffe" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
        return "", ErrMalformedSPIFFEURI
    }
    if !strings.EqualFold(u.Host, trustDomain) { return "", ErrInvalidTrustDomain }
    if u.Path == "" || u.Path == "/" { return "", ErrMalformedSPIFFEURI }
    u.Host = strings.ToLower(u.Host)
    return u.String(), nil
}
```

```go
// issuer constant-time compare, shape only
func passwordOK(want, got string) bool {
    a, b := sha256.Sum256([]byte(want)), sha256.Sum256([]byte(got))
    return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
```

```bash
# per-replica propagation, shape only
t0="$(date +%s.%N)"
for g in "${GATEWAYS[@]}"; do
  while :; do
    out="$(dc exec -T "$g" curl -s -w '\n%{http_code}' -H "Authorization: Bearer ${tok}" http://127.0.0.1:8080/api/orders || true)"
    if [ "${out##*$'\n'}" = "403" ] && [[ "$out" == *TOKEN_REVOKED* ]]; then break; fi
    el="$(awk -v a="$t0" -v b="$(date +%s.%N)" 'BEGIN{printf "%.1f", b-a}')"
    awk -v e="$el" 'BEGIN{exit !(e>5)}' && { fail "REV-03 jti revoke ${g}" "not denied within 5s"; break; }
    sleep 0.2
  done
  echo "REV-03 jti revoke: ${g} denied after ${el:-0}s"
done
```

## State of the Art

| Old | Current | Impact |
|-----|---------|--------|
| Issuer: role-only body, no jti | username/password, jti per token | Revocation can match |
| Raw chi param as Redis key | PathUnescape + SPIFFE validation | Dashboard key == gateway key |
| `POST /quarantine` always 400 | body `principal_id` | Matches runbook |

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Vite dev proxy forwards `%2F` unchanged | Q1 | Dev-only; production dashboard has no proxy |
| A2 | `docker compose exec` overhead is 0.2-0.5s per call | Q7 | Only affects how conservative the printed time is |
| A3 | Alpine busybox/GNU `stat -f` semantics on the host: host uses GNU `stat` (verified here), macOS hosts differ | Q8 | Disk check skips with a message on non-GNU stat |
| A4 | `TOKEN_MISSING_JTI` as `classIdentified` / HTTP 401 is the right fit | Q2 | Discretionary per CONTEXT; low risk |

## Open Questions (RESOLVED)

1. **Body-form schema:** optional `principal_id` on `QuarantineRequest` vs a new schema. Recommend optional field on the existing schema (smallest change, one regenerated struct). RESOLVED: optional `principal_id` on `QuarantineRequest` (plan 09-03).
2. **Dashboard jti column:** recommend API + `types.ts` only, skip `AuditStream.tsx` and the dist rebuild unless the build is cheap. RESOLVED: dashboard jti column skipped; `types.ts` hand-edit only, no dist rebuild (plan 09-03).
3. **Runbook fix scope:** section 3 auth/field drift is beyond the strict D-03 text; recommend fixing it since the phase makes `POST /quarantine` real. RESOLVED: runbook section 3 fixed in plan 09-06. Missing-jti reason: RESOLVED as `TOKEN_MISSING_JTI`, 401, `classIdentified` (plan 09-05).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | all tests | yes | go.mod says 1.26.0 | — |
| oapi-codegen | D-13 | yes | v2.5.0 (matches header) | — |
| docker + compose | live smoke | yes (docker info works) | — | hermetic only |
| jq, curl on host | smoke | required by script's own tool check | — | — |
| curl in gateway image | per-replica probes | yes (Dockerfile gateway target) | alpine 3.21 | — |
| Host disk headroom | live smoke | **no: statfs ratio 0.878 > 0.85** | — | operator frees space |

**Blocking:** disk headroom for live runs (operator action, D-17).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` + testify, pgxmock v4, miniredis v2.34.0; `-race` supported |
| Config file | none (go.mod; Makefile `test-quick`, `compose-test`, `compose-smoke`) |
| Quick run | `go test -race -count=1 ./internal/audit/ ./internal/identity/ ./internal/control/ ./internal/storage/ ./cmd/demo-issuer/ ./cmd/gateway/ ./tests/compose/` |
| Full suite | `go test -race -count=1 $(go list ./... \| grep -v /tests/integration)` (never `./tests/integration`, it starts Docker) |
| Live | `bash scripts/compose-smoke.sh {mvp\|hardened\|distributed}` |

### Phase Requirements -> Test Map
| Req / Decision | Behavior | Type | Command | File exists? |
|----------------|----------|------|---------|--------------|
| AUTH-04 / D-01,D-02,D-04 | creds required; 401 wrong/missing; 400 old `role` body; 429 still after 20 | unit | `go test -count=1 ./cmd/demo-issuer/` | update existing `main_test.go` |
| REV-03 / D-05,D-06 | token carries `jti` (UUID); response has `jti`; unique per login | unit | `go test -count=1 ./cmd/demo-issuer/ -run Jti` | Wave 0 |
| D-09 | missing jti -> deny `TOKEN_MISSING_JTI` 401; classified | unit + AST | `go test -count=1 ./cmd/gateway/ ./internal/audit/ -run 'MissingJTI\|TestEveryMainReasonIsClassified'` | Wave 0 (helper + wiring test) |
| D-07 | `CompletionEvent.JTI` normalized, JSON round-trips, old frame without field decodes | unit | `go test -count=1 ./internal/audit/ -run 'Normalize\|JTI'` | extend `event_test.go`, `spool_test.go` |
| D-07 | worker passes 21 args, jti last, NULL when empty | unit (pgxmock) | `go test -count=1 ./internal/audit/ -run Worker` | update `worker_test.go` |
| D-07 | migration 000004 embedded, has Up/Down | unit | `go test -count=1 ./internal/storage/ -run Migrations` | update `db_test.go` |
| D-07 | audit read API returns `jti` | unit (pgxmock) | `go test -count=1 ./internal/storage/ -run AuditRepo` | update `audit_repo_test.go` |
| D-10,D-12 | encoded SPIFFE id decoded, validated, stored under canonical key; bad trust domain / `%zz` / `a%2541` -> 400 | unit (miniredis) | `go test -count=1 ./internal/control/ -run Quarantine` | extend `api_test.go` |
| D-12 | `ParseSPIFFEID` table | unit | `go test -count=1 ./internal/identity/ -run SPIFFE` | extend `spiffe_test.go` |
| D-11 | `POST /quarantine {principal_id}` -> 200, same Redis key; path/body mismatch 400 | unit | same as above | Wave 0 |
| D-13 | spec regenerates to committed `types.gen.go` | check | `oapi-codegen -generate types -package controlv1 api/openapi/control-v1.yaml \| diff - pkg/api/control/v1/types.gen.go` | manual/script |
| D-03 | compose sets issuer password vars in all profiles | unit | `go test -count=1 ./tests/compose/ -run Issuer` | Wave 0 |
| D-14,D-17,D-18 | smoke script lint: syntax, new sections, disk check, no credential echo | unit | `go test -count=1 ./tests/compose/` | extend `smoke_script_test.go` |
| D-15,D-16 | per-replica deny <5s (jti, workload :9443 encoded, body form), audit row has jti | live | `bash scripts/compose-smoke.sh mvp` / `hardened` / `distributed` | extend script |

### Sampling Rate
- Per task commit: quick run for touched packages (<30s).
- Per wave merge: full suite above plus `go test -count=1 ./tests/compose/`.
- Phase gate: full suite green, then live smoke on all three profiles (after the operator frees disk below the 0.85 statfs ratio), then `/gsd:verify-work`.

### Wave 0 Gaps
- [ ] `missingJTI` helper + gateway wiring test (D-09)
- [ ] `ParseSPIFFEID` + tests
- [ ] control handler tests for encoded/body forms
- [ ] compose test for issuer env vars; smoke lint additions
- [ ] gofmt gate scoped to changed files (repo has pre-existing unformatted files, per Phase 8 VALIDATION)

## Security Domain

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | yes | issuer username/password, constant-time compare, per-IP throttle |
| V3 Session Management | partial | existing operator session + CSRF (unchanged) |
| V4 Access Control | yes | `RequireRole("sec-ops")` on write routes (unchanged); gateway default-deny on missing jti |
| V5 Input Validation | yes | strict principal id allowlist, SPIFFE parse, `DisallowUnknownFields` on issuer body, body size bounds |
| V6 Cryptography | yes | `crypto/subtle`, SHA-256 digest compare, Ed25519 signing unchanged |
| V7 Logging | yes | never log passwords/tokens; warn only that a default is in use |

### Threat Patterns
| Pattern | STRIDE | Mitigation |
|---------|--------|------------|
| Role-name token minting without credential | Elevation | username/password required (D-01) |
| Password timing / user enumeration | Info disclosure | digest constant-time compare, dummy compare for unknown user, same 401 body |
| Revocation bypass via jti-less token | Elevation | fail-closed `TOKEN_MISSING_JTI` before revocation check |
| Quarantine key mismatch (encoded vs decoded) | Tampering/DoS of control | single canonicalization in control plane |
| Id injection into Redis key / audit row | Tampering | allowlist, length cap 128, reject `%`/control chars |
| SQL injection in smoke helpers | Tampering | `valid_rid` before interpolation |
| Credential leakage in smoke output | Info disclosure | lint regex extended; never echo tokens/passwords |
| Hard-coded demo passwords | Info disclosure | env override, startup warning, compose sets explicitly; demo-only, documented |

## Sources

### Primary (HIGH confidence, read or reproduced in this session)
- chi v5.2.1 module source (`mux.go`, `context.go`) and a local reproduction test
- Repo files: `cmd/demo-issuer/main.go`, `cmd/gateway/main.go`, `internal/{control,identity,audit,revocation,storage}/*`, `migrations/*`, `api/openapi/*`, `scripts/{compose-smoke.sh,generate.sh}`, `tests/compose/*`, `deployments/compose/*`, `deployments/certs/*` (openssl SAN inspection), `web/dashboard/src/*`
- `oapi-codegen -version` (v2.5.0) and a diff-clean regeneration
- `stat -f` / `df` on this host for the disk ratio

### Secondary / Tertiary
- None; Vite proxy behavior is ASSUMED (A1).

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH, no new packages
- Architecture/touch points: HIGH, every file:line read
- Pitfalls: HIGH for chi/audit/suppression (reproduced or read), MEDIUM for exec-latency and dev-proxy points

**Research date:** 2026-10-10
**Valid until:** 2026-11-10 (stable internal code; re-check if Phase 8 governor changes)
