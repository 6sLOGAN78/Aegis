# SRE Runbook: Cryptographic Credential & Key Rotation

**Runbook ID:** RB-SEC-01  
**Category:** Cryptographic Operations & Security Hardening  
**Target Systems:** Ingress JWT Issuers, Control Plane Ed25519 Snapshot Signing, and Workload mTLS Intermediate CAs  
**Standard Structure:** Standard 6-Part Operational Runbook (Pattern 11)  

---

## 1. Metadata, Severity & Rotation Cadence

### Operational Classification
- **Standard Scheduled Rotation:** Maintenance Level (SEV-4 / Non-Disruptive). Executed during standard deployment windows with zero customer impact.
- **Emergency Suspected Compromise:** Incident Level (SEV-1 / Fast-Path Invalidation). Immediate key revocation and fleet reconfiguration.

### Rotation Cadence Table
| Credential Type | Algorithm / Standard | Rotation Cadence | Max Overlap Window | SLO Target Impact |
|---|---|---|---|---|
| **User Ingress Bearer JWT** | `EdDSA` / `RS256` / `ES256` | **30 Days** | 24 Hours ($> 2 \times \text{max token TTL}$) | 0% 401 Error Spikes (0 req/s drop) |
| **Snapshot Signing Key** | `Ed25519` | **90 Days** | 60 Minutes | 0 Rejected Snapshots / 0 Lease Timeouts |
| **Workload mTLS Intermediate CA** | `ECDSA P-256` / `x509` | **180 Days** | 7 Days | 0 Handshake Failures on `:8443` |
| **Emergency Compromise** | Any | **Immediate (< 15 min)** | 0 Seconds (Cutoff) | Bounded 401s for revoked key |

---

## 2. Prerequisites & Cryptographic Invariants

1. **Zero-Repair / Zero-Downtime Rule:** Key rotation must NEVER replace a verification key instantaneously without an active overlap window where both $\{K_{\text{old}}, K_{\text{new}}\}$ are trusted simultaneously.
2. **Key Material Isolation:** Gateway instances MUST ONLY receive public keys (`crypto.PublicKey`, `ed25519.PublicKey`) and public X.509 certificates. Gateway ConfigMaps and secrets must NEVER contain private keys.
3. **Clock Leeway Adherence:** Gateway `TokenValidator` enforces a 30-second clock skew leeway. Overlap windows must account for token expiration TTL + 30 seconds before retiring old keys.
4. **Required Tooling:**
   - `kubectl` v1.28+ authenticated to the production cluster.
   - `curl` or `httpie` for control plane management API calls.
   - `openssl` v3.0+ or `cfssl` for generating PKI materials.

---

## 3. Decision Tree: Routine vs. Emergency Rotation

```text
                     +---------------------------------------+
                     | Initiate Credential Rotation Trigger   |
                     +---------------------------------------+
                                         |
                                         v
                         /-------------------------------\
                        < Is Key Compromise Suspected?    >
                         \-------------------------------/
                               /                   \
                        YES   /                     \  NO (Routine Cadence)
                             v                       v
               +---------------------------+   +---------------------------------+
               | Fast-Path Emergency Cut   |   | Phase 1: Expand Keyset          |
               | (Section 6)               |   | Deploy {K_old, K_new} to Gateways|
               | - Invalidate old key now  |   +---------------------------------+
               | - Terminate active sessions|                   |
               | - Re-issue new credentials|                   v
               +---------------------------+   +---------------------------------+
                                               | Phase 2: Switch Active Signer   |
                                               | CP / Issuer signs with K_new    |
                                               +---------------------------------+
                                                               |
                                                               v
                                               +---------------------------------+
                                               | Wait Token TTL + Clock Leeway   |
                                               | In-flight tokens drain naturally|
                                               +---------------------------------+
                                                               |
                                                               v
                                               +---------------------------------+
                                               | Phase 3: Prune Retired Keyset   |
                                               | Retire K_old from Gateway Config|
                                               +---------------------------------+
                                                               |
                                                               v
                                               +---------------------------------+
                                               | Verification & Exit (Section 5) |
                                               +---------------------------------+
```

---

## 4. Step-by-Step Execution Procedures

### 4.1. User Ingress JWT Issuer Key Rotation

