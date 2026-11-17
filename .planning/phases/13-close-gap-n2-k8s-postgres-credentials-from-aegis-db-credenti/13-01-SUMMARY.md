---
phase: 13-close-gap-n2-k8s-postgres-credentials-from-aegis-db-credenti
plan: 01
subsystem: deployments/kubernetes
tags: [k8s, postgres, secrets, manifest-test]
requires: []
provides: [postgres credentials from aegis-db-credentials]
affects: [base/postgres/statefulset.yaml]
key-files:
  created: [tests/manifests/db_credentials_test.go]
  modified:
    - deployments/kubernetes/base/postgres/statefulset.yaml
    - deployments/kubernetes/secrets/README.md
    - deployments/kubernetes/secrets/create-secrets.sh
requirements-completed: [AUD-03, REV-03, CTRL-01, CTRL-02, CTRL-03, OPS-01, OPS-03]
metrics:
  completed: 2026-11-18
---

# Phase 13 Plan 01: Postgres credentials from Secret Summary

Postgres StatefulSet now reads POSTGRES_USER/POSTGRES_PASSWORD via secretKeyRef to `aegis-db-credentials` (same Secret as control-plane and audit-worker), guarded by a red-then-green manifest test.

## Commits
- 8bcf403: test(13-01) red test TestManifestDBCredentialsFromSecret (failed naming POSTGRES_USER/POSTGRES_PASSWORD)
- 7dadbd8: fix(13-01) statefulset secretKeyRef + README section + create-secrets.sh comment

## Notes
- Test scans base and all overlays; forbids literal values on any *PASSWORD* env; vacuity guard checks postgres, control-plane and audit-worker envs in base docs only (so partial overlay patches do not false-fail).
- README documents first-init-only caveat (ALTER ROLE or fresh volume).
- `grep aegis-secret-pw deployments/kubernetes` is empty; manifests tests and `go test -short ./...` pass; gofmt clean.

## Deviations from Plan
None. (Verify step run once with captured output per checker note rather than the double-run pipeline.)

## Self-Check: PASSED
