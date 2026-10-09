---
status: partial
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
source: [08-VERIFICATION.md]
started: 2026-10-09T00:00:00Z
updated: 2026-10-09T00:00:00Z
---

## Current Test

[awaiting human testing]

## Tests

### 1. Accept or reproduce the recorded live MVP run
expected: From `down -v --remove-orphans` and `AEGIS_SPOOL_SEGMENT_BYTES=65536 AEGIS_AUDIT_SUPPRESS_WINDOW=5s docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build --wait`, `SMOKE_SUPPRESS_WINDOW_SECS=5 SMOKE_ROTATION_REQUESTS=300 bash scripts/compose-smoke.sh mvp --stop-check` exits 0 with PASS for every AUDIT-TYPES assertion, DEDUPE, ROTATION and GRACE. It passed once on 2026-10-09 (08-11-SUMMARY.md); the verifier could not re-run it.
result: [pending]

### 2. A failed upstream produces a completion row with an error code (optional)
expected: With the MVP stack up, stop the `orders` backend and send one allowed request to `/api/orders` on :8080. The response is 502, and `audit_events` gets a completion row for that request with `decision=allow`, `http_status=502` and `error_code=UPSTREAM_UNAVAILABLE`. Live completion rows so far all had an empty error code; upstream error codes are proven only by hermetic tests.
result: [pending]

### 3. The workload mTLS listener writes typed audit rows (optional)
expected: One allowed and one denied request on the :9443 mTLS listener each produce the same typed rows as on :8080 (decision + completion for the allowed request, one denial row for the denied one). The workload listener reaches the pipeline through the same middleware and an AST wiring guard, but was never exercised live.
result: [pending]

### 4. Requirements owner accepts "AUD-03 satisfied with stated limits (MVP profile)"
expected: The owner accepts AUD-03 as satisfied given: delivery of all four row kinds, dedupe replay and multi-segment rotation proven live on the MVP profile against real Postgres; NOT proven live: a worker crash between insert and cursor save (unit-level only), the hardened and distributed profiles with Phase 8 code, and a full-queue shutdown drain (hermetic only). If more evidence is required, AUD-03 is reverted to unchecked / "Gap closure".
result: [pending]

## Summary

total: 4
passed: 0
issues: 0
pending: 4
skipped: 0
blocked: 0

## Gaps