#### Phase 1: Keyset Expansion (Deploy $K_{\text{new}}$ Verification Key)
1. Generate new Ed25519 keypair for the auth issuer:
   ```bash
   openssl genpkey -algorithm ED25519 -out jwt-issuer-new.key
   openssl pkey -in jwt-issuer-new.key -pubout -out jwt-issuer-new.pub
   ```
2. Update the Gateway ConfigMap to include both public keys:
   ```bash
   kubectl create configmap gateway-jwt-keyset \
     --namespace aegis-system \
     --from-file=keyset.pub.0=jwt-issuer-current.pub \
     --from-file=keyset.pub.1=jwt-issuer-new.pub \
     --dry-run=client -o yaml | kubectl apply -f -
   ```
3. Trigger rolling reload of gateway replicas:
   ```bash
   kubectl rollout restart statefulset/aegis-gateway -n aegis-system
   kubectl rollout status statefulset/aegis-gateway -n aegis-system --timeout=120s
   ```
4. Confirm gateway logs show multi-key keyset loaded:
   ```bash
   kubectl logs -n aegis-system -l app=aegis-gateway --tail=50 | grep "TokenValidator: loaded"
   # Expected: "TokenValidator: loaded 2 trusted public verification keys"
   ```

#### Phase 2: Switch Active Signing Key in Auth Issuer
1. Update Auth Issuer service secret with `jwt-issuer-new.key`:
   ```bash
   kubectl create secret generic auth-issuer-signing-key \
     --namespace aegis-system \
     --from-file=active.key=jwt-issuer-new.key \
     --dry-run=client -o yaml | kubectl apply -f -
   kubectl rollout restart deployment/demo-issuer -n aegis-system
   ```
2. Monitor gateway Prometheus metrics:
   ```bash
   # Ensure zero increase in 401 Unauthorized responses
   rate(http_requests_total{status_code="401"}[1m]) == 0
   ```

#### Phase 3: Prune Retired Key (After Max Token TTL + 30s)
1. Wait for standard token TTL to elapse (e.g., 1 hour):
   ```bash
   sleep 3600
   ```
2. Remove retired `jwt-issuer-current.pub` from gateway keyset:
   ```bash
   kubectl create configmap gateway-jwt-keyset \
     --namespace aegis-system \
     --from-file=keyset.pub.0=jwt-issuer-new.pub \
     --dry-run=client -o yaml | kubectl apply -f -
   kubectl rollout restart statefulset/aegis-gateway -n aegis-system
   ```

---

### 4.2. Control Plane Snapshot Signing Key Rotation

#### Phase 1: Deploy New Control Plane Public Key to Gateways
1. Generate new Ed25519 signing keypair for Control Plane:
   ```bash
   openssl genpkey -algorithm ED25519 -out snapshot-signer-new.key
   openssl pkey -in snapshot-signer-new.key -pubout -out snapshot-signer-new.pub
   ```
2. Append `snapshot-signer-new.pub` to gateway snapshot trusted keys ConfigMap:
   ```bash
   kubectl patch configmap aegis-gateway-config -n aegis-system --type merge -p \
     '{"data":{"SNAPSHOT_TRUSTED_KEYS":"current-key-pub,new-key-pub"}}'
   kubectl rollout restart statefulset/aegis-gateway -n aegis-system
   ```

#### Phase 2: Activate New Signing Key on Control Plane
1. Inject `snapshot-signer-new.key` into Control Plane:
   ```bash
   kubectl create secret generic control-plane-signing-key \
     --namespace aegis-system \
     --from-file=signing.key=snapshot-signer-new.key \
     --dry-run=client -o yaml | kubectl apply -f -
   kubectl rollout restart deployment/aegis-control-plane -n aegis-system
   ```
2. Verify Control Plane publishes snapshot $N+1$ signed with new key:
   ```bash
   kubectl logs -n aegis-system -l app=aegis-control-plane --tail=50 | grep "Published snapshot"
   # Expected: "Published snapshot version=<N+1> signature_verified=true"
   ```
3. Verify gateways acknowledge receipt:
   ```bash
   kubectl logs -n aegis-system -l app=aegis-gateway --tail=50 | grep "Snapshot activated"
   # Expected: "Snapshot activated version=<N+1> status=ACK_STATUS_APPLIED"
   ```

