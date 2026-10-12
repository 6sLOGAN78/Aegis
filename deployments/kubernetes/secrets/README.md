# Kubernetes Secrets and ConfigMaps

> [!WARNING]
> **The keys committed in this repository are DEV KEYS ONLY. Never deploy them to production.**
>
> `deployments/certs` and `deployments/keys` are committed on purpose (D-24) so the compose profiles and
> the base kustomization work out of the box. Every private key there, including the root CA key, the JWT
> issuer key, the snapshot signing key and all mTLS keys, is readable by anyone with access to this repo.
> A cluster built from them trusts tokens, snapshots and client certificates that anyone can forge.
>
> Production must generate its own material outside the repo (for example
> `go run ./scripts/certificates -out /secure/certs -keys-dir /secure/keys`, kept off shared disks) and run
> the script with `AEGIS_CERTS_DIR` and `AEGIS_KEYS_DIR` pointing at it. The script prints a
> `DEV KEYS ONLY` banner whenever it falls back to the committed directories.

`create-secrets.sh` packages the material in `AEGIS_CERTS_DIR` (default `deployments/certs`) and
`AEGIS_KEYS_DIR` (default `deployments/keys`) into the objects below. The manifests reference them by
name only; no key material lives in `deployments/kubernetes`. The root CA private key (`root-ca.key`) is
never packaged: before creating anything, the script refuses to run if any input is missing, resolves to a
`root-ca*.key` file, has the same content as `$AEGIS_CERTS_DIR/root-ca.key`, or (for ConfigMaps) contains
a private key.

Run (dev): `AEGIS_DB_PASSWORD=... deployments/kubernetes/secrets/create-secrets.sh`

Run (production): `AEGIS_CERTS_DIR=/secure/certs AEGIS_KEYS_DIR=/secure/keys AEGIS_DB_PASSWORD=... deployments/kubernetes/secrets/create-secrets.sh`

It needs `kubectl` and is idempotent. The DB password is passed to `kubectl` through a temporary 0600
file, never on the command line.

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
