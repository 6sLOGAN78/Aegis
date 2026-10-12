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
   - **Expiry (`expires_at`):** Set duration (e.g., `24 Hours`, `7 Days`, or `Permanent`).
5. **Typed Confirmation Gate:** Type `QUARANTINE` into the confirmation field to unlock the submit button.
6. Click **"Confirm & Broadcast Quarantine"**.
7. Confirm that the status badge updates to **"QUARANTINED"** and latency badge shows `< 50ms`.

---

## 3. Emergency Quarantine via Control Plane REST API

For automated SIEM/SOAR webhooks or headless SRE terminal operations. The management API uses an operator **session cookie plus a CSRF header** (not a Bearer token; data plane Bearer credentials are rejected on management endpoints). The caller needs the `sec-ops` role.

**Transport.** The control plane serves **plain HTTP** on `:8084` (`http://control-plane:8084` inside the compose network, the same URL the seed job and healthchecks use); there is no TLS listener, so `https://` fails with a TLS handshake error. The `aegis_session` cookie is issued with `Secure`, so a cookie jar (`curl -c/-b cookies.txt`) will **not** send it back over plain HTTP (curl makes an exception only for `localhost`). Extract the session id from the `Set-Cookie` header and pass it explicitly as `-H "Cookie: aegis_session=${SESSION}"` (this is what `op_login` in `scripts/compose-smoke.sh` does). On the mvp profile port 8084 is not published to the host, so run these commands inside the compose network, e.g. prefix each `curl` with `docker compose -f deployments/compose/docker-compose.mvp.yml exec -T control-plane`. On hardened and distributed it is also published as `http://localhost:8084`.

```bash
CP_URL=http://control-plane:8084

# 1. Log in as an operator. Capture the session id from Set-Cookie and the
#    csrf_token from the JSON body ({username, role, csrf_token, expires_at}).
RESP="$(curl -s -i -X POST "${CP_URL}/control/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"username":"<operator>","password":"<operator-password>"}' | tr -d '\r')"
SESSION="$(printf '%s\n' "${RESP}" | sed -n 's/^[Ss]et-[Cc]ookie: aegis_session=\([^;]*\).*/\1/p' | head -n1)"
CSRF_TOKEN="$(printf '%s\n' "${RESP}" | sed -n '/^$/,$p' | jq -r '.csrf_token')"

# 2a. Path form. SPIFFE ids MUST be percent-encoded (an unencoded "/" returns 404):
#     spiffe://aegis.local/workload/orders -> spiffe%3A%2F%2Faegis.local%2Fworkload%2Forders
curl -s -X POST \
  "${CP_URL}/control/v1/principals/spiffe%3A%2F%2Faegis.local%2Fworkload%2Forders/quarantine" \
  -H "Cookie: aegis_session=${SESSION}" \
  -H "X-CSRF-Token: ${CSRF_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"reason":"CRITICAL_TOKEN_EXFILTRATION","expires_at":"2026-10-08T16:00:00Z"}'

# 2b. Body form (no path encoding needed; principal_id in the body):
curl -s -X POST "${CP_URL}/control/v1/quarantine" \
  -H "Cookie: aegis_session=${SESSION}" \
  -H "X-CSRF-Token: ${CSRF_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"principal_id":"usr_compromised_9918","reason":"CRITICAL_TOKEN_EXFILTRATION","expires_at":"2026-10-08T16:00:00Z"}'
```

Notes:
- Quarantine duration is set with `expires_at` (RFC 3339 timestamp), not `ttl_seconds`. Omit it for the default duration.
- Only `aegis.local` SPIFFE ids (`spiffe://aegis.local/...`) are accepted as workload principals.
- Quarantine takes effect on every gateway replica within the **5 second** per-replica convergence target (ADR-0005).

---

## 4. Emergency JTI Revocation Procedure

When an individual token must be invalidated without blacklisting the entire user account. Every token minted by the demo issuer now carries a `jti`, returned in the issuer login response:

```bash
# 0. Issuer login: body is {username,password}; the response includes access_token and jti.
curl -s -X POST http://demo-issuer:8085/login \
  -H "Content-Type: application/json" \
  -d '{"username":"developer","password":"<demo-password>"}'

# 1. Revoke the token by jti (same plain-HTTP transport, Cookie and CSRF headers as section 3).
curl -s -X POST "${CP_URL}/control/v1/revocations" \
  -H "Cookie: aegis_session=${SESSION}" \
  -H "X-CSRF-Token: ${CSRF_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"jti":"<jti from login response>","reason":"LEAKED_TOKEN","ttl_seconds":3600}'
```

- `ttl_seconds` defaults to 24 hours if omitted; set it at least as long as the remaining token lifetime.
- `ttl_seconds` above 2592000 (30 days) and a `jti` longer than 128 bytes are rejected with `400 BAD_REQUEST` before anything is written.
- Tokens that carry **no `jti`** are denied by the gateway with `401 TOKEN_MISSING_JTI`, so a revoked token cannot be replayed by stripping the claim.
- Revoked jtis are enforced across all replicas within the 5 second convergence target (ADR-0005).

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
#    (SPIFFE ids must be percent-encoded in the path, e.g. spiffe%3A%2F%2Faegis.local%2Fworkload%2Forders)
#    (same plain-HTTP transport, Cookie and CSRF headers as section 3)
curl -s -X DELETE \
  "${CP_URL}/control/v1/principals/usr_compromised_9918/quarantine" \
  -H "Cookie: aegis_session=${SESSION}" \
  -H "X-CSRF-Token: ${CSRF_TOKEN}"

# 2. Verify Principal Access Restored with Fresh Token
curl -i -H "Authorization: Bearer $(mint-fresh-token-for usr_compromised_9918)" http://gateway:8080/api/orders
# Expected output: HTTP/1.1 200 OK

# 3. Document Action in Security Incident Ticket
# Record: Operator ID, Ticket Reference, Time of Quarantine, Time of Removal, Reason.
```
