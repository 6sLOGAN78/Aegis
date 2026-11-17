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
| Credential Type | Algorithm / Standard | Rotation Cadence | Minimum Overlap | Hot reload? |
|---|---|---|---|---|
| **JWT verification keyset** | `Ed25519` | **30 Days** | reload interval + max token TTL + 30s | Yes (directory poll and SIGHUP) |
| **Snapshot verification keyset** | `Ed25519` | **90 Days** | reload interval + 30s, plus a republish (see 4.2) | Yes (directory poll and SIGHUP) |
| **Workload mTLS CA / certificates** | `ECDSA P-256` / `x509` | **180 Days** | 7 Days | **No. Restart required (section 4.3)** |
| **Emergency Compromise** | Any | **Immediate (< 15 min)** | 0 Seconds (Cutoff) | Yes for keysets |

---

## 2. Prerequisites & Cryptographic Invariants

1. **Overlap rule:** never replace a verification key in one step. Both the old and new public keys must be trusted at the same time for at least `AEGIS_KEYSET_RELOAD_INTERVAL` (poll interval) + max token TTL + 30 seconds (gateway clock-skew leeway) before the old key is removed.
2. **Separate keys:** the JWT issuer key and the snapshot signing key are different keys with different keysets. Never reuse one for the other.
3. **Key material isolation:** gateways receive public keys only. Private keys live only with the signer (the issuer for JWTs, the control plane for snapshots). Keep private keys out of ConfigMaps, tickets and this document.
4. **Keysets are directories.** Gateways read `AEGIS_JWT_KEYSET_DIR` and `AEGIS_SNAPSHOT_KEYSET_DIR`. Every regular file in the directory whose name does not start with `.` is one public key. Names are free-form: `keyset.pub.0`, `keyset.pub.1` and `current.pub` all work. Dot files (including Kubernetes `..data` symlink internals) and non-regular entries are ignored.
5. **Reload behavior:**
   - The directory is re-read every `AEGIS_KEYSET_RELOAD_INTERVAL` (default 10s; values are clamped to 1s-10m; the compose profile uses 2s) and immediately on `SIGHUP`.
   - If any file fails to parse, or the directory is empty, the whole reload is rejected and the **last good set stays in force**. Partial sets are never applied. Every rejected attempt logs the offending file name (`keyset[<family>]: RELOAD REJECTED ...`), increments `aegis_keyset_reload_total{family,result="error"}`, and holds `aegis_keyset_reload_failing{family}` at `1` until a reload succeeds.
   - **A rejected reload also blocks removals.** If a change prunes a key (routine phase 3 or an emergency cutoff) and the same or a later change leaves any bad entry in the directory (an editor backup such as `keyset.pub.1~`, a README, a half-written file), every gateway keeps trusting the removed key. Treat `aegis_keyset_reload_failing == 1` as an incident: alert on it (for example `max by (family) (aegis_keyset_reload_failing) == 1 for 2m`), and follow section 5.1.
6. **Signers load private keys from files:** the control plane uses `AEGIS_SIGNING_KEY_PATH`, the demo issuer uses `AEGIS_ISSUER_PRIVATE_KEY_PATH`. A configured path that cannot be loaded is fatal at startup. The built-in demo seed (used only when the path is unset) is for development and must not run in production. Signers do not hot reload: switching a signer key means restarting that signer.
7. **Required tooling:** `kubectl` authenticated to the cluster, `curl`, and `go` (to run the keypair generator from this repository).

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
               | Fast-Path Emergency Cut   |   | Phase 1: Expand keyset          |
               | (Section 6)               |   | add the new public key file     |
               +---------------------------+   +---------------------------------+
                                                               |
                                                               v
                                               +---------------------------------+
                                               | Phase 2: Switch signer          |
                                               | restart signer with new key     |
                                               +---------------------------------+
                                                               |
                                                               v
                                               +---------------------------------+
                                               | Wait reload + token TTL + 30s   |
                                               +---------------------------------+
                                                               |
                                                               v
                                               +---------------------------------+
                                               | Phase 3: Prune old public key   |
                                               +---------------------------------+
                                                               |
                                                               v
                                               +---------------------------------+
                                               | Verification & Exit (Section 5) |
                                               +---------------------------------+
