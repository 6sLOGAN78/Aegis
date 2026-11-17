---
phase: 13-close-gap-n2-k8s-postgres-credentials-from-aegis-db-credenti
reviewed: 2026-11-17T19:15:48Z
depth: standard
files_reviewed: 8
files_reviewed_list:
  - deployments/kubernetes/base/postgres/statefulset.yaml
  - deployments/kubernetes/base/gateway/statefulset.yaml
  - deployments/kubernetes/secrets/README.md
  - deployments/kubernetes/secrets/create-secrets.sh
  - docs/runbooks/spool-saturation-recovery.md
  - tests/manifests/db_credentials_test.go
  - tests/manifests/drain_grace_test.go
  - tests/manifests/runbook_names_test.go
findings:
  critical: 2
  warning: 4
  info: 5
  total: 11
status: issues_found
---

# Phase 13: Code Review Report

**Reviewed:** 2026-11-17T19:15:48Z
**Depth:** standard
**Files Reviewed:** 8
**Status:** issues_found

## Narrative Findings (AI reviewer)

## Summary

The review covered the diff since `9eb71f9`: Postgres credentials now come from `aegis-db-credentials`, the audit-worker sidecar gets a `sleep 55` preStop, the spool runbook was rewritten, and three guard tests were added. The Postgres secret wiring is correct. No literal password remains under `deployments/`; `aegis-secret-pw` now appears only in `.planning/` history. The new tests pass (`go test ./tests/manifests` ok).

I ran mutation checks on a scratch copy of the repo:
- Lowering the sidecar sleep or the grace period makes the drain test fail, as it should. Changing the sleep command shape also makes it fail.
- Removing `POSTGRES_USER` entirely still lets `TestManifestDBCredentialsFromSecret` **pass**. The test misses that regression (WR-03).
- The runbook still contains invalid `kubectl` targets, metrics that do not exist, and a binary that does not exist. The "real objects" guard does not detect any of them (CR-02, WR-04).
- The sidecar manifest still lacks `AEGIS_PRUNE_ARCHIVED=true`. Without it, the spool this runbook is meant to recover is guaranteed to saturate (CR-01).

On the preStop questions:
- `sh` exists in the alpine audit-worker image, so the hook will run.
- The sleep is fixed. It does not end early when the gateway exits early, so every pod termination and rollout now takes at least 55s per pod (WR-01).
- The budget has zero margin and leaves some gateway shutdown steps out of the count (WR-02).

## Critical Issues

### CR-01: K8s audit-worker never prunes delivered segments, so the WAL PVC fills and the gateway fails closed (503 SPOOL_SATURATED)

**File:** `deployments/kubernetes/base/gateway/statefulset.yaml:127-147` (audit-worker env); related: `docs/runbooks/spool-saturation-recovery.md:93-101`
**Issue:** `internal/audit/worker.go:274-276` deletes a fully delivered segment only when `PruneArchived` is true. `cmd/audit-worker/main.go:41` sets that flag only when `AEGIS_PRUNE_ARCHIVED == "true"`. All three compose files set it. The K8s sidecar env does not set it, and no other code path deletes segments. Delivered segments therefore pile up on the 2Gi `wal-spool` PVC until `Statfs` utilisation reaches 0.90 and admission halts. This defect predates this phase, but it sits in a file under review. It is the most likely cause of the incident this runbook covers. The runbook's Procedure D ("segments processed but not automatically deleted") tells the operator to run `/usr/local/bin/aegis-wal-prune`. That binary does not exist in the repo or in any image.
**Fix:**
```yaml
            - name: AEGIS_PRUNE_ARCHIVED
              value: "true"
```
Add this to the audit-worker env. In Procedure D, replace the nonexistent prune command with: confirm `AEGIS_PRUNE_ARCHIVED=true` on the `audit-worker` container (`kubectl set env statefulset/aegis-gateway -c audit-worker AEGIS_PRUNE_ARCHIVED=true`). Add a manifest test that asserts this env value on the sidecar.

### CR-02: The "real objects" runbook still has invalid commands and names metrics, an alert, a binary and an HPA that do not exist

