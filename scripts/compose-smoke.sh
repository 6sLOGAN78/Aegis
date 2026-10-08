#!/usr/bin/env bash
# Live smoke for a RUNNING Aegis Docker Compose profile.
#
# Usage: scripts/compose-smoke.sh [mvp|hardened|distributed] [--stop-check]
#
# This script verifies compose wiring for audit blockers B1, B3 and B4:
#   B4  control plane reaches Redis      (quarantine returns 200, not 503)
#   B1  gateway has policy and routes    (developer allowed /api/orders, denied /api/admin/users)
#   B3  every audit worker drains its own spool (wal.cursor advances per worker)
#   A4  (assumption, non-fatal) a quarantined principal is denied
#   GRACE (--stop-check only) a gateway stops with exit code 0, not 137
#
# It does NOT establish REV-03 or AUD-03; those requirements stay open.
# It never starts or tears down the stack: callers run the stack bring-up
# (with --build --wait) and the teardown (with -v) themselves.
#
# Env: SMOKE_USER (default admin), SMOKE_PASSWORD (default: demo operator password).
# Credentials, session ids and CSRF tokens are never printed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

usage() {
  echo "usage: $0 [mvp|hardened|distributed] [--stop-check]" >&2
}

PROFILE="${1:-mvp}"
STOP_CHECK="false"
if [ "${2:-}" = "--stop-check" ]; then
  STOP_CHECK="true"
elif [ -n "${2:-}" ]; then
  usage
  exit 2
fi

case "${PROFILE}" in
  mvp|hardened|distributed) ;;
  *) usage; exit 2 ;;
esac

for tool in docker curl jq; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "missing required tool on host: ${tool}" >&2
    exit 2
  fi
done

FILE="${ROOT_DIR}/deployments/compose/docker-compose.${PROFILE}.yml"
if [ ! -f "${FILE}" ]; then
  echo "compose file not found: ${FILE}" >&2
  exit 2
fi

SMOKE_USER="${SMOKE_USER:-admin}"
SMOKE_PASSWORD="${SMOKE_PASSWORD:-admin-secret}"

if [ "${PROFILE}" = "distributed" ]; then
  GW="gateway-1"
  BATCH=30
else
  GW="gateway"
  BATCH=5
fi

GATEWAY_URL="http://127.0.0.1:8080"
CP_URL="http://127.0.0.1:8084"
WAL_CURSOR="/var/log/aegis/wal/wal.cursor"

FAILS=0
SESSION=""
CSRF=""

dc() { docker compose -f "${FILE}" "$@"; }

pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1 ($2)"; FAILS=$((FAILS + 1)); }

# --- control plane helpers (run inside the compose network) ---------------

# op_login sets SESSION and CSRF from a fresh operator login.
op_login() {
  local resp body
  SESSION=""
  CSRF=""
  resp="$(dc exec -T control-plane curl -s -i -X POST \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"${SMOKE_USER}\",\"password\":\"${SMOKE_PASSWORD}\"}" \
    "${CP_URL}/control/v1/auth/login" 2>/dev/null | tr -d '\r' || true)"
  SESSION="$(printf '%s\n' "${resp}" | sed -n 's/^[Ss]et-[Cc]ookie: aegis_session=\([^;]*\).*/\1/p' | head -n1)"
  body="$(printf '%s\n' "${resp}" | sed -n '/^$/,$p')"
  CSRF="$(printf '%s' "${body}" | jq -r '.csrf_token // empty' 2>/dev/null || true)"
  [ -n "${SESSION}" ] && [ -n "${CSRF}" ]
}

# cp_call METHOD PATH [JSON_BODY] prints the HTTP status code only.
cp_call() {
  local method="$1" path="$2" data="${3:-}"
  if [ -n "${data}" ]; then
    dc exec -T control-plane curl -s -o /dev/null -w '%{http_code}' -X "${method}" \
      -H "Cookie: aegis_session=${SESSION}" \
      -H "X-CSRF-Token: ${CSRF}" \
      -H 'Content-Type: application/json' \
      -d "${data}" "${CP_URL}${path}" 2>/dev/null || true
  else
    dc exec -T control-plane curl -s -o /dev/null -w '%{http_code}' -X "${method}" \
      -H "Cookie: aegis_session=${SESSION}" \
      -H "X-CSRF-Token: ${CSRF}" \
      "${CP_URL}${path}" 2>/dev/null || true
  fi
}

