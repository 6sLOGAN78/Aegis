---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
plan: 01
subsystem: k8s-manifests, pki
tags: [networkpolicy, postgres, certs, audit]
requirements: [AUD-01, AUD-03, REV-03]
key-files:
  created: [tests/manifests/networkpolicy_peers_test.go]
  modified:
    - deployments/kubernetes/base/postgres/networkpolicy.yaml
    - scripts/certificates/main.go
    - scripts/certificates/main_test.go
    - deployments/certs (leaf certs/keys regenerated)
metrics:
  completed: 2026-11-16
---

# Phase 12 Plan 01: Postgres NetworkPolicy gateway peer and gateway cert SANs

Postgres ingress now admits `app: aegis-gateway` (audit-worker sidecar) on 5432, guarded by a manifest cross-check test, and the gateway-server cert carries the K8s Service DNS and gateway-N SANs.

## Tasks

1. Cross-check test + N1 fix - commit dce86a9
2. Gateway SANs + cert regeneration - commit 139bdb4

## RED evidence

- Task 1 (pre-YAML edit) failed: `postgres ingress does not admit aegis-gateway (labels map[app:aegis-gateway]) on TCP 5432` and `postgres-networkpolicy ingress peer map[app:aegis-audit-worker] matches no workload pod label`. Green after the edit; full tests/manifests green.
- Task 2 (pre-edit) TestGatewaySANs failed for aegis-gateway*, gateway-1..3; green after.

## Decisions

- Dead-peer assertion is scoped to the aegis-system pod-label space: only peers with a podSelector and no namespaceSelector are checked against workload labels. Peers with a namespaceSelector (aegis-apps, kube-system DNS) are skipped, so external peers do not fail it.
- Edges are derived from resolved env (direct values + envFrom ConfigMaps) across all containers, including the audit-worker sidecar.
- Cert regeneration reused the CA: `git diff` on root-ca.crt and root-ca.key is empty (byte-identical). All leaf certs/keys were reissued (generator reissues every leaf), as expected.

## Deviations from Plan

None. Verification: `go build ./...`, `go vet ./scripts/... ./tests/...`, and `go test -short` for scripts/certificates, tests/manifests and tests/compose all pass.

## Known Stubs

None.

## Self-Check: PASSED
