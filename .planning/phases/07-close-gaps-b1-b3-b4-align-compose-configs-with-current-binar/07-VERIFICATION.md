---
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
verified: 2026-10-09T00:00:00Z
status: passed
score: 5/5 success criteria verified (SC4 live grace path exercised on 2026-10-09, see Human verification results)
overrides_applied: 0
re_verification: false
gaps: []
deferred: []
human_verification:
  - test: "Stop a gateway with the configured grace period (no -t override) while requests are in flight, then read its exit code and elapsed time"
    expected: "On a running profile: start a slow or looping request load through the gateway, run `docker compose -f deployments/compose/docker-compose.<profile>.yml stop gateway` (or gateway-1/2/3), then `docker inspect -f '{{.State.ExitCode}}' <container>` returns 0 (not 137) and the stop completes in under 45s"
    why_human: "scripts/compose-smoke.sh runs `stop -t 60`, which overrides stop_grace_period 45s, and the executor stops were on idle gateways (0.28 to 0.37 s). The 45s setting was never exercised live and a loaded drain was never measured. Needs a running Docker stack, which this verification could not start (environment constraints)."
  - test: "Optional independent re-run of the live evidence on a machine with disk headroom: `down -v`, `up -d --build --wait`, `bash scripts/compose-smoke.sh <profile> --stop-check`, then TestMVPEndToEnd and TestBackendBypassPrevention on mvp"
    expected: "Smoke exits 0 with 0 hard failures on mvp, hardened and distributed; integration tests 9/9 and 7/7"
    why_human: "The live results for SC1, SC2 and SC3 are executor-recorded (2026-10-08) and were not reproduced here. They are corroborated by leftover built images but not re-observed."
---

# Phase 7: Close gaps B1, B3, B4 (compose alignment) Verification Report

**Phase Goal:** Make the three shipped compose profiles (mvp, hardened, distributed) run the current binaries, closing audit blockers B1 (MVP compose cannot run the current gateway), B3 (distributed audit worker drains 1 of 3 spools), B4 (control plane has no AEGIS_REDIS_ADDR), plus gateway stop_grace_period. Deployment wiring only; no Go source change. REV-03 and AUD-03 are partial contributions and REMAIN UNSATISFIED by design.
**Verified:** 2026-10-09
**Status:** passed (human verification items resolved 2026-10-09)
**Re-verification:** No, initial verification

## Evidence classes used

- **Static (reproduced here):** compose files read line by line and traced against the env vars read by cmd/ and internal/config; `docker compose config -q` for all three files; `go test -count=1 -v ./tests/compose/` (10 tests, all PASS); the same test run against the pre-phase compose files from commit 469cba3 (copied to scratch, not the repo) FAILS for every B1, B3, B4, dead-env, grace-period, seed and healthcheck test, so the lint is not vacuous; `git diff --stat 469cba3 HEAD -- cmd internal` is empty; REQUIREMENTS.md read.
- **Recorded, not reproduced:** the live runs on 2026-10-08 in 07-05-SUMMARY.md and 07-06-SUMMARY.md (clean `down -v` and `up -d --build --wait`, smoke exit 0, TestMVPEndToEnd 9/9, TestBackendBypassPrevention 7/7, per-worker wal.cursor, A4 PASS). I did not start Docker (constraint). Corroboration only: locally built images compose-gateway, -gateway-1/2/3, -audit-worker, -audit-worker-1/2/3, -control-plane, -seed, -orders, -payments, -admin exist (about 20 hours old), and there are 0 compose- containers and 0 compose_ volumes left, matching the SUMMARY teardown claims.

## Goal Achievement: Success Criteria

