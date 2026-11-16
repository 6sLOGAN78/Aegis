# Phase 12 Security Notes

## Accepted Risks

### AR-12-01: The gateway pod can reach postgres:5432 because the audit-worker sidecar shares the pod

**Source:** 12-REVIEW WR-06. **Status:** accepted for v1, full fix deferred to v2.

**Risk.** NetworkPolicy selects pods, not containers. The postgres ingress policy (`deployments/kubernetes/base/postgres/networkpolicy.yaml`) admits `app: aegis-gateway` so that the audit-worker sidecar can deliver WAL records (D-23). That gives every container in the gateway pod, including the internet-facing `gateway` container (:8080 user, :8443 workload mTLS), a network path to Postgres:5432. A compromised gateway process could open TCP connections to the audit database.

**Mitigations in place (checked against `deployments/kubernetes/base/gateway/statefulset.yaml`):**
- The `gateway` container has no DB credentials. Its environment is `envFrom: configMapRef aegis-gateway-config` plus `POD_NAME`, `AEGIS_CP_CLIENT_CERT_PATH` and `AEGIS_CP_CLIENT_KEY_PATH`. The configmap has no `AEGIS_DB_*` keys.
- The `aegis-db-credentials` Secret is referenced only by the `audit-worker` container. It is injected there as the `AEGIS_DB_USER` and `AEGIS_DB_PASSWORD` env vars via `secretKeyRef`.
- The gateway mounts no Secret volume holding DB credentials. Its Secret mounts are `aegis-gateway-workload-tls`, `aegis-gateway-assertion-key` and `aegis-gateway-cp-clients`.
- `shareProcessNamespace` is not set, so the gateway container cannot read the sidecar's environment through `/proc`.
- The only volumes the containers share are `wal-spool` (audit frames, no credentials) and the `tmp` emptyDir.

**Residual exposure (stated accurately; not yet mitigated):**
- The DB role is not least-privilege. `aegis-db-credentials` is the same Secret the control plane uses (see `deployments/kubernetes/secrets/README.md`). The worker's role is therefore not INSERT-only on `audit_events`.
- No `serviceAccountName` or `automountServiceAccountToken: false` is set, so both containers get the namespace's default ServiceAccount token. That token must not have RBAC permission to read Secrets; if it did, the gateway could fetch `aegis-db-credentials` itself.
- Postgres TLS is out of scope for Phase 12 (`AEGIS_DB_SSLMODE=disable`).
- A compromised gateway can still reach the Postgres port without credentials. That exposes it to pre-auth Postgres protocol attacks and connection exhaustion.

**Full fix (deferred to v2):**
- Run the audit worker as its own pod with its own label and per-workload identity, sharing the WAL through storage, so the postgres peer can be narrowed to that label.
- Alternatively, adopt per-container network identity (a service mesh, or a CNI with container-level policy).
- In either case, give the worker a dedicated INSERT-only DB role on `audit_events`, and set `automountServiceAccountToken: false` on the gateway pod.
