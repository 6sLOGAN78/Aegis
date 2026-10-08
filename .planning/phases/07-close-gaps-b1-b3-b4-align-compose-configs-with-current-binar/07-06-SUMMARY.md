---
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
plan: 06
subsystem: infra
tags: [docker-compose, hardened, distributed, haproxy, audit-worker, live-verification, grace-period]

requires:
  - phase: 07-04
    provides: distributed compose with per-spool audit workers
  - phase: 07-05
    provides: MVP live proof and recorded teardown approval
provides:
  - "Live proof that hardened and distributed stacks start from down -v, seed, pass smoke hard checks and stop with exit 0"
  - "Per-worker wal.cursor evidence for audit-worker-1/2/3 (B3 drain wiring)"
  - "Measured stop results for gateway-1/2/3"
  - "Populated 07-VALIDATION.md"
affects: []

key-decisions:
  - "No fixes were needed: both profiles passed unchanged on the first live attempt"

requirements-completed: []
requirements-partial: [REV-03, AUD-03]

completed: 2026-10-08
---

# Phase 7 Plan 06: Live hardened + distributed verification Summary

**Hardened and distributed stacks both came up from `down -v --remove-orphans` with `up -d --build --wait` (exit 0), seeded (exit 0), passed every smoke hard check on the first attempt, and all four gateways (hardened gateway, distributed gateway-1/2/3) stop with exit 0. REV-03 and AUD-03 remain unsatisfied.**

## Precondition

07-05-SUMMARY.md records the teardown approval (user reply "Approved", 2026-10-08). At start no `compose-` container existed; the only running containers were the unrelated devrag-stack-* set, which were never touched. All required host ports were free.

## Task 1: hardened (07-06-01)

| Step | Outcome |
|------|---------|
| `down -v --remove-orphans` | no stack present; ports 8080, 9443, 8085, 8084, 9090 free |
| `up -d --build --wait` | exit 0, first attempt, about 46 s (images were cached from earlier plans) |
| seed exit code | 0 (`seed complete: 5 routes, policy mvp-authz, snapshot version 2`) |
| `bash scripts/compose-smoke.sh hardened --stop-check` | exit 0, 0 hard failures |
| teardown | volumes and network removed, 0 `compose-` containers, ports free |

Smoke PASS lines: READY /readyz 200; B4 quarantine POST 200 and DELETE 200; B1 developer GET /api/orders 200 and GET /api/admin/users 403; AUDIT audit_events count grew above baseline 1; AUDIT audit-worker wal.cursor advanced; GRACE gateway exited 0 on stop.

```
A4 RESULT: PASS (HTTP 403)
```

## Task 2: distributed (07-06-02)

| Step | Outcome |
|------|---------|
| clean start | `down -v --remove-orphans`; ports 8080, 9443, 8404, 8084, 9090, 9092 free |
| `up -d --build --wait` | exit 0, first attempt; seed exit code 0; gateway-1/2/3, control-plane, postgres, redis, haproxy, audit-worker-1/2/3 healthy or running |
| HAProxy stats CSV (aegis_http_back) | gateway-1 UP, gateway-2 UP, gateway-3 UP, BACKEND UP |
| `bash scripts/compose-smoke.sh distributed --stop-check` | exit 0, 0 hard failures |

Smoke PASS lines: READY via HAProxy 200; B4 quarantine POST 200 / DELETE 200; B1 200 and 403; AUDIT count grew above baseline 1; AUDIT wal.cursor advanced for audit-worker-1, audit-worker-2 and audit-worker-3; GRACE gateway-1 exited 0.

```
A4 RESULT: PASS (HTTP 403)
```

### B3 evidence beyond the smoke

`ls -l /var/log/aegis/wal` in each worker (each lists its own spool, one wal file and one cursor):

```
audit-worker-1: wal-01791418320684166838-000001.log (9760 B)  wal.cursor (127 B)
audit-worker-2: wal-01791418320644410261-000001.log (10246 B) wal.cursor (128 B)
audit-worker-3: wal-01791418320698945634-000001.log (9757 B)  wal.cursor (127 B)
```

- `SPOOL_SATURATED` count across gateway-1/2/3 logs: 0.
- Audit-worker logs: 6 lines total, no error/fail/warn lines (no sustained per-tick errors).
- `select count(*) from pg_stat_activity`: 21 (assumption A2, well below 100).
- `select count(*) from audit_events`: 61 after the smoke.