| # | Success criterion | Verdict | Evidence |
|---|---|---|---|
| 1 | mvp starts postgres, redis, control-plane, one-shot seed, gateway, audit worker, demo issuer and three backends under unchanged service names; gateway gets a snapshot, serves 200 and 403 from a clean `down -v` + `up -d --build --wait` (B1) | VERIFIED (static) + recorded live | `docker-compose.mvp.yml` defines exactly: postgres, redis, control-plane, seed, gateway, audit-worker, demo-issuer, orders, payments, admin. Gateway sets `AEGIS_CONTROL_PLANE_GRPC_ADDR=control-plane:9090`, `AEGIS_REDIS_ADDR=redis:6379`, `AEGIS_SPOOL_DIR=/var/log/aegis/wal`, mounts `aegis_wal_spool`, depends_on control-plane `service_healthy` and seed `service_completed_successfully`. Env names traced to `internal/config/config.go`, `cmd/gateway/main.go`. Seed (`deployments/compose/seed/seed.sh`, Dockerfile `seed` target with curl+jq) logs in, posts 5 routes with burst=max(burst,rps), posts the Rego policy, publishes with If-Match and an ETag retry on 412; every non-2xx exits non-zero. Endpoints exist in `internal/control/api.go`. Recorded live: seed exit 0 "seed complete: 5 routes, policy mvp-authz", smoke PASS developer 200 on /api/orders and 403 on /api/admin/users, TestMVPEndToEnd 9/9, TestBackendBypassPrevention 7/7. POL-03 stays partial (seed is a workaround, stated in seed.sh header and ROADMAP). |
| 2 | control-plane sets `AEGIS_REDIS_ADDR` in mvp, hardened, distributed; quarantine POST returns 200 not 503 on each running profile (B4) | VERIFIED (static) + recorded live | `AEGIS_REDIS_ADDR=redis:6379` present on control-plane in all three files (grep and `TestControlPlaneHasRedis` pass). `cmd/control-plane/main.go:114-117` defaults to `localhost:6379` when unset, confirming why the old files returned 503. Recorded live: `B4 quarantine POST returned 200` and DELETE 200 on mvp, hardened and distributed. |
| 3 | distributed runs audit-worker-1/2/3, each mounting exactly one of aegis_wal_spool_1/2/3 rw; three gateways ready behind HAProxy; every worker's spool shows an advancing wal.cursor; audit_events rises (B3). Drain wiring only, not AUD-03 | VERIFIED (static) + recorded live | `docker-compose.distributed.yml`: `x-audit-worker` anchor, `audit-worker-1/2/3` each override only `volumes` with their own `aegis_wal_spool_N:/var/log/aegis/wal` (no `:ro`); gateway-1/2/3 each mount a distinct spool; anchor carries postgres and control-plane `service_healthy` dependencies and the DB env. `TestAuditWorkerPerSpool` asserts workers == gateways, no shared spool, exactly one worker per spool, rw, depends_on control-plane. `docker compose config -q` passes. Wal cursor path is `spoolDir/wal.cursor` (`internal/audit/cursor.go:29`). Recorded live: HAProxy stats show gateway-1/2/3 UP; smoke PASS "wal.cursor advanced" for each of the three workers; `ls` of each worker's spool shows its own wal file and cursor; audit_events 61 rows; 0 SPOOL_SATURATED. |
| 4 | No service sets AEGIS_ROUTES_PATH / AEGIS_POLICY_PATH; every gateway sets `stop_grace_period: 45s` and stops with exit 0 not 137; no compose ports change; backends and the MVP control plane unpublished | PARTIAL: verified statically, live grace path not exercised (human item) | Dead env: `grep` over compose files finds neither key; `TestNoDeadEnv` PASS for all profiles. Grace: 45s on mvp gateway, hardened gateway, gateway-1/2/3 (`TestGatewayStopGracePeriod` requires >= drain+5s). `cmd/gateway/main.go:921-956` shows 30s drain default (set to 30s in distributed) plus 5s metrics shutdown = 35s < 45s, so the setting is arithmetically sound. Ports: diff of published ports against 469cba3 for all three files shows no change (mvp 8080/9443/8085; hardened 8084/9090/8080/9443/8085; distributed haproxy 8080/9443/8404, control-plane 8084/9090/9092); `TestPortsMatchBinaries` pins them. Backends only `expose`; MVP control-plane only `expose` (`TestMVPTopology`, backends subtests). Exit 0: observed live for gateway (mvp, hardened) and gateway-1/2/3 but with `stop -t 60` on idle gateways (see WR-02 below), so "exit 0, not 137 under the configured 45s" was never measured. |
| 5 | Hermetic Go test (`tests/compose`, no Docker) guards the above; `make compose-up` rebuilds and waits; `scripts/compose-smoke.sh` reports the quarantine-denial assumption as explicit PASS/FAIL | VERIFIED | `go test -count=1 -v ./tests/compose/`: TestControlPlaneHasRedis, TestMVPTopology, TestNoDeadEnv, TestGatewayWiring, TestAuditWorkerPerSpool, TestGatewayStopGracePeriod, TestSeedService, TestControlPlaneHealthcheck, TestPortsMatchBinaries, TestSeedDockerfileTarget all PASS (0.02 s). Same test against pre-phase compose files: FAIL for 7 of 8 compose-lint tests across all profiles, so it truly guards B1/B3/B4. `Makefile`: `compose-up: certs` runs `up -d --build --wait`; `compose-test` and `compose-smoke` targets added. Smoke prints `A4 RESULT: PASS/FAIL (HTTP n)` explicitly (script lines around 280-303). |

