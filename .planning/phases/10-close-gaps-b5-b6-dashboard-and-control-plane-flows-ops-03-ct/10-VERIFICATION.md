---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
verified: 2026-10-11T12:00:00Z
status: human_needed
score: 19/19 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 17/19
  gaps_closed:
    - "B5 reconnect regression (CR-01): re-delivered active snapshot no longer flips gateway to REJECTED (plan 10-07)"
    - "Idempotency key on insecure origins (CR-03): newIdempotencyKey() with getRandomValues fallback (plan 10-08)"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "make compose-smoke (incl. 65s idle healthy check), then restart control-plane and re-check /control/v1/gateways"
    expected: "Gateways stay healthy at idle and after CP restart"
    why_human: "Needs running compose stack"
  - test: "Dashboard UAT: author, validate, publish, observe convergence, quarantine via http://localhost and http://<host-ip>:8084"
    expected: "Works on both origins"
    why_human: "Visual/browser flow on a non-secure origin"
---

# Phase 10 Verification Report

**Phase Goal:** Dashboard author -> validate -> publish -> swap works end to end, and the convergence view shows healthy gateways as converged (B5, B6, CTRL-01, CTRL-02, OPS-01, OPS-02).
**Status:** human_needed
**Re-verification:** Yes - after gap closure plans 10-07 and 10-08

## Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Idle gateway acks each verified lease (heartbeat) | VERIFIED | client.go Lease case sends buildHeartbeatAck from the single Send goroutine |
| 2 | Heartbeat echoes last real outcome (REJECTED not masked) | VERIFIED | lastStatus/lastErr passed to buildHeartbeatAck |
| 3 | Failed lease verification yields no ack | VERIFIED | ack only inside `err == nil` |
| 4 | CP handles Pong without upgrading REJECTED | VERIFIED (WARNING WR-01: non-atomic; Ping never sent in prod) | server.go Pong branch |
| 5 | Convergence view healthy/partitioned thresholds | VERIFIED | first connect and after reconnect |
| 6 | Publish with invalid Rego returns 422, no sign/persist/broadcast | VERIFIED | publish_handler.go + tests (orchestrator: tests pass) |
| 7 | Unknown draft 404; save-draft then publish 200 | VERIFIED | handler tests pass |
| 8 | Snapshots carry identity mappings; carry-forward; rollback preserves | VERIFIED | identity_mappings.go, publish_handler.go, tests pass |
| 9 | nil repos 503, DB errors not leaked | VERIFIED | tests pass |
| 10 | Malformed mappings file rejected; CP exits non-zero | VERIFIED | loader + main.go wiring, compose mounts in 3 profiles |
| 11 | Studio defaults to mvp-authz; save-then-publish; dist has no pol_authz_core | VERIFIED | grep of dist finds no pol_authz_core |
| 12 | 422 shown, fresh key minted after failure | VERIFIED | fallback helper |
| 13 | Each mutation sends fresh Idempotency-Key | VERIFIED | helper with fallback |
| 14 | Mutating routes return 400 IDEMPOTENCY_KEY_REQUIRED; GETs/validate/simulate not gated | VERIFIED | idempotency_required_test passes |
| 15 | seed.sh / smoke script / tests send keys | VERIFIED | make test + compose tests pass |
| 16 | Router routes <-> OpenAPI spec drift test | VERIFIED | openapi_drift_test passes |
| 17 | Idempotency-Key required; publish 422; IfMatch refs | VERIFIED | spec + drift test |
| 18 | types.gen.go regenerated | VERIFIED | commit 263de7e |
| 19 | End-to-end dashboard flow and healthy convergence (phase goal) | VERIFIED (code) | live UAT pending |

**Score:** 19/19

## Re-verification (gap closure)

| Prior gap | Status now | Evidence |
|-----------|-----------|----------|
| Gap 1 / CR-01 reconnect -> REJECTED heartbeats (truths 5, 19) | CLOSED | client.go processSnapshot (l.299-330): if Version and PayloadSha256 match manager.Active(), returns ACTIVATED ack and returns prevStatus/prevErr unchanged; PayloadSha256 stored in ActiveState (manager.go:143) from an envelope whose digest verifier.go:77 already checked. Otherwise full ValidateAndActivate, so older/different snapshots still REJECTED. Snapshot case in runStream calls it (l.219). `go test -race -count=1 ./internal/snapshot/... ./internal/control/...` pass. |
| Gap 2 / CR-03 crypto.randomUUID on HTTP (truths 12, 13) | CLOSED | web/dashboard/src/api/idempotency.ts feature-detects randomUUID, falls back to getRandomValues v4. grep: randomUUID only inside idempotency.ts; PolicyStudio publishKeyRef lazily initialised (l.130-131). `npx tsc -b` clean; dist bundle contains getRandomValues and is tracked/rebuilt. |

Residual note: the optional bufconn reconnect test was skipped in 10-07; coverage is at the processSnapshot-to-heartbeat unit level (three passing tests), not a live stream reconnect.

## Requirements Coverage

All six IDs (OPS-03, CTRL-01, CTRL-02, CTRL-05, OPS-01, OPS-02) appear in PLAN frontmatter and REQUIREMENTS.md; no orphans. (REQUIREMENTS.md checkboxes for all but OPS-01 are still unchecked; traceability table says "Gap closure". Orchestrator should tick them on phase completion.)

| Req | Status | Note |
|-----|--------|------|
| CTRL-01 | SATISFIED | publish-time Rego validation, 422 |
| CTRL-02 | SATISFIED | identity mappings in snapshots, carry-forward, rollback |
| CTRL-05 | SATISFIED | heartbeat ack + reconnect idempotency |
| OPS-01 | SATISFIED | drift test + spec |
| OPS-02 | SATISFIED | server gating + client key works on insecure origins |
| OPS-03 | SATISFIED in code | live browser/compose confirmation pending (human) |

## Anti-Patterns / Review Findings

| Item | Severity | Disposition |
|------|----------|-------------|
| CR-01 reconnect -> REJECTED heartbeats | resolved | plan 10-07 |
| CR-03 crypto.randomUUID on HTTP | resolved | plan 10-08 |
| CR-02 unauthenticated gRPC, spoofable GatewayId | WARNING for this phase | pre-existing (grpc.NewServer with no creds); phase added Pong path. Track as security debt, not a phase-10 goal failure |
| WR-01..03 (non-atomic Pong guard, client-supplied AcknowledgedAt) | WARNING | follow-up |

## Spot-checks / Not run

Orchestrator-reported: go build, make test, opa 8/8, and listed tests/* packages pass. tests/integration TestBackendBypassPrevention fails identically at 48ce48d (pre-existing). Live compose-smoke and dashboard UAT were not run; they appear under human_verification. I did not re-run Go tests.

## Gaps Summary

No blocking gaps remain. Out-of-scope judgments: CR-02 (unauthenticated gRPC / spoofable GatewayId) is pre-existing security debt in grpc.NewServer without credentials and does not defeat the phase goal; track as deferred. WR-03 (gateway that rejects the CP's current version fails lease verification, so no later REJECTED heartbeat) is benign: the REJECTED ack is sent immediately on snapshot receipt and the gateway then ages to partitioned, which is the correct outcome for a non-converging replica. WR-01/02 are follow-ups. Status is human_needed only because live compose-smoke and browser UAT on a non-localhost origin were not run.

_Verifier: Claude (gsd-verifier)_
