---
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
reviewed: 2026-10-09T00:00:00Z
depth: standard
files_reviewed: 8
files_reviewed_list:
  - Makefile
  - deployments/compose/Dockerfile
  - deployments/compose/docker-compose.distributed.yml
  - deployments/compose/docker-compose.hardened.yml
  - deployments/compose/docker-compose.mvp.yml
  - deployments/compose/seed/seed.sh
  - scripts/compose-smoke.sh
  - tests/compose/compose_test.go
findings:
  critical: 0
  warning: 5
  info: 9
  total: 14
status: issues_found
---

# Phase 07: Code Review Report

**Reviewed:** 2026-10-09
**Depth:** standard
**Files Reviewed:** 8
**Status:** issues_found

## Summary

The wiring changes themselves (AEGIS_REDIS_ADDR on the control plane, seed one-shot, per-gateway spool plus per-spool audit worker, service_healthy / service_completed_successfully gating, stop_grace_period 45s, anchor merge in distributed) are correct as written. I traced the dependency graph in all three profiles and found no deadlock: seed depends only on control-plane, postgres and redis; gateways depend on seed; nothing depends back on a gateway. The `x-audit-worker` merge only overrides `volumes`, which the anchor does not define, so nothing is silently lost. Backends (orders, payments, admin) publish no host ports in any profile, and the spool volumes are not shared between gateways in distributed.

The problems are in verification quality and in what the profiles expose. The smoke script's graceful-stop check cannot detect a short `stop_grace_period`. Its cleanup can leave the real demo user quarantined. The "hardened" and "distributed" profiles publish the control plane (with its built-in `admin` / `admin-secret` operator) on all host interfaces, and the new hermetic test asserts that exposure as correct. No Critical findings. Credentials are demo-grade throughout.

## Warnings

### WR-01: Control plane (default sec-ops credential) and token issuer are published on all host interfaces; test locks this in

**File:** `deployments/compose/docker-compose.hardened.yml:29-31`, `deployments/compose/docker-compose.distributed.yml:101-104`, `deployments/compose/docker-compose.mvp.yml:156-157` (demo-issuer), `tests/compose/compose_test.go:422,431,434,446`
**Issue:** `control-plane` publishes `8084:8084` and `9090:9090` (and `9092`) in hardened and distributed. The REST API accepts the hard-coded `admin` / `admin-secret` operator (`internal/control/auth.go:17`), which can publish policy snapshots, edit routes and quarantine or release principals. Anyone who can reach the host can therefore replace the gateway's policy. That crosses the trust boundary the product exists to enforce. mvp deliberately keeps the control plane unpublished ("not published to the host in mvp"), so the profile named "hardened" is the less hardened one. `demo-issuer` (`8085:8085` in mvp and hardened) mints tokens for an arbitrary requested role on all interfaces. `TestPortsMatchBinaries` asserts these published ports are correct, so the test would fail if someone closed them. The test also never asserts that postgres and redis are unpublished in hardened or distributed (only mvp is checked, `compose_test.go:257-261`).
**Fix:** Bind published control-plane ports to loopback (`"127.0.0.1:8084:8084"`, same for 9090 and 9092). Better, drop the publish entirely as in mvp, since the seed and smoke script both work in-network. Bind 8085 and 8404 to loopback as well. Update the test to assert the loopback prefix (or absence) and extend the "no host ports" assertion to postgres and redis for all profiles.

### WR-02: `compose-smoke.sh --stop-check` overrides `stop_grace_period`, so it cannot detect a too-short grace period

**File:** `scripts/compose-smoke.sh:309,314`
**Issue:** `dc stop -t 60 "${GW}"` replaces the service's configured `stop_grace_period` (45s) with 60s for that invocation. If someone shortens or removes `stop_grace_period` (compose default 10s), this check still gives the gateway 60s and passes with exit 0. The failure message ("137 means SIGKILL so stop_grace_period was too short") describes something the command cannot observe. 07-06-SUMMARY cites "exit 0 under stop -t 60" as evidence. The hermetic test covers the configured value, but the live check provides no independent assurance, and in an idle stack the drain is instantaneous anyway. In distributed only `gateway-1` is checked.
**Fix:** Drop `-t` so the configured grace period is exercised (`dc stop "${GW}"`). Optionally loop over all gateway services in distributed. Wrap with an outer `timeout 90`.

### WR-03: Cleanup trap can silently leave the real demo user (`usr_developer_01`) quarantined

**File:** `scripts/compose-smoke.sh:156-161`
**Issue:** `cleanup` skips re-login whenever `SESSION` is non-empty (`[ -n "${SESSION}" ] || op_login`). The session may be expired, or invalidated by a later failed `op_login`, which sets `SESSION=""` and is then refilled. If the session is stale, both `DELETE` calls return 401/403, are swallowed by `|| true`, and the script exits 0 with the demo user still quarantined in Redis. That user then breaks the next smoke run or test run on a persistent stack (redis has no volume, but the stack may stay up). The DELETE status codes are never checked or reported.
**Fix:** Always log in fresh in cleanup and report failures:
```bash
cleanup() {
  local rc=$?
  if op_login; then
    for p in usr_developer_01 smoke-b4-probe; do
      c="$(cp_call DELETE "/control/v1/principals/${p}/quarantine")"
      case "${c}" in 2??|404) ;; *) echo "WARN: could not release quarantine for ${p} (HTTP ${c:-none})" >&2 ;; esac
    done
  else
    echo "WARN: cleanup could not log in; principals may remain quarantined" >&2
  fi
  return "${rc}"
}
```

### WR-04: Quarantine denial (the fail-closed property) is explicitly non-gating in the smoke script

**File:** `scripts/compose-smoke.sh:280-303`
**Issue:** A4 prints `A4 RESULT: FAIL` but never increments `FAILS`, so `make compose-smoke` exits 0 even when a quarantined principal is still served 200. Separately, the same script treats `quarantine POST == 200` (B4) as pass, so a profile where quarantine is accepted but not enforced goes green. For a default-deny gateway this is the one live check that a revocation takes effect, and CI cannot see it regress. The 07-06 note says it is intentionally non-fatal because of known open causes (no jti, SPIFFE key encoding, B8), which is reasonable for this phase. Without a guard, though, a later regression is invisible.
**Fix:** Add an opt-in strict mode (`SMOKE_STRICT_A4=1` makes the A4 failure call `fail`) and enable it once the open items land. At minimum print the A4 outcome in the final summary line so a FAIL is not buried above "0 hard failure(s)".

### WR-05: Hermetic test cannot catch the most likely `up --wait` deadlock (`service_healthy` on a service with no healthcheck)

**File:** `tests/compose/compose_test.go` (missing assertion; relevant helpers at `:122-141,207-223`)
**Issue:** Compose will hang or error if a service has `depends_on: X: condition: service_healthy` and X defines no healthcheck. The file has `dependsOn()` and `healthcheckTest()` available, but no test cross-checks them. Today the profiles are consistent (postgres, redis and control-plane define healthchecks), so the gap only matters on the next edit, which is when this guard is needed. Similarly, `service_completed_successfully` on a service that has `restart` other than "no" is not checked for non-seed targets.
**Fix:** Add a test iterating every profile, every service and every dependency with `condition == "service_healthy"`, asserting `healthcheckTest(services[dep]) != ""` and that the dependency exists in the file. Also assert the dependency graph is acyclic.

## Info

### IN-01: Hard-coded demo credentials repeated in compose and scripts

**File:** `deployments/compose/docker-compose.*.yml` (`POSTGRES_PASSWORD`, `AEGIS_DB_PASSWORD=aegis-secret-pw`, `AEGIS_SEED_PASSWORD=admin-secret`), `scripts/compose-smoke.sh:56`
**Issue:** Demo-grade; comments mark the seed password. `AEGIS_SEED_PASSWORD` is visible via `docker inspect` and the same password is the compiled-in operator password. The DB password is duplicated in 8+ places per profile. Not a real secret. Escalates to a warning only if these profiles are ever used off a developer machine (see WR-01).
**Fix:** Use a `.env` file or `${AEGIS_DB_PASSWORD:-aegis-secret-pw}` interpolation so there is one definition.

### IN-02: Smoke login builds JSON by string interpolation and passes the password on a command line

