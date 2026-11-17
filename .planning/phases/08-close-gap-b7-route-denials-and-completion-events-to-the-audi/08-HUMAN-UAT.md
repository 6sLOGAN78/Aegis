---
status: complete
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
source: [08-VERIFICATION.md]
started: 2026-10-09T00:00:00Z
updated: 2026-11-17T00:00:00Z
---

## Current Test

[awaiting human testing]

## Tests

### 1. Accept or reproduce the recorded live MVP run
expected: From `down -v --remove-orphans` and `AEGIS_SPOOL_SEGMENT_BYTES=65536 AEGIS_AUDIT_SUPPRESS_WINDOW=5s docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build --wait`, `SMOKE_SUPPRESS_WINDOW_SECS=5 SMOKE_ROTATION_REQUESTS=300 bash scripts/compose-smoke.sh mvp --stop-check` exits 0 with PASS for every AUDIT-TYPES assertion, DEDUPE, ROTATION and GRACE. It passed once on 2026-10-09 (08-11-SUMMARY.md); the verifier could not re-run it.
result: pass — reproduced 2026-11-17 on current main (Phase 12 + D-12a): `compose-smoke.sh mvp --stop-check` with SMOKE_SUPPRESS_WINDOW_SECS=5, SMOKE_ROTATION_REQUESTS=300, AEGIS_SPOOL_SEGMENT_BYTES=65536, AEGIS_AUDIT_SUPPRESS_WINDOW=5s — 63 PASS, 0 hard failures (AUDIT-TYPES, DEDUPE, ROTATION, GRACE all PASS).

### 2. A failed upstream produces a completion row with an error code (optional)
expected: With the MVP stack up, stop the `orders` backend and send one allowed request to `/api/orders` on :8080. The response is 502, and `audit_events` gets a completion row for that request with `decision=allow`, `http_status=502` and `error_code=UPSTREAM_UNAVAILABLE`. Live completion rows so far all had an empty error code; upstream error codes are proven only by hermetic tests.
result: pass — 2026-11-17, orders stopped: allowed GET /api/orders → 502; audit_events has a completion row decision=allow, http_status=502, error_code=UPSTREAM_UNAVAILABLE, plus the separate decision row.

### 3. The workload mTLS listener writes typed audit rows (optional)
expected: One allowed and one denied request on the :9443 mTLS listener each produce the same typed rows as on :8080 (decision + completion for the allowed request, one denial row for the denied one). The workload listener reaches the pipeline through the same middleware and an AST wiring guard, but was never exercised live.
result: pass — 2026-11-17 on :9443 with the orders workload cert: allowed POST /api/payments → decision + completion rows (ALLOWED_WORKLOAD_ORDERS_PAYMENTS); forbidden admin call → one denial row (DENIED_WORKLOAD_ADMIN_FORBIDDEN); a client without a cert is rejected at the TLS handshake.

### 4. Requirements owner accepts "AUD-03 satisfied with stated limits (MVP profile)"
expected: The owner accepts AUD-03 as satisfied given: delivery of all four row kinds, dedupe replay and multi-segment rotation proven live on the MVP profile against real Postgres; NOT proven live: a worker crash between insert and cursor save (unit-level only), the hardened and distributed profiles with Phase 8 code, and a full-queue shutdown drain (hermetic only). If more evidence is required, AUD-03 is reverted to unchecked / "Gap closure".
result: pass — accepted with stated limits (owner delegated the call, 2026-11-17)
evidence: added since the item was written: hardened smoke 61/0 and distributed smoke 101/0 with Phase 8 code (DEDUPE and ROTATION sections included); Phase 12 UAT item 4 SIGKILLed a gateway under load with zero duplicate rows and every decision row delivered. Still not live: worker crash between insert and cursor save (unit-level, TestAuditWorker_RestartRecovery) and a full-queue shutdown drain (hermetic). Completion rows can lose the last few ms on a gateway crash, as D-08 allows.

## Summary

total: 4
passed: 4
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps
