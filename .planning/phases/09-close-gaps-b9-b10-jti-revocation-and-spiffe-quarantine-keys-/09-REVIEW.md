---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
reviewed: 2026-10-10T00:00:00Z
depth: standard
files_reviewed: 33
files_reviewed_list:
  - api/openapi/control-v1.yaml
  - cmd/demo-issuer/main.go
  - cmd/demo-issuer/main_test.go
  - cmd/gateway/main.go
  - cmd/gateway/missing_jti_test.go
  - deployments/compose/docker-compose.distributed.yml
  - deployments/compose/docker-compose.hardened.yml
  - deployments/compose/docker-compose.mvp.yml
  - docs/runbooks/quarantine-and-revocation.md
  - internal/audit/event.go
  - internal/audit/governor.go
  - internal/audit/governor_test.go
  - internal/audit/jti_test.go
  - internal/audit/logger.go
  - internal/audit/worker.go
  - internal/audit/worker_test.go
  - internal/control/api_test.go
  - internal/control/quarantine_handler.go
  - internal/identity/spiffe.go
  - internal/identity/spiffe_test.go
  - internal/storage/audit_repo.go
  - internal/storage/audit_repo_test.go
  - internal/storage/db_test.go
  - migrations/000004_add_audit_jti.sql
  - pkg/api/control/v1/types.gen.go
  - scripts/compose-smoke.sh
  - tests/compose/compose_test.go
  - tests/compose/smoke_script_test.go
  - tests/dr/dr_test.go
  - tests/integration/bypass_test.go
  - tests/integration/mvp_test.go
  - tests/integration/operator_telemetry_test.go
  - web/dashboard/src/api/types.ts
findings:
  critical: 1
  warning: 6
  info: 6
  total: 13
status: issues_found
---

# Phase 9: Code Review Report

**Reviewed:** 2026-10-10
**Depth:** standard
**Files Reviewed:** 33
**Status:** issues_found

## Summary

Scope: the Phase 9 diff `bd9cfa6..HEAD`. It covers demo-issuer credentials and jti, the gateway TOKEN_MISSING_JTI guard, jti plumbing through the audit pipeline (event, logger, worker, migration 000004, repo), SPIFFE parsing and canonical principal ids in the quarantine handlers, OpenAPI and generated types, compose env, and the smoke script. The hermetic tests for the touched packages pass (`go test ./cmd/demo-issuer ./cmd/gateway ./internal/{audit,control,identity,storage} ./tests/compose`).

The main jti path works. The issuer mints a UUID jti, the gateway attaches it before the revocation check, the guard fails closed, and the worker, migration and repo stay consistent at 21 columns.

The main problem is in the canonical-id contract that B10 exists to fix:

- The control plane still accepts SPIFFE-like ids that it never validates. It answers 200 and the quarantine never matches.
- `ParseSPIFFEID` and `ExtractSPIFFEID` produce different canonical forms.

Secondary problems:

- The governor collapses distinct jtis into one summary row.
- The new ordering test cannot catch the regression it was written for.
- The compose files silence the default-password warning.
- The runbook commands do not work against the plain-HTTP control plane.

Known finding, noted and not re-argued here: on the distributed profile, the Phase 8 AUDIT-TYPES suppression assertions in `scripts/compose-smoke.sh` send DENY1 and DENY2 through round-robin HAProxy. Governor suppression is per gateway process, so they fail there. See 09-08-SUMMARY.md.

## Narrative Findings (AI reviewer)

## Critical Issues

### CR-01: A SPIFFE id whose scheme is not lowercase skips validation, gets 200, and the quarantine never takes effect

**File:** `internal/control/quarantine_handler.go:47`
**Issue:** `canonicalPrincipalID` sends an id to `identity.ParseSPIFFEID` only when `strings.HasPrefix(id, "spiffe://")`, and that check is case-sensitive. Every other id passes through as a "user id".

- `SPIFFE://aegis.local/workload/orders`, `Spiffe://...` or ` spiffe://...` (leading space; 0x20 is not rejected by the control-character loop) is written verbatim as `quarantine:principal:SPIFFE://aegis.local/workload/orders`.
- The handler returns 200 with an ACTIVE `QuarantineRecord`.
- The gateway looks up the key from `ExtractSPIFFEID`, which goes through `url.Parse`. That lowercases the scheme, so the gateway checks `spiffe://aegis.local/workload/orders`.
- The key never matches, and the workload keeps getting 200 on :9443.

