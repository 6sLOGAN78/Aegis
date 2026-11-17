---
phase: 13
review: 13-REVIEW.md
fixed_at: 2026-11-18
status: complete
in_scope: [CR-01, CR-02, WR-03, WR-04]
fixed: [CR-01, CR-02, WR-03, WR-04, IN (sidecar budget comment), IN (pg_isready user)]
deferred: [WR-01 native sidecar, WR-02 zero-margin budget]
---

# Phase 13 Code Review Fix Report

| ID | Severity | Outcome | Commit | Notes |
|---|---|---|---|---|
| CR-01 | critical | fixed | 2258ff8 | K8s audit-worker sidecar now sets `AEGIS_PRUNE_ARCHIVED=true`, as all three compose files do. Without it, delivered WAL segments were never deleted, the 2Gi PVC filled to the 0.90 gate, and the gateway returned 503 on Kubernetes. The guard is `TestManifestAuditWorkerPrunesDelivered`, which failed before the fix and passes after. This predates Phase 13 and was missed by re-audit 4. |
| CR-02 | critical | fixed | 1e19d07 | `spool-saturation-recovery.md` now triggers on `aegis_spool_utilization_ratio`. It no longer refers to the nonexistent alert, HPA or `aegis-wal-prune` tool. Pod commands use `pod/aegis-gateway-0 -c <container>`. Procedure D checks `AEGIS_PRUNE_ARCHIVED` and states which segments are safe to remove. The same class of stale names was fixed in three more runbooks: `aegis_snapshot_lease_valid` (twice), `aegis_auth_verifications_total`, `aegis_quarantine_enforcements_total`, and `statefulset/aegis-gateway-0`. |
| WR-03 | warning | fixed | 2258ff8 | The vacuity guard now also requires the `*_USER` refs on postgres, control-plane and audit-worker. |
| WR-04 | warning | fixed | 1e19d07 | `TestRunbooksNameRealMetrics` checks every `aegis_*` token in `docs/runbooks` against the metric names registered under `internal/`. The `aegis_session` cookie is allowlisted, and `aegis_keyset_` is accepted as a prefix. The test also rejects the known-bad kubectl and tool names. |
| WR-01 | warning | partially addressed (deferred) | 2258ff8, 1e19d07 | `sleep 55` is kept (D-05; a native sidecar needs K8s >= 1.29 and a pod restructure). The cost (at least 55s per pod to terminate, about 3 minutes for a rollout) is now documented in the manifest comment and the runbook. A stuck worker is restarted with `kill -TERM 1` in its own container. Native sidecar is carried to v2. |
| WR-02 | warning | deferred | — | The budget has zero margin (sleep 55 = 5+10+30+10). Gateway steps that are not counted, and stranded WAL on scale-down, are carried to v2 alongside the K8s live UAT. |
| Info | info | 2 fixed, rest deferred | 2258ff8, 1e19d07 | Fixed: the "15s vs 10s" comment now states both, and `pg_isready` uses `$POSTGRES_USER`. Deferred: the duplicated parsing in the drain test and the bare-seconds strictness, checks on overlays, and literal-secret detection beyond `*PASSWORD*` env names. |

Checks after the fixes: `go test ./tests/manifests/ -count=1` ok, `go test -short ./...` ok, `gofmt -l tests` clean.