```

---

## 4. Step-by-Step Execution Procedures

Object names used below:

| Purpose | Kubernetes object (namespace `aegis-system`) | Compose |
|---|---|---|
| JWT public keyset | ConfigMap `gateway-jwt-keyset` | `${AEGIS_KEYS_DIR}/keyset-jwt` mounted at `/keysets/jwt` |
| Snapshot public keyset | ConfigMap `gateway-snapshot-keyset` | `${AEGIS_KEYS_DIR}/keyset-snapshot` mounted at `/keysets/snapshot` |
| Snapshot signing private key | Secret `aegis-control-plane-signing-key` (key `snapshot-current.key`) | `${AEGIS_KEYS_DIR}/snapshot-current.key` |
| JWT issuer private key | issuer deployment's own secret (the demo issuer is a compose/demo component) | `${AEGIS_KEYS_DIR}/jwt-current.key` |

`AEGIS_KEYS_DIR` defaults to `deployments/keys` (`../keys` relative to the compose files). `deployments/kubernetes/secrets/create-secrets.sh` creates the Kubernetes objects; see `deployments/kubernetes/secrets/README.md`.

### 4.1. JWT Issuer Key Rotation

#### Phase 1: Expand (trust both public keys)
1. Generate a new Ed25519 keypair (writes `<prefix>.key` private and `<prefix>.pub` public, both base64):
   ```bash
   go run ./scripts/certificates -keypair jwt-new
   ```
2. Add the new public key next to the current one (name it `keyset.pub.1`). Kubernetes:
   ```bash
   kubectl -n aegis-system get configmap gateway-jwt-keyset -o yaml   # note the current entry name, keyset.pub.0
   kubectl -n aegis-system create configmap gateway-jwt-keyset \
     --from-file=keyset.pub.0=jwt-current.pub \
     --from-file=keyset.pub.1=jwt-new.pub \
     --dry-run=client -o yaml | kubectl apply -f -
   ```
   Compose: copy `jwt-new.pub` into `deployments/keys/keyset-jwt/` under a new name.
3. No gateway restart is needed. Wait for the reload interval (a mounted ConfigMap also takes up to the kubelet sync period to appear), or force it:
   ```bash
   kubectl -n aegis-system exec aegis-gateway-0 -- kill -HUP 1   # optional; adjust to the gateway PID if different
   ```
4. Confirm both keys are loaded:
   ```bash
   curl -s http://gateway-replica-0:9091/metrics | grep -E 'aegis_keyset_keys\{family="jwt"\}|aegis_keyset_reload_total'
   # Expected: aegis_keyset_keys{family="jwt"} 2, no new result="error"
   ```

#### Phase 2: Switch the signer
1. Point the issuer at the new private key (`AEGIS_ISSUER_PRIVATE_KEY_PATH`) and restart it. For compose, replace the mounted `jwt-current.key` and run `docker compose restart demo-issuer`.
2. Confirm tokens signed by the new key are accepted and watch for a rise in 401 responses.

#### Phase 3: Prune the old key
1. Wait at least `AEGIS_KEYSET_RELOAD_INTERVAL` + max token TTL + 30s after the switch (in-flight tokens signed by the old key must expire first).
2. Remove the old entry:
   ```bash
   kubectl -n aegis-system create configmap gateway-jwt-keyset \
     --from-file=keyset.pub.1=jwt-new.pub \
     --dry-run=client -o yaml | kubectl apply -f -
   ```
3. Confirm `aegis_keyset_keys{family="jwt"} 1` after the next reload. Never leave the directory empty; an empty set is rejected and the old set stays active.

---

### 4.2. Control Plane Snapshot Signing Key Rotation

The gateway snapshot keyset is separate from the JWT keyset and uses `AEGIS_SNAPSHOT_KEYSET_DIR`.

#### Phase 1: Expand
1. Generate a keypair: `go run ./scripts/certificates -keypair snapshot-new`.
2. Add the new public key to `gateway-snapshot-keyset` as `keyset.pub.1` next to `keyset.pub.0` (same `kubectl create configmap ... --dry-run=client -o yaml | kubectl apply -f -` pattern, with the name `gateway-snapshot-keyset`). Compose: copy the `.pub` into `deployments/keys/keyset-snapshot/`.
3. Confirm `aegis_keyset_keys{family="snapshot"} 2` on every gateway.

#### Phase 2: Switch the signer
1. Replace the control plane signing key and restart it:
   ```bash
   kubectl -n aegis-system create secret generic aegis-control-plane-signing-key \
     --from-file=snapshot-current.key=snapshot-new.key \
     --dry-run=client -o yaml | kubectl apply -f -
   kubectl -n aegis-system rollout restart deployment/aegis-control-plane
   ```
   The control plane reads the key from `AEGIS_SIGNING_KEY_PATH` (`/etc/aegis/signing/snapshot-current.key` in Kubernetes). An unreadable or invalid file stops the control plane at startup.
2. Publish a fresh snapshot so the active snapshot is signed by the new key, and confirm gateways acknowledge it (`/control/v1/gateways` shows the new version applied).

#### Phase 3: Prune
1. After the new snapshot is active everywhere, and at least one reload interval + 30s later, remove `keyset.pub.0` from `gateway-snapshot-keyset`.
2. Confirm `aegis_keyset_keys{family="snapshot"} 1` and no `result="error"` increase.

---

### 4.3. Workload mTLS CA / certificate rotation (NOT hot reloaded)

mTLS CA bundles and workload certificates (gateway server and client certs, control plane server cert, per-pod control-plane client certs, backend certs) are read at process start. They are **not** hot reloaded: after changing the Secret or ConfigMap, restart the affected workloads. Hot reload of mTLS material is out of scope for the current release.

1. Generate the new intermediate or leaf certificates and bundle old and new CAs into a combined trust bundle.
2. Update the relevant objects (`aegis-root-ca`, `aegis-gateway-workload-tls`, `aegis-gateway-cp-clients`, `aegis-control-plane-tls`, `aegis-backend-*-tls`).
3. Restart in dependency order: backends, control plane, then gateways:
   ```bash
   kubectl -n aegis-system rollout restart deployment/aegis-control-plane
   kubectl -n aegis-system rollout restart statefulset/aegis-gateway
   kubectl -n aegis-system rollout status statefulset/aegis-gateway --timeout=180s
   ```
4. After every workload presents certificates from the new CA, replace the bundle with the new CA only and restart again.

---

## 5. Verification & Exit Criteria

```bash
# 1. Gateway readiness
kubectl get pods -n aegis-system -l app=aegis-gateway
# All replicas Running with no unexpected restarts