# dev_token prints a fresh developer access token (issuer is only reachable inside the network).
dev_token() {
  dc exec -T "${GW}" curl -s -X POST -H 'Content-Type: application/json' \
    -d '{"role":"developer"}' http://demo-issuer:8085/login 2>/dev/null \
    | jq -r '.access_token // empty' 2>/dev/null || true
}

# gw_code TOKEN PATH prints the HTTP status code from the user-facing listener.
gw_code() {
  curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $1" \
    "${GATEWAY_URL}$2" 2>/dev/null || true
}

audit_count() {
  dc exec -T postgres psql -U aegis -d aegis -tAc 'select count(*) from audit_events' 2>/dev/null \
    | tr -d '[:space:]' || true
}

send_batch() {
  local tok i
  tok="$(dev_token)"
  if [ -z "${tok}" ]; then
    return 1
  fi
  for i in $(seq 1 "${BATCH}"); do
    gw_code "${tok}" /api/orders >/dev/null
  done
}

# wait_count_above N SECONDS: poll until audit_events count exceeds N.
wait_count_above() {
  local base="$1" secs="$2" i cur
  for i in $(seq 1 "${secs}"); do
    cur="$(audit_count)"
    if [ -n "${cur}" ] && [ "${cur}" -gt "${base}" ] 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

# Always release quarantines, even on interrupt or failure (T-07-08).
cleanup() {
  # Always log in again: a session from earlier in the run may have gone stale.
  if op_login; then
    cp_call DELETE /control/v1/principals/usr_developer_01/quarantine >/dev/null \
      || echo "WARN: could not release quarantine for usr_developer_01" >&2
    cp_call DELETE /control/v1/principals/smoke-b4-probe/quarantine >/dev/null \
      || echo "WARN: could not release quarantine for smoke-b4-probe" >&2
  else
    echo "WARN: cleanup login failed; usr_developer_01 and smoke-b4-probe may still be quarantined" >&2
  fi
}
trap cleanup EXIT

echo "=== Aegis compose smoke: profile=${PROFILE} gateway=${GW} ==="

# --- 1. READY ---------------------------------------------------------------
echo "=== READY ==="
ready="false"
for _ in $(seq 1 60); do
  code="$(curl -s -o /dev/null -w '%{http_code}' "${GATEWAY_URL}/readyz" 2>/dev/null || true)"
  if [ "${code}" = "200" ]; then
    ready="true"
    break
  fi
  sleep 1
done
if [ "${ready}" = "true" ]; then
  pass "READY gateway /readyz returned 200"
else
  fail "READY" "gateway /readyz did not return 200 within 60s, last HTTP ${code:-none}"
fi

# --- 2. B4: quarantine must not 503 ----------------------------------------
echo "=== B4: control plane reaches Redis ==="
if op_login; then
  code="$(cp_call POST /control/v1/principals/smoke-b4-probe/quarantine '{"reason":"compose-smoke"}')"
  if [ "${code}" = "200" ]; then
    pass "B4 quarantine POST returned 200"
  elif [ "${code}" = "503" ]; then
    fail "B4 quarantine POST" "HTTP 503: control plane cannot reach Redis (AEGIS_REDIS_ADDR missing?)"
  else
    fail "B4 quarantine POST" "HTTP ${code:-none}"
  fi
  code="$(cp_call DELETE /control/v1/principals/smoke-b4-probe/quarantine)"
  case "${code}" in
    2??) pass "B4 quarantine DELETE returned ${code} (probe removed)" ;;
    *) fail "B4 quarantine DELETE" "HTTP ${code:-none}" ;;
  esac
else
  fail "B4 operator login" "could not obtain session and CSRF token from control plane"
fi

# --- 3. B1: policy and routes loaded ---------------------------------------
echo "=== B1: gateway authorizes with seeded policy ==="
tok="$(dev_token)"
if [ -z "${tok}" ]; then
  fail "B1 developer token" "demo issuer returned no access_token"
else
  code="$(gw_code "${tok}" /api/orders)"
  if [ "${code}" = "200" ]; then
    pass "B1 developer GET /api/orders returned 200"
  else
    fail "B1 developer GET /api/orders" "expected 200, got HTTP ${code:-none}"
  fi
  code="$(gw_code "${tok}" /api/admin/users)"
  if [ "${code}" = "403" ]; then
    pass "B1 developer GET /api/admin/users returned 403"
  else
    fail "B1 developer GET /api/admin/users" "expected 403, got HTTP ${code:-none}"
  fi
fi

# --- 4. AUDIT: every worker drains its own spool ---------------------------
echo "=== AUDIT: per-spool drain (B3) ==="
base="$(audit_count)"
if [ -z "${base}" ]; then
  fail "AUDIT baseline" "could not read audit_events count from postgres"
  base=0
