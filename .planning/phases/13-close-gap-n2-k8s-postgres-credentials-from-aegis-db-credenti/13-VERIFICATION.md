---
phase: 13-close-gap-n2-k8s-postgres-credentials-from-aegis-db-credenti
verified: 2026-11-18T00:00:00Z
status: human_needed
score: 5/5 roadmap success criteria verified
overrides_applied: 0
human_verification:
  - test: "Bring up K8s with a non-default AEGIS_DB_PASSWORD on an empty PVC and check control-plane and audit-worker reach Postgres"
    expected: "Postgres initialises with the Secret credentials; clients connect; audit rows land in Postgres"
    why_human: "Live Kubernetes is out of scope for hermetic verification (D-19)"
  - test: "Roll the gateway StatefulSet under load and watch audit-worker shutdown"
    expected: "Sidecar gets SIGTERM only after gateway drain; no stranded WAL; pod terminates within the 70s grace period"
    why_human: "Real termination ordering needs a live cluster (D-19)"
---

# Phase 13 Verification

**Goal:** A K8s bring-up with any operator-chosen AEGIS_DB_PASSWORD delivers audit and control-plane state to Postgres; the audit-worker sidecar keeps shipping through the gateway's drain; the spool-saturation runbook names real objects.
**Status:** human_needed (all automated checks pass; only live-cluster checks remain)

## Roadmap Success Criteria

| # | Criterion | Status | Evidence |
|---|-----------|--------|----------|
| 1 | postgres StatefulSet takes POSTGRES_USER/PASSWORD from aegis-db-credentials; no hardcoded DB password under deployments/kubernetes | VERIFIED | base/postgres/statefulset.yaml env uses secretKeyRef aegis-db-credentials keys AEGIS_DB_USER / AEGIS_DB_PASSWORD; `grep aegis-secret-pw deployments/kubernetes` is empty |
| 2 | tests/manifests fails if a client and the server read credentials from different Secrets/keys | VERIFIED | `TestManifestDBCredentialsFromSecret` passes. It covers base and overlays, forbids literal *PASSWORD* values, and has a vacuity guard requiring the postgres, control-plane and audit-worker containers (and, per WR-03, the *_USER refs) |
| 3 | audit-worker outlives gateway preStop + propagation + drain + flush, checked against terminationGracePeriodSeconds | VERIFIED | gateway statefulset.yaml:122 `sleep 55`, grace period 70 (line 24). `TestManifestAuditWorkerOutlivesGatewayDrain` passes and derives its numbers from the manifests |
| 4 | spool-saturation runbook references statefulset/aegis-gateway, container audit-worker, AEGIS_AUDIT_* env | VERIFIED | `TestRunbooksNameRealObjects` passes. No `aegis-audit-worker` remains under docs/runbooks. The required strings are present |
| 5 | go test -short ./... passes | VERIFIED | Run in this session, no failures |

## Post-review fixes (13-REVIEW-FIX.md)

| ID | Status | Evidence |
|----|--------|----------|
| CR-01 | VERIFIED | gateway statefulset.yaml:133-134 audit-worker has `AEGIS_PRUNE_ARCHIVED: "true"`. `TestManifestAuditWorkerPrunesDelivered` passes |
| CR-02 | VERIFIED | `TestRunbooksNameRealMetrics` passes. No `aegis-wal-prune` / `aegis-audit-worker` remain in docs/runbooks. Runbook uses `pod/aegis-gateway-0 -c audit-worker` |
| WR-03 | VERIFIED | db_credentials_test.go guard covers the USER refs. The test passes |
| WR-04 | VERIFIED | `TestRunbooksNameRealMetrics` exists and passes |

## Commands run

| Command | Result |
|---------|--------|
| go build ./... | OK |
| go vet ./... | OK |
| go test -short ./... | all packages pass |
| go test ./tests/manifests/ -count=1 | ok |

## Requirements coverage

PLAN frontmatter IDs: 13-01 has AUD-03, REV-03, CTRL-01, CTRL-02, CTRL-03, OPS-01, OPS-03. 13-02 has DIST-01, AUD-03, OPS-01. Every ID is present in REQUIREMENTS.md and marked complete, and the union equals the ROADMAP list of 8 IDs. No orphaned IDs. The K8s-path credential and sidecar behaviour is verified at the manifest level. Live behaviour is under human verification.

## Deferred items (WR-01 native sidecar, WR-02 zero-margin budget)

Both are recorded truthfully as deferred in 13-REVIEW-FIX.md. WR-01's cost (at least 55s per pod to terminate, about 3 minutes per rollout) is documented in the statefulset.yaml comment and in the runbook, including the `kill -TERM 1` workaround. The code is consistent with those statements.

WARNING (non-blocking): REVIEW-FIX says these are "carried to v2", but REQUIREMENTS.md (v2 section) and ROADMAP.md contain no entry for them. They live only in the review-fix report, so there is no durable backlog item. Recommend adding a backlog or v2 entry for "native sidecar + budget margin + K8s live UAT". The planned live UAT is likewise not tracked as a roadmap item.

## Anti-patterns

No TBD/FIXME/XXX in the phase-modified files. No stub patterns found.

## Gaps

None blocking.
