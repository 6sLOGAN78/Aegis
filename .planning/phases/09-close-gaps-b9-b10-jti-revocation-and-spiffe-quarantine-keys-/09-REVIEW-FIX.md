---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
fixed_at: 2026-10-10T17:41:48Z
review_path: .planning/phases/09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys-/09-REVIEW.md
iteration: 1
findings_in_scope: 7
fixed: 7
skipped: 0
status: all_fixed
---

# Phase 9: Code Review Fix Report

**Fixed at:** 2026-10-10T17:41:48Z
**Source review:** .planning/phases/09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys-/09-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 7 (CR-01, WR-01..WR-06; Info findings out of scope)
- Fixed: 7
- Skipped: 0

Post-fix verification: `go build ./... && go vet ./... && go test -count=1 $(go list ./... | grep -v /tests/integration)` all green. `bash -n scripts/compose-smoke.sh` passes. No Docker, live smoke or tests/integration runs were done.

## Fixed Issues

### CR-01: A SPIFFE id whose scheme is not lowercase skips validation, gets 200, and the quarantine never takes effect

**Files modified:** `internal/control/quarantine_handler.go`, `internal/control/api_test.go`, `internal/control/principal_id_test.go` (new)
**Commit:** 815cf71
**Status:** fixed: requires human verification (logic change)
**Applied fix:** `canonicalPrincipalID` now rejects ids with leading or trailing whitespace (`strings.TrimSpace(id) != id`, which also covers Unicode spaces). A new `looksLikeSPIFFE` helper sends an id to strict `identity.ParseSPIFFEID` when it has a `spiffe:` scheme in any case or contains `://`. URI-shaped ids can no longer fall through as opaque user ids. Tests check that `SPIFFE://`, `Spiffe://AEGIS.LOCAL/...` and the encoded uppercase form all write the lowercase gateway key, through the path form, the body form and unquarantine. They also check that padded ids, `https://...`, `spiffe:/...` and `SPIFFE:` without `//` all return 400.

### WR-01: ParseSPIFFEID and ExtractSPIFFEID disagree on the canonical form

**Files modified:** `internal/identity/spiffe.go`, `internal/identity/spiffe_test.go`
**Commit:** ac996c2
**Status:** fixed: requires human verification (logic change)
**Applied fix:** Added one shared `canonicalSPIFFE(u *url.URL, trustDomain, allowAnyDomain)` helper, used by both `ExtractSPIFFEID` and `ParseSPIFFEID`. It checks the scheme without regard to case, rejects userinfo, port, query, fragment and opaque URIs, requires a non-empty host, checks the trust domain, rejects encoded paths, empty or dot segments and characters outside `[A-Za-z0-9._-/]`, and enforces the 128-byte limit on the canonical form. The output is `spiffe://<lowercase host><path>`. `ExtractSPIFFEID` keeps its old behaviour of skipping the trust-domain check when the expected domain is empty; `ParseSPIFFEID` still requires a match. Peer URIs that fail strict parsing are now rejected at authentication (fail closed). Updated the existing test case to expect a lowercase host. Added `TestSPIFFE_ExtractRejectsNonCanonical` and `TestSPIFFE_ExtractAndParseAgree`, which check byte-identical output and matching accept/reject results for the same id. I checked every SPIFFE id in the repo (certs, compose, tests); all already meet the strict form.

### WR-02: Governor suppression key ignores jti

**Files modified:** `internal/audit/governor.go`, `internal/audit/governor_test.go`
**Commit:** 9280ccc
**Status:** fixed: requires human verification (logic change)
**Applied fix:** This is the minimal change. `govKeyIdentified` appends the clipped jti to the key only when the reason is `TOKEN_REVOKED`, so each revoked token gets its own first row and summary. Cardinality is bounded by the revoked set, and the existing MaxKeys/overflow bounds still apply. Other reasons, including TOKEN_MISSING_JTI, whose jti is always empty, keep the (principal, route, reason) key. The new `TestGovernorRevokedJTIsNotCollapsed` checks this. All Phase 8 governor tests still pass. The smoke script's REV-03 WINDOW GUARD was left as is.

### WR-03: TestMissingJTIGuardOrdering compares against the last CheckRevocation call

