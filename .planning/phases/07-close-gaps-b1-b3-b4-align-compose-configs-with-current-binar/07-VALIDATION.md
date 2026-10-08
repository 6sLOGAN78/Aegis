---
phase: 7
slug: close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
status: complete
nyquist_compliant: true
wave_0_complete: true
created: 2026-10-08
---

# Phase 7 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` + testify (require/assert) + gopkg.in/yaml.v3; Go 1.26.0 |
| **Config file** | none — plain `go test` packages (module `aegis`) |
| **Quick run command** | `go test -count=1 ./tests/compose/... ./tests/manifests/... ./internal/config/...` |
| **Full suite command** | `go build ./... && go vet ./... && go test -count=1 ./...` |
| **Static config check** | `for f in mvp hardened distributed; do docker compose -f deployments/compose/docker-compose.$f.yml config -q; done` |
| **Live (docker) check** | `docker compose -f deployments/compose/docker-compose.mvp.yml down -v && docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build --wait && go test -count=1 -v -race ./tests/integration/ -run 'TestMVPEndToEnd|TestBackendBypassPrevention'` |
| **Estimated runtime** | ~15 seconds quick; several minutes for the live check (image builds) |

---

## Sampling Rate

- **After every task commit:** Run `go test -count=1 ./tests/compose/...` plus `docker compose -f <edited file> config -q`
- **After every plan wave:** Static check on all three compose files + `go build ./... && go vet ./...` + `go test -count=1 ./tests/manifests/... ./internal/config/...`
- **Before `/gsd:verify-work`:** Clean `down -v` + `up -d --build --wait` of mvp and distributed, the live smoke steps, `TestMVPEndToEnd` / `TestBackendBypassPrevention` green on a fresh image, then `go test -count=1 ./...`
- **Max feedback latency:** 30 seconds for hermetic checks

---

## Per-Task Verification Map

Task IDs use the form `07-PP-TT` (plan, task). Owners were assigned at planning time; Status is updated by plan 07-06 Task 3 from recorded outcomes.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 07-02-01 (test), 07-03-01, 07-03-02, 07-04-01 (fix) | 02, 03, 04 | 1, 2 | REV-03 (B4) | — | control-plane sets `AEGIS_REDIS_ADDR` in mvp, hardened, distributed | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run TestControlPlaneHasRedis` | ✅ | ✅ green |
| 07-02-02 (script), 07-05-03, 07-06-01, 07-06-02 (live) | 02, 05, 06 | 1, 3, 4 | REV-03 (B4) | — | quarantine returns 200, not 503, through the shipped stack | live smoke | login + `POST /control/v1/principals/smoke-b4-probe/quarantine` → 200 (smoke script) | ✅ | ✅ green |
| 07-02-02 (script), 07-05-03, 07-06-01, 07-06-02 (live; record, do not fix) | 02, 05, 06 | 1, 3, 4 | REV-03 (B4) | — | gateway denies the quarantined principal (unverified in research, assumption A4) | live smoke | token for quarantined subject, request `/api/orders` → 403 | ✅ | ✅ PASS recorded (HTTP 403 on mvp, hardened, distributed); single observation, not a closure of REV-03 |
| 07-02-01 (test), 07-04-02 (fix) | 02, 04 | 1, 2 | AUD-03 (B3) | — | one audit-worker per gateway spool; each volume mounted by exactly one gateway and one worker | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run TestAuditWorkerPerSpool` | ✅ | ✅ green |
| 07-02-02 (script), 07-06-02 (live) | 02, 06 | 1, 4 | AUD-03 (B3) | — | events from all 3 gateways reach `audit_events` | live smoke | requests through HAProxy, then `select count(*) from audit_events` increases; `wal.cursor` present in all 3 volumes | ✅ | ✅ green |
| 07-02-01 (test), 07-01-01, 07-01-02 (seed), 07-03-01 (fix) | 01, 02, 03 | 1, 2 | B1 / BYP-01 / DIST-03 | — | mvp has control-plane, redis, postgres, audit-worker and gateway env wiring; no dead env vars | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run 'TestMVPTopology|TestNoDeadEnv'` | ✅ | ✅ green |
| 07-01-01, 07-01-02 (seed), 07-02-03 (make compose-up), 07-05-03 (live) | 01, 02, 05 | 1, 3 | B1 | — | MVP E2E and bypass tests pass on a rebuilt image | live integration | `make test-e2e` after `compose-up` becomes `up -d --build --wait` | ✅ | ✅ green |
| 07-02-01 (test), 07-03-01, 07-03-02, 07-04-01 (fix), 07-06-02 (live stop) | 02, 03, 04, 06 | 1, 2, 4 | DIST-01 (grace) | — | every gateway `stop_grace_period >= drain + 5s` | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run TestGatewayStopGracePeriod` | ✅ | ✅ green |
| 07-02-01 | 02 | 1 | port drift | — | compose ports equal binary defaults | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run TestPortsMatchBinaries` | ✅ | ✅ green |
| 07-03-01, 07-03-02, 07-04-01, 07-04-02, 07-05-01 | 03, 04, 05 | 2, 3 | all | — | compose files parse | static | `docker compose -f ... config -q` | n/a | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

Statuses transcribed from recorded outcomes (07-05-SUMMARY.md, 07-06-SUMMARY.md): hermetic rows green in `go test ./tests/compose/` (10 tests); live rows green on mvp, hardened and distributed on the first attempt each; gateway-1/2/3 `stop -t 60` exit 0. `nyquist_compliant: true` because every row has an automated command and a recorded result. Limitation: the live rows are smoke evidence (rows rose, cursors advanced), not coverage of the requirements they reference.

### Open after Phase 7

- REV-03 and AUD-03 remain unsatisfied (and unchecked in REQUIREMENTS.md): the demo issuer emits no jti, SPIFFE quarantine keys are percent-encoded, B8 (ReconstructQuarantines / key rotation not wired), and B7 (`audit.NewLogger(nil)`: only pre-forward allow events reach the WAL; denials and completions are stdout-only).
- B2, B5, B6 untouched.
- POL-03: the seed container is a workaround, not product behavior.
- DIST-01 two-phase drain not implemented; the grace period only covers the existing 30s drain.
- `tests/integration` is still unconditional under `go test ./...` (calls ensureComposeCluster and starts a stack).

---

## Wave 0 Requirements

- [x] `tests/compose/compose_test.go` — new package `compose` with the hermetic assertions above; reuse the `loadYAML` helper pattern from `tests/manifests/manifest_test.go`
- [x] `deployments/compose/seed/seed.sh` (+ Dockerfile `seed` target) — idempotent seed of routes and policy through the control-plane REST API
- [x] `scripts/compose-smoke.sh` — login, quarantine 200, developer allow, admin deny, audit row count
- [x] Makefile: `compose-up` → `up -d --build --wait`
- [x] No framework install needed (yaml.v3 already in `go.mod`)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Gateway exits 0 within the grace period | DIST-01 (grace) | Needs a running stack and a timed stop | `docker compose stop gateway-1`, then `docker inspect -f '{{.State.ExitCode}}'` expects 0 |
| A stale pre-Phase-3 MVP stack is not answering on 8080/9443/8085 before the live check | B1 | Environment state on the developer machine | `docker compose -f deployments/compose/docker-compose.mvp.yml down` before verifying |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-10-08 (plan 07-06 close-out)
