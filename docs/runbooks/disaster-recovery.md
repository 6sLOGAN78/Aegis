# SRE Runbook: Disaster Recovery & State Reconstruction

**Runbook ID:** RB-DR-01  
**Category:** Disaster Recovery & Business Continuity  
**Target Systems:** PostgreSQL Primary Database, Redis In-Memory Cache, Local WAL Disk Spools, Gateway Grid  
**Standard Structure:** Standard 6-Part Operational Runbook (Pattern 11)  

---

## 1. Disaster Recovery Objectives & Classification

### Recovery Targets
- **Recovery Point Objective (RPO):**
  - Configuration & Policy: **0 seconds** (Strictly monotonic, immutable versioning).
  - Audit Trail: **< 1 second** (Guaranteed by pre-forward synchronous `fsync` to local WAL spool).
- **Recovery Time Objective (RTO):**
  - Gateway Ingress Service: **< 5 minutes** (Cold cluster restart to full readiness).
  - Cache Reconstruction: **< 60 seconds** (Postgres-driven restore, marker written last).

### Failure Scenarios Covered
1. **Catastrophic Redis Memory Wipeout:** Cache nodes crash and restart empty.
2. **PostgreSQL Point-in-Time Recovery (PITR):** Database restore from backup with version desynchronization.
3. **Gateway Host / Node Crash:** Local pod killed with uncommitted `.wal` segments on persistent disk.
4. **Complete Multi-Zone Data Center Failure:** Cold bootstrap into a clean Kubernetes cluster.

---

## 2. Redis State Reconstruction Procedure

Postgres is the source of truth for security state; Redis is the fast copy that gateways read. The control plane requires Postgres: it exits at startup if Postgres is unreachable, and there is no in-memory mode.

### 2.1 Mechanism

- **Durable tables** (database `aegis`, migration `000005_create_security_state.sql`):
  - `principal_quarantines(principal_id PK, reason, created_by, created_at, expires_at)`
  - `revoked_tokens(jti PK, reason, created_by, created_at, expires_at)`
- **Write-through ordering:** quarantine, unquarantine and token-revocation handlers write Postgres first, then Redis. If Postgres fails, the call returns `503 DEPENDENCY_FAILURE` and nothing changed. If Postgres succeeds but Redis fails, the call also returns `503 DEPENDENCY_FAILURE`; the durable row exists and the next reconcile re-asserts it into Redis, so retrying the call is safe.
- **Marker key:** `aegis:security-state:generation`. It has no TTL and is written only after a restore finishes (it is written last, so a failed restore leaves it absent). Gateways fail closed while it is missing.
- **Reconstruct:** the control plane checks the marker every 2s. When it is missing (for example after a Redis wipe), the control plane reloads every unexpired row from both tables, writes each key with its remaining TTL, then writes the marker. On a first start with no marker it also imports pre-existing Redis quarantines and revocations into Postgres (backfill) before writing the marker.
- **Reconcile:** every 30s the control plane re-asserts missing rows into Redis and prunes stray quarantine keys that have no Postgres row. Stray revoked-token (`revocation:jti:*`) keys are not pruned; they expire by their own TTL.

### 2.2 What a Redis wipe looks like

| Component | Symptom | Timing |
|---|---|---|
| Gateways | Protected requests are denied with `SECURITY_STATE_MISSING`; `/readyz` returns `503 SECURITY_STATE_MISSING` | About 1-3s (marker poll every 1s with a 200ms timeout; treated as missing after 3 stale intervals) |
| Control plane | `/readyz` returns `503 SECURITY_STATE_MISSING` until reconstruct finishes (`503 DEPENDENCY_FAILURE` if Postgres is unreachable) | Detected within 2s |
| Recovery | Control plane restores state and writes the marker; gateways become ready on their next poll | SLO: under 60s |

### 2.3 Automatic recovery (normal path)

No manual action is needed while the control plane is running and Postgres is reachable.

1. Confirm the loss:
   ```bash
   redis-cli -h redis EXISTS aegis:security-state:generation
   # 0 = marker missing; reconstruct pending or failing
   ```
2. Watch the control plane become ready:
   ```bash
   curl -i http://control-plane:8084/readyz
   # 503 SECURITY_STATE_MISSING while restoring, 200 READY afterwards
   ```
3. Confirm the reconstruct counter and the marker:
   ```bash
   curl -s http://control-plane:9092/metrics | grep aegis_securitystate_reconstruct_total
   # result="ok" increments on success; result="error" means check Postgres and the control plane log
   redis-cli -h redis EXISTS aegis:security-state:generation   # expected: 1
   ```
   (9092 is the default control plane metrics port; use your configured `AEGIS_METRICS_PORT`.)
4. Verify the restored quarantines against Postgres:
   ```bash
   psql -h postgres -U aegis -d aegis -t -c \
     "SELECT count(*) FROM principal_quarantines WHERE expires_at > NOW();"
   curl -s http://control-plane:8084/control/v1/quarantine | jq '.quarantines | length'
   # Expected: both counts match.
   ```
   The response shape is `{"quarantines":[{"principal_id","reason","status","quarantined_at","expires_at","actor"}]}`. There is no `principals` key. The endpoint needs an authenticated session with the `sec-ops`, `auditor` or `viewer` role.
5. Verify gateways:
   ```bash
   curl -i http://gateway:8080/readyz
   # Expected: HTTP 200 once the marker is back
   ```

### 2.4 Manual drill

To rehearse recovery in a non-production environment, wipe Redis and time the recovery:

```bash
redis-cli -h redis FLUSHALL
# Gateways answer SECURITY_STATE_MISSING within ~1-3s; the control plane restores within the 60s SLO.
```

