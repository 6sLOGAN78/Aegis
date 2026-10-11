# Kubernetes Secrets and ConfigMaps

DEV KEYS ONLY. `create-secrets.sh` packages the generated material in `deployments/certs` and
`deployments/keys` into the objects below. The manifests reference them by name only; no key material
lives in `deployments/kubernetes`. The root CA private key (`root-ca.key`) is never packaged.

Run: `AEGIS_DB_PASSWORD=... deployments/kubernetes/secrets/create-secrets.sh` (needs `kubectl`; it is idempotent).

| Object | Kind | Mounted/used by |
|--------|------|-----------------|
| `aegis-root-ca` | ConfigMap (both namespaces) | gateway, control plane, backends (trust anchor) |
| `gateway-jwt-keyset` | ConfigMap | gateway `AEGIS_JWT_KEYSET_DIR` (`keyset.pub.N`) |
| `gateway-snapshot-keyset` | ConfigMap | gateway `AEGIS_SNAPSHOT_KEYSET_DIR` |
| `aegis-assertion-pubkey` | ConfigMap (aegis-apps) | backends `GATEWAY_ASSERTION_PUBKEY_PATH` |
| `aegis-gateway-workload-tls` | Secret | gateway server and workload client certs |
| `aegis-gateway-assertion-key` | Secret | gateway assertion signing key |
| `aegis-gateway-cp-clients` | Secret | gateway per-pod control-plane client cert (ordinals 0..2) |
| `aegis-control-plane-tls` | Secret | control plane gRPC server cert |
| `aegis-control-plane-signing-key` | Secret | control plane snapshot signing key |
| `aegis-db-credentials` | Secret | control plane and audit-worker sidecar (`AEGIS_DB_USER`, `AEGIS_DB_PASSWORD`) |
| `aegis-backend-{orders,payments,admin}-tls` | Secret (aegis-apps) | one per backend (`tls.crt`, `tls.key`) |

## JWT key rotation

`gateway-jwt-keyset` holds one key per `keyset.pub.N` entry. To rotate: add `keyset.pub.1` with the new
public key, let gateways reload (`AEGIS_KEYSET_RELOAD_INTERVAL`), switch the issuer to the new private key,
then remove `keyset.pub.0`. See `docs/runbooks/credential-rotation.md`.

## Per-pod control-plane client cert tradeoff (D-21)

Each gateway pod selects its client cert with `cp-client-$(POD_NAME).{crt,key}`, where `POD_NAME` comes
from the downward API. A Secret is mounted per pod template, so every gateway pod can read the keys of all
ordinals. A compromised pod could therefore present a sibling's stream identity to the control plane. The
impact is limited to convergence telemetry; snapshot integrity stays protected by the snapshot signature.
Replicas must stay at 3 or fewer until more ordinal certs are generated.
