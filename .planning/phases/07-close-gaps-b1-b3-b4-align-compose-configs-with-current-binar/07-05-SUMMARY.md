---
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
plan: 05
subsystem: infra
tags: [docker-compose, mvp, live-verification, smoke, seed, audit-gaps]

requires:
  - phase: 07-01
    provides: seed Dockerfile target and seed.sh
  - phase: 07-02
    provides: hermetic compose lint test and scripts/compose-smoke.sh
  - phase: 07-03
    provides: rewritten MVP compose (B1, B4)
provides:
  - "Live proof that the rebuilt MVP stack starts from a clean slate, seeds, and passes smoke and integration tests"
  - "Recorded A4 result (quarantined principal denied)"
affects: [07-06]

key-decisions:
  - "No fixes were needed; MVP compose, seed and smoke script worked unchanged on first live run"

requirements-completed: []
requirements-partial: [REV-03, AUD-03]

completed: 2026-10-08
---

# Phase 7 Plan 05: Live MVP verification Summary

**The rebuilt MVP stack came up from `down -v` with `up -d --build --wait` (exit 0), the seed exited 0 with `seed complete`, the smoke script passed every hard check, A4 is PASS, and TestMVPEndToEnd and TestBackendBypassPrevention pass on the fresh images.**

## Teardown approval (Task 2, blocking human-verify)

The user was shown the inventory (stale containers compose-gateway-1, compose-orders-1, compose-payments-1, compose-admin-1, compose-demo-issuer-1; network compose_aegis-internal; no compose_* named volumes; unrelated devrag-stack containers untouched; redis:7.2-alpine pull and eight image builds; no Aegis stack left running at the end) and asked: "Approve tearing down the stale Aegis stack and running the live Docker verification for plans 07-05 and 07-06?"

User response (verbatim, 2026-10-08): **approved** — the exact reply was: "Approved"

Teardown approval for plans 07-05 and 07-06 is recorded here (approved, 2026-10-08).

## Task 1: hermetic pre-flight gate

All green, nothing to commit: `go test -count=1 ./tests/compose/` ok (re-verified at the start of this continuation), `docker compose config -q` for mvp, hardened and distributed, `go build ./...`, `go vet ./...`, `./tests/manifests/...` and `./internal/config/...` tests, `sh -n seed.sh`, `bash -n compose-smoke.sh`, empty `git diff -- cmd internal`.

Pre-teardown inventory: stale pre-Phase-3 stack with project name `compose` held ports 8080 and 9443 (compose-gateway-1) and 8085 (compose-demo-issuer-1); orders/payments/admin had no host ports. No `compose_*` volumes. Unrelated devrag-stack-* containers (app, mailpit, mysql, minio, es01, redis) were running and were not touched.

## Task 3: live results

| Step | Command | Outcome |
|------|---------|---------|
| Certs | present in deployments/certs (root-ca.crt, assertion-ed25519.pub) | no regeneration needed |
| Clean slate | `docker compose -f deployments/compose/docker-compose.mvp.yml down -v --remove-orphans` | removed the 5 stale containers and compose_aegis-internal; 0 `compose-` containers afterward; `ss -ltn` showed 8080, 9443, 8085, 8084, 9090 free (the only holders were the stale compose containers) |
| Build and start | `docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build --wait` | exit 0 on first attempt |
| Service state | `docker compose ... ps -a` | control-plane, postgres, redis, gateway healthy; audit-worker, admin, orders, payments, demo-issuer running; seed exited |
| Seed exit code | `docker inspect -f '{{.State.ExitCode}}' compose-seed-1` | 0 |
| Smoke | `bash scripts/compose-smoke.sh mvp --stop-check` | exit 0, 0 hard failures |
| Gateway restart | `docker compose ... up -d --wait` | gateway healthy again |
| Quarantine residue | `redis-cli --scan --pattern 'quarantine:principal:*'` | empty (0 lines) |
| Integration tests | `go test -count=1 -v -race ./tests/integration/ -run 'TestMVPEndToEnd\|TestBackendBypassPrevention'` | PASS (see below) |
| Teardown | `docker compose ... down -v --remove-orphans` | volumes and network removed; 0 `compose-` containers; 8080, 9443, 8085 free; 6 devrag-stack containers still running |

Assumption A1 (`apk add jq` in the seed image) held: the seed image built and ran.

### Seed log tail

```
seed-1  | === Preparing route payloads ===
seed-1  | === Waiting for control plane at http://control-plane:8084 ===
seed-1  | === Logging in ===
seed-1  | === Seeding routes ===
seed-1  | === Seeding policy ===
seed-1  | === Publishing snapshot ===
seed-1  | seed complete: 5 routes, policy mvp-authz, snapshot version 2
```

### Smoke output

```
=== Aegis compose smoke: profile=mvp gateway=gateway ===
PASS: READY gateway /readyz returned 200
PASS: B4 quarantine POST returned 200
PASS: B4 quarantine DELETE returned 200 (probe removed)
PASS: B1 developer GET /api/orders returned 200
PASS: B1 developer GET /api/admin/users returned 403
PASS: AUDIT audit_events count grew above baseline 1
PASS: AUDIT audit-worker wal.cursor advanced (proves drain wiring per spool, not AUD-03)
A4 RESULT: PASS (HTTP 403)
PASS: GRACE gateway exited 0 on stop
=== Summary: 0 hard failure(s) ===
```

### A4 result

`A4 RESULT: PASS (HTTP 403)` — a quarantined principal was denied at the gateway on the MVP profile. This is a single observation on this stack; it does not close the open items (no jti in demo issuer, percent-encoded SPIFFE quarantine keys, B8).

### Integration tests (fresh images, no retries, single run)

TestBackendBypassPrevention PASS (7/7 subtests): backend microservices have 0 published host ports; direct TCP to backend ports fails; direct HTTP/HTTPS to backends fails; lateral call without client cert fails at TLS; lateral call with workload cert fails authorization; gateway workload mediation on 9443 succeeds; gateway user mediation on 8080 succeeds.

TestMVPEndToEnd PASS (9/9 subtests): developer orders/payments 200; developer create payment 403; developer admin routes 403 with reason code; finance create payments but not read orders; app admin /admin/users 200; path traversal 400; client-injected X-Aegis-User stripped; direct host-to-backend fails (BYP-01); X-Request-ID valid UUID on all responses.

## Deviations from Plan

None. The plan executed as written; no fixes were needed to compose, seed, Dockerfile, smoke script or Makefile, and no commits were made for Task 1 or Task 3. No file under cmd/ or internal/ changed.

## Out-of-scope findings

None observed. Every attempt is recorded above; nothing was rerun.

## Honesty statement

REV-03 and AUD-03 remain unsatisfied. This plan verified B1 and B4 wiring and the per-spool audit drain on the MVP profile only. Still open regardless of results: no jti in the demo issuer, percent-encoded SPIFFE quarantine keys, B7 `audit.NewLogger(nil)`, and B8 reconstruction/key rotation wiring. Hardened and distributed profiles are verified in plan 07-06.

## Known Stubs

None.

## Self-Check: PASSED

Verified before commit: SUMMARY contains `A4 RESULT` and `seed complete`; no `compose-` containers remain; `git diff --name-only -- cmd internal` is empty.