Do not run this in production outside an announced drill: protected requests fail closed until the marker returns.

### 2.5 Residual windows

Two narrow windows exist and are both bounded by the 30s reconcile interval (see the header comment in `internal/control/securitystate.go`):
- **Resurrection race:** a reconcile that read a quarantine row just before an unquarantine deleted it can re-create the Redis key; the next reconcile prunes it.
- **Partial restore:** if Redis loses individual keys while the marker still exists (old marker, lost keys), those keys are re-asserted by the next reconcile, not by the marker check.

### 2.6 Manual fallback (control plane cannot start)

Prefer restarting the control plane. If that is impossible, rebuild by hand and write the marker last, only after every key is restored; otherwise gateways resume with incomplete state.

```bash
psql -h postgres -U aegis -d aegis -A -F"," -t -c "
  SELECT principal_id, reason, GREATEST(EXTRACT(EPOCH FROM (expires_at - NOW())), 1)::int
  FROM principal_quarantines WHERE expires_at > NOW();" > active_quarantines.csv
# Quarantine keys are quarantine:principal:<principal_id>; revoked tokens are revocation:jti:<jti>
# (restore the latter from revoked_tokens the same way, each with its remaining TTL).
# Last step, only when everything above succeeded:
redis-cli -h redis SET aegis:security-state:generation "manual-$(date -u +%FT%TZ)"
```

---

## 3. PostgreSQL Point-in-Time Recovery (PITR) & Version Reconciliation

When PostgreSQL is restored from backup, the restored latest snapshot version $M$ may be lower than the version $N$ actively cached in running gateway memory ($M < N$).

### Step-by-Step PITR Procedure
1. **Restore PostgreSQL Base Backup and Replay WAL:**
   ```bash
   pg_basebackup -h backup-host -D /var/lib/postgresql/data -U replicator -Fp -Xs -P -R
   # Configure recovery.signal with target time and start PostgreSQL
   touch /var/lib/postgresql/data/recovery.signal
   systemctl start postgresql
   ```
2. **Query Fleet Acknowledged Version:**
   Query connected gateways via Control Plane gRPC stream to discover active version:
   ```bash
   curl -s http://control-plane:8084/control/v1/gateways | jq '[.gateways[].active_version] | max'
   # Example output: 7 (Fleet is serving Version 7)
   ```
3. **Reconcile Monotonic Counter ($\max(N_{\text{gateway}}, M_{\text{db}}) + 1$):**
   ```sql
   -- If DB latest version is 4, but Fleet is on 7:
   -- Force next published snapshot version to 8 to prevent ErrMonotonicVersionViolation
   UPDATE config_snapshots_metadata
   SET current_version = 7
   WHERE id = 1;
   ```
4. **Publish Reconciled Snapshot:**
   ```bash
   curl -X POST http://control-plane:8084/control/v1/snapshots/publish \
     -H "Content-Type: application/json" \
     -d '{"reason":"PITR_DISASTER_RECOVERY_RECONCILIATION"}'
   # Control plane signs version 8; gateways accept without violation
   ```

---

## 4. Local WAL Spool Replay & Deduplication

When a gateway replica crashes mid-operation, un-ingested audit events remain on its dedicated PersistentVolume (`/var/log/aegis/wal`).

### Replay Protocol
1. **Audit Worker Startup:**
   The background `AuditWorker` mounts the PVC, reads `wal.cursor`, and scans existing segment files in lexicographical order.
2. **Resumption from Cursor Offset:**
   The worker reads from the byte offset recorded in `wal.cursor`. If the process crashed before the cursor was updated, it re-reads from the last committed offset.
3. **Deduplicated Database Insertion:**
   Events are written with `ON CONFLICT (event_date, event_id) DO NOTHING`. Any replayed events are silently ignored by PostgreSQL without throwing primary key violations.
4. **Verify Cursor Progress:**
   ```bash
   cat /var/log/aegis/wal/wal.cursor
   # Expected JSON: {"segment_file":"wal_0000000000000002.wal","offset":482910,"updated_at":"..."}
   ```

---

## 5. Cold-Cluster Bootstrap from Declarative Manifests

In the event of a total cluster loss, bootstrap into a clean Kubernetes namespace:

```bash
# 1. Apply Base Infrastructure and Namespaces
kubectl apply -k deployments/kubernetes/base

# 2. Deploy Storage State (PostgreSQL & Redis)
kubectl apply -f deployments/kubernetes/base/postgres/
kubectl apply -f deployments/kubernetes/base/redis/
kubectl wait --for=condition=ready pod -l app=postgres -n aegis-system --timeout=120s
kubectl wait --for=condition=ready pod -l app=redis -n aegis-system --timeout=60s

# 3. Run Goose Database Migrations
kubectl create job --from=cronjob/aegis-migration-bootstrap migration-init -n aegis-system
kubectl wait --for=condition=complete job/migration-init -n aegis-system --timeout=60s

# 4. Deploy Control Plane & Gateway Fleet
kubectl apply -k deployments/kubernetes/overlays/production
kubectl rollout status statefulset/aegis-gateway -n aegis-system --timeout=180s
```

---

## 6. Post-Recovery Verification Checklist

- [ ] All 3 gateway pods report `1/1 Running` and `readyz` returns 200 OK.
- [ ] `redis-cli EXISTS aegis:security-state:generation` returns 1 and control plane `/readyz` returns 200.
- [ ] `GET /control/v1/quarantine` `.quarantines | length` matches unexpired rows in `principal_quarantines`.
- [ ] Active snapshot version in gateways strictly matches control plane published version.
- [ ] Test mTLS request to backend microservices succeeds with HTTP 200 OK.
- [ ] Audit worker cursor is actively advancing with 0 ingestion errors.
