---
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
plan: 01
subsystem: infra
tags: [compose, docker, seed, control-plane, posix-sh, jq, curl]
requires: []
provides:
  - "seed one-shot image (Dockerfile target `seed`) that loads 5 routes + authz.rego into the control plane and publishes a snapshot"
affects: [07-03, 07-04, 07-05]
tech-stack:
  added: []
  patterns: ["POSIX sh REST client with explicit Cookie/X-CSRF-Token headers (no cookie jar)", "jq -n --arg/--rawfile JSON construction"]
key-files:
  created: [deployments/compose/seed/seed.sh]
  modified: [deployments/compose/Dockerfile]
key-decisions:
  - "Seed shipped (orchestrator default S3) rather than a boots-and-denies profile; a workaround, POL-03 stays partial"
  - "Posted burst = max(burst, rps) because the API rejects burst < rps and routes.json has that on all 5 routes"
  - "Re-runs publish a new snapshot version each time; no skip logic"
requirements-completed: []
duration: 10min
completed: 2026-10-08
---

# Phase 7 Plan 01: Compose seed image Summary

**Idempotent POSIX sh seed (curl + jq) that loads the 5 routes and the Rego policy through the control-plane REST API and publishes a signed snapshot, packaged as a `seed` Dockerfile target.**

## Tasks

| Task | Name | Commit |
|------|------|--------|
| 1 | Write the idempotent seed script | fea6da2 |
| 2 | Add the `seed` target to the compose Dockerfile | c6a5c06 |

## What was built

- `deployments/compose/seed/seed.sh`: env-driven (`AEGIS_CP_URL`, `AEGIS_SEED_USER`, `AEGIS_SEED_PASSWORD`, `AEGIS_SEED_POLICY_DIR`, `AEGIS_SEED_POLICY_ID`, `AEGIS_SEED_WAIT_SECONDS`, `AEGIS_SEED_DRY_RUN`). Credentials are required and checked before any file read or network call (exit 2, names the variable only). Flow: wait for control plane, login, post routes (count asserted), post policy draft, publish with `If-Match: "1"` and one retry using the ETag on 412. Every non-2xx exits 1 so compose can gate the gateway on `service_completed_successfully`.
- `deployments/compose/Dockerfile`: new final stage `seed` (alpine:3.21, curl, jq, seed.sh, `policies/`). Eight lines added, none removed or modified.

## Verification

- `sh -n` passes; dry-run over the real `policies/data/routes.json` yields 5 routes, all with burst >= rps.
- Missing `AEGIS_SEED_PASSWORD` exits 2 immediately with the variable name in the message.
- All grep acceptance checks pass (no cookie jar, no credential literals, `--rawfile` and `jq -n --arg` used, 2 `If-Match` uses, `ETag` handled).
- Beyond the plan: ran the full flow against a local Python mock control plane (cookie and CSRF header assertions, 412 then ETag retry); it ended with `seed complete: 5 routes, policy mvp-authz, snapshot version 5`.
- Not verified here: the actual Docker image build (alpine `jq` availability, assumption A1), covered by plan 07-05. No Docker commands were run.

## Deviations from Plan

None - plan executed exactly as written.

## Known Stubs

None.

## Threat Flags

None.

## Notes

REV-03 and AUD-03 are intentionally not marked complete (partial contribution only). No changes under cmd/ or internal/.

## Self-Check: PASSED