This is the B10 failure mode again (operator sees success, control has no effect), in an emergency-response path. It breaks D-12 ("an id starting with spiffe:// must parse ... or the request returns 400") and the fail-closed invariant. The path form, the body form and unquarantine are all affected. For unquarantine the effect is reversed: a release with the wrong case returns 200 but removes nothing.

**Fix:** Recognise the scheme without regard to case, and reject leading or trailing whitespace before classifying:
```go
if strings.TrimSpace(id) != id {
    return "", fmt.Errorf("principal id has leading or trailing whitespace")
}
if len(id) >= 7 && strings.EqualFold(id[:7], "spiffe:") || strings.Contains(id, "://") {
    canon, err := identity.ParseSPIFFEID(id, quarantineTrustDomain)
    if err != nil {
        return "", fmt.Errorf("invalid SPIFFE principal id: %w", err)
    }
    return canon, nil
}
```
In `ParseSPIFFEID`, `u.Scheme` is already lowercased by `url.Parse`, so the `u.Scheme != "spiffe"` check accepts `SPIFFE://` correctly once the input reaches it. Add test cases for `SPIFFE://aegis.local/workload/orders` (must canonicalise to lowercase and hit the same key) and ` spiffe://...` (must return 400).

## Warnings

### WR-01: ParseSPIFFEID and ExtractSPIFFEID disagree on the canonical form, so some valid peer ids cannot be quarantined

**File:** `internal/identity/spiffe.go:37` and `internal/identity/spiffe.go:48-80`
**Issue:** The doc comment on `ParseSPIFFEID` says it yields "the value ExtractSPIFFEID yields from peer certificates". It does not:

- `ExtractSPIFFEID` accepts the host with `EqualFold` and returns `uri.String()`, which keeps host case, userinfo, query, fragment and percent-encoding.
- `ParseSPIFFEID` lowercases the host and rejects userinfo, query, fragment, encoded paths and any path character outside `[A-Za-z0-9._-/]`.

Consequences:

- A peer cert with `spiffe://AEGIS.LOCAL/workload/x` makes the gateway check `quarantine:principal:spiffe://AEGIS.LOCAL/workload/x`. The operator can only write the lowercased key, so that workload cannot be quarantined.
- A cert URI carrying userinfo or a query string authenticates on :9443, but no id the control plane accepts can quarantine it.

The CA is internal, so this is not directly exploitable. It still breaks the "one canonical id" contract that the phase depends on, and any mis-issued cert becomes unquarantinable.

**Fix:** Have the gateway canonicalise through the same function: `ExtractSPIFFEID` should return `ParseSPIFFEID(uri.String(), expectedTrustDomain)`, so ids that fail strict parsing are rejected at authentication (fail closed). Alternatively, make both functions use one shared `canonicalSPIFFE(u *url.URL)` helper.

### WR-02: Governor suppression key ignores jti, so summary rows give suppressed TOKEN_REVOKED and TOKEN_MISSING_JTI denials the wrong jti

**File:** `internal/audit/governor.go:231-233` (with `internal/audit/governor.go:374-389` and the new reason entry at `:62`)
**Issue:** `govKeyIdentified` keys on `principal\0route\0reason`. When one principal replays two different revoked tokens on the same route inside a window, the second denial is suppressed. The summary row is a copy of `first`, so it carries only the first token's `JTI`, and the second revoked jti never appears in `audit_events`. D-07 calls for denial rows that carry the jti, and the smoke query `where jti='<JTI>' and reason_code='TOKEN_REVOKED'` relies on it. In forensics, "which leaked token was replayed" then gives a wrong answer that looks right. The smoke script needs a WINDOW GUARD (`compose-smoke.sh` REV-03 section) only because of this.

**Fix:** For `TOKEN_REVOKED`, add the jti to the identified key so each revoked token gets its own first row and summary:
```go
func govKeyIdentified(ev *CompletionEvent) string {
    k := clipString(ev.PrincipalID, 128) + "\x00" + clipString(ev.RouteID, 64) + "\x00" + clipString(ev.ReasonCode, 64)
    if ev.ReasonCode == "TOKEN_REVOKED" {
        k += "\x00" + clipString(ev.JTI, 128)
    }
    return k
}
```
Alternatively, document that summary rows carry only the first jti, and stop the summary from copying `JTI` (set `s.JTI = ""`) so it does not claim a jti for the events it stands for.

