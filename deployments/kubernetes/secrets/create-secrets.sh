#!/usr/bin/env bash
# Creates the Secrets and ConfigMaps the Aegis manifests reference (Phase 11, D-20).
#
# Sources: deployments/certs and deployments/keys (generated dev material).
# Usage:   AEGIS_DB_PASSWORD=... [AEGIS_DB_USER=aegis] [NS=aegis-system] [APPS_NS=aegis-apps] \
#          deployments/kubernetes/secrets/create-secrets.sh
# Idempotent: every object goes through `--dry-run=client -o yaml | kubectl apply -f -`.
set -euo pipefail

NS="${NS:-aegis-system}"
APPS_NS="${APPS_NS:-aegis-apps}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CERTS="$ROOT/deployments/certs"
KEYS="$ROOT/deployments/keys"
: "${AEGIS_DB_PASSWORD:?set AEGIS_DB_PASSWORD}"
AEGIS_DB_USER="${AEGIS_DB_USER:-aegis}"

printf '\033[1m*** DEV KEYS ONLY: these objects come from deployments/certs and deployments/keys. Never use them in production. ***\033[0m\n' >&2

# Guard: the root CA private key is never packaged into any object.
if grep -nE -e '--from-file=[^ ]*root-ca\.key' "${BASH_SOURCE[0]}" >/dev/null; then
  echo "refusing to run: the root CA private key must never be packaged" >&2
  exit 1
fi

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