**File:** `scripts/compose-smoke.sh:88`
**Issue:** `-d "{\"username\":\"${SMOKE_USER}\",\"password\":\"${SMOKE_PASSWORD}\"}"` breaks on passwords containing `"` or `\`. The password is also visible in the container's process list for the life of the `curl` call. The script header claims credentials are never printed, which holds for stdout only.
**Fix:** Build the body with `jq -n --arg u ... --arg p ...` and pipe it via `curl -d @-` with `dc exec -T`.

### IN-03: Smoke script requires bash 4+ and fails obscurely elsewhere

**File:** `scripts/compose-smoke.sh:239,244`
**Issue:** `mapfile` and `declare -A` do not exist in macOS system bash 3.2, which `make compose-smoke` (`bash scripts/...`) will pick up. Failure is a cryptic error mid-run after earlier checks have already quarantined a probe principal (cleanup still runs).
**Fix:** Add `((BASH_VERSINFO[0] >= 4)) || { echo "bash >= 4 required" >&2; exit 2; }` near the tool checks.

### IN-04: GRACE check restarts the gateway without waiting for health

**File:** `scripts/compose-smoke.sh:316`
**Issue:** `dc start "${GW}" >/dev/null 2>&1 || true` returns immediately. The script then exits 0 while the gateway is still starting, so a following command (for example `make test-e2e`) races it. A failed restart is also swallowed.
**Fix:** Use `dc up -d --wait "${GW}"` and count a failure.

### IN-05: Seed accepts an empty route set and its route-count check is tautological

**File:** `deployments/compose/seed/seed.sh:107,158`
**Issue:** `posted` counts lines of an ndjson derived from the same `routes.json` that `EXPECTED_ROUTES` is read from, so the comparison cannot fail in a way that matters. If `routes.json` is `[]`, the seed posts nothing, publishes a snapshot and reports "seed complete: 0 routes". The gateway fails closed on it (deny-all), but the step that gates `service_completed_successfully` reports success. Also, `cp_call` under `set -e` exits silently with curl's status on a transport error (no message naming which step failed), and the wait loop counts iterations rather than seconds (up to ~6x `AEGIS_SEED_WAIT_SECONDS` when the host black-holes).
**Fix:** `[ "$EXPECTED_ROUTES" -gt 0 ] || { echo "seed: routes.json has no routes" >&2; exit 1; }`. Make `cp_call` callers use `code="$(cp_call ...)" || { echo "seed: <step> transport error" >&2; exit 1; }`. Use `date +%s` deadlines in the wait loop.

### IN-06: Re-running `up` against a persisted volume re-applies repo policy over operator changes

**File:** `deployments/compose/seed/seed.sh:23-25,143-173` with `docker-compose.*.yml` (seed is a dependency of every gateway)
**Issue:** Each `up` re-upserts every route and the `mvp-authz` policy and publishes a new snapshot. A route an operator removed, or a policy tightened through the API, is silently reverted to the repo's content on the next `up` or `--build`. The 412 retry path also re-publishes with whatever ETag is current, bypassing the optimistic-concurrency check meant to catch concurrent edits. Acceptable for a demo workaround (POL-03), but worth documenting.
**Fix:** Document in the seed header, or make the seed skip when a published snapshot already exists unless `AEGIS_SEED_FORCE=1`.

### IN-07: Hermetic test brittleness and vacuous spots

**File:** `tests/compose/compose_test.go:152,143-158,67-69,257-261`
**Issue:** (a) Read-only detection uses `strings.Contains(parts[2], "ro")`, a substring match rather than an option-list match. (b) `volumeMounts` silently skips long-syntax (map) volume entries, so a spool declared in long form yields zero mounts; the downstream `assert.Len(..., 1)` fails loudly there, but `ro` on a long-form certs mount is never checked. (c) The certs mounts are never asserted `:ro`, so dropping `:ro` on `/certs` (private keys) would pass. (d) `TestMVPTopology` conditionals (`if svc, ok := ...`) make the unpublished-port assertion vacuous for any missing service. (e) `composePath` uses cwd-relative `../..`, which is correct under `go test` but breaks if the compiled test binary is run from elsewhere.
**Fix:** Split `parts[2]` on `,` and compare to `ro`. Add an assertion that every `/certs` mount is read-only. Use `require.Contains` instead of guarded `if`. Resolve paths via `runtime.Caller` or an env override.

### IN-08: "hardened" profile is functionally the mvp profile plus published control-plane ports

**File:** `deployments/compose/docker-compose.hardened.yml` (whole file)
**Issue:** No `read_only`, `cap_drop`, `no-new-privileges`, resource limits, internal-only network or loopback binding is present; the only differences from mvp are the published control-plane ports. The name implies a posture the file does not deliver. Related: `haproxy` stats (`8404:8404`, `distributed.yml:23`) is published unauthenticated on all interfaces.
**Fix:** Either rename or document it, or add the hardening options (`security_opt: [no-new-privileges:true]`, `cap_drop: [ALL]`, `read_only: true` with tmpfs for writable paths) in a follow-up phase.

### IN-09: Dockerfile and build-context hygiene

**File:** `deployments/compose/Dockerfile:1,7,26-34,46-47`; repo root (no `.dockerignore`)
**Issue:** `golang:alpine` is unpinned. All runtime stages run as root. There is no `.dockerignore`, so `COPY . .` in the builder sends `.git`, `.planning/` and generated `deployments/certs/*.key` to the daemon and into a builder layer (not the final images, but visible in the build cache). `gateway` and `control-plane` still `COPY policies` although no binary reads it any more (POL-03); only the seed stage needs it. `Makefile` `PROFILE ?= mvp` is a generic name that an ambient environment variable will silently override.
**Fix:** Pin the Go base image, add `USER nobody` to runtime stages (check cert and spool permissions first), add `.dockerignore` excluding `.git`, `.planning`, `deployments/certs`. Drop `COPY policies` from gateway and control-plane. Rename the Makefile variable to `COMPOSE_PROFILE`.

---

_Reviewed: 2026-10-09_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