**File:** `docs/runbooks/spool-saturation-recovery.md:14-15,24,96,100,111,115,128`
**Issue:** This phase's goal (W5, T-13-07) was a runbook that names real objects. Several lines still break that:
- `kubectl exec statefulset/aegis-gateway-0` (lines 96, 100, 111) fails with `statefulsets.apps "aegis-gateway-0" not found`. The correct target is `pod/aegis-gateway-0`, or `statefulset/aegis-gateway`, which only reaches the first pod. These commands also omit `-c`. They only work because kubectl defaults to the first container (`gateway`); add `-c gateway` (or `-c audit-worker`) explicitly.
- The trigger metrics `aegis_wal_spool_bytes` / `aegis_wal_spool_quota_bytes` (line 14) and `aegis_wal_spool_saturation_ratio` (line 115) are not exported anywhere. The real gauge is `aegis_spool_utilization_ratio`.
- The alert `AegisWALSpoolSaturated` (lines 15, 24) is not defined anywhere in the repo.
- `aegis-wal-prune` (line 100) does not exist.
- Line 128 refers to an HPA on `aegis_audit_worker_lag_seconds`, but neither the HPA nor the metric exists. The decision tree at line 48 says "Scale Worker", but a sidecar cannot be scaled on its own.

An on-call engineer following this SEV-1 runbook will hit failing commands at Procedure D and in every verification step.
**Fix:** Use `kubectl exec -n aegis-system pod/aegis-gateway-0 -c gateway -- ...`. Replace the metric with `aegis_spool_utilization_ratio >= 0.90`. Delete or mark as "not yet implemented" the alert, the prune binary and the HPA lines. Rename the "Scale Worker" branch to "Tune worker batch/flush". Extend the guard test (WR-04).

## Warnings

### WR-01: Fixed `sleep 55` holds every gateway pod in Terminating for at least 55s, even after the gateway has exited; runbook restarts now cost far more capacity

**File:** `deployments/kubernetes/base/gateway/statefulset.yaml:117-120`; `docs/runbooks/spool-saturation-recovery.md:66-81`
**Issue:**
- The kubelet runs each container's preStop hook in parallel, and nothing cuts the sidecar's sleep short. When the gateway drains quickly (idle pod: about 16s), the pod still stays Terminating until t=55s or later.
- The gateway is an OrderedReady StatefulSet. A replacement `aegis-gateway-N` cannot be created until the old pod is fully gone. Each pod restart therefore loses one third of gateway capacity for at least 55s plus startup, and a 3-replica rollout takes at least about 3 minutes. Node drains are serialised by the PDB (`minAvailable: 2`).
- Procedure A step 3 and Procedure B tell operators to roll the whole gateway StatefulSet during a SEV-1 saturation incident. The runbook does not mention this cost.
- Restarting a stuck sidecar does not require restarting the gateway at all.

**Fix:**
- Preferred: make audit-worker a native sidecar (`initContainers` entry with `restartPolicy: Always`, K8s >= 1.29). The kubelet then sends it SIGTERM only after the gateway container has exited, and the fixed sleep can be dropped.
- Alternatively: have the preStop poll for gateway exit (for example via a marker file on the shared `tmp` emptyDir that the gateway writes after `diskSpool.Close()`), with 55s as the upper bound.
- In the runbook, document the 55s per-pod cost. For a stuck worker only, offer `kubectl exec -n aegis-system pod/aegis-gateway-N -c audit-worker -- kill -TERM 1`. The container restarts in place and the gateway is untouched.

### WR-02: The sidecar budget has zero slack and leaves gateway shutdown steps uncounted; the "after the gateway's last WAL write" claim does not hold

**File:** `deployments/kubernetes/base/gateway/statefulset.yaml:19-21,120`; `tests/manifests/drain_grace_test.go:93-95`
**Issue:**
- The test computes `need = 5 + 10 + 30 + 10 = 55`, and the sleep is exactly 55.
- The gateway's shutdown (`cmd/gateway/main.go:1035-1058`) runs audit flush (5s) and metrics shutdown (5s), then `streamCancel`/`streamClient.Stop()`, `rdb.Close()` and `diskSpool.Close()`. None of those last steps has a bound or appears in the budget.
- So in the worst case the sidecar gets SIGTERM while the gateway is still finalising the spool.
- After SIGTERM, the worker's final drain is capped at 2s (`internal/audit/worker.go:519`) and does not keep tailing.
- On a normal restart, leftover WAL sits on the PVC and is shipped later. On a scale-down (for example 3 to 2), the orphaned `wal-spool-aegis-gateway-2` PVC has no reader, and anything undelivered there is stranded.

**Fix:**
- Add margin: require `sidecar >= need + 5s`, and raise grace to keep `sidecar + 10 <= grace`.
- Better, adopt the native-sidecar ordering from WR-01.
- Reword the manifest comment to say "after the gateway's budgeted drain", not "after the last WAL write".
- Document in the runbook or scaling docs that scale-down needs a manual drain of the orphaned PVC.

### WR-03: DB credentials test misses a missing `POSTGRES_USER` (verified by mutation)

**File:** `tests/manifests/db_credentials_test.go:59-65`
**Issue:**
- The vacuity guard requires only the three `*_PASSWORD` pairs.
- Deleting the `POSTGRES_USER` env entirely leaves the test **passing** (verified in a scratch copy). Postgres would then initialise superuser `postgres`. Clients authenticating as `AEGIS_DB_USER` (default `aegis`) would fail, which is the same outage as gap N2.
- The same applies to removing `AEGIS_DB_USER` from control-plane or audit-worker. Those clients would fall back to their binary default user.

**Fix:**
```go
for _, s := range []string{
    "postgres/POSTGRES_PASSWORD", "postgres/POSTGRES_USER",
    "control-plane/AEGIS_DB_PASSWORD", "control-plane/AEGIS_DB_USER",
    "audit-worker/AEGIS_DB_PASSWORD", "audit-worker/AEGIS_DB_USER",
} { ... }
```

### WR-04: The runbook guard test is a single-substring blocklist and misses the real defects

**File:** `tests/manifests/runbook_names_test.go:21-31`
**Issue:**
- The test rejects only the literal `aegis-audit-worker`, then checks that four strings are present. It passes while the runbook still contains `statefulset/aegis-gateway-0`, nonexistent metric names and a nonexistent binary (CR-02).
- The presence check accepts any mention of `statefulset/aegis-gateway`, including the broken `statefulset/aegis-gateway-0`.
- The test name `TestRunbooksNameRealObjects` claims more than the test checks.

**Fix:**
- Extract every `(statefulset|deployment|configmap|secret|pvc|pod)/<name>` reference with a regex, and check each against the names in `baseDocs`. Pods should match `<sts>-<ordinal>`; PVCs should match `<vct>-<sts>-<ordinal>`.
- Extract `aegis_[a-z_]+` metric names and require each one to appear in `internal/` or `cmd/` source.

## Info

### IN-01: Sidecar budget comment disagrees with the test

**File:** `deployments/kubernetes/base/gateway/statefulset.yaml:20-21` vs `tests/manifests/drain_grace_test.go:58,95`
**Issue:** The comment says the sidecar "has up to 15s for its own shutdown", but the test enforces only `sleep + 10s <= grace`. Raising the sleep to 60 would pass the test and silently leave 10s, not 15s.
**Fix:** Pick one number and use it in both places.

### IN-02: Runbook hardcodes `-U aegis`, but the DB user is now configurable

**File:** `docs/runbooks/spool-saturation-recovery.md:59`
**Issue:** `create-secrets.sh:22` accepts a custom `AEGIS_DB_USER`, and Postgres now uses it. `pg_isready` does not authenticate, so the command still works, but the runbook implies a fixed role.
**Fix:** Drop `-U`, or use `-U "$POSTGRES_USER"` via `sh -c`.

### IN-03: Stale or incomplete credential comments

**File:** `deployments/kubernetes/secrets/create-secrets.sh:124`; `deployments/kubernetes/secrets/README.md:50-52`
**Issue:** The section header still says "(control plane and audit-worker sidecar)". The README caveat covers changing the password on an existing PVC, but changing `AEGIS_DB_USER` breaks in the same way (the role is never created), and the caveat does not say so. Re-running the script with a new password updates the Secret immediately. Clients pick the new password up on their next restart, but Postgres keeps the old one, and the script gives no warning.
**Fix:** Update the header. Extend the caveat to cover the user. Have the script print a warning when the Secret already exists with a different value.

### IN-04: Drain test duplicates preStop parsing, is stricter than the binary, and checks only base

**File:** `tests/manifests/drain_grace_test.go:68-78,82-86`
**Issue:**
- The `sleepOf` closure duplicates the parsing in `TestManifestGatewayDrainBudget`.
- The test parses `AEGIS_DRAIN_PROPAGATION_DELAY` with `time.ParseDuration`. The binary also accepts bare seconds (`"10"`) and caps values at 60s (`cmd/gateway/drain.go:52-85`), so a valid config would fail the test.
- Only `base/` is checked, although the production overlay patches the gateway StatefulSet. A future overlay change to the grace period or the sidecar lifecycle would go unchecked.

**Fix:** Share a `preStopSleep(t, sts, name)` helper. Mirror `parseDrainPropagationDelay`. Also run the budget check against `kustomize build overlays/production`, or at least against the overlay patch docs.

### IN-05: Literal-secret check covers only env names containing `PASSWORD`

**File:** `tests/manifests/db_credentials_test.go:40-43`
**Issue:** A DSN-style variable (for example `AEGIS_DB_URL=postgres://u:pw@...`), a ConfigMap `data` key consumed via `envFrom`, or a kustomize `secretGenerator` `literals:` entry would all get past the check.
**Fix:** Also scan ConfigMap `data` and kustomization generator literals for `PASSWORD`, and scan for `://[^/]*:[^@]+@` patterns.

---

_Reviewed: 2026-11-17T19:15:48Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
