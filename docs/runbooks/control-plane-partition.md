# SRE Runbook: Control Plane Network Partition & Lease Expiry Recovery

**Runbook ID:** RB-OPS-04  
**Category:** Control Plane Resilience & Freshness Leases  
**Target Systems:** Aegis Control Plane gRPC Stream (:9090), Gateway Snapshot Manager, Freshness Lease Evaluator  
**Standard Structure:** Standard 6-Part Operational Runbook (Pattern 11)  

---

## 1. Metadata, Alert Triggers & Symptoms

### Operational Metadata
- **Severity Level:** SEV-1 (When lease exceeds 60s fail-closed boundary) / SEV-2 (Heartbeat missed for >30s).
- **Primary Mechanism:** Invariant 9 & ADR-0004: Gateways require signed 10-second freshness leases from the control plane over gRPC. If leases cease for $>60$ seconds, gateway readiness fails (`readyz` 503) and requests fail closed (`LEASE_EXPIRED`).
- **Prometheus Alerts:**
  - `AegisSnapshotLeaseExpired`: Time since last valid signed lease $>60$s.
  - `AegisControlPlaneDisconnected`: Gateway gRPC stream severed.
- **Client Symptom:** Ingress callers receive `HTTP 503 Service Unavailable` with `reason_code: LEASE_EXPIRED`.

---

## 2. Triage Decision Tree

```text
                     +---------------------------------------+
                     | Alert: AegisSnapshotLeaseExpired      |
                     +---------------------------------------+
                                         |
                                         v
                         /-------------------------------\
                        < How long has lease been expired?>
                         \-------------------------------/
                               /                   \
                        < 60s /                     \  > 60s (Fail-Closed Tripped)
                             v                       v
               +---------------------------+   +---------------------------------+
               | Warning State: Grace Period|  | Gateway Dropped Readiness       |
               | - Traffic still allowed   |   | - All protected routes 503      |
               | - Reconnect jitter active |   | - Reconnect stream immediately  |
               +---------------------------+   +---------------------------------+
                             |                               |
                             +---------------+---------------+
                                             |
                                             v
                         /---------------------------------------\
                        < Is Control Plane Pod Running & Healthy? >
                         \---------------------------------------/
                               /                   \
                        YES   /                     \  NO (Pod Crash / OOM)
                             v                       v
               +---------------------------+   +---------------------------------+
               | Investigate Network & mTLS|   | Check K8s Pod Status            |
               | - CoreDNS port 53 egress? |   | - kubectl describe pod cp       |
               | - Port 9090 mTLS cert exp?|   | - Restart control plane         |
               +---------------------------+   +---------------------------------+
```

---

## 3. Step-by-Step Remediation Procedures

### Procedure A: Inspect Control Plane Status
1. Check if Control Plane pods are running:
   ```bash
   kubectl get pods -n aegis-system -l app=aegis-control-plane
   # Expected: 2/2 Running
   ```
2. If pods are in `CrashLoopBackOff`, inspect logs:
   ```bash
   kubectl logs -n aegis-system -l app=aegis-control-plane --tail=50
   ```
3. Restart stuck control plane instances:
   ```bash
   kubectl rollout restart deployment/aegis-control-plane -n aegis-system
   ```

### Procedure B: Verify gRPC Port 9090 mTLS & Certificates
1. Check if the control plane gRPC server certificate expired:
   ```bash
   openssl s_client -connect aegis-control-plane.aegis-system.svc.cluster.local:9090 -showcerts </dev/null 2>/dev/null | openssl x509 -noout -dates
   ```
2. If expired, regenerate or rotate the certificate using `docs/runbooks/credential-rotation.md`.

### Procedure C: Inspect NetworkPolicy & CoreDNS Resolution
1. Verify Gateway NetworkPolicy allows outbound traffic to CoreDNS (port 53 UDP/TCP) and Control Plane (port 9090 TCP):
   ```bash
   kubectl describe networkpolicy aegis-gateway-networkpolicy -n aegis-system
   ```
2. Verify DNS resolution from within a gateway container:
   ```bash
   kubectl exec statefulset/aegis-gateway-0 -n aegis-system -- getent hosts aegis-control-plane
   ```

### Procedure D: Verify Reconnect Backoff with Jitter
Gateways implement exponential backoff with randomized jitter (100ms base, 5s max) to prevent thundering herd upon control plane recovery.
1. Check gateway logs for reconnect attempts:
   ```bash
   kubectl logs statefulset/aegis-gateway-0 -n aegis-system --tail=30 | grep "gRPC stream reconnect"
   # Expected: Reconnecting in <jittered_duration>ms
   ```

---

## 4. Verification & Convergence Telemetry

Confirm that all gateways reconnected, renewed freshness leases, and converged on active configuration:

```bash
# 1. Query Gateway Fleet Convergence via Control Plane REST API
curl -s http://control-plane:8084/control/v1/gateways | jq .
# Expected: All gateway instances report status: "CONNECTED", lease_age_seconds: < 5s

# 2. Check Gateway Prometheus Metrics
curl -s http://gateway-0:9091/metrics | grep "aegis_snapshot_lease_valid"
# Expected: aegis_snapshot_lease_valid 1

# 3. Verify Ingress Protected Routes Pass
curl -i -H "Authorization: Bearer $(get-token)" http://gateway.aegis.local:8080/api/orders
# Expected: HTTP 200 OK
```

---

## 5. Post-Partition Verification Checklist

- [ ] Control Plane gRPC stream is connected across all 3 gateway replicas.
- [ ] Freshness leases are renewing every 10 seconds with valid Ed25519 signatures.
- [ ] Gateway `/readyz` probes return HTTP 200 OK.
- [ ] Client error rate on protected routes returned to 0%.
- [ ] Sub-5-second convergence confirmed across replica fleet.