fi
send_batch || fail "AUDIT first batch" "could not obtain developer token"
if wait_count_above "${base}" 30; then
  pass "AUDIT audit_events count grew above baseline ${base}"
else
  fail "AUDIT audit_events" "count did not exceed baseline ${base} within 30s"
fi
after_first="$(audit_count)"
after_first="${after_first:-0}"

mapfile -t WORKERS < <(dc config --services 2>/dev/null | grep '^audit-worker' || true)
if [ "${#WORKERS[@]}" -eq 0 ]; then
  fail "AUDIT workers" "no audit-worker service found in ${PROFILE} profile"
fi

declare -A SAMPLE1
for w in "${WORKERS[@]}"; do
  got=""
  for _ in $(seq 1 30); do
    got="$(dc exec -T "${w}" cat "${WAL_CURSOR}" 2>/dev/null || true)"
    [ -n "${got}" ] && break
    sleep 1
  done
  if [ -n "${got}" ]; then
    SAMPLE1["${w}"]="${got}"
  else
    fail "AUDIT ${w} wal.cursor" "cursor file never appeared within 30s"
  fi
done

send_batch || fail "AUDIT second batch" "could not obtain developer token"
wait_count_above "${after_first}" 30 || true

for w in "${WORKERS[@]}"; do
  [ -n "${SAMPLE1[${w}]:-}" ] || continue
  advanced="false"
  for _ in $(seq 1 30); do
    cur="$(dc exec -T "${w}" cat "${WAL_CURSOR}" 2>/dev/null || true)"
    if [ -n "${cur}" ] && [ "${cur}" != "${SAMPLE1[${w}]}" ]; then
      advanced="true"
      break
    fi
    sleep 1
  done
  if [ "${advanced}" = "true" ]; then
    pass "AUDIT ${w} wal.cursor advanced (proves drain wiring per spool, not AUD-03)"
  else
    fail "AUDIT ${w} wal.cursor" "cursor did not advance within 30s after second batch (B3)"
  fi
done

# --- 5. A4: quarantine denial (assumption, non-fatal) ----------------------
echo "=== A4: quarantined principal denied (assumption) ==="
a4_code="none"
a4_denied="false"
tok="$(dev_token)"
if [ -z "${tok}" ] || ! op_login; then
  echo "A4 RESULT: FAIL (HTTP none, could not obtain token or operator session; record in SUMMARY, do not fix in this phase)"
else
  qcode="$(cp_call POST /control/v1/principals/usr_developer_01/quarantine '{"reason":"compose-smoke-a4"}')"
  for _ in $(seq 1 10); do
    a4_code="$(gw_code "${tok}" /api/orders)"
    if [ "${a4_code}" = "401" ] || [ "${a4_code}" = "403" ]; then
      a4_denied="true"
      break
    fi
    sleep 1
  done
  if [ "${a4_denied}" = "true" ]; then
    echo "A4 RESULT: PASS (HTTP ${a4_code})"
  else
    echo "A4 RESULT: FAIL (HTTP ${a4_code}, quarantined user still allowed; quarantine POST was HTTP ${qcode:-none}; record in SUMMARY, out-of-scope causes are the missing jti / encoded SPIFFE key / B8 gaps, do not fix in this phase)"
  fi
  cp_call DELETE /control/v1/principals/usr_developer_01/quarantine >/dev/null || true
fi

# --- 6. GRACE: gateway stops cleanly (only with --stop-check) --------------
if [ "${STOP_CHECK}" = "true" ]; then
  echo "=== GRACE: ${GW} stops with exit code 0 ==="
  cid="$(dc ps -aq "${GW}" 2>/dev/null | head -n1 || true)"
  # No -t: the stop must succeed within the service's own stop_grace_period.
  dc stop "${GW}" >/dev/null 2>&1 || true
  exit_code="$(docker inspect -f '{{.State.ExitCode}}' "${cid}" 2>/dev/null || true)"
  if [ "${exit_code}" = "0" ]; then
    pass "GRACE ${GW} exited 0 on stop"
  else
    fail "GRACE ${GW}" "exit code ${exit_code:-unknown}; 137 means SIGKILL so stop_grace_period was too short"
  fi
  dc up -d --wait "${GW}" >/dev/null 2>&1 || fail "GRACE ${GW}" "did not become healthy again after restart"
fi

echo "=== Summary: ${FAILS} hard failure(s) ==="
if [ "${FAILS}" -gt 0 ]; then
  exit 1
fi
exit 0