**Score:** 4 of 5 verified; SC4 verified except the live exercise of the 45s grace period.

### SC4 decision and reasoning

SC4 is **met by configuration and by static reasoning, partially met by live observation**. Every gateway carries `stop_grace_period: 45s` and the binary's worst-case shutdown is about 35s, so the setting should work. What was observed live is that gateways exit 0 on SIGTERM when idle. That would also hold with a 10s default grace period, because an idle gateway drains instantly, and the smoke check supplies `-t 60`. So the live evidence cannot distinguish a correct 45s setting from a missing one; only the hermetic test and the code reading do that. I treat this as a WARNING (not a BLOCKER): there is no evidence of failure, and the static guard is real. It is routed to human verification rather than marked verified in full.

## Required Artifacts

| Artifact | Status | Details |
|---|---|---|
| `deployments/compose/docker-compose.mvp.yml` | VERIFIED | Rewritten to current topology; wired to the env names the binaries read |
| `deployments/compose/docker-compose.hardened.yml` | VERIFIED | B4, seed gate, 45s grace, healthchecks; dead env removed |
| `deployments/compose/docker-compose.distributed.yml` | VERIFIED | Three per-spool workers, B4, seed gate, grace, healthchecks |
| `deployments/compose/Dockerfile` | VERIFIED | `seed` stage added (8 lines), no existing stage changed |
| `deployments/compose/seed/seed.sh` | VERIFIED | Substantive (201 lines), POSIX sh, exits non-zero on any non-2xx, used by the `seed` target |
| `scripts/compose-smoke.sh` | VERIFIED with caveats | 323 lines; real checks for READY, B4, B1, per-worker cursor, A4, grace. Caveats WR-02, WR-03, WR-04 below |
| `tests/compose/compose_test.go` | VERIFIED | 484 lines, red against pre-phase files, green now |
| `Makefile` | VERIFIED | compose-up builds and waits; compose-test, compose-smoke present |

## Key Link Verification

| From | To | Via | Status |
|---|---|---|---|
| gateway (all profiles) | control-plane gRPC | `AEGIS_CONTROL_PLANE_GRPC_ADDR=control-plane:9090` | WIRED (read in internal/config/config.go; control-plane `AEGIS_GRPC_PORT=9090`) |
| gateway | seed | `depends_on seed: service_completed_successfully` | WIRED (TestSeedService) |
| seed | control-plane REST | `AEGIS_CP_URL=http://control-plane:8084` and `/control/v1/{auth/login,routes,policies,policies/{id}/publish}` | WIRED (routes exist in internal/control/api.go) |
| control-plane | redis | `AEGIS_REDIS_ADDR=redis:6379` | WIRED |
| audit-worker-N | spool N | one rw named volume at `/var/log/aegis/wal` | WIRED, one-to-one with gateway-N |
| audit-worker | audit_events | depends_on control-plane `service_healthy` (control plane migration creates the table) | WIRED |
| make compose-up | fresh images | `up -d --build --wait` | WIRED |

## Env var trace (compose vs binaries)

