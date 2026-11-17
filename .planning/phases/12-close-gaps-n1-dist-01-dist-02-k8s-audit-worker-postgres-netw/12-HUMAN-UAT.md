---
status: partial
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
source: [12-VERIFICATION.md, 12-REVIEW-FIX.md]
started: 2026-11-17T00:00:00Z
updated: 2026-11-17T00:00:00Z
---

## Current Test

[awaiting human testing — blocked on Docker disk headroom, see Notes]

## Tests

### 1. HAProxy config validates and :9443 tracks :8080 readyz
expected: `haproxy -c -f deployments/compose/haproxy/haproxy.cfg` passes (or the distributed stack's haproxy starts); on /stats the aegis_workload_back servers go DOWN/UP with aegis_http_back/gateway-N.
result: [pending]

### 2. Live two-phase drain behind HAProxy (distributed compose)
expected: SIGTERM one gateway under load → /readyz 503 at once, HAProxy removes it within ~2s, in-flight requests finish, keep-alive clients on :8080 and :9443 get `Connection: close`, container exits 0 within stop_grace_period 60s with the audit spool drained. `scripts/compose-smoke.sh distributed --stop-check` GRACE section passes.
result: [pending]

### 3. Live Kubernetes (D-19, needs a CNI-enforcing cluster)
expected: delete a gateway pod; preStop 5s + propagation 10s + drain fits in terminationGracePeriodSeconds 70; the audit-worker sidecar reaches postgres:5432 (N1); gateway-server cert SANs match aegis-gateway.aegis-system.svc.
result: [pending]

### 4. WAL recovery on a real volume (CR-01 / R2 CR-01)
expected: on a running stack, `docker kill -s KILL` a gateway mid-load, restart it; a new segment is opened if the last one ended torn; `audit_events` row count equals the number of acknowledged requests (no loss, duplicates absorbed by ON CONFLICT); the audit-worker skipped-frames counter (if AEGIS_AUDIT_WORKER_METRICS_ADDR is set) stays consistent.
result: [pending]

## Summary

total: 4
passed: 0
issues: 0
pending: 4
skipped: 0
blocked: 0

## Gaps

## Notes

- Hermetic coverage for every item exists and passes (see 12-VERIFICATION.md, 12-REVIEW-FIX.md R1 and R2 sections).
- 2026-11-17: live runs paused. Docker root at 85.6% after clearing Go and Docker build caches; the smoke pre-flight refuses above 85% and the gateway fails closed at 90%. Removal of exited sentinel-* containers/images (approved by the user) was blocked by the tool permission classifier and must be run by the user.
- Open decision: the audit-worker skipped-frames counter is only exposed when AEGIS_AUDIT_WORKER_METRICS_ADDR is set; compose and K8s leave it unset so no new port/NetworkPolicy change was made.
