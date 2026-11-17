#!/usr/bin/env bash
# Creates the Secrets and ConfigMaps the Aegis manifests reference (Phase 11, D-20).
#
# Sources: AEGIS_CERTS_DIR and AEGIS_KEYS_DIR, defaulting to the dev material
# committed in deployments/certs and deployments/keys (D-24). The committed keys
# are public to anyone with repo access: production MUST point both variables
# at its own freshly generated material.
# Usage:   AEGIS_DB_PASSWORD=... [AEGIS_DB_USER=aegis] [NS=aegis-system] [APPS_NS=aegis-apps] \
#          [AEGIS_CERTS_DIR=/secure/certs] [AEGIS_KEYS_DIR=/secure/keys] \
#          deployments/kubernetes/secrets/create-secrets.sh
# Idempotent: every object goes through `--dry-run=client -o yaml | kubectl apply -f -`.
set -euo pipefail

NS="${NS:-aegis-system}"
APPS_NS="${APPS_NS:-aegis-apps}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DEV_CERTS="$ROOT/deployments/certs"
DEV_KEYS="$ROOT/deployments/keys"
CERTS="${AEGIS_CERTS_DIR:-$DEV_CERTS}"
KEYS="${AEGIS_KEYS_DIR:-$DEV_KEYS}"
: "${AEGIS_DB_PASSWORD:?set AEGIS_DB_PASSWORD}"
AEGIS_DB_USER="${AEGIS_DB_USER:-aegis}"

for d in "$CERTS" "$KEYS"; do
  [ -d "$d" ] || { echo "refusing to run: key material dir $d does not exist" >&2; exit 1; }
done
CERTS="$(cd "$CERTS" && pwd -P)"
KEYS="$(cd "$KEYS" && pwd -P)"

if [ "$CERTS" = "$(cd "$DEV_CERTS" && pwd -P)" ] || [ "$KEYS" = "$(cd "$DEV_KEYS" && pwd -P)" ]; then
  printf '\033[1m*** DEV KEYS ONLY: these objects come from the committed deployments/certs and deployments/keys, whose private keys anyone with repo access holds. Never use them in production: set AEGIS_CERTS_DIR and AEGIS_KEYS_DIR to your own material. ***\033[0m\n' >&2
else
  echo "Using key material from AEGIS_CERTS_DIR=$CERTS and AEGIS_KEYS_DIR=$KEYS" >&2
fi

# Every --from-file source below, split by destination. Keep these lists in
# sync with the kubectl commands (tests/manifests enforces it).
SECRET_INPUTS=(
  "$CERTS/gateway-server.crt" "$CERTS/gateway-server.key"
  "$CERTS/gateway-client.crt" "$CERTS/gateway-client.key"
  "$CERTS/assertion-ed25519.key"
  "$CERTS/cp-client-aegis-gateway-0.crt" "$CERTS/cp-client-aegis-gateway-0.key"
  "$CERTS/cp-client-aegis-gateway-1.crt" "$CERTS/cp-client-aegis-gateway-1.key"
  "$CERTS/cp-client-aegis-gateway-2.crt" "$CERTS/cp-client-aegis-gateway-2.key"
  "$CERTS/control-plane-server.crt" "$CERTS/control-plane-server.key"
  "$KEYS/snapshot-current.key"
  "$CERTS/orders.crt" "$CERTS/orders.key"
  "$CERTS/payments.crt" "$CERTS/payments.key"
  "$CERTS/admin.crt" "$CERTS/admin.key"
)
CONFIGMAP_INPUTS=(
  "$CERTS/root-ca.crt"
  "$KEYS/keyset-jwt/current.pub"
  "$KEYS/keyset-snapshot/current.pub"
  "$CERTS/assertion-ed25519.pub"
)

# Guard: the root CA private key is never packaged into any object, and no
# private key ever lands in a ConfigMap. Checks the actual inputs: by name,
# by resolved path and by content against $CERTS/root-ca.key when present.
ROOT_CA_KEY="$CERTS/root-ca.key"
for f in "${SECRET_INPUTS[@]}" "${CONFIGMAP_INPUTS[@]}"; do
  [ -r "$f" ] || { echo "refusing to run: missing or unreadable input $f" >&2; exit 1; }
  real="$(cd "$(dirname "$f")" && pwd -P)/$(basename "$f")"
  [ -L "$f" ] && real="$(readlink -f "$f")"
  case "$(basename "$real")" in
    root-ca\.key | *root-ca*.key)
      echo "refusing to run: $f resolves to the root CA private key, which must never be packaged" >&2
      exit 1
      ;;
  esac
  if [ -f "$ROOT_CA_KEY" ] && cmp -s "$f" "$ROOT_CA_KEY"; then
    echo "refusing to run: $f has the same content as the root CA private key" >&2
    exit 1
  fi
