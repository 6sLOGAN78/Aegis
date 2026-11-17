# SRE Runbook: Local WAL Spool Saturation Recovery

**Runbook ID:** RB-OPS-03  
**Category:** Storage & Admission Circuit Breaking  
**Target Systems:** Gateway Local WAL Spool (`/var/log/aegis/wal`), Ingestion Audit Worker, PostgreSQL Batch Sink  
**Standard Structure:** Standard 6-Part Operational Runbook (Pattern 11)  

---

## 1. Metadata, Alert Triggers & Symptoms

### Operational Metadata
- **Severity Level:** SEV-1 (When admissions halted at $\ge 90\%$) / SEV-2 (Warning at $\ge 80\%$).
- **Trigger Metric:** `aegis_spool_utilization_ratio >= 0.90` (gateway, `:9091/metrics`; filesystem used/total of the WAL volume)
- **Alerting:** no Prometheus or alert rules ship with Aegis. Alert on the metric above, and on `/readyz` returning 503 with `SPOOL_SATURATED`.
- **Client Symptom:** Ingress callers receive `HTTP 503 Service Unavailable` with RFC 7807 problem details: `SPOOL_SATURATED: local audit storage exceeds 90% capacity`.

---

## 2. Triage Decision Tree

```text
                     +---------------------------------------+
                     | aegis_spool_utilization_ratio >= 0.90 |
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
   kubectl exec -it statefulset/postgres -n aegis-system -- sh -c 'pg_isready -U "$POSTGRES_USER"'
   # Expected: accepting connections
   ```
2. Check audit worker logs for database batch insertion errors:
   ```bash
   kubectl logs -n aegis-system -l app=aegis-gateway -c audit-worker --tail=50 | grep -E "batch|pgx|error"
   ```
3. Restart a stuck audit worker if its connection pool stalled. It is a sidecar container of
   the gateway pod; restart only that container (the kubelet starts it again, the gateway keeps
   serving, and the worker resumes from `wal.cursor`):
   ```bash
   kubectl exec pod/aegis-gateway-0 -c audit-worker -n aegis-system -- kill -TERM 1
   ```
   Avoid `kubectl rollout restart statefulset/aegis-gateway` for this: every pod waits at least
   55s in Terminating (the sidecar's preStop sleep), so a 3-replica rollout takes about 3 minutes
   at reduced capacity.

### Procedure B: Manually Scale Ingestion Throughput
If traffic volume surged beyond single-worker batch ingestion rate:
1. Increase batch size or reduce the flush interval by setting env on the `audit-worker`
   container of `statefulset/aegis-gateway` (this triggers a rolling restart):
   ```bash
   kubectl set env statefulset/aegis-gateway -c audit-worker -n aegis-system \
     AEGIS_AUDIT_BATCH_SIZE=1000 AEGIS_AUDIT_FLUSH_INTERVAL_MS=50
   ```
2. These settings are inline env in `base/gateway/statefulset.yaml`, so make the same change
   in the manifest or an overlay to persist it across deploys.

### Procedure C: Dynamic PVC Volume Expansion (Emergency Space Recovery)
If disk volume quota is physically near 100%:
1. Expand the volumeClaimTemplate / PVC in Kubernetes:
   ```bash
   kubectl patch pvc wal-spool-aegis-gateway-0 -n aegis-system -p '{"spec":{"resources":{"requests":{"storage":"5Gi"}}}}'
   kubectl patch pvc wal-spool-aegis-gateway-1 -n aegis-system -p '{"spec":{"resources":{"requests":{"storage":"5Gi"}}}}'
   kubectl patch pvc wal-spool-aegis-gateway-2 -n aegis-system -p '{"spec":{"resources":{"requests":{"storage":"5Gi"}}}}'
   ```

### Procedure D: Delivered Segments Not Pruned
The audit worker deletes a segment after its cursor moves past it, but only when the
sidecar has `AEGIS_PRUNE_ARCHIVED=true` (set in `base/gateway/statefulset.yaml`).
1. Confirm the setting and see where the cursor is:
   ```bash
   kubectl exec pod/aegis-gateway-0 -c audit-worker -n aegis-system -- printenv AEGIS_PRUNE_ARCHIVED
   kubectl exec pod/aegis-gateway-0 -c audit-worker -n aegis-system -- cat /var/log/aegis/wal/wal.cursor
   kubectl exec pod/aegis-gateway-0 -c audit-worker -n aegis-system -- ls -l /var/log/aegis/wal
   ```
2. If it is unset, add it to the manifest or an overlay and roll out. Segments ordered before
   the cursor's segment are delivered and can be archived or removed by hand; never remove the
   cursor's segment or anything after it (those records are not yet in PostgreSQL).

---

## 4. Verification & Drain Monitoring

Monitor the gateway capacity draining below the 90% circuit breaker threshold:

```bash
# 1. Check Filesystem Utilization
kubectl exec pod/aegis-gateway-0 -c gateway -n aegis-system -- df -h /var/log/aegis/wal
# Must report < 80% usage

# 2. Check Prometheus Metric
curl -s http://gateway-0:9091/metrics | grep "aegis_spool_utilization_ratio"
# Value must drop below 0.90

# 3. Verify Permitted Ingress Traffic Resumes
curl -i -H "Authorization: Bearer $(get-valid-token)" http://gateway.aegis.local:8080/api/orders
# Expected: HTTP 200 OK
```

---

## 5. Post-Incident Review & Capacity Planning

- **Volume Quota Review:** Adjust PVC default sizes in `deployments/kubernetes/base/gateway/statefulset.yaml` from 2Gi to 10Gi if sustained ingestion requires longer buffer capacity.
- **Worker throughput:** there is one worker per gateway pod (sidecar) and no autoscaling. Watch `aegis_spool_utilization_ratio` and `aegis_audit_records_written_total`; raise `AEGIS_AUDIT_BATCH_SIZE` (Procedure B) if the ratio climbs under sustained load.
