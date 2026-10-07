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
  - Cache Reconstruction: **< 60 seconds** (Pipelined quarantine restore).

### Failure Scenarios Covered
1. **Catastrophic Redis Memory Wipeout:** Cache nodes crash and restart empty.
2. **PostgreSQL Point-in-Time Recovery (PITR):** Database restore from backup with version desynchronization.
3. **Gateway Host / Node Crash:** Local pod killed with uncommitted `.wal` segments on persistent disk.
4. **Complete Multi-Zone Data Center Failure:** Cold bootstrap into a clean Kubernetes cluster.

---

## 2. Redis State Reconstruction Procedure

When Redis restarts with empty memory, gateways fail closed to prevent quarantined actors from accessing backends. Gateways MUST NOT be marked ready until active security state is repopulated.

### Step-by-Step Restoration
1. **Extract Active Quarantines from PostgreSQL:**
   ```bash
   psql -h postgres -U aegis -d aegis_control -c "
     SELECT principal_id, reason, GREATEST(EXTRACT(EPOCH FROM (expires_at - NOW())), 60)::int AS remaining_ttl
     FROM principal_quarantines
     WHERE expires_at > NOW();
   " -A -F"," -t > active_quarantines.csv
   ```
2. **Execute Pipelined Batch Reconstruction:**
   Trigger the control plane DR reconstruction routine or execute via `redis-cli`:
   ```bash
   cat active_quarantines.csv | awk -F',' '{print "SET quarantine:principal:"$1" "$2" EX "$3}' | redis-cli -h redis --pipe
   ```
3. **Verify Restoration via Store API:**
   ```bash
   curl -s http://control-plane:8084/control/v1/quarantine | jq '.principals | length'
   # Expected: Matches number of active records in PostgreSQL
   ```
4. **Allow Gateway Readiness Probes to Pass:**
   ```bash
   # Gateways will query Redis CheckRevocation successfully and /readyz will return 200 OK
   curl -i http://gateway:8080/readyz
   # Expected: HTTP 200 OK {"status":"UP"}
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
- [ ] Redis contains all unexpired principal quarantines (`quarantine:principal:*`).
- [ ] Active snapshot version in gateways strictly matches control plane published version.
- [ ] Test mTLS request to backend microservices succeeds with HTTP 200 OK.
- [ ] Audit worker cursor is actively advancing with 0 ingestion errors.