done
for f in "${CONFIGMAP_INPUTS[@]}"; do
  if grep -q 'PRIVATE KEY' "$f"; then
    echo "refusing to run: ConfigMap input $f contains a private key" >&2
    exit 1
  fi
done

# --- ConfigMaps (public material) ---
kubectl -n "$NS" create configmap aegis-root-ca \
  --from-file=root-ca.crt="$CERTS/root-ca.crt" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$APPS_NS" create configmap aegis-root-ca \
  --from-file=root-ca.crt="$CERTS/root-ca.crt" --dry-run=client -o yaml | kubectl apply -f -
# Rotation: add keyset.pub.1 (then prune keyset.pub.0) per docs/runbooks/credential-rotation.md.
kubectl -n "$NS" create configmap gateway-jwt-keyset \
  --from-file=keyset.pub.0="$KEYS/keyset-jwt/current.pub" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NS" create configmap gateway-snapshot-keyset \
  --from-file=keyset.pub.0="$KEYS/keyset-snapshot/current.pub" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$APPS_NS" create configmap aegis-assertion-pubkey \
  --from-file=assertion-ed25519.pub="$CERTS/assertion-ed25519.pub" --dry-run=client -o yaml | kubectl apply -f -

# --- Secrets: gateway ---
kubectl -n "$NS" create secret generic aegis-gateway-workload-tls \
  --from-file=gateway-server.crt="$CERTS/gateway-server.crt" \
  --from-file=gateway-server.key="$CERTS/gateway-server.key" \
  --from-file=gateway-client.crt="$CERTS/gateway-client.crt" \
  --from-file=gateway-client.key="$CERTS/gateway-client.key" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NS" create secret generic aegis-gateway-assertion-key \
  --from-file=assertion-ed25519.key="$CERTS/assertion-ed25519.key" --dry-run=client -o yaml | kubectl apply -f -
# All three ordinal pairs are mounted into every pod; the pod picks its own via $(POD_NAME).
kubectl -n "$NS" create secret generic aegis-gateway-cp-clients \
  --from-file=cp-client-aegis-gateway-0.crt="$CERTS/cp-client-aegis-gateway-0.crt" \
  --from-file=cp-client-aegis-gateway-0.key="$CERTS/cp-client-aegis-gateway-0.key" \
  --from-file=cp-client-aegis-gateway-1.crt="$CERTS/cp-client-aegis-gateway-1.crt" \
  --from-file=cp-client-aegis-gateway-1.key="$CERTS/cp-client-aegis-gateway-1.key" \
  --from-file=cp-client-aegis-gateway-2.crt="$CERTS/cp-client-aegis-gateway-2.crt" \
  --from-file=cp-client-aegis-gateway-2.key="$CERTS/cp-client-aegis-gateway-2.key" \
  --dry-run=client -o yaml | kubectl apply -f -

# --- Secrets: control plane ---
kubectl -n "$NS" create secret generic aegis-control-plane-tls \
  --from-file=control-plane-server.crt="$CERTS/control-plane-server.crt" \
  --from-file=control-plane-server.key="$CERTS/control-plane-server.key" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NS" create secret generic aegis-control-plane-signing-key \
  --from-file=snapshot-current.key="$KEYS/snapshot-current.key" --dry-run=client -o yaml | kubectl apply -f -

# --- Secret: database credentials (control plane and audit-worker sidecar) ---
# Postgres reads this same Secret; note it only applies on first init of an empty PGDATA
# (existing PVC: ALTER ROLE inside Postgres or a fresh volume).
# The password never appears in any argv (ps, /proc/<pid>/cmdline, exec
# audit logs): it is written with the printf builtin into a 0600 file in a
# 0700 temp dir and passed with --from-file, then removed on exit.
DB_SECRET_DIR="$(umask 077 && mktemp -d)"
trap 'rm -rf "$DB_SECRET_DIR"' EXIT
(umask 077 && printf '%s' "$AEGIS_DB_PASSWORD" >"$DB_SECRET_DIR/AEGIS_DB_PASSWORD")
kubectl -n "$NS" create secret generic aegis-db-credentials \
  --from-literal=AEGIS_DB_USER="$AEGIS_DB_USER" \
  --from-file=AEGIS_DB_PASSWORD="$DB_SECRET_DIR/AEGIS_DB_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -
rm -rf "$DB_SECRET_DIR"

# --- Secrets: backends ---
kubectl -n "$APPS_NS" create secret generic aegis-backend-orders-tls \
  --from-file=tls.crt="$CERTS/orders.crt" --from-file=tls.key="$CERTS/orders.key" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$APPS_NS" create secret generic aegis-backend-payments-tls \
  --from-file=tls.crt="$CERTS/payments.crt" --from-file=tls.key="$CERTS/payments.key" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$APPS_NS" create secret generic aegis-backend-admin-tls \
  --from-file=tls.crt="$CERTS/admin.crt" --from-file=tls.key="$CERTS/admin.key" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Aegis secrets and configmaps applied to $NS and $APPS_NS"