#### Phase 3: Retire Old Key from Gateways
1. Remove old public key from `aegis-gateway-config`:
   ```bash
   kubectl patch configmap aegis-gateway-config -n aegis-system --type merge -p \
     '{"data":{"SNAPSHOT_TRUSTED_KEYS":"new-key-pub"}}'
   kubectl rollout restart statefulset/aegis-gateway -n aegis-system
   ```

---

### 4.3. Workload mTLS Intermediate CA Rotation

#### Phase 1: Install New Intermediate CA in Workload Trust Pool
1. Generate Intermediate CA 2 signed by Root CA:
   ```bash
   openssl req -new -newkey ec:<(openssl ecparam -name prime256v1) -nodes \
     -keyout intermediate-ca-2.key -out intermediate-ca-2.csr -subj "/CN=Aegis Workload Intermediate CA 2"
   openssl x509 -req -in intermediate-ca-2.csr -CA root-ca.crt -CAkey root-ca.key \
     -CAcreateserial -out intermediate-ca-2.crt -days 180 -extfile <(printf "basicConstraints=critical,CA:TRUE,pathlen:0\nkeyUsage=critical,digitalSignature,keyCertSign,cRLSign")
   ```
2. Bundle `intermediate-ca-1.crt` and `intermediate-ca-2.crt` into Gateway Workload CA pool:
   ```bash
   cat intermediate-ca-1.crt intermediate-ca-2.crt > combined-ca-bundle.crt
   kubectl create configmap aegis-workload-ca-bundle \
     --namespace aegis-system \
     --from-file=ca.crt=combined-ca-bundle.crt \
     --dry-run=client -o yaml | kubectl apply -f -
   kubectl rollout restart statefulset/aegis-gateway -n aegis-system
   ```

#### Phase 2: Issue New Workload Certificates from CA 2
1. Re-issue certificates for backend microservices (`orders`, `payments`, `admin`) using `intermediate-ca-2.crt`.
2. Microservices restart with new certificates. Gateways accept handshakes seamlessly.

#### Phase 3: Retire Intermediate CA 1
1. Replace `aegis-workload-ca-bundle` with `intermediate-ca-2.crt` only.
2. Roll out gateway restart. Workloads presenting CA 1 certs will now be rejected.

---

## 5. Verification & Exit Criteria

Before closing the maintenance ticket, execute the following operational checks:

```bash
# 1. Verify Gateway Readiness Probes
kubectl get pods -n aegis-system -l app=aegis-gateway
# All replicas must be 1/1 Running with 0 Restarts

# 2. Query Gateway Metrics for Authentication Status
curl -s http://gateway-replica-0:9091/metrics | grep "aegis_auth_verifications_total"
# Ensure error count is zero and success counters increment

# 3. Test Ingress Endpoints with Rotated Token
curl -k -H "Authorization: Bearer $(get-new-token)" https://gateway.aegis.local:8080/api/orders
# Must return HTTP 200 OK

# 4. Verify Monotonic Lease Renewals
curl -s http://gateway-replica-0:9091/metrics | grep "aegis_snapshot_lease_valid"
# Must report 1 (healthy lease <= 10s age)
```

---

## 6. Emergency Key Compromise Fast-Path Invalidation

If an active signing key is compromised or exfiltrated:

1. **Immediate Keyset Cutoff (No Overlap):**
   ```bash
   # Immediately replace gateway keyset with Emergency New Key only
   kubectl create configmap gateway-jwt-keyset -n aegis-system \
     --from-file=keyset.pub.0=emergency-new.pub --dry-run=client -o yaml | kubectl apply -f -
   # Force delete gateway pods to terminate active in-memory connections immediately
   kubectl delete pods -n aegis-system -l app=aegis-gateway --grace-period=0 --force
   ```
2. **Execute Principal Quarantine in Redis:**
   ```bash
   curl -X POST http://control-plane:8084/control/v1/quarantine \
     -H "Content-Type: application/json" \
     -d '{"principal_id":"compromised-actor","reason":"EMERGENCY_KEY_LEAK","ttl_seconds":86400}'
   ```
3. **Verify Compromised Tokens Rejected:**
   ```bash
   curl -i -H "Authorization: Bearer <compromised-token>" http://gateway:8080/api/orders
   # Expected: HTTP 401 Unauthorized / HTTP 403 Forbidden
   ```
4. **Conduct Post-Incident Forensic Audit:** Review PostgreSQL `audit_events` partition for all access during the suspected exposure window.
