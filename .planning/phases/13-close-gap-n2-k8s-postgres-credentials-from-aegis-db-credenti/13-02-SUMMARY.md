---
phase: 13-close-gap-n2-k8s-postgres-credentials-from-aegis-db-credenti
plan: 02
requirements-completed: [DIST-01, AUD-03, OPS-01]
key-files:
  modified: [deployments/kubernetes/base/gateway/statefulset.yaml, tests/manifests/drain_grace_test.go, docs/runbooks/spool-saturation-recovery.md]
  created: [tests/manifests/runbook_names_test.go]
---
# Phase 13 Plan 02: Sidecar drain ordering and runbook names Summary

audit-worker sidecar gets preStop sleep 55 so it outlives the gateway drain (W3), and the spool-saturation runbook now targets statefulset/aegis-gateway, container audit-worker and AEGIS_AUDIT_* env (W5), each guarded by a manifest test.

## Commits
- 49e6d57: sidecar preStop + budget comment + TestManifestAuditWorkerOutlivesGatewayDrain (red first: failed with empty preStop, then green)
- 755b310: runbook fixes + TestRunbooksNameRealObjects

## Deviations
None. (Test used repoRoot constant rather than a function.)

## Verification
go test ./tests/manifests/ and go test -short ./... pass; gofmt -l tests empty; no aegis-audit-worker in docs/runbooks.

## Self-Check: PASSED
