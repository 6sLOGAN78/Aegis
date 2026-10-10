# Phase 9: Close gaps B9, B10: jti revocation and SPIFFE quarantine keys (REV-03, AUTH-04, OPS-01) - Context

**Gathered:** 2026-10-10
**Status:** Ready for planning

<domain>
## Phase Boundary

Close v1.0 audit blockers B9 and B10 (see `.planning/v1.0-MILESTONE-AUDIT.md`, re-audit of 2026-10-10):

- **B9:** the demo issuer emits no `jti`, so JTI revocation can never match (`cmd/demo-issuer/main.go:117-123`; `internal/revocation/store.go:80` skips an empty jti). The issuer also hands out a token for a bare role name with no credential (AUTH-04).
- **B10:** workload quarantine keys never match. The dashboard sends `encodeURIComponent(spiffeID)` (`web/dashboard/src/components/EmergencyQuarantine.tsx:67`), but `internal/control/quarantine_handler.go:21` uses the raw `chi.URLParam` while the gateway checks the decoded SPIFFE ID (`cmd/gateway/main.go:693`). Separately, `POST /control/v1/quarantine` (`internal/control/api.go:137`) has no `{id}`, so it always returns 400.

The phase is done when, on all three compose profiles live:
- a revoked token's jti is denied on every gateway replica within 5 seconds;
- a quarantined workload is denied on :9443 on every replica within 5 seconds;
- the issuer requires a seeded username and password.

Target requirements: **REV-03** (from partial to satisfied), **AUTH-04** (from partial to satisfied), and **OPS-01** for the quarantine and revocation part only (OPS-01 stays partial for the rest of the spec drift).

Not in scope: B5, B6, B8 (including `ReconstructQuarantines` wiring and rotation keysets), B2, the other OpenAPI drift, and dashboard UI work beyond what the encoded-path fix needs.

</domain>

<decisions>
## Implementation Decisions

### Issuer credentials (AUTH-04)
- **D-01:** The demo issuer takes a username and password, for example `{"username":"developer","password":"..."}`, for the seeded users `developer`, `finance` and `application-admin`. A wrong or missing password returns 401. The old `{"role":...}` body is no longer accepted: it returns 400 and has no compatibility path.
- **D-02:** Seeded passwords come from env vars, one per user (for example `AEGIS_DEMO_PASSWORD_DEVELOPER`), with demo defaults. The issuer logs a warning at startup for each default in use. The compose files set the variables explicitly. Compare passwords in constant time.
- **D-03:** Update every caller in this phase: `scripts/compose-smoke.sh` (around line 141), `tests/integration/*`, the seed script if it calls the issuer, and any docs or runbooks that show the login body.
- **D-04:** Keep the existing per-IP throttling (`checkRateLimit`). Failed logins count like any other attempt. No per-username lockout.

### jti and revocation (REV-03)
- **D-05:** The issuer sets `RegisteredClaims.ID = uuid.NewString()` (random UUID v4) on every token. `github.com/google/uuid` is already a dependency.
- **D-06:** The issuer's login response includes `jti` next to `access_token`.
- **D-07:** The gateway records the token's jti in the audit event, so the decision, denial and completion rows carry it. This needs a new event field (normalized like the other ids), a new migration `000004` adding a nullable `jti` column to `audit_events`, the worker insert, and the audit read API. The dashboard audit stream should show it if that is a small change; otherwise the API alone is enough.
- **D-08:** Revocation TTL keeps the existing 24h default with the `ttl_seconds` override (`HandleRevokeToken`). No change.
- **D-09:** The user listener denies tokens without a jti, fail closed. Use the same deny path and status as other invalid-token rejections, with a distinct reason code (for example `TOKEN_MISSING_JTI`). The reason code must be classified in the Phase 8 governor reason table, or `TestEveryMainReasonIsClassified` fails.

