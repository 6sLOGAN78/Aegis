---
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
plan: 03
subsystem: infra
tags: [docker-compose, mvp, hardened, control-plane, redis, seed, audit-gaps]

requires:
  - phase: 07-01
    provides: seed Dockerfile target and seed.sh
  - phase: 07-02
    provides: hermetic compose lint test (tests/compose)
provides:
  - "docker-compose.mvp.yml rewritten to the current topology (postgres, redis, control-plane, seed, gateway, audit-worker plus demo-issuer and backends)"
  - "docker-compose.hardened.yml with B4 fix, seed gate, 45s gateway grace period, healthchecks"
affects: [07-04, 07-05, 07-06]

tech-stack:
  added: []
  patterns:
    - "gateway depends_on control-plane service_healthy + seed service_completed_successfully"
    - "control-plane healthcheck via curl without -f so a 401 counts as up"

key-files:
  created: []
  modified:
    - deployments/compose/docker-compose.mvp.yml
    - deployments/compose/docker-compose.hardened.yml

key-decisions:
  - "mvp publishes only gateway 8080/9443 and demo-issuer 8085; control plane uses expose only (8084/9090 not on host)"
  - "hardened keeps its pre-existing 8084/9090 host publication (unchanged, flagged tech debt)"
  - "AEGIS_UPSTREAM_SCHEME=https kept (live in internal/proxy/router.go); AEGIS_ROUTES_PATH/AEGIS_POLICY_PATH removed (read by no binary)"

requirements-completed: []
requirements-partial: [REV-03, AUD-03]

duration: 8min
completed: 2026-10-08
---

# Phase 7 Plan 03: MVP and hardened compose alignment Summary

**The mvp and hardened compose files now describe the current binaries: control plane with Redis (B4), one-shot seed gating the gateway, explicit snapshot/Redis/spool wiring, no dead env, 45s gateway grace period and readiness healthchecks.**

## Tasks

| Task | Name | Commit |
|------|------|--------|
| 1 | Rewrite docker-compose.mvp.yml to the current binaries (B1, B4) | 8f6c039 |
| 2 | Apply B4, dead-env, grace, healthcheck and seed fixes to hardened | c56babf |

## What changed

- **mvp (full rewrite, same path, standalone):** added postgres, redis, control-plane (`AEGIS_REDIS_ADDR=redis:6379`, healthcheck, `expose` only), seed, audit-worker (rw spool mount, waits on postgres and control-plane health). Gateway got `AEGIS_CONTROL_PLANE_GRPC_ADDR`, `AEGIS_REDIS_ADDR`, `AEGIS_SPOOL_DIR` plus the `aegis_wal_spool` volume, `stop_grace_period: 45s`, `/readyz` healthcheck, and long-form `depends_on` (control-plane healthy, seed completed, redis healthy, backends started). Dead `AEGIS_ROUTES_PATH`/`AEGIS_POLICY_PATH` removed. demo-issuer and backends unchanged.
- **hardened (in-place edits, +45/-3):** control-plane gained `AEGIS_REDIS_ADDR` and healthcheck; new seed service between control-plane and gateway; gateway lost the two dead env vars and gained grace period, healthcheck, control-plane `service_healthy` and seed `service_completed_successfully`; audit-worker now depends on control-plane health. postgres, redis, demo-issuer, backends untouched; published ports unchanged.

## Verification

- `docker compose config -q` passes for both files (parse only, nothing started).
- Services: mvp and hardened both resolve to admin, audit-worker, control-plane, demo-issuer, gateway, orders, payments, postgres, redis, seed.
- mvp host-published services: only demo-issuer and gateway.
- `go test ./tests/compose/`: all `mvp` and `hardened` subtests PASS, including TestMVPTopology, TestControlPlaneHasRedis, TestNoDeadEnv, TestGatewayWiring, TestAuditWorkerPerSpool, TestGatewayStopGracePeriod, TestSeedService, TestControlPlaneHealthcheck, TestPortsMatchBinaries. `distributed` subtests remain FAIL as expected (plan 07-04). TestSeedDockerfileTarget passes.
- `go build ./...` and `go vet ./...` pass.
- Not verified here: any live behavior (container health, 200 on `/api/orders`, quarantine 200, readiness). No Docker containers were started, stopped or built. That is plans 07-05 and 07-06.

## Deviations from Plan

None - plan executed exactly as written.

## Known Stubs

None.

## Threat Flags

None. No new network surface; mvp publishes fewer ports than hardened by design.

## Notes

- REV-03 and AUD-03 are not marked complete: jti in the demo issuer, percent-encoded SPIFFE quarantine keys, B7 and B8 remain open. REQUIREMENTS.md untouched.
- No changes under cmd/ or internal/, no edits to tests/compose/compose_test.go, docker-compose.distributed.yml untouched.

## Self-Check: PASSED

- FOUND: deployments/compose/docker-compose.mvp.yml
- FOUND: deployments/compose/docker-compose.hardened.yml
- FOUND commits: 8f6c039, c56babf