### WR-03: TestMissingJTIGuardOrdering compares against the last CheckRevocation call (the workload listener), so it cannot catch a misplaced user-listener guard

**File:** `cmd/gateway/missing_jti_test.go:333-338`
**Issue:** `revPos` is overwritten for every `revStore.CheckRevocation` call. `wiringCalls` walks the AST in source order, so `revPos` ends up at the workload listener's call (`cmd/gateway/main.go:717`), not the user listener's call (`:394`). If the jti guard moved below the user-listener `CheckRevocation`, for example to line 450, `denyPos < revPos` would still hold and the test would pass, even though the guard now runs after the check it protects. The test also never checks that the `SetDecision("deny","TOKEN_MISSING_JTI")` literal sits under `if missingJTI(claims)`. No behavioural test sends a jti-less token and checks for 401 plus the audit reason, which D-18 asks for ("a gateway deny when the jti is missing").
**Fix:** Take the first `CheckRevocation` position (`if revPos == 0 { revPos = int(c.pos) }`), or select the call whose second argument is `claims.ID`. Add a handler-level test that signs a token with `ID: ""` using the demo seed and asserts 401 and `ac.ReasonCode == "TOKEN_MISSING_JTI"`.

### WR-04: The compose files hard-code the demo default passwords, which silences the D-02 default-password warning

**File:** `cmd/demo-issuer/main.go:44-57`; `deployments/compose/docker-compose.mvp.yml:176-178`, `docker-compose.hardened.yml:171-173`, `docker-compose.distributed.yml:308-310`
**Issue:** `loadSeedPasswords` warns only when the env var is empty. All three compose files set the variables to exactly the built-in defaults (`demo-developer-pass`, and so on), so the stacks run with public, committed passwords and never log the warning. On mvp and hardened the issuer is also published on the host (`"8085:8085"`, all interfaces). Anyone who can reach the host can mint developer, finance or application-admin tokens with the passwords in the repo. This undoes the point of AUTH-04, especially on the "hardened" profile.
**Fix:** Warn whenever the effective value equals the default, whether or not it came from env:
```go
if u.Password == cfg.def {
    log.Printf("WARN: demo user %q is using the public default password; set %s", name, cfg.env)
}
```
In compose, use interpolation with a default, `AEGIS_DEMO_PASSWORD_DEVELOPER=${AEGIS_DEMO_PASSWORD_DEVELOPER:-demo-developer-pass}`, so operators can override without editing the files. Consider binding the issuer port to `127.0.0.1:8085:8085` on hardened.

### WR-05: Runbook commands use https:// against a plain-HTTP control plane that issues Secure-only session cookies

**File:** `docs/runbooks/quarantine-and-revocation.md:42,50,56,80,121`
**Issue:** The control plane serves plain HTTP (`cmd/control-plane/main.go:266` `ListenAndServe`; compose healthchecks and `AEGIS_CP_URL` use `http://control-plane:8084`). Every rewritten command uses `curl -sk ... https://control-plane:8084/...`. That fails with a TLS handshake error ("wrong version number"), so the emergency runbook fails at step 1. Switching to `http://` alone is not enough: the session cookie is `Secure: true` (`internal/control/auth.go:79`), so `curl -b cookies.txt` does not send it over plain HTTP except to localhost. In compose this cannot be reached from outside the network on mvp, and the smoke script works around it by parsing `Set-Cookie` by hand.
**Fix:** Document the transport that actually works: `http://`, with the session passed explicitly as `-H "Cookie: aegis_session=${SESSION}"` (extracted the same way `op_login` in `scripts/compose-smoke.sh` does it). Otherwise, if a TLS-terminating front end is expected, name it. Test the runbook commands against a running mvp stack.

### WR-06: replica_poll measures cumulative time from one t0, so the per-replica figures and the 5s limit depend on the order replicas are polled

