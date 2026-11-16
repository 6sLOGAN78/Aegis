# Phase 12 Context — Close gaps N1, DIST-01, DIST-02

**Source:** `.planning/v1.0-MILESTONE-AUDIT.md` (re-audit 3, 2026-11-16) and `12-RESEARCH.md`.
**Gathered:** 2026-11-16 (no discuss-phase; decisions taken from the audit plus one user decision).

## Phase Boundary

In scope: N1 (K8s postgres NetworkPolicy + manifest cross-check), DIST-01 (two-phase drain, grace periods, :9443 readiness in HAProxy), DIST-02 (production callers for the six unused recorders + request-latency histogram + wiring guard), audit-worker read-error hardening, completion-queue overflow no longer faults the replica, W2 gateway cert SANs.

Out of scope (stay tech debt / v2): multi-replica control-plane session/idempotency state (W1), IdentityMappings consumption on the gateway (W3), Postgres TLS, multi-replica quarantine race, K8s seed job, live Kubernetes bring-up (D-19 still applies; no Docker or cluster in this phase).

## Implementation Decisions

### Audit queue overflow
- **D-12a (user decision 2026-11-16, amends Phase 8 D-12):** completion-queue overflow sheds the completion record, increments the existing overflow/drop metric and logs (rate-limited), and **never** calls `setFault` / flips the spool saturated / fails `/readyz`. Rationale: completion rows are post-response; a burst must not pull every replica out of the load balancer.
- Unchanged and still fail-closed: the pre-forward allow row (D-09), the 0.90 saturation gate, the 0.95 hard limit, and write-error faults.
- Existing `queue_full` committer tests are rewritten to assert the new behaviour, not deleted. Record D-12a in STATE.md decisions.

### Audit worker read errors
- A CRC-valid frame whose JSON fails to decode is skipped and counted (logged), not a reason to stop.
- A torn tail on the newest segment is retried later without advancing the cursor.
- Any other read error is returned; cursor advance and segment prune happen only on an explicit reached-EOF.

### N1
- Postgres NetworkPolicy admits `app: aegis-gateway`; remove the dead `app: aegis-audit-worker` peer (no pod carries that label).
- New tests/manifests cross-check: for each pod template whose env (direct or via configmap) points at postgres, redis or the control-plane gRPC address, the target's ingress admits it and its own egress allows the port. Must fail on the pre-fix tree.

### Drain (DIST-01)
- `AEGIS_DRAIN_PROPAGATION_DELAY`: code default 0; distributed compose 5s; K8s configmap 10s. Sequence: mark draining (fail /readyz) → sleep delay while serving → Shutdown with drain timeout → audit flush.
- Post-signal block extracted into a testable function.
- Compose `stop_grace_period` 60s; K8s `terminationGracePeriodSeconds: 70` plus a preStop `sh -c "sleep 5"`. Tests assert grace ≥ sum of delays.
- HAProxy :9443 backend servers track the :8080 readyz check; workload listener sends `Connection: close` while draining.

### Metrics (DIST-02)
- Wire RecordPolicyEval/RecordPolicyDecision (around PolicyEngine.Evaluate, both listeners), RecordRateLimitRejection, RecordSpoolBytes (via a bytes-written hook on DiskSpool, not by extending audit.Recorder), SetConnectedGateways (stream connect/disconnect), RecordSnapshotPublish (BroadcastSnapshot).
- Latency histogram labels `{method, route_id, status_class}`; methods normalized to a closed set. Normalizing the existing `aegis_http_requests_total` method label is allowed (only tests consume it).
- AST guard test: every recorder has a non-test caller (including method-value references).

### W2
- `scripts/certificates/main.go` gateway-server SANs add `aegis-gateway`, `aegis-gateway.aegis-system`, `aegis-gateway.aegis-system.svc`, `aegis-gateway.aegis-system.svc.cluster.local`, plus `gateway-1`, `gateway-2`, `gateway-3`. Regenerate committed certs reusing the root CA.

### Claude's Discretion
Plan split, waves, test file names, exact metric bucket boundaries.

## Canonical References
- `.planning/v1.0-MILESTONE-AUDIT.md`
- `.planning/phases/12-*/12-RESEARCH.md`
- `.planning/phases/08-*/08-CONTEXT.md` (D-09, D-12)
- `.planning/phases/11-*/11-CONTEXT.md`

## Deferred Ideas
W1, W3, Postgres TLS, quarantine race, K8s seed job, live K8s bring-up.
