---
status: complete
phase: 13-close-gap-n2-k8s-postgres-credentials-from-aegis-db-credenti
source: [13-VERIFICATION.md]
started: 2026-11-18T00:00:00Z
updated: 2026-11-18T00:00:00Z
---

## Current Test

[complete; both items need a CNI-enforcing Kubernetes cluster (D-19)]

## Tests

### 1. K8s bring-up with a non-default AEGIS_DB_PASSWORD
expected: on an empty PVC, Postgres initialises from aegis-db-credentials; control plane and audit-worker sidecar connect; audit rows land in audit_events; delivered WAL segments are pruned (AEGIS_PRUNE_ARCHIVED).
result: skipped
reason: no Kubernetes cluster here (D-19). Hermetic coverage: TestManifestDBCredentialsFromSecret, TestManifestAuditWorkerPrunesDelivered. Carried to the v2 todo k8s-live-uat-and-sidecar-drain.

### 2. Gateway StatefulSet rollout under load
expected: the audit-worker receives SIGTERM only after the gateway drains; no WAL stranded; each pod terminates within 70s.
result: skipped
reason: no Kubernetes cluster here (D-19). Hermetic coverage: TestManifestAuditWorkerOutlivesGatewayDrain. Carried to the same v2 todo.

## Summary

total: 2
passed: 0
issues: 0
pending: 0
skipped: 2
blocked: 0

## Gaps
