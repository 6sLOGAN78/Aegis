---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
verified: 2026-10-11T00:00:00Z
status: gaps_found
score: 17/19 must-haves verified
overrides_applied: 0
gaps:
  - truth: "A healthy idle gateway keeps a fresh last-heartbeat / convergence view reports healthy gateways as converged (B5)"
    status: partial
    reason: "Holds only for a gateway on its first stream. On any stream reconnect (CP restart, gRPC blip) the CP re-pushes the active snapshot (internal/control/server.go initial push). Verifier.VerifySnapshot rejects version <= active (verifier.go:70), client.go sets lastStatus=REJECTED via nextAckState, and every later heartbeat ack echoes REJECTED. HandleListGateways maps REJECTED to partitioned, so healthy gateways show partitioned until the next publish. lastStatus is reset to ACTIVATED at each runStream start, then immediately overwritten by the re-pushed snapshot."
    artifacts:
      - path: "internal/snapshot/client.go"
        issue: "Snapshot case calls ValidateAndActivate on an already-active version and records REJECTED (lines ~216-231); heartbeat echoes it (~248-259)"
    missing:
      - "Treat re-delivery of the already-active version as a no-op/ACTIVATED confirm without changing lastStatus"
      - "Test: reconnect a stream against the same Manager and assert status stays ACTIVATED"
  - truth: "Dashboard author -> validate -> publish -> swap works end to end; each mutation sends a fresh Idempotency-Key (B6, OPS-02, OPS-03)"
    status: partial
    reason: "PolicyStudio.tsx:129 does useRef(crypto.randomUUID()) during render; EmergencyQuarantine.tsx:73,96,115 and PolicyStudio 139,148,158,184 call it in handlers. crypto.randomUUID is undefined in non-secure contexts. The SPA is served over plain HTTP on a host-published port (distributed/hardened compose), so for any non-localhost operator Policy Studio throws during render and quarantine/unquarantine/revoke/rollback all throw. Works on localhost and HTTPS only."
    artifacts:
      - path: "web/dashboard/src/components/PolicyStudio.tsx"
        issue: "crypto.randomUUID() with no fallback, evaluated every render in useRef initializer"
      - path: "web/dashboard/src/components/EmergencyQuarantine.tsx"
        issue: "crypto.randomUUID() with no fallback in incident-response mutations"
    missing:
      - "Shared newIdempotencyKey() helper falling back to crypto.getRandomValues; lazy useRef init; rebuild web/dashboard/dist"
human_verification:
  - test: "make compose-smoke (live, including 65s idle healthy check), then restart control-plane and re-check /control/v1/gateways"
    expected: "Gateways healthy at idle; after CP restart they remain healthy (currently expected to FAIL per CR-01)"
    why_human: "Needs running compose stack; no executor ran it"
  - test: "Dashboard UAT: author, validate, publish, observe convergence, quarantine, via http://localhost and via http://<host-ip>:8084"
    expected: "Works on both origins (currently expected to fail on the non-localhost origin per CR-03)"
    why_human: "Visual/browser flow"
---

# Phase 10 Verification Report

**Phase Goal:** Dashboard author -> validate -> publish -> swap works end to end, and the convergence view shows healthy gateways as converged (B5, B6, CTRL-01, CTRL-02, OPS-01, OPS-02).
**Status:** gaps_found
**Re-verification:** No (initial)

## Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Idle gateway acks each verified lease (heartbeat) | VERIFIED | client.go Lease case sends buildHeartbeatAck from the single Send goroutine |
| 2 | Heartbeat echoes last real outcome (REJECTED not masked) | VERIFIED | lastStatus/lastErr passed to buildHeartbeatAck |
| 3 | Failed lease verification yields no ack | VERIFIED | ack only inside `err == nil` |
| 4 | CP handles Pong without upgrading REJECTED | VERIFIED (WARNING WR-01: non-atomic; Ping never sent in prod) | server.go Pong branch |
| 5 | Convergence view healthy/partitioned thresholds | VERIFIED for first-connect; FAILED after reconnect | see gap 1 |
| 6 | Publish with invalid Rego returns 422, no sign/persist/broadcast | VERIFIED | publish_handler.go + tests (orchestrator: tests pass) |
| 7 | Unknown draft 404; save-draft then publish 200 | VERIFIED | handler tests pass |
| 8 | Snapshots carry identity mappings; carry-forward; rollback preserves | VERIFIED | identity_mappings.go, publish_handler.go, tests pass |
| 9 | nil repos 503, DB errors not leaked | VERIFIED | tests pass |
| 10 | Malformed mappings file rejected; CP exits non-zero | VERIFIED | loader + main.go wiring, compose mounts in 3 profiles |
| 11 | Studio defaults to mvp-authz; save-then-publish; dist has no pol_authz_core | VERIFIED | grep of dist finds no pol_authz_core |
| 12 | 422 shown, fresh key minted after failure | VERIFIED in code; unusable on insecure origins | gap 2 |
| 13 | Each mutation sends fresh Idempotency-Key | PARTIAL | code present but crashes where crypto.randomUUID is absent |
| 14 | Mutating routes return 400 IDEMPOTENCY_KEY_REQUIRED; GETs/validate/simulate not gated | VERIFIED | idempotency_required_test passes |
| 15 | seed.sh / smoke script / tests send keys | VERIFIED | make test + compose tests pass |
| 16 | Router routes <-> OpenAPI spec drift test | VERIFIED | openapi_drift_test passes |
| 17 | Idempotency-Key required; publish 422; IfMatch refs | VERIFIED | spec + drift test |
| 18 | types.gen.go regenerated | VERIFIED | commit 263de7e |
| 19 | End-to-end dashboard flow and healthy convergence (phase goal) | FAILED/PARTIAL | gaps 1 and 2 |

**Score:** 17/19

## Requirements Coverage

| Req | Status | Note |
|-----|--------|------|
| CTRL-01 | SATISFIED | publish-time Rego validation |
| CTRL-02 | SATISFIED | identity mappings in snapshots (CR-02 confidentiality caveat, pre-existing) |
| CTRL-05 | PARTIAL | ack/convergence works first connect, regresses after reconnect (gap 1) |
| OPS-01 | SATISFIED | drift test + spec |
| OPS-02 | PARTIAL | server side satisfied; dashboard client fails on insecure origins |
| OPS-03 | PARTIAL | dashboard flow works on localhost/HTTPS only |

All six IDs appear in PLAN frontmatter and REQUIREMENTS.md; no orphans.

## Anti-Patterns / Review Findings

| Item | Severity | Disposition |
|------|----------|-------------|
| CR-01 reconnect -> REJECTED heartbeats | BLOCKER | gap 1, confirmed in source |
| CR-03 crypto.randomUUID on HTTP | BLOCKER | gap 2, confirmed (8 call sites) |
| CR-02 unauthenticated gRPC, spoofable GatewayId | WARNING for this phase | pre-existing (grpc.NewServer with no creds); phase added Pong path. Track as security debt, not a phase-10 goal failure |
| WR-01..03 (non-atomic Pong guard, client-supplied AcknowledgedAt) | WARNING | follow-up |

## Spot-checks / Not run

Orchestrator-reported: go build, make test, opa 8/8, and listed tests/* packages pass. tests/integration TestBackendBypassPrevention fails identically at 48ce48d (pre-existing). Live compose-smoke and dashboard UAT were not run; they appear under human_verification. I did not re-run Go tests.

## Gaps Summary

Server-side work (CTRL-01, CTRL-02, OPS-01, server OPS-02, B6 publish 404) is solid and test-backed. Two goal-level defects remain. B5 is only achieved until the first stream reconnect, because a re-pushed active snapshot is treated as a monotonic-version rejection and then echoed forever by heartbeats, which recreates the false "partitioned" state the phase set out to remove. B6/OPS-02/OPS-03 work only on secure origins because crypto.randomUUID has no fallback, and the dashboard is served over plain HTTP on a published port. Both fixes are small and specified in 10-REVIEW.md. Recommend a gap-closure plan (`/gsd-plan-phase 10 --gaps`) covering CR-01 and CR-03, with CR-02 logged as deferred security debt.

_Verifier: Claude (gsd-verifier)_