### Quarantine API shape (REV-03, OPS-01)
- **D-10:** The server decodes the path `{id}` with `url.PathUnescape` in both `HandleQuarantinePrincipal` and `HandleUnquarantinePrincipal`. The dashboard keeps using `encodeURIComponent`. The researcher must confirm how chi matches an encoded `%2F` in a path parameter (RawPath routing) and that `/principals/{id}/quarantine` matches for a full SPIFFE ID.
- **D-11:** `POST /control/v1/quarantine` works with `principal_id` in the JSON body. It goes to a handler (or shared core) that runs the same validation and Redis write as the path form.
- **D-12:** After decoding, an id starting with `spiffe://` must parse as a valid SPIFFE ID in trust domain `aegis.local`, or the request returns 400. Reuse the gateway's SPIFFE parsing (`internal/identity`) if it fits. User ids pass through unchanged. The same rule applies to unquarantine and to the body form.
- **D-13:** Add the routes this phase touches to `api/openapi/control-v1.yaml`: `GET /principals/quarantine`, `GET /quarantine`, `POST /quarantine` (body form), `POST /revocations`, and the path-form quarantine routes if they are missing. Regenerate the Go and TS types the repo generates from it (`types.gen.go`, `web/dashboard/src/api/types.ts`). The other missing routes (auth/*, simulate, rollback, audit/events) are out of scope.

### Proof of done
- **D-14:** Extend `scripts/compose-smoke.sh` to run the new checks live on **mvp, hardened and distributed**.
- **D-15:** Propagation is measured per replica. After a revoke or quarantine, poll each gateway directly (gateway-1/2/3 on distributed, not through HAProxy) until it denies. The check passes only if every replica denies within 5s, and it prints the elapsed time for each.
- **D-16:** Live flows the smoke must prove:
  1. **JTI revoke:** log in (with the new credentials) → 200 with the token → revoke its jti → the same token is denied with `TOKEN_REVOKED` → a freshly issued token still gets 200.
  2. **Workload quarantine on :9443:** quarantine a backend's SPIFFE ID using the dashboard-style encoded path → its mTLS call is denied (`PRINCIPAL_QUARANTINED`) → unquarantine → 200 again.
  3. **Body-form quarantine:** `POST /quarantine {principal_id}` quarantines exactly like the path form.
  4. **Audit row carries jti:** the denial row for the revoked request shows the revoked jti.
- **D-17:** The smoke script runs a disk pre-flight check. If the filesystem holding the Docker volumes is above about 85%, it exits early with a clear message instead of failing on readiness later (the spool's 90% gate measures the whole filesystem). Freeing disk space stays with the operator. The host is at 87.4% right now, so the live runs need space freed first.
- **D-18:** Hermetic tests must cover:
  - issuer credential checks and jti presence;
  - a gateway deny when the jti is missing;
  - handler decoding and SPIFFE validation;
  - the body-form route;
  - the worker and migration persisting jti.

  Extend the hermetic smoke-script lint in `tests/compose` to cover the new sections.

### Claude's Discretion
- Exact env var names, the reason-code names (as long as they are classified), and whether the path and body handlers share one core function.
- Whether the dashboard audit stream shows the jti column (D-07), depending on effort.
- The exact 401 vs 403 split for revoked tokens and tokens without a jti. Follow the existing gateway conventions for invalid tokens.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Audit and requirements
- `.planning/v1.0-MILESTONE-AUDIT.md` — re-audit 2026-10-10. B9 and B10 evidence, and REV-03, AUTH-04 and OPS-01 verdicts with file:line refs.
- `.planning/REQUIREMENTS.md` — REV-03, AUTH-04 and OPS-01 wording.
- `docs/adr/0005-redis-fail-closed-semantics.md` — revocation and quarantine semantics, the 5s convergence driver, the 200ms fail-closed bound.
- `docs/adr/0003-dual-listener-identity-separation.md` — user listener vs workload :9443 listener identity.

### Issuer and gateway
- `cmd/demo-issuer/main.go` — seed map (:25-29), login handler (:80-140), throttling.
- `cmd/gateway/main.go` — user revocation check (:371), workload SPIFFE extraction and check (:675-693).
- `internal/identity/` — SPIFFE ID extraction and parsing (reuse it for D-12).
- `internal/revocation/store.go` — Redis key layout (`revocation:jti:`, `quarantine:principal:`), `CheckRevocation`.

### Control plane and dashboard
- `internal/control/api.go` — routes (:132-138).
- `internal/control/quarantine_handler.go` — quarantine, unquarantine, revoke and list handlers.
- `api/openapi/control-v1.yaml` — spec to extend (D-13).
- `web/dashboard/src/components/EmergencyQuarantine.tsx` — encoded path (:67) and revoke form.

### Audit pipeline (for D-07, D-09)
- `internal/audit/event.go` — event fields and `Normalize()`.
- `internal/audit/worker.go` — insert column list (:78-88).
- `internal/audit/governor.go` — `reasonTable`; new reason codes must be classified.
- `migrations/000003_add_audit_suppressed_count.sql` — pattern for an additive audit column.
- `.planning/phases/08-close-gap-b7-route-denials-and-completion-events-to-the-audi/08-CONTEXT.md` — Phase 8 locked decisions on audit event types and normalization.

### Live proof
- `scripts/compose-smoke.sh` — existing sections (READY, B4, A4, AUDIT-TYPES, DEDUPE, ROTATION, GRACE) to extend.
- `deployments/compose/docker-compose.{mvp,hardened,distributed}.yml` — issuer env, gateway replicas.
- `tests/compose/` — hermetic compose and smoke-script lint.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `revStore.CheckRevocation(ctx, principal, jti)` already pipelines the quarantine and JTI checks, and the gateway already passes `claims.ID`. Only the issuer side is missing for JTI revocation.
- `POST /control/v1/revocations` (`HandleRevokeToken`) and the dashboard revoke form already exist and work once tokens carry a jti.
- `identity.ExtractSPIFFEID` in `internal/identity`: SPIFFE parsing for D-12.
- `uuid.NewString()` and `canonicalUUID` in the audit package. The audit `Normalize()` chokepoint is where the jti should be bounded.
- Smoke script helpers for logging in, quarantine POST/DELETE (the A4 section) and per-gateway exec.

### Established Patterns
- Fail-closed, default-deny everywhere (PROJECT.md invariants). D-09 follows this.
- Additive audit columns go in a new numbered migration applied by the control plane at startup (`cmd/control-plane/main.go:167`).
- Every gateway deny reason must be in the governor's `reasonTable` (Phase 8 drift guards).
- Hermetic compose lint (`tests/compose`) guards the compose and smoke files without Docker.

### Integration Points
- Issuer login body → smoke script, integration tests, possibly the seed script.
- Gateway audit context → `SetPrincipal` area (:365); attach the jti there.
- Control-plane handlers ↔ dashboard encoded path ↔ gateway SPIFFE check: the three must agree on one canonical id.

</code_context>

<specifics>
## Specific Ideas

- The per-replica propagation check should print elapsed time for each gateway, such as `REV-03 jti revoke: gateway-1 denied after 0.8s`, so the 5s claim shows in the output.
- The workload quarantine check must go through the same encoded-path form the dashboard uses, because that encoding is the bug being fixed.

</specifics>

<deferred>
## Deferred Ideas

- Per-username login lockout for the demo issuer (D-04 keeps per-IP only).
- Dashboard feature to paste a JWT and extract its jti.
- Revocation TTL derived from the token's remaining lifetime.
- The rest of the OpenAPI drift: auth/*, /policies/simulate, /snapshots/rollback, /audit/events. Belongs with the dashboard and control-plane flows phase (B5, B6).
- `ReconstructQuarantines` startup wiring and rotation keyset loading (B8). Separate phase.

</deferred>

---

*Phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys*
*Context gathered: 2026-10-10*