All gateway keys used (`AEGIS_PORT`, `AEGIS_WORKLOAD_PORT`, `AEGIS_GATEWAY_ID`, `AEGIS_DRAIN_TIMEOUT`, `AEGIS_CONTROL_PLANE_GRPC_ADDR`, `AEGIS_REDIS_ADDR`, `AEGIS_SPOOL_DIR`, TLS/cert paths, `AEGIS_UPSTREAM_SCHEME`, `AEGIS_ISSUER`, `AEGIS_AUDIENCE`) are read in `internal/config/config.go`, `cmd/gateway/main.go` or `internal/proxy/router.go`. Audit-worker keys (`AEGIS_SPOOL_DIR`, `AEGIS_DB_*`, `AEGIS_PRUNE_ARCHIVED`) are read in `cmd/audit-worker/main.go`. Control-plane keys (`AEGIS_PORT`, `AEGIS_GRPC_PORT`, `AEGIS_METRICS_PORT`, `AEGIS_REDIS_ADDR`, `AEGIS_DB_*`) are read in `cmd/control-plane/main.go`. No orphan keys.

Precision note (info): the plans say AEGIS_ROUTES_PATH is "read by no binary". In fact `internal/config/config.go:79` parses it into `RoutesFilePath`, but nothing outside config consumes that field (grep of cmd/ and internal/ shows no other use), so removing it from compose is still correct and behavior-neutral.

## Requirements Coverage

Every plan (07-01 to 07-06) declares `requirements: [REV-03, AUD-03]`. Both IDs exist in `.planning/REQUIREMENTS.md`. No other IDs are mapped to Phase 7 and no orphaned requirements were found.

| Requirement | Source plans | Contribution delivered | Status after phase | Evidence |
|---|---|---|---|---|
| REV-03 | 07-01..07-06 | Partial: B4 closed, so quarantine POST returns 200 on every profile and A4 observed denial (HTTP 403) once per profile | UNSATISFIED (as designed) | REQUIREMENTS.md line 46 `- [ ] **REV-03**` unchecked; traceability line 132 `REV-03 \| Phase 3 \| Gap closure`. Open: demo issuer emits no jti, percent-encoded SPIFFE quarantine keys, B8 |
| AUD-03 | 07-01..07-06 | Partial: B3 closed at the wiring level, one worker per spool, cursors advance, rows rise | UNSATISFIED (as designed) | REQUIREMENTS.md line 53 `- [ ] **AUD-03**` unchecked; line 136 `AUD-03 \| Phase 3 \| Gap closure`. Open: B7 (`audit.NewLogger(nil)`, denials and completions not in the WAL), dedup and at-least-once not demonstrated |

### Over-claim check (the requirement the caller asked for)

- All six SUMMARYs have `requirements-completed: []` and use `requirements-partial: [REV-03, AUD-03]`; 07-05 and 07-06 carry explicit honesty statements that both remain unsatisfied.
- 07-VALIDATION.md: row labels use "REV-03 (B4)" / "AUD-03 (B3)" with green status on the wiring checks; the A4 row says "not a closure of REV-03"; the table note says "live rows are smoke evidence ... not coverage of the requirements they reference"; the "Open after Phase 7" section states both unsatisfied. `status: complete` and `nyquist_compliant: true` refer to the validation contract, not to the requirements. No over-claim found.
- ROADMAP.md Phase 7 section states "REV-03 and AUD-03 remain UNSATISFIED". STATE.md says "REV-03/AUD-03 remain open". No file claims satisfaction.
- REQUIREMENTS.md (untracked user file, read only): both boxes unchecked, both rows "Gap closure". Confirmed.
- No SUMMARY/VALIDATION wording equates the A4 single PASS with REV-03 closure.

Result: no over-claim. REV-03/AUD-03 still unsatisfied is not counted as a gap of this phase.

## No Go source change

`git diff --stat 469cba3 HEAD -- cmd internal` is empty (0 lines). `git status --short -- cmd internal pkg migrations policies` is empty. Files changed by the phase outside .planning: Makefile, Dockerfile, three compose files, seed.sh, compose-smoke.sh, compose_test.go. Confirmed deployment wiring only.

## Anti-pattern scan

