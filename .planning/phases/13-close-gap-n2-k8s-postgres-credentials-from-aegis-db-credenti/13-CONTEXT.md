# Phase 13: Close gap N2 (K8s Postgres credentials), W3 sidecar drain, W5 runbook names - Context

**Gathered:** 2026-11-17
**Status:** Ready for planning
**Source:** v1.0 re-audit 4 (.planning/v1.0-MILESTONE-AUDIT.md), integration checker findings N2, W3, W5. Owner said "go" and delegated choices; decisions below were made by the orchestrator from the audit evidence.

<domain>
## Phase Boundary

Kubernetes manifests, manifest tests and one runbook only. No Go production code changes, no compose changes, no dashboard changes. Live Kubernetes is still out of scope (D-19): proof is static manifest tests plus `go test -short ./...`.
</domain>

<decisions>
## Implementation Decisions

### N2: Postgres credentials (blocker)
- **D-01:** `deployments/kubernetes/base/postgres/statefulset.yaml` sets `POSTGRES_USER` from `secretKeyRef {name: aegis-db-credentials, key: AEGIS_DB_USER}` and `POSTGRES_PASSWORD` from `secretKeyRef {name: aegis-db-credentials, key: AEGIS_DB_PASSWORD}`. `POSTGRES_DB` (aegis) and `PGDATA` stay literal. The literal `aegis-secret-pw` is removed.
- **D-02:** No other file under `deployments/kubernetes/` may carry a literal DB password value. Overlays do not patch postgres today; keep it that way.
- **D-03:** New manifest test in `tests/manifests/` (reuse the loaders in `crosscheck_test.go` / `manifest_test.go`): for every container in the rendered base and each overlay whose env contains `AEGIS_DB_PASSWORD` or `POSTGRES_PASSWORD`, the value must come from `secretKeyRef` with name `aegis-db-credentials`; password keys must be `AEGIS_DB_PASSWORD`, user keys `AEGIS_DB_USER`. Also assert no env var named `*PASSWORD*` has a literal `value:`. The test must fail on the current postgres manifest (red first).
- **D-04:** `deployments/kubernetes/secrets/README.md` (and create-secrets.sh comment if it describes postgres) notes that Postgres reads the same Secret, and that the official image applies POSTGRES_PASSWORD only on first init of an empty PGDATA: changing AEGIS_DB_PASSWORD on an existing PVC needs `ALTER ROLE` or a fresh volume.

### W3: audit-worker sidecar stops too early
- **D-05:** Use a preStop on the `audit-worker` container, not a native sidecar (native sidecars need K8s >= 1.29 and restructure the pod; out of scope). The gateway's worst case after SIGTERM is preStop 5 + propagation 10 + drain 30 + audit flush 5 + metrics 5 = 55s. The sidecar gets `lifecycle.preStop.exec.command: ["sh", "-c", "sleep 55"]` so it receives SIGTERM only after the gateway's last WAL write; the worker then has 15s of the 70s `terminationGracePeriodSeconds` for its own shutdown. The audit-worker image is alpine-based (has sh/sleep).
- **D-06:** Update the drain-budget comment at statefulset.yaml:19 to include the sidecar.
- **D-07:** Extend `tests/manifests/drain_grace_test.go`: sidecar preStop sleep >= gateway preStop + AEGIS_DRAIN_PROPAGATION_DELAY + AEGIS_DRAIN_TIMEOUT + 10, and sidecar sleep + 10 <= terminationGracePeriodSeconds. Values read from the rendered manifests/configmap, not hardcoded in the test.

### W5: runbook names
- **D-08:** `docs/runbooks/spool-saturation-recovery.md` lines ~60-80: replace `-l app=aegis-audit-worker` with `-l app=aegis-gateway -c audit-worker`, `deployment/aegis-audit-worker` with `statefulset/aegis-gateway` (container audit-worker), the `aegis-audit-worker-config` configmap with the inline StatefulSet env, and `BATCH_SIZE` / `FLUSH_INTERVAL_MS` with `AEGIS_AUDIT_BATCH_SIZE` / `AEGIS_AUDIT_FLUSH_INTERVAL_MS` (names from cmd/audit-worker/main.go:28,35). Optionally a manifest/doc test greps docs/runbooks for `aegis-audit-worker` (crosscheck_test.go:335 already guards deployments/).

### Claude's Discretion
- Test helper structure and file split inside tests/manifests.
- Whether W5 lands in the same plan as N2 (small phase: one or two plans is fine).
</decisions>

<canonical_refs>
## Canonical References

- `.planning/v1.0-MILESTONE-AUDIT.md` — N2, W3, W5 evidence
- `deployments/kubernetes/base/postgres/statefulset.yaml` — lines 36-44 env
- `deployments/kubernetes/base/control-plane/deployment.yaml` — lines ~45-56 DB secretKeyRefs (pattern to copy)
- `deployments/kubernetes/base/gateway/statefulset.yaml` — lines 19-20 grace, 65-68 gateway preStop, 112-153 audit-worker sidecar
- `deployments/kubernetes/base/gateway/configmap.yaml` — AEGIS_DRAIN_PROPAGATION_DELAY 10s, AEGIS_DRAIN_TIMEOUT 30s
- `deployments/kubernetes/secrets/create-secrets.sh` — lines 21-22, 125-135 (aegis-db-credentials keys)
- `deployments/kubernetes/secrets/README.md`
- `tests/manifests/crosscheck_test.go`, `manifest_test.go`, `drain_grace_test.go`, `networkpolicy_peers_test.go` — existing loaders and style
- `cmd/audit-worker/main.go` — env names (lines 22-75), SIGTERM handling (132-140)
- `docs/runbooks/spool-saturation-recovery.md` — lines 60-80
</canonical_refs>

<specifics>
## Specific Ideas
- Red-first: the new credential test must fail before the postgres manifest change.
</specifics>

<deferred>
## Deferred Ideas (v2 / tech debt, not this phase)
- W4 aegis-apps egress to gateway :8443; W6 metrics port netpol / LB exposure and AEGIS_AUDIT_WORKER_METRICS_ADDR; W9 drain env parsing; W1/W2 audit error codes for 413/408/502; W7 simulator drift; W8 error_code in dashboard; postgres-tls-in-cluster todo.
</deferred>
