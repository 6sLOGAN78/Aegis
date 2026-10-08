---
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
plan: 04
subsystem: infra
tags: [docker-compose, distributed, audit-worker, wal-spool, redis, seed, audit-gaps]

requires:
  - phase: 07-01
    provides: seed Dockerfile target and seed.sh
  - phase: 07-02
    provides: hermetic compose lint test (tests/compose)
provides:
  - "docker-compose.distributed.yml with three per-spool audit workers (B3), control-plane Redis address (B4), seed gate, gateway grace period and readiness healthchecks"
affects: [07-05, 07-06]

tech-stack:
  added: []
  patterns:
    - "x-audit-worker YAML anchor with per-service volumes override (shallow merge)"
    - "one audit worker per gateway spool; spool volumes never shared"

key-files:
  created: []
  modified:
    - deployments/compose/docker-compose.distributed.yml

key-decisions:
  - "Three audit workers (audit-worker-1/2/3), each mounting exactly one spool rw, instead of a multi-directory worker (no Go change)"
  - "Anchor merge verified with docker compose config: env, depends_on and networks survive, volumes set per worker"

requirements-completed: []
requirements-partial: [REV-03, AUD-03]

duration: 6min
completed: 2026-10-08
---

# Phase 7 Plan 04: Distributed compose alignment Summary

**The distributed profile now has one rw-mounted audit worker per gateway spool (B3), the control plane has `AEGIS_REDIS_ADDR=redis:6379` (B4), and gateways wait for the seed with a 45s grace period and a /readyz healthcheck.**

## Tasks

| Task | Name | Commit |
|------|------|--------|
| 1 | Control-plane B4 fix, seed, gateway-1/2/3 edits | 97fc1d2 |
| 2 | Split audit-worker into audit-worker-1/2/3 (B3) | 97fc1d2 |

Both tasks edit the same single file and were interdependent (the audit-worker anchor depends on the control-plane healthcheck added in Task 1), so they were committed together as one commit.

## What changed

- control-plane: added `AEGIS_REDIS_ADDR=redis:6379` and a healthcheck (curl without `-f`, 401 counts as up). Ports 8084/9090/9092 unchanged.
- New `seed` service (target `seed`, restart "no", demo credential commented), gated on postgres, redis and control-plane healthy.
- gateway-1/2/3: removed dead `AEGIS_ROUTES_PATH` / `AEGIS_POLICY_PATH`; added `stop_grace_period: 45s`, `/readyz` healthcheck; depends_on control-plane `service_healthy` and seed `service_completed_successfully`. Each keeps its own gateway id and spool volume. No gateway port published.
- `audit-worker` replaced by `audit-worker-1/2/3` via an `x-audit-worker` anchor; each overrides only `volumes` with its own `aegis_wal_spool_N:/var/log/aegis/wal` (read-write). Workers also wait for control-plane healthy.
- HAProxy, demo-issuer, backends, postgres, redis and the top-level volumes list are untouched.

## Verification

- `docker compose -f deployments/compose/docker-compose.distributed.yml config -q`: OK
- `go test -count=1 ./tests/compose/...`: ok (all profiles: mvp, hardened, distributed, distributed-backends)
- jq checks: each worker has exactly its own spool volume, none read_only, merged `AEGIS_DB_HOST=postgres` present, published services are exactly `control-plane` and `haproxy`.
- grep counts: `stop_grace_period: 45s` = 3, `AEGIS_REDIS_ADDR=redis:6379` = 4, dead env = 0.
- `git diff --numstat -- cmd internal` empty.

## Deviations from Plan

None - plan executed as written. The anchor merge worked on this Compose version, so no fallback to fully written-out services was needed. (A first scripted edit attempt produced invalid YAML because of a duplicated block; it was reverted with `git checkout` of that one file before any commit and redone.)

## Known Stubs

None.

## Notes

- Does not satisfy AUD-03 or REV-03: B7, jti, encoded SPIFFE keys and B8 remain open. Live drain/ingestion is proven only in plan 07-06.
- Cost accepted (T-07-20): three worker DB pools, about 30 connections against the assumed Postgres default of 100.

## Self-Check: PASSED

- deployments/compose/docker-compose.distributed.yml modified: FOUND
- commit 97fc1d2: FOUND