No TBD, FIXME, XXX, TODO or HACK markers in any file changed by the phase. No stubs: seed, smoke script and tests are substantive. Demo credentials (`admin-secret`, `aegis-secret-pw`) are commented as demo and are pre-existing in kind.

## Code review findings (07-REVIEW.md: 0 critical, 5 warnings, 9 info), weighed

| ID | Weight in this verification |
|---|---|
| WR-01 control plane (default `admin`/`admin-secret`) and issuer published on all host interfaces in hardened and distributed; test pins it | Tech debt, not a failure. Pre-dates the phase (ports identical at 469cba3). SC4 says "no compose ports change", so the phase was required to leave this alone, and the MVP control plane (the one SC4 requires unpublished) is unpublished. Does not contradict any must_have. Carry to a later phase (bind to 127.0.0.1 or unpublish; extend test). |
| WR-02 smoke `--stop-check` uses `stop -t 60`, overriding `stop_grace_period`; gateways idle | Accepted as the reason SC4 is routed to human verification (see above). Not a BLOCKER. |
| WR-03 smoke cleanup trap skips re-login if SESSION non-empty and swallows DELETE failures, can leave `usr_developer_01` quarantined | Verification-tooling weakness, WARNING-level tech debt. Recorded runs show redis quarantine keys empty after the mvp run. |
| WR-04 A4 is non-fatal by design | Matches SC5 ("recorded, not fixed") and the plan. A4 PASS observed (HTTP 403) once on each of mvp, hardened, distributed. Single observation, with known open causes that could change behavior; not a closure of REV-03. Tech debt: add an opt-in strict mode. |
| WR-05 hermetic test does not cross-check `service_healthy` dependencies against defined healthchecks or acyclicity | Not required by any success criterion; I traced the graph by hand and found it consistent (postgres, redis, control-plane and gateways all have healthchecks; no cycles). Tech debt. |
| IN-01..IN-09 | Info only (credential duplication, bash 4 requirement, seed empty-route and rerun semantics, test brittleness, "hardened" profile is not hardened, Dockerfile hygiene). None contradicts a must_have. |

## Behavioral spot-checks and probes

| Behavior | Command | Result | Status |
|---|---|---|---|
| Compose files parse | `docker compose -f <each> config -q` | all three OK | PASS |
| Hermetic lint | `go test -count=1 -v ./tests/compose/` | 10/10 PASS | PASS |
| Lint is not vacuous | same test against 469cba3 compose files (scratch copy) | 7 compose-lint tests FAIL across mvp/hardened/distributed | PASS (guard works) |
| No Go source change | `git diff --stat 469cba3 HEAD -- cmd internal` | empty | PASS |
| Live stack, smoke, integration tests | not run (Docker constraint) | recorded by executor only | SKIPPED, see human items |
| Probes (`scripts/*/tests/probe-*.sh`) | none declared or present in this phase | n/a | n/a |

Environment note (not a phase gap): the host disk is about 99 percent full. Because of the audit spool saturation gate, tests in internal/audit, tests/chaos, tests/dr and tests/failure fail with "audit spool saturated" unless TMPDIR is on /dev/shm. I did not rerun the whole suite; tests/compose does not depend on the spool.

## Data-flow

Not applicable beyond the seed to control plane to gateway snapshot path, which is evidenced by the recorded live 200/403 results and the code-level link checks above.

## Human Verification Required

### 1. Gateway stops within the configured 45s grace period under load

**Test:** On any running profile, generate in-flight traffic through the gateway, run `docker compose -f deployments/compose/docker-compose.<profile>.yml stop gateway` (gateway-1/2/3 for distributed) with no `-t`, then `docker inspect -f '{{.State.ExitCode}}'` on the container.
**Expected:** Exit code 0 (not 137) and the stop completes in under 45s.
**Why human:** needs a running Docker stack; the live smoke overrides the grace period and runs idle.

### 2. (Optional) Reproduce the recorded live evidence

**Test:** From `down -v`, `up -d --build --wait`, `bash scripts/compose-smoke.sh <profile> --stop-check` for mvp, hardened and distributed; on mvp also run TestMVPEndToEnd and TestBackendBypassPrevention.
**Expected:** 0 hard failures; 9/9 and 7/7; per-worker wal.cursor advances in distributed.
**Why human:** executor-recorded, not reproduced here; requires disk headroom to build images.