# 2. Keyset state (gateway metrics on :9091)
curl -s http://gateway-replica-0:9091/metrics | grep -E 'aegis_keyset_(keys|reload_total|last_success_timestamp_seconds)'
curl -s http://gateway-replica-0:9091/metrics | grep aegis_keyset_reload_failing
# Expected: keys count matches the phase you are in; reload_total{result="error"} not increasing;
# aegis_keyset_reload_failing is 0 for both families on every replica (see 5.1 if not)

# 3. Authentication status
curl -s http://gateway-replica-0:9091/metrics | grep -E 'aegis_policy_decisions_total\{decision="allow"|aegis_http_requests_total'
# Allow decisions and 2xx requests keep increasing; a key mismatch shows as status="401" instead

# 4. Request with a freshly issued token
curl -k -H "Authorization: Bearer $(get-new-token)" https://gateway.aegis.local:8080/api/orders
# Must return HTTP 200 OK

# 5. Snapshot lease
curl -s http://gateway-replica-0:9091/metrics | grep aegis_snapshot_lease_age_seconds
# Must stay at a few seconds (well under the lease TTL); a growing value means the signer change broke snapshot verification
```

### 5.1. Keyset reload is failing (`aegis_keyset_reload_failing == 1`)

The gateway is still serving the **last good** key set. Any key removed since that set was loaded is still trusted.

1. Find the offending file on an affected gateway:
   ```bash
   kubectl -n aegis-system logs aegis-gateway-0 -c gateway | grep 'RELOAD REJECTED' | tail -n 3
   # The message names the file, for example: invalid key file "keyset.pub.1~"
   kubectl -n aegis-system get configmap gateway-jwt-keyset gateway-snapshot-keyset -o yaml   # list the entries
   ```
2. Remove or fix that entry in the ConfigMap (Compose: in `deployments/keys/keyset-<family>/`). Every remaining entry must be a valid Ed25519 public key (PEM `PUBLIC KEY` or base64 of the 32 raw bytes). Keep at least one key.
   ```bash
   kubectl -n aegis-system create configmap gateway-jwt-keyset \
     --from-file=keyset.pub.1=jwt-new.pub --dry-run=client -o yaml | kubectl apply -f -
   ```
3. Wait for the reload interval plus the kubelet sync period, or send `SIGHUP` to the gateway process.
4. Confirm `aegis_keyset_reload_failing{family}` is `0` and `aegis_keyset_keys{family}` matches the intended set on **every** replica. The log line `keyset[<family>]: reload recovered after N rejected attempt(s)` marks the recovery.
5. If the failure began during an emergency cutoff (section 6), the compromised key was trusted for the whole failing period. Include that window in the post-incident audit.

---

## 6. Emergency Key Compromise Fast-Path Invalidation

If an active signing key is compromised:

1. **Immediate cutoff (no overlap):** replace the affected keyset with the emergency replacement public key only. Gateways reload within `AEGIS_KEYSET_RELOAD_INTERVAL`; send `SIGHUP` to force it. No pod deletion is required.
   ```bash
   kubectl -n aegis-system create configmap gateway-jwt-keyset \
     --from-file=keyset.pub.0=emergency-new.pub --dry-run=client -o yaml | kubectl apply -f -
   ```
   Then switch the signer to the matching private key at once (section 4.1 phase 2).
   Confirm `aegis_keyset_reload_failing{family} 0` and `aegis_keyset_keys{family} 1` on every gateway. A rejected reload keeps the compromised key trusted (section 5.1).
2. **Quarantine affected principals:**
   ```bash
   curl -X POST http://control-plane:8084/control/v1/quarantine \
     -H "Content-Type: application/json" -H "Idempotency-Key: $(uuidgen)" \
     -d '{"principal_id":"compromised-actor","reason":"EMERGENCY_KEY_LEAK","ttl_seconds":86400}'
   ```
   (Requires an authenticated `sec-ops` session.) Quarantines are written to Postgres first and survive a Redis loss; see RB-DR-01.
3. **Verify compromised tokens are rejected:**
   ```bash
   curl -i -H "Authorization: Bearer <compromised-token>" http://gateway:8080/api/orders
   # Expected: HTTP 401 Unauthorized / HTTP 403 Forbidden
   ```
4. **Post-incident audit:** review the PostgreSQL `audit_events` partition for all access during the suspected exposure window.
