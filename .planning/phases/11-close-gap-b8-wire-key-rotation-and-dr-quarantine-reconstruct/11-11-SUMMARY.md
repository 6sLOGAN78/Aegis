---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 11
subsystem: infra
tags: [kubernetes, manifests, secrets, audit-sidecar, crosscheck-tests]
requires: [11-07, 11-08]
provides:
  - Kubernetes manifests aligned with current gateway, control-plane, audit-worker and backend binaries
  - create-secrets.sh and README for all referenced Secrets/ConfigMaps
  - Source-derived hermetic cross-check tests (tests/manifests/crosscheck_test.go)
affects: [deployments/kubernetes, tests/manifests]
key-files:
  created:
    - tests/manifests/crosscheck_test.go
    - deployments/kubernetes/base/control-plane/configmap.yaml
    - deployments/kubernetes/secrets/create-secrets.sh
    - deployments/kubernetes/secrets/README.md
  modified:
    - deployments/kubernetes/base/gateway/{configmap,statefulset,networkpolicy}.yaml
    - deployments/kubernetes/base/control-plane/{deployment,kustomization}.yaml
    - deployments/kubernetes/base/backends/{orders,payments,admin}.yaml
    - deployments/kubernetes/base/kustomization.yaml
  deleted:
    - deployments/kubernetes/base/audit-worker/ (Deployment, kustomization, NetworkPolicy)
key-decisions:
  - "Audit worker is a sidecar in the gateway StatefulSet sharing the wal-spool PVC (D-23)."
  - "Backends use tcpSocket probes because mTLS rules out httpGet."
  - "Workload port stays 8443 everywhere via AEGIS_WORKLOAD_PORT=8443 (binary default 9443)."
  - "Control-plane ConfigMap omits AEGIS_IDENTITY_MAPPINGS_FILE; no mapping file is provisioned."
requirements-completed: [REV-03, AUTH-01, CTRL-02, CTRL-03, CTRL-05]
completed: 2026-10-12
---

# Phase 11 Plan 11: Kubernetes manifests aligned with binaries Summary

Gateway/control-plane/backend manifests now only set env keys their binaries read (verified from Go source by tests), the audit worker is a PVC-sharing sidecar, and a script creates every referenced Secret/ConfigMap from deployments/certs and deployments/keys.

## Tasks
1. Gateway manifests, sidecar, test harness: commit 7fd2f26
2. Control plane, backends, overlay checks: commit e336aa6
3. Secrets script, README, secret-ref / key-material / syntax tests: commit cd16e4e

## Verification
`bash -n deployments/kubernetes/secrets/create-secrets.sh && go test ./tests/manifests -race -count=1` passes (existing hardening tests included).

## Assumption A3 (POD_NAME expansion)
Kubernetes dependent-env-var semantics: `$(VAR)` in an env value expands against variables defined earlier in the same container's env list, and a fieldRef variable counts. POD_NAME is therefore declared first. Recorded from known Kubernetes documentation; ctx7/web lookup was not run here, and a live cluster check is deferred per D-19. The test enforces declaration order.

## Deviations from Plan
- TDD RED step skipped for tasks 1-3: manifests were rewritten and the tests written together, so the tests were never observed failing against the old manifests. The tests do check for the dead keys explicitly.
- Acceptance grep `grep -c "kubectl create"` does not match literally because commands are written `kubectl -n "$NS" create ...`; the test counts 11+ create commands (13 here) and each has the dry-run/apply idempotency form.
- `aegis-root-ca` is created in both aegis-system and aegis-apps (backends need it in their namespace); `aegis-assertion-pubkey` and backend TLS secrets are in aegis-apps.
- Backend pod env keys are the plain names (PORT, TLS_CERT_PATH, ...) so the env test uses `os.Getenv` extraction for services.
- Control-plane NetworkPolicy and production overlay needed no change (already allowed 9090/6379/5432; patches target `gateway`/`control-plane`).

## Known Stubs
None.

## Threat Flags
None beyond the plan's register. T-11-11-02 (sibling cp-client keys readable by every gateway pod) is documented in secrets/README.md.

## Self-Check: PASSED
Files and commits 7fd2f26, e336aa6, cd16e4e exist.
