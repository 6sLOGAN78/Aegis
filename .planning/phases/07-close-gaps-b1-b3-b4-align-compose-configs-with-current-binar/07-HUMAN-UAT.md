---
status: partial
phase: 07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar
source: [07-VERIFICATION.md]
started: 2026-10-09T00:00:00Z
updated: 2026-10-09T00:00:00Z
---

## Current Test

[awaiting human testing]

## Tests

### 1. Gateway stops cleanly under its configured grace period while requests are in flight
expected: With a profile running and requests in flight, `docker compose -f deployments/compose/docker-compose.<profile>.yml stop gateway` (gateway-1/2/3 on distributed, no `-t` flag) exits with code 0, not 137, in under 45 seconds. The Phase 7 smoke check used `stop -t 60` on idle gateways, so the configured `stop_grace_period: 45s` and a loaded drain were never exercised live.
result: [pending]

### 2. Recorded live evidence reproduces on a machine with disk headroom (optional)
expected: From `down -v --remove-orphans`, `up -d --build --wait` succeeds and `bash scripts/compose-smoke.sh <profile> --stop-check` exits 0 on mvp, hardened and distributed; `TestMVPEndToEnd` and `TestBackendBypassPrevention` pass against the fresh mvp stack. These passed once on 2026-10-08 during execution; the verifier could not re-run them because the host disk was 99% full.
result: [pending]

## Summary

total: 2
passed: 0
issues: 0
pending: 2
skipped: 0
blocked: 0

## Gaps