### Gateway stop check, `stop -t 60`, all three gateways

Run as a separate sequence after the smoke (the smoke's own GRACE check already passed for gateway-1; the sequence below was run for all three so each has a recorded exit code and elapsed time):

| Gateway | Exit code | Elapsed | After `start` |
|---------|-----------|---------|---------------|
| gateway-1 | 0 | 0.37 s | healthy |
| gateway-2 | 0 | 0.28 s | healthy |
| gateway-3 | 0 | 0.28 s | healthy |

None returned 137. Elapsed times are short because the gateways were idle at stop time, so the drain had nothing to wait for. This confirms a graceful exit (SIGTERM handled, exit 0) but does not measure the 30s drain under in-flight load; the 45s `stop_grace_period` margin over that drain is asserted by the hermetic test `TestGatewayStopGracePeriod` and by reading cmd/gateway/main.go, not by a loaded live run.

Teardown: `down -v --remove-orphans`; 0 `compose-` containers, 0 `compose_` volumes, published ports free.

## Task 3: final regression and close-out (07-06-03)

- `go build ./...` ok; `go vet ./...` ok.
- `go test -count=1 $(go list ./... | grep -v /tests/integration)` all packages ok (including tests/compose, tests/manifests, tests/chaos, tests/dr, tests/failure, tests/rotation, tests/security). tests/integration deliberately excluded.
- `opa test policies/rego policies/tests`: PASS 8/8.
- `docker compose config -q` for mvp, hardened, distributed: ok. `make -n compose-up compose-test compose-smoke` resolves to the expected commands (`up -d --build --wait`, `go test ./tests/compose/...`, `bash scripts/compose-smoke.sh mvp`).
- `git status --short -- cmd internal pkg services migrations policies`: empty. No Go, policy or migration change in this plan.
- 07-VALIDATION.md populated from the above (commit 7de1d5c): all rows green; A4 row recorded as PASS on mvp, hardened and distributed; `wave_0_complete: true`, `nyquist_compliant: true`; "Open after Phase 7" note added.
- Requirements honesty guard: `.planning/REQUIREMENTS.md` still has `- [ ] **REV-03**` and `- [ ] **AUD-03**` unchecked and both traceability rows read `Gap closure`. The file was not edited by this plan and is not committed (untracked user work). Completion tooling for requirements was not run.

## Deviations from Plan

None. Both profiles passed on the first attempt, nothing was rerun, and no file under deployments/compose, scripts or Makefile needed a fix. The only commit from the tasks is the VALIDATION.md update; Tasks 1 and 2 are live-verification only and produced no file changes.

## Out-of-scope findings

- Gateway stop timing was measured while idle (see above); a loaded drain test was not part of the plan.
- A4 PASS on three profiles is a single observation each. Known open causes (no jti in demo issuer, percent-encoded SPIFFE quarantine keys, B8) are unchanged and could make A4 behave differently with other principals.

## Known Stubs

None.

## Phase result

Now true: B1 (policy and routes seeded, developer 200 / admin 403), B3 (one audit-worker per gateway spool, each cursor present and advancing, audit_events rows rising, no SPOOL_SATURATED) and B4 (quarantine returns 200 not 503) are closed at the compose level and proven live on mvp, hardened and distributed from clean rebuilds. Every gateway stops with exit 0 under `stop -t 60`. The hermetic compose test guards these assertions. A4: `A4 RESULT: PASS (HTTP 403)` on mvp, hardened and distributed.

Not true: REV-03 and AUD-03 remain unsatisfied. The audit evidence is a smoke test (rows rose, cursors exist), not coverage; B7 (`audit.NewLogger(nil)`) means only pre-forward allow events reach the WAL and denials/completions are stdout-only. Still open: no jti in the demo issuer, percent-encoded SPIFFE quarantine keys, B8 (ReconstructQuarantines / key rotation not wired), B2, B5, B6, POL-03 (seed is a workaround), DIST-01 two-phase drain, and `tests/integration` remaining unconditional under `go test ./...`.

Final Docker state: no `compose-` containers, no `compose_` volumes, devrag-stack containers (app, mailpit, mysql, minio, es01, redis) still running and untouched.

## Self-Check: PASSED

Verified: this SUMMARY contains both `A4 RESULT:` lines and a Phase result section; VALIDATION.md commit 7de1d5c exists; `git diff --name-only -- cmd internal` is empty; 0 `compose-` containers.