## Human verification results (2026-10-09)

After disk space was freed (root filesystem 85% used), the user asked for the outstanding items to be run. Both were run by the orchestrator on clean rebuilt stacks. `scripts/compose-smoke.sh` was first changed (commit 9daf588) so its GRACE check no longer passes `-t 60` and its exit trap always logs in again (review findings WR-02, WR-03).

**Item 1 — gateway stop under the configured grace period, with requests in flight: passed.**
Five concurrent request loops ran against `http://127.0.0.1:8080/api/orders` while each gateway was stopped with `docker compose stop <service>` (no `-t`). `Config.StopTimeout` was 45 on every gateway container.

| Profile | Service | Exit code | Elapsed |
|---|---|---|---|
| mvp | gateway | 0 | 0.46s |
| hardened | gateway | 0 | 0.59s |
| distributed | gateway-1 | 0 | 0.28s |
| distributed | gateway-2 | 0 | 0.32s |
| distributed | gateway-3 | 0 | 0.32s |

Each gateway returned to healthy afterwards. Limit of this evidence: the in-flight requests were short (unauthenticated requests answered in milliseconds), so the drain finished almost immediately. A drain that needs most of the 30s window was not produced; that margin still rests on the code arithmetic (30s drain + 5s metrics shutdown < 45s).

**Item 2 — reproduce the live evidence: passed.**
From `down -v --remove-orphans` and `up -d --build --wait` on each profile: seed exit 0 (`seed complete: 5 routes, policy mvp-authz, snapshot version 2`), smoke exit 0 with 0 hard failures on mvp, hardened and distributed, `A4 RESULT: PASS (HTTP 403)` on all three, `wal.cursor` advanced for audit-worker-1, -2 and -3 on distributed, and `go test -race ./tests/integration/ -run 'TestMVPEndToEnd|TestBackendBypassPrevention'` ok against the fresh mvp stack. No quarantine keys remained in Redis. All stacks were torn down; no non-Aegis container was touched.

**Regression note.** `go test ./...` (excluding `tests/integration`) passes with disk space restored, except `benchmarks/TestPolicyEngine_LatencyBudget`, which exceeded its 2ms p99 budget twice when run inside the full parallel suite on a loaded machine (load average about 11) and passed three times out of three in isolation (p99 0.31 to 0.64 ms). No Go source changed in this phase; this is a timing-sensitive test, not a regression.

## Gaps Summary

No gaps (no truth FAILED, no artifact missing or stubbed, no key link broken, no over-claim). All five success criteria are supported by static evidence reproduced here. Status is `human_needed` solely because the 45s grace setting was never exercised live and the live results are recorded-not-reproduced evidence.

## Tech debt carried forward

- Control plane (demo `admin`/`admin-secret` operator), demo-issuer and HAProxy stats published on all interfaces in hardened and distributed (WR-01, IN-08); the "hardened" profile has no actual hardening options; test asserts the exposure and does not check postgres/redis unpublished outside mvp.
- Smoke script: `stop -t 60` masks grace period (WR-02), fragile cleanup (WR-03), non-gating A4 (WR-04), restart without health wait (IN-04), bash 4 dependency (IN-03).
- Hermetic test: no healthcheck/dependency cross-check or cycle check (WR-05); substring `ro` match and unchecked `/certs` read-only (IN-07).
- Seed: re-run reapplies repo policy over operator edits (IN-06); accepts an empty route set (IN-05); POL-03 remains partial.
- Dockerfile: unpinned base image, root runtime, no `.dockerignore`, unused `COPY policies` in gateway and control-plane (IN-09).
- Still open and out of scope for this phase: demo issuer emits no jti, percent-encoded SPIFFE quarantine keys, B7 `audit.NewLogger(nil)`, B8 quarantine reconstruction and key rotation wiring, B2, B5, B6, DIST-01 two-phase drain, `tests/integration` unconditional under `go test ./...`. REV-03 and AUD-03 stay unsatisfied.

---

_Verified: 2026-10-09_
_Verifier: Claude (gsd-verifier)_
