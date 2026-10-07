# SRE Runbook: Local WAL Spool Saturation Recovery

**Runbook ID:** RB-OPS-03  
**Category:** Storage & Admission Circuit Breaking  
**Target Systems:** Gateway Local WAL Spool (`/var/log/aegis/wal`), Ingestion Audit Worker, PostgreSQL Batch Sink  
**Standard Structure:** Standard 6-Part Operational Runbook (Pattern 11)  

---

## 1. Metadata, Alert Triggers & Symptoms

### Operational Metadata
- **Severity Level:** SEV-1 (When admissions halted at $\ge 90\%$) / SEV-2 (Warning at $\ge 80\%$).
- **Trigger Metric:** `aegis_wal_spool_bytes / aegis_wal_spool_quota_bytes >= 0.90`
- **Prometheus Alert:** `AegisWALSpoolSaturated`
- **Client Symptom:** Ingress callers receive `HTTP 503 Service Unavailable` with RFC 7807 problem details: `SPOOL_SATURATED: local audit storage exceeds 90% capacity`.

---

## 2. Triage Decision Tree

```text
                     +---------------------------------------+
                     | Alert: AegisWALSpoolSaturated (>90%)  |
                     +---------------------------------------+
                                         |
                                         v
                         /-------------------------------\
                        < Is PostgreSQL Healthy & Reachable? >
                         \-------------------------------/
                               /                   \
                        YES   /                     \  NO (Database Outage)
                             v                       v
               +---------------------------+   +---------------------------------+
               | Check Audit Worker Status |   | 1. Restore PostgreSQL DB        |
               | - Worker pod crashed?     |   | 2. Drain will resume once DB up |
               | - DB connection pool full?|   | 3. Expand PVC if disk critical  |
               +---------------------------+   +---------------------------------+
                             |
                             v
               /-------------------------------\
              < Is Disk Capacity Exhausted?    >
               \-------------------------------/
                     /                   \
              YES   /                     \  NO (Worker Ingestion Bottleneck)
                   v                       v
      +---------------------------+  +-----------------------------------+
      | Dynamic PVC Volume Expand |  | Scale Worker & Prune Old Segments |
      +---------------------------+  +-----------------------------------+
```

---

## 3. Step-by-Step Remediation Procedures

### Procedure A: Verify Database Reachability & Worker Status
1. Check if PostgreSQL primary is accepting writes:
   ```bash
   kubectl exec -it statefulset/postgres -n aegis-system -- pg_isready -U aegis
   # Expected: accepting connections
   ```
2. Check audit worker logs for database batch insertion errors:
   ```bash
   kubectl logs -n aegis-system -l app=aegis-audit-worker --tail=50 | grep -E "batch|pgx|error"
   ```
3. Restart stuck audit workers if connection pool stalled:
   ```bash
   kubectl rollout restart deployment/aegis-audit-worker -n aegis-system
   ```

### Procedure B: Manually Scale Ingestion Throughput
If traffic volume surged beyond single-worker batch ingestion rate:
1. Increase worker concurrency or reduce flush interval in ConfigMap:
   ```bash
   kubectl patch configmap aegis-audit-worker-config -n aegis-system --type merge -p \
     '{"data":{"BATCH_SIZE":"1000","FLUSH_INTERVAL_MS":"50"}}'
   kubectl rollout restart deployment/aegis-audit-worker -n aegis-system
   ```

### Procedure C: Dynamic PVC Volume Expansion (Emergency Space Recovery)
If disk volume quota is physically near 100%:
1. Expand the volumeClaimTemplate / PVC in Kubernetes:
   ```bash
   kubectl patch pvc wal-spool-aegis-gateway-0 -n aegis-system -p '{"spec":{"resources":{"requests":{"storage":"5Gi"}}}}'
   kubectl patch pvc wal-spool-aegis-gateway-1 -n aegis-system -p '{"spec":{"resources":{"requests":{"storage":"5Gi"}}}}'
   kubectl patch pvc wal-spool-aegis-gateway-2 -n aegis-system -p '{"spec":{"resources":{"requests":{"storage":"5Gi"}}}}'
   ```

### Procedure D: Archive & Prune Committed Segments
If segments were processed by the worker but not automatically deleted:
1. Verify `wal.cursor` checkpoint offset to ensure records are safely in PostgreSQL:
   ```bash
   kubectl exec statefulset/aegis-gateway-0 -n aegis-system -- cat /var/log/aegis/wal/wal.cursor
   ```
2. Compress and archive processed segments to remote object storage:
   ```bash
   kubectl exec statefulset/aegis-gateway-0 -n aegis-system -- /usr/local/bin/aegis-wal-prune --older-than-cursor
   ```

---

## 4. Verification & Drain Monitoring

Monitor the gateway capacity draining below the 90% circuit breaker threshold:

```bash
# 1. Check Filesystem Utilization
kubectl exec statefulset/aegis-gateway-0 -n aegis-system -- df -h /var/log/aegis/wal
# Must report < 80% usage

# 2. Check Prometheus Metric
curl -s http://gateway-0:9091/metrics | grep "aegis_wal_spool_saturation_ratio"
# Value must drop below 0.90

# 3. Verify Permitted Ingress Traffic Resumes
curl -i -H "Authorization: Bearer $(get-valid-token)" http://gateway.aegis.local:8080/api/orders
# Expected: HTTP 200 OK
```

---

## 5. Post-Incident Review & Capacity Planning

- **Volume Quota Review:** Adjust PVC default sizes in `deployments/kubernetes/base/gateway/statefulset.yaml` from 2Gi to 10Gi if sustained ingestion requires longer buffer capacity.
- **Worker Auto-scaling:** Verify HPA triggers on `aegis_audit_worker_lag_seconds`.