**Files modified:** `cmd/gateway/main.go`, `cmd/gateway/missing_jti_test.go`
**Commit:** 918f765
**Applied fix:** Moved the inline guard into `rejectMissingJTI(w, ac, claims, reqID) bool`, still in main.go, so `TestEveryMainReasonIsClassified` still sees the literal. `main` calls it as `if rejectMissingJTI(...) { return }`, and runtime behaviour is unchanged. The ordering test now picks the one `revStore.CheckRevocation` whose third argument is `claims.ID`, finds the handler function literal that contains it, and requires exactly one `if rejectMissingJTI(...)` guard there. The guard must come before that call and end with `return`. The test also requires the `SetDecision("deny","TOKEN_MISSING_JTI")` literal to be inside `rejectMissingJTI`. A new behavioural test, `TestMissingJTIBehaviour`, signs a token with an empty `ID` using the demo seed and validates it through `identity.TokenValidator`. It asserts a 401 response and audit `ReasonCode == "TOKEN_MISSING_JTI"`, fail-closed behaviour with a nil audit context, and pass-through for a token that has a jti.

### WR-04: The compose files hard-code the demo default passwords, which silences the D-02 warning

**Files modified:** `cmd/demo-issuer/main.go`, `cmd/demo-issuer/main_test.go`, `deployments/compose/docker-compose.mvp.yml`, `deployments/compose/docker-compose.hardened.yml`, `deployments/compose/docker-compose.distributed.yml`, `scripts/compose-smoke.sh`
**Commit:** 08b28a5
**Applied fix:** `loadSeedPasswords` now warns whenever the effective password equals the built-in default (constant-time compare via `passwordOK`), whether it came from the fallback or was set explicitly. The log line names only the user and the env var, never the password. The compose files now use `${AEGIS_DEMO_PASSWORD_X:-<demo default>}` interpolation, the same pattern the stack already uses for spool and suppression settings. The live defaults are unchanged and no secrets were added. The smoke script's `SMOKE_DEV_PASSWORD` now falls back to `$AEGIS_DEMO_PASSWORD_DEVELOPER` and then to the demo default, so an override still works with the smoke. The new `TestSeedPasswordDefaultWarning` covers this. I did not change the issuer port bind to 127.0.0.1 on hardened: `tests/compose` pins `8085:8085`, and the review only said to consider it.

### WR-05: Runbook commands use https:// against a plain-HTTP control plane that issues Secure-only session cookies

**Files modified:** `docs/runbooks/quarantine-and-revocation.md`
**Commit:** 12f8dae
**Applied fix:** I first confirmed the actual behaviour: `cmd/control-plane/main.go` uses `ListenAndServe`, which is plain HTTP, and `internal/control/auth.go` sets the cookie with `Secure: true`. CSRF comes from `X-CSRF-Token` (session.go). Added a "Transport" paragraph and rewrote the commands in sections 3, 4 and 6. They now use `CP_URL=http://control-plane:8084` and capture `aegis_session` from `Set-Cookie` and `csrf_token` from the body, the same way `op_login` does. They pass `-H "Cookie: aegis_session=${SESSION}"` explicitly, with no `-sk` or cookie jar. The runbook also states that 8084 is not published to the host on mvp, so the commands must run inside the network via `docker compose exec -T control-plane`, and that it is published on hardened and distributed. The commands have not been run against a live stack.

### WR-06: replica_poll measures cumulative time from one t0

**Files modified:** `scripts/compose-smoke.sh`
**Commit:** 4cec512
**Applied fix:** `replica_poll` now polls all replicas in parallel from a single t0 taken right after the control call. Each replica runs in a background subshell (`dc exec ... </dev/null`) and writes `ok|timeout <el> <status>` to a mktemp directory. After `wait`, the parent shell calls pass/fail, so the FAILS counter stays correct. Each replica's figure is now its own time to converge after the control call, still with a 5s limit per replica. The output reads "<g> denied after Xs since control call (includes exec latency)", and the "denied after" string the lint checks for is kept. I checked the function with a stubbed `dc`: one replica converged late but within 5s, one at once, one never, and all three were reported correctly. `bash -n` and `go test ./tests/compose/` pass.

---

_Fixed: 2026-10-10T17:41:48Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
