---
status: complete
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
source: [12-VERIFICATION.md, 12-REVIEW-FIX.md]
started: 2026-11-17T00:00:00Z
updated: 2026-11-17T23:00:00Z
---

## Current Test

[complete; item 3 needs a cluster]

## Tests

### 1. HAProxy config validates and :9443 tracks :8080 readyz
expected: `haproxy -c -f deployments/compose/haproxy/haproxy.cfg` passes (or the distributed stack's haproxy starts); on /stats the aegis_workload_back servers go DOWN/UP with aegis_http_back/gateway-N.
result: pass
evidence: 2026-11-17 `haproxy -c` valid (one warning: `option httplog` falls back to tcplog on the TCP workload frontend). On /stats the aegis_workload_back servers followed aegis_http_back/gateway-N DOWN and UP during the drain test.

### 2. Live two-phase drain behind HAProxy (distributed compose)
expected: SIGTERM one gateway under load → /readyz 503 at once, HAProxy removes it within ~2s, in-flight requests finish, keep-alive clients on :8080 and :9443 get `Connection: close`, container exits 0 within stop_grace_period 60s with the audit spool drained. `scripts/compose-smoke.sh distributed --stop-check` GRACE section passes.
result: pass
evidence: 2026-11-17 distributed stack. `compose-smoke.sh distributed --stop-check` 101 PASS / 0 FAIL. Drain under load: 3748 × 200 on :8080, zero errors; :9443 814 × 200 and 1548 × 429 (rate limit), no 5xx. Both backends DOWN about 1.8s after SIGTERM; stop took 5.29s, exit 0; gateway rejoined UP.

### 3. Live Kubernetes (D-19, needs a CNI-enforcing cluster)
expected: delete a gateway pod; preStop 5s + propagation 10s + drain fits in terminationGracePeriodSeconds 70; the audit-worker sidecar reaches postgres:5432 (N1); gateway-server cert SANs match aegis-gateway.aegis-system.svc.
result: skipped
reason: no CNI-enforcing Kubernetes cluster is available here. Hermetic coverage in 12-VERIFICATION.md stands; re-run on the first real cluster.

### 4. WAL recovery on a real volume (CR-01 / R2 CR-01)
expected: on a running stack, `docker kill -s KILL` a gateway mid-load, restart it; a new segment is opened if the last one ended torn; `audit_events` row count equals the number of acknowledged requests (no loss, duplicates absorbed by ON CONFLICT); the audit-worker skipped-frames counter (if AEGIS_AUDIT_WORKER_METRICS_ADDR is set) stays consistent.
result: pass (within the D-08 crash window)
evidence: 2026-11-17 distributed stack, owner go-ahead. 12s load from gateway-3 to gateway-2:8080; `docker kill -s KILL` gateway-2 at +4s; restarted healthy. 437 × 200 acknowledged (789 × 000 while it was down). Decision rows 437/437. Completion rows 435/437: the two missing are the last two responses before the kill, which Phase 8 D-08 accepts (completions are queued off the request path with a 25ms batched fsync). Duplicate event_ids 0; duplicate (request_id, event_type) 0. Restart reopened the existing segment (wal-…-000043.log) and the worker resumed from wal.cursor; no torn-tail errors logged. The gateway replaces client X-Request-ID, so the test keyed rows by the returned header.

## Summary

total: 4
passed: 3
issues: 0
pending: 0
skipped: 1
blocked: 0

## Gaps

## Notes

- Hermetic coverage for every item exists and passes (see 12-VERIFICATION.md, 12-REVIEW-FIX.md R1 and R2 sections).
- 2026-11-17: live runs paused. Docker root at 85.6% after clearing Go and Docker build caches; the smoke pre-flight refuses above 85% and the gateway fails closed at 90%. Removal of exited sentinel-* containers/images (approved by the user) was blocked by the tool permission classifier and must be run by the user.
- Open decision: the audit-worker skipped-frames counter is only exposed when AEGIS_AUDIT_WORKER_METRICS_ADDR is set; compose and K8s leave it unset so no new port/NetworkPolicy change was made.
- 2026-11-17: disk headroom recovered (npm cache, browser caches, unused images removed with the owner's approval). Smoke pre-flight uses the statfs free-blocks ratio: 0.81 against the 0.85 limit, while `df` shows 86%.
- Hardened smoke 61 PASS / 0 FAIL and distributed 101 PASS / 0 FAIL ran with AEGIS_SPOOL_SEGMENT_BYTES and AEGIS_AUDIT_SUPPRESS_WINDOW passed through to the gateways (commit 7d72f65) so suppression and rotation could be checked with short windows. That passthrough broke T-08-42 (demo knobs must stay out of hardened and distributed; TestGatewayAuditKnobsDefaultSafe) and was reverted; the gateway binary under test is unchanged, only those two values differed from production defaults.
