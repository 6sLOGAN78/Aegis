---
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
plan: 02
subsystem: infra
tags: [docker-compose, testing, smoke, makefile, audit-gaps]

requires:
  - phase: 07-01
    provides: seed Dockerfile target (deployments/compose/seed/seed.sh)
provides:
  - Hermetic compose lint test (tests/compose) that is red today for B1, B3, B4
  - Live compose smoke script (scripts/compose-smoke.sh) for mvp, hardened, distributed
  - Makefile compose-up that builds and waits; compose-test and compose-smoke targets
affects: [07-03, 07-04, 07-05, 07-06]

tech-stack:
  added: []
  patterns:
    - "Hermetic YAML lint with yaml.v3 + testify, one t.Run per compose profile"
    - "Smoke runs control-plane and issuer calls through docker compose exec, no new host ports"

key-files:
  created:
    - tests/compose/compose_test.go
    - scripts/compose-smoke.sh
  modified:
    - Makefile

key-decisions:
  - "Assertions are never weakened; plans 07-03/07-04 must change the YAML to turn the test green"
  - "A4 (quarantine denial) is reported as an explicit non-fatal result line and does not affect the exit code"
  - "Smoke script never starts or tears down the stack; callers own up/down"

patterns-established:
  - "Failure messages name the audit blocker (B1, B3, B4) so a red run is self-explanatory"

requirements-completed: []
requirements-partial: [REV-03, AUD-03]

duration: 8min
completed: 2026-10-07
---

# Phase 7 Plan 02: Compose verification artifacts Summary

**Hermetic YAML lint test (red against today's compose files), a live smoke script with an explicit non-fatal A4 line, and a Makefile whose `compose-up` now builds fresh images and waits for health.**

## Performance

- **Duration:** ~8 min
- **Completed:** 2026-10-07
- **Tasks:** 3
- **Files:** 3 (2 created, 1 modified)

## Accomplishments

- `tests/compose/compose_test.go` (484 lines, 10 Test functions, one subtest per profile) runs in well under a second with no Docker.
- `scripts/compose-smoke.sh` checks READY, B4 (quarantine 200 not 503), B1 (developer 200 on `/api/orders`, 403 on `/api/admin/users`), per-worker `wal.cursor` advance (two samples around a second request batch), A4 as a non-fatal line, and an optional `--stop-check` that requires exit code 0.
- `make compose-up` is now `up -d --build --wait`, closing the stale-image path behind `make test-e2e` (RESEARCH Pitfall 1).

## Task Commits

1. **Task 1: Hermetic compose lint test (RED)** - `ec8d56a` (test)
2. **Task 2: Live compose smoke script** - `251ff2a` (feat)
3. **Task 3: Makefile targets** - `4396ea5` (chore)

## RED result recorded against the unmodified compose files

Run: `go test -count=1 ./tests/compose/ -run '^<Name>$'` per test.

| Test | Result | Why it fails today |
|---|---|---|
| TestControlPlaneHasRedis | FAIL (mvp, hardened, distributed) | mvp has no control-plane or redis service; hardened and distributed control-plane lack `AEGIS_REDIS_ADDR` (B4) |
| TestMVPTopology | FAIL | mvp is missing postgres, redis, control-plane, seed, audit-worker; gateway has no control-plane/Redis/spool wiring; no `postgres_data` or `aegis_wal_spool` volume (B1/B3) |
| TestNoDeadEnv | FAIL (all three) | gateways still set `AEGIS_ROUTES_PATH` and `AEGIS_POLICY_PATH` (B1) |
| TestGatewayWiring | FAIL (all three) | mvp gateway lacks control-plane, Redis and spool env; hardened and distributed gateways carry the two dead keys that are not in the gateway allowlist |
| TestAuditWorkerPerSpool | FAIL (all three) | mvp has no worker; distributed has 1 worker for 3 gateway spools (only spool 1 is drained); hardened and distributed workers do not depend_on control-plane (B3) |
| TestGatewayStopGracePeriod | FAIL (all three) | no `stop_grace_period` set anywhere, so Docker's default 10s would SIGKILL a 30s drain |
| TestSeedService | FAIL (all three) | no `seed` service and no gateway `service_completed_successfully` dependency (B1) |
| TestControlPlaneHealthcheck | FAIL (all three) | no healthcheck on control-plane or gateway |
| TestPortsMatchBinaries | PASS | published ports already match the binaries |
| TestSeedDockerfileTarget | PASS | satisfied by plan 07-01 (`seed` stage with `curl jq` and `seed.sh`) |

Plans 07-03 (mvp, hardened) and 07-04 (distributed) are expected to turn the eight red tests green by editing the YAML only. The plan listed five tests as the required minimum red set; three more (GatewayWiring, SeedService, ControlPlaneHealthcheck) are also red because the compose files do not yet have the seed, healthchecks or clean env. TestSeedDockerfileTarget is green because of 07-01, not because anything was relaxed.

## Decisions Made

- Assertion messages embed the blocker id (B1, B3, B4) so a red run explains itself.
- The spool check is per volume source: each gateway spool must be mounted by exactly one gateway and exactly one worker, workers read-write.
- The smoke reads the operator session from `Set-Cookie` and sends explicit `Cookie` and `X-CSRF-Token` headers because the session cookie is `Secure`; no cookie jar is used.

## Deviations from Plan

None - plan executed as written. One note: the plan's task-1 verify command expects the whole package to be red only on named tests; the extra red tests listed above are a consequence of the contract in the behavior block, not a change to it.

## Issues Encountered

None. `go vet ./tests/compose/`, `bash -n scripts/compose-smoke.sh`, `make -n compose-up|compose-test|compose-smoke` and `go build ./...` all succeed.

## Known Stubs

None.

## Honesty Notes

- Nothing here proves REV-03 or AUD-03. They stay open (no jti in the demo issuer, percent-encoded SPIFFE quarantine keys, B7 `audit.NewLogger(nil)`, B8 reconstruction). REQUIREMENTS.md was not touched.
- The smoke script was syntax-checked and its argument handling exercised (`bogus-profile` exits 2 with usage and runs no Docker command), but it was deliberately not run against any stack; live proof belongs to plans 07-05/07-06.
- No files under `cmd/`, `internal/` or the compose YAML were changed.

## Threat Flags

None. The smoke script opens no host ports and never prints credentials, session ids or CSRF tokens; a `trap ... EXIT` removes the `usr_developer_01` and `smoke-b4-probe` quarantines.

## Self-Check: PASSED

- FOUND: tests/compose/compose_test.go
- FOUND: scripts/compose-smoke.sh
- FOUND: Makefile (compose-up with `up -d --build --wait`, compose-test, compose-smoke)
- FOUND commits: ec8d56a, 251ff2a, 4396ea5
