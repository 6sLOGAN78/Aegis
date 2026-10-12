---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 15
subsystem: verification
tags: [phase-gate, compose-smoke, dr, key-rotation, mtls]
requires: [11-10, 11-11, 11-13, 11-14]
provides:
  - live evidence for B8 (rotation, DR) and CR-02 (mTLS) on mvp, hardened, distributed
affects: [10-SECURITY.md AR-01]
key-files:
  created: [.planning/todos/pending/distributed-dedupe-flake.md]
  modified: [.planning/phases/10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct/10-SECURITY.md]
decisions:
  - "Distributed first-run DEDUPE failures treated as a pre-existing smoke flake (see todo), not a Phase 11 regression."
metrics:
  completed: 2026-10-12
requirements: [REV-03, AUTH-01, CTRL-02, CTRL-03, CTRL-05]
---

# Phase 11 Plan 15: Phase gate (hermetic suite and live smoke) Summary

Full hermetic suite green and the compose smoke passes on mvp, hardened and distributed with DR, KEYROT-JWT, KEYROT-SNAPSHOT and MTLS sections PASS; AR-01 closure confirmed with live evidence.

## Task 1: Hermetic suite and static gates
- `go build ./...` and `go vet ./...` clean.
- `go test ./... -race -count=1 -p 1`: 31 packages ok. Only `tests/integration` failed, against the stale pre-Phase-11 mvp stack (503 policy-lease-expired); after the stack was rebuilt with `--build` it passed.
- 0 non-test `insecure.NewCredentials` uses in cmd and internal.
- `git status --short deployments/certs deployments/keys` empty before and after the live runs.

## Task 2: Live smoke (one profile at a time, `up -d --build --wait`, smoke, `down -v`)
| Profile | Summary line | PASS (DR/KEYROT/MTLS[/GATEWAYS]) |
|---|---|---|
| mvp | `=== Summary: 0 hard failure(s) ===` | 29 |
| hardened | `=== Summary: 0 hard failure(s) ===` (60 PASS) | 29 |
| distributed, run 1 | `=== Summary: 3 hard failure(s) ===` (DEDUPE worker-1/2/3 only) | all DR/KEYROT/MTLS PASS |
| distributed, rerun (clean `down -v`, no code change) | `=== Summary: 0 hard failure(s) ===` (100 PASS) | 54 |

DR timings: fail-closed window observed (503 SECURITY_STATE_MISSING) on every profile; recovery /readyz, recovered quarantine and recovered revoke all PASS within the 60s bound. Polled figures: mvp 0.12s, hardened 0.13-0.15s, distributed gateway-1/2/3 0.14-0.16s per replica (these include exec latency and are the time since the control call at poll time, so they bound but do not measure the reconstruct duration). Reconstruct ok counter increased (mvp 1 -> 2). KEYROT-JWT and KEYROT-SNAPSHOT passed with no gateway container restart. MTLS: plaintext refused, no-cert refused, authenticated positive control, cert-derived gateway ids, rejection metric exposed.

Disk: 84% -> 86%; pre-flight never exited 3. `docker image prune -f` and `docker builder prune -f` used once between profiles; no volumes or other projects touched. No stack left running.

## Commit isolation
- 16cbd7e chore(11): regenerate dev PKI ... touches only deployments/certs.
- b0daa7e chore(11): add dev JWT and snapshot signing keys and keyset directories touches only deployments/keys.

## Deviations from Plan
None to code. One live flake:

**DEDUPE flake on distributed (first run).** audit_events counts 125 -> 159 -> 187 -> 216 across the three worker checks. The monotonic growth suggests the count was sampled before traffic had stopped (drain still in progress), not duplicate inserts from replay; cause unconfirmed. The rerun passed with the count unchanged at 125 on all workers. Pre-existing Phase 9/10 section, unrelated to Phase 11 changes. Tracked in `.planning/todos/pending/distributed-dedupe-flake.md`.

## Task 3: Operator checkpoint
Approved by the operator. AR-01 line in 10-SECURITY.md updated to record the live MTLS PASS (2026-10-12). Live Kubernetes bring-up remains deferred (D-19).

## Known Stubs
None.

## Self-Check: PASSED
