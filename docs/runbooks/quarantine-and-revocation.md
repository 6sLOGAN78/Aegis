# SRE Runbook: Principal Quarantine & Token Revocation

**Runbook ID:** RB-SEC-02  
**Category:** Emergency Security Operations  
**Target Systems:** Redis Revocation Index, Control Plane REST APIs, Operator Dashboard, Gateway Ingress  
**Standard Structure:** Standard 6-Part Operational Runbook (Pattern 11)  

---

## 1. Use Cases & Incident Triggers

This runbook is invoked when a credential compromise or anomalous behavior requires immediate revocation of access:
- **Compromised User / Workload Principal:** Stolen device, leaked credentials, or insider threat.
- **Leaked API Bearer Token:** Secret posted in public GitHub repo or plaintext pastebin.
- **Detected Credential Stuffing / Brute Force:** Automated rate limit breach triggering automated or manual containment.
- **Target Response Time:** Enforce quarantine across all running gateway replicas in **< 5 seconds** (REV-03 SLA).

---

## 2. Emergency Quarantine via Operator Dashboard UI

1. Open the **Aegis Operator Dashboard** in browser (`https://dashboard.aegis.local:8084` or local dev `:5173`).
2. Navigate to **Access Control -> Quarantine Management**.
3. Click the red **"Emergency Quarantine Principal"** button.
4. Fill in the modal fields:
   - **Principal ID:** Enter the exact `sub` claim (e.g. `usr_compromised_9918`).
   - **Reason Code:** Select or type reason (e.g. `TOKEN_EXFILTRATION_INCIDENT`).
   - **TTL / Duration:** Set duration (e.g., `24 Hours`, `7 Days`, or `Permanent`).
5. **Typed Confirmation Gate:** Type `QUARANTINE` into the confirmation field to unlock the submit button.
6. Click **"Confirm & Broadcast Quarantine"**.
7. Confirm that the status badge updates to **"QUARANTINED"** and latency badge shows `< 50ms`.

---

## 3. Emergency Quarantine via Control Plane REST API

For automated SIEM/SOAR webhooks or headless SRE terminal operations:

```bash
# 1. Authenticate with Control Plane Management Token
MANAGEMENT_TOKEN=$(cat /etc/aegis/secrets/admin-token)

# 2. Issue Emergency Quarantine Request
curl -X POST http://control-plane:8084/control/v1/quarantine \
  -H "Authorization: Bearer ${MANAGEMENT_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "principal_id": "usr_compromised_9918",
    "reason": "CRITICAL_TOKEN_EXFILTRATION",
    "ttl_seconds": 86400
  }'

# Expected Response:
# HTTP 200 OK
# {"status":"success","principal_id":"usr_compromised_9918","quarantined_until":"2026-10-08T16:00:00Z"}
```

---

## 4. Emergency Mass JTI Revocation Procedure

When individual token JTIs must be invalidated without blacklisting the entire user account:

```bash
# 1. Revoke Specific Token JTI
curl -X POST http://control-plane:8084/control/v1/revocations \
  -H "Authorization: Bearer ${MANAGEMENT_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "jti": "jti-compromised-token-uuid-1234",
    "ttl_seconds": 3600
  }'

# Expected Response:
# HTTP 200 OK
# {"status":"revoked","jti":"jti-compromised-token-uuid-1234"}
```

---

## 5. Verification of Propagation Across Fleet (< 5s SLA)

To verify the quarantine is actively enforced across all gateway replicas:

```bash
# 1. Verify Redis Key Exists
redis-cli -h redis get "quarantine:principal:usr_compromised_9918"
# Expected output: "CRITICAL_TOKEN_EXFILTRATION"

# 2. Probe Gateway User Listener with Quarantined Token
curl -i -H "Authorization: Bearer $(mint-token-for usr_compromised_9918)" http://gateway:8080/api/orders
# Expected output:
# HTTP/1.1 403 Forbidden
# {"error":"access_denied","reason":"PRINCIPAL_QUARANTINED"}

# 3. Check Prometheus Metrics for Quarantine Enforcement Counter
curl -s http://gateway:9091/metrics | grep "aegis_quarantine_enforcements_total"
```

---

## 6. Quarantine Removal & Audit Sign-Off

Once an investigation is concluded and credentials have been securely reset:

```bash
# 1. Remove Principal Quarantine via REST API
curl -X DELETE http://control-plane:8084/control/v1/quarantine/usr_compromised_9918 \
  -H "Authorization: Bearer ${MANAGEMENT_TOKEN}"

# 2. Verify Principal Access Restored with Fresh Token
curl -i -H "Authorization: Bearer $(mint-fresh-token-for usr_compromised_9918)" http://gateway:8080/api/orders
# Expected output: HTTP/1.1 200 OK

# 3. Document Action in Security Incident Ticket
# Record: Operator ID, Ticket Reference, Time of Quarantine, Time of Removal, Reason.
```
