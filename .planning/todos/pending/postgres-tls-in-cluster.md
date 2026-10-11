---
title: Enable TLS between services and Postgres (sslmode verify-full)
created: 2026-10-12
source: automated security review during phase 11 wave 4 (11-11 manifests)
severity: medium
---

## Problem

All Postgres connections are plaintext. `storage.NewPool` and `cmd/audit-worker`
default to `sslmode=disable`, and the K8s manifests set `AEGIS_DB_SSLMODE: disable`
explicitly (`deployments/kubernetes/base/control-plane/configmap.yaml`, audit-worker
sidecar env in `deployments/kubernetes/base/gateway/statefulset.yaml`). The in-cluster
Postgres (`deployments/kubernetes/base/postgres`) has no server TLS. DB credentials and
security state (quarantines, revoked tokens) cross the pod network in cleartext.
Pre-existing; Phase 11 only made the default explicit.

## Fix outline

1. `scripts/certificates`: issue a `postgres-server` cert (SAN `postgres.aegis-system`) from the root CA.
2. Postgres StatefulSet + compose: `ssl=on`, mount server cert/key from a Secret.
3. `internal/storage/db.go` and `cmd/audit-worker`: support `AEGIS_DB_SSLROOTCERT` (add `sslrootcert` to the DSN); default production to `verify-full`.
4. Mount `aegis-root-ca` into the control plane and the audit-worker sidecar; set `AEGIS_DB_SSLMODE=verify-full` in the production overlay.
5. Manifest test asserting no `sslmode=disable` in the production overlay render.
