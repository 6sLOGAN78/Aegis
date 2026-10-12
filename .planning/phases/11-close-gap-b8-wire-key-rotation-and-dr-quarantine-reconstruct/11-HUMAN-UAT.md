---
status: partial
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
source: [11-VERIFICATION.md]
started: 2026-10-12T14:52:10Z
updated: 2026-10-12T14:52:10Z
---

## Current Test

[awaiting human testing]

## Tests

### 1. Apply the Kubernetes manifests to a real cluster and confirm pods go Ready
expected: Run deployments/kubernetes/secrets/create-secrets.sh (with production keys via AEGIS_CERTS_DIR / AEGIS_KEYS_DIR), then kubectl apply -k deployments/kubernetes/overlays/production. Gateway StatefulSet (with audit-worker sidecar), control plane and backends start; /livez and /readyz probes pass; the wal-spool PVC binds; each gateway pod uses its own cp-client-aegis-gateway-N cert via POD_NAME.
result: [pending]

## Summary

total: 1
passed: 0
issues: 0
pending: 1
skipped: 0
blocked: 0

## Gaps

## Notes

2026-10-12 browser UAT (operator approved): distributed compose profile driven through the operator dashboard in Chrome plus direct gateway/gRPC probes. All passed: login; Cluster Topology 3/3 gateways HEALTHY with cert-derived ids; dashboard quarantine -> 403 on all replicas, persisted to Postgres; Redis FLUSHALL -> 503 fail-closed then quarantine reconstructed within 0.6s; unquarantine -> 200, no resurrection after a reconcile cycle; dashboard JTI revoke -> 403 TOKEN_REVOKED, survives a second FLUSHALL; control-plane gRPC rejects plaintext, no-cert and workload certs (PermissionDenied), accepts gateway cert. Cosmetic: quarantine list "Quarantined At" shows reconstruct time after FLUSHALL; /favicon.ico 404. Test 1 (live Kubernetes) remains pending.