**File:** `scripts/compose-smoke.sh:192-224`
**Issue:** `t0` is taken once before the loop over `GATEWAYS`, and `el` for gateway-N includes all the time spent polling gateways 1..N-1. Each poll uses `docker compose exec`, at about 0.15 to 0.2 s each. The 09-08 output shows the figures adding up: 0.20 / 0.36 / 0.55 s. The script labels them "<g> denied after Xs", which reads as per-replica latency (D-15). With more replicas, slow exec, or one slow replica early in the list, later healthy replicas fail the 5s check even though they converged in time. The reverse also holds: a replica that converged late is never measured on its own.
**Fix:** Either poll all replicas in parallel from one t0 (background subshells writing per-replica results), or report both values: record the moment each replica first denies relative to t0, print it as "since control call", and drop the "per replica" wording. At minimum, rename the output to "cumulative" so the 5s claim is not overstated.

## Info

### IN-01: The recovery poll prints "denied after" for a 200 response

**File:** `scripts/compose-smoke.sh:209` (called with `"WORKLOAD UNQUARANTINE (recovered 200)" 200 ""`)
**Issue:** `replica_poll` always prints "denied after", including when it is waiting for 200 after unquarantine. The output is misleading, and `TestSmokeScriptCoversJTIAndWorkloadQuarantine` greps for "denied after".
**Fix:** Print "reached HTTP ${want_status} after ${el}s", or pass a verb argument.

### IN-02: Quarantine keys written by the pre-fix handler can no longer be released

**File:** `internal/control/quarantine_handler.go:44-46,152-161`
**Issue:** Before Phase 9, the raw `chi.URLParam` stored keys such as `quarantine:principal:spiffe%3A%2F%2Faegis.local%2Fworkload%2Forders`. `HandleListQuarantines` still lists them, but `DELETE` now rejects any id that contains `%` with 400, so the dashboard release button fails until the 24h TTL runs out. This is harmless for enforcement because those keys never matched, but it is confusing for operators.
**Fix:** Delete such keys once during upgrade (`SCAN quarantine:principal:*%*`), or have the list handler flag non-canonical keys.

### IN-03: Revocation jti is unvalidated and unbounded, while the audit jti is clipped at 128 bytes

**File:** `internal/control/quarantine_handler.go:181-195`; `internal/audit/event.go:93`
**Issue:** `HandleRevokeToken` accepts any non-empty `jti` (any length, control characters included) as a Redis key. `Normalize` clips the audit jti to 128 bytes. A revoked jti over 128 bytes would then never equal the audit `jti` column in a correlation query. The issuer only mints UUIDs, so this is latent.
**Fix:** Apply the same 1–128 byte and no-control-character rule to `req.JTI` (as `canonicalPrincipalID` does), or require UUID form.

### IN-04: OpenAPI body-form POST /quarantine does not mark principal_id as required

**File:** `api/openapi/control-v1.yaml:349-358,907-912`
**Issue:** The body form reuses `QuarantineRequest`, where `principal_id` is optional, but the handler returns 400 without it. Generated clients will not enforce it.
**Fix:** Add a `QuarantineByBodyRequest` schema built with `allOf: [QuarantineRequest]` and `required: [principal_id]`.

### IN-05: Runbook overstates what the jti guard protects against

**File:** `docs/runbooks/quarantine-and-revocation.md:87`
**Issue:** "a revoked token cannot be replayed by stripping the claim". Stripping a claim already breaks the Ed25519 signature. The guard actually protects against validly signed tokens from an issuer that omits jti.
**Fix:** Reword: "tokens minted without a jti (which revocation could not target) are rejected with 401; audit reason TOKEN_MISSING_JTI."

### IN-06: Demo-issuer login body has no size limit; the test cleanup re-sets env inside Cleanup

**File:** `cmd/demo-issuer/main.go:131-133`; `cmd/demo-issuer/main_test.go:154-159`
**Issue:** `json.NewDecoder(r.Body)` reads an unbounded body (rate limiting is per request, not per byte). In the test, calling `t.Setenv` inside `t.Cleanup` registers more restore cleanups during cleanup. It works, but it is fragile and also mutates the global `seedDatabase`.
**Fix:** Wrap the body with `r.Body = http.MaxBytesReader(w, r.Body, 4096)`. In the test, call `loadSeedPasswords()` in the cleanup after the `Setenv` restores have run, or have `IssuerServer` hold the user table instead of using a package-level map.

---

_Reviewed: 2026-10-10_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
