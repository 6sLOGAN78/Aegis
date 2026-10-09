#!/usr/bin/env bash
# Live smoke for a RUNNING Aegis Docker Compose profile.
#
# Usage: scripts/compose-smoke.sh [mvp|hardened|distributed] [--stop-check]
#
# This script verifies compose wiring and audit evidence for blockers B1, B3, B4, B7:
#   B4  control plane reaches Redis      (quarantine returns 200, not 503)
#   B1  gateway has policy and routes    (developer allowed /api/orders, denied /api/admin/users)
#   B3  every audit worker drains its own spool (wal.cursor advances per worker)
#   B7  AUDIT-TYPES: typed audit_events rows by request_id (allowed decision+completion pair,
#       one denial row, a suppressed identical repeat, a suppression summary row with
#       suppressed_count=1, an unauthenticated 401 denial) plus the gateway's own counters
#   AUD-03 dedupe  DEDUPE: delete wal.cursor, restart the worker, the audit_events row count
#       must not change (replay absorbed by ON CONFLICT DO NOTHING against real Postgres)
#   AUD-03 rotation ROTATION: only when SMOKE_ROTATION_REQUESTS is set; otherwise it prints
#       ROTATION SKIPPED and rotation is NOT claimed
#   A4  (assumption, non-fatal) a quarantined principal is denied
#   GRACE (--stop-check only) a gateway stops with exit code 0, not 137
#
# AUD-03 and AUD-04 evidence requires AUDIT-TYPES, DEDUPE and ROTATION to all PASS in the
# same run, on a stack started with a small AEGIS_SPOOL_SEGMENT_BYTES for ROTATION.
# A crash between the Postgres insert and the cursor save is covered by unit tests only
# (TestAuditWorker_RestartRecovery); this script does not exercise it. REV-03 stays open.
# It never starts or tears down the stack: callers run the stack bring-up
# (with --build --wait) and the teardown (with -v) themselves.
#
# Env: SMOKE_USER (default admin), SMOKE_PASSWORD (default: demo operator password).
#      SMOKE_SUPPRESS_WINDOW_SECS (default 60) must match the stack's
#        AEGIS_AUDIT_SUPPRESS_WINDOW in seconds; the wait for the suppression summary row
#        is 2x this plus 30 seconds.
#      SMOKE_ROTATION_REQUESTS (default 0 = skip ROTATION) number of allowed requests to
#        send; start the stack with a small AEGIS_SPOOL_SEGMENT_BYTES (for example 65536).
#      Compose variables read at stack start (not by this script): AEGIS_SPOOL_SEGMENT_BYTES
#        (default 16777216) and AEGIS_AUDIT_SUPPRESS_WINDOW (default 60s).
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
SMOKE_SUPPRESS_WINDOW_SECS="${SMOKE_SUPPRESS_WINDOW_SECS:-60}"
SMOKE_ROTATION_REQUESTS="${SMOKE_ROTATION_REQUESTS:-0}"
case "${SMOKE_SUPPRESS_WINDOW_SECS}" in
  ''|*[!0-9]*) echo "SMOKE_SUPPRESS_WINDOW_SECS must be a positive integer" >&2; exit 2 ;;
esac
case "${SMOKE_ROTATION_REQUESTS}" in
  ''|*[!0-9]*) echo "SMOKE_ROTATION_REQUESTS must be a non-negative integer" >&2; exit 2 ;;
esac

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

# gw_probe TOKEN PATH prints "<http_status> <request_id>" (request_id is the X-Request-ID
# response header, empty when absent).
gw_probe() {
  local hdr code rid
  hdr="$(mktemp)"
  code="$(curl -s -o /dev/null -D "${hdr}" -w '%{http_code}' -H "Authorization: Bearer $1" \
    "${GATEWAY_URL}$2" 2>/dev/null || true)"
  rid="$(tr -d '\r' <"${hdr}" | awk 'tolower($1) == "x-request-id:" { v = $2 } END { print v }')"
  rm -f "${hdr}"
  echo "${code} ${rid}"
}

# valid_rid ID succeeds only for a UUID-shaped string (it is interpolated into SQL).
valid_rid() {
  [[ "${1:-}" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]]
}

# psql_q SQL runs the SQL (on stdin) in the postgres container and prints the trimmed rows.
psql_q() {
  printf '%s\n' "$1" \
    | dc exec -T postgres psql -U aegis -d aegis -tA \
    | tr -d '\r' \
    | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e '/^$/d' || true
}

# wait_q_eq SQL WANT SECONDS polls psql_q until its output equals WANT; LAST_Q holds the
# last output.
LAST_Q=""
wait_q_eq() {
  local sql="$1" want="$2" secs="$3" i
  LAST_Q=""
  for i in $(seq 1 "${secs}"); do
    LAST_Q="$(psql_q "${sql}")"
    if [ "${LAST_Q}" = "${want}" ]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

# cursor_field WORKER FIELD prints a field of the worker's wal.cursor JSON (jq runs on the
# host: the audit-worker image has no jq).
cursor_field() {
  dc exec -T "$1" cat "${WAL_CURSOR}" 2>/dev/null | jq -r ".$2 // empty" 2>/dev/null || true
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
ALLOW_CODE=""
ALLOW_ID=""
DENY1_CODE=""
DENY1_ID=""
DENY2_CODE=""
DENY2_ID=""
UNAUTH_CODE=""
UNAUTH_ID=""
tok="$(dev_token)"
if [ -z "${tok}" ]; then
  fail "B1 developer token" "demo issuer returned no access_token"
else
  probe="$(gw_probe "${tok}" /api/orders)"
  code="${probe%% *}"
  ALLOW_ID="${probe#* }"
  ALLOW_CODE="${code}"
  if [ "${code}" = "200" ]; then
    pass "B1 developer GET /api/orders returned 200"
  else
    fail "B1 developer GET /api/orders" "expected 200, got HTTP ${code:-none}"
  fi

  # WINDOW GUARD: the suppression key is (principal, route, reason) and excludes the path,
  # so a unique path cannot make the first denial a new key. Wait out any earlier denial of
  # this reason that is younger than the suppression window (a re-run on the same stack).
  guard_age="$(psql_q "select coalesce(extract(epoch from (now() - max(\"timestamp\")))::int, 1000000) from audit_events where event_type='denial' and reason_code='DENIED_DEVELOPER_ADMIN_FORBIDDEN'")"
  case "${guard_age}" in
    ''|*[!0-9-]*) guard_age=1000000 ;;
  esac
  if [ "${guard_age}" -lt 0 ]; then
    guard_age=0
  fi
  guard_need=$((SMOKE_SUPPRESS_WINDOW_SECS + 2))
  if [ "${guard_age}" -lt "${guard_need}" ]; then
    guard_wait=$((guard_need - guard_age))
    echo "B7 waiting ${guard_wait}s for the previous suppression window to close so the first denial is a new suppression key"
    sleep "${guard_wait}"
  fi

  probe="$(gw_probe "${tok}" /api/admin/users)"
  code="${probe%% *}"
  DENY1_ID="${probe#* }"
  DENY1_CODE="${code}"
  # Identical repeat immediately after the first denial: no sleep.
  probe="$(gw_probe "${tok}" /api/admin/users)"
  DENY2_CODE="${probe%% *}"
  DENY2_ID="${probe#* }"
  if [ "${code}" = "403" ]; then
    pass "B1 developer GET /api/admin/users returned 403"
  else
    fail "B1 developer GET /api/admin/users" "expected 403, got HTTP ${code:-none}"
  fi
fi

# Unauthenticated request: a bearer value that is not a valid JWT.
probe="$(gw_probe "invalid.bearer.token" /api/orders)"
UNAUTH_CODE="${probe%% *}"
UNAUTH_ID="${probe#* }"
if [ "${UNAUTH_CODE}" = "401" ]; then
  pass "B7 unauthenticated request returned 401"
else
  fail "B7 unauthenticated request" "expected 401, got HTTP ${UNAUTH_CODE:-none}"
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
    pass "AUDIT ${w} wal.cursor advanced (drain wiring per spool; typed rows are asserted in AUDIT-TYPES)"
  else
    fail "AUDIT ${w} wal.cursor" "cursor did not advance within 30s after second batch (B3)"
  fi
done

# --- 4b. AUDIT-TYPES: typed rows by request_id ------------------------------
echo "=== AUDIT-TYPES: typed rows for allowed, denied, suppressed and unauthenticated requests (B7 / AUD-04) ==="

# a. allowed request: a completion row and a decision row with different event_ids.
if ! valid_rid "${ALLOW_ID}"; then
  fail "AUDIT-TYPES allowed pair" "no X-Request-ID header"
elif [ "${ALLOW_CODE}" != "200" ]; then
  fail "AUDIT-TYPES allowed pair" "allowed request returned HTTP ${ALLOW_CODE:-none}, not 200"
elif wait_q_eq "select count(*) from audit_events where request_id='${ALLOW_ID}'" 2 30; then
  rows="$(psql_q "select event_type||'|'||decision||'|'||coalesce(http_status,-1)||'|'||(coalesce(duration_ms,0)>0)::int from audit_events where request_id='${ALLOW_ID}' order by event_type")"
  distinct_ids="$(psql_q "select count(distinct event_id) from audit_events where request_id='${ALLOW_ID}'")"
  want=$'completion|allow|200|1\ndecision|allow|0|0'
  if [ "${rows}" = "${want}" ] && [ "${distinct_ids}" = "2" ]; then
    pass "AUDIT-TYPES allowed request has a completion row (200, duration_ms>0) and a decision row (status 0) with distinct event_ids"
  else
    fail "AUDIT-TYPES allowed pair" "rows=[$(printf '%s' "${rows}" | tr '\n' ';')] distinct_event_ids=${distinct_ids:-none}"
  fi
else
  fail "AUDIT-TYPES allowed pair" "expected 2 rows for the allowed request id within 30s, last count ${LAST_Q:-none}"
fi

# b. denied request: exactly one denial row (not the suppression summary).
deny1_ok="false"
if ! valid_rid "${DENY1_ID}"; then
  fail "AUDIT-TYPES denial row" "no X-Request-ID header"
elif [ "${DENY1_CODE}" != "403" ]; then
  fail "AUDIT-TYPES denial row" "first denial returned HTTP ${DENY1_CODE:-none}, not 403"
elif wait_q_eq "select count(*) from audit_events where request_id='${DENY1_ID}' and suppressed_count is null" 1 30; then
  rows="$(psql_q "select event_type||'|'||decision||'|'||http_status||'|'||(duration_ms>0)::int||'|'||(reason_code<>'')::int||'|'||coalesce(error_code,'') from audit_events where request_id='${DENY1_ID}' and suppressed_count is null")"
  if [ "${rows}" = "denial|deny|403|1|1|FORBIDDEN" ]; then
    deny1_ok="true"
    pass "AUDIT-TYPES denied request has exactly one denial row (403, deny, reason set, duration_ms>0, FORBIDDEN)"
  else
    fail "AUDIT-TYPES denial row" "row=[${rows:-none}]"
  fi
else
  fail "AUDIT-TYPES denial row" "no denial row for the first denial within 30s, last count ${LAST_Q:-none}"
fi

# c. suppressed repeat: counted, not recorded (D-15).
if [ "${DENY1_CODE}" = "403" ] && [ "${DENY2_CODE}" = "403" ]; then
  if ! valid_rid "${DENY2_ID}"; then
    fail "AUDIT-TYPES suppressed repeat" "no X-Request-ID header"
  elif [ "${deny1_ok}" != "true" ]; then
    echo "AUDIT-TYPES SKIPPED: suppressed-repeat assertion (the first denial row was not proven above)"
  else
    sleep 3
    n="$(psql_q "select count(*) from audit_events where request_id='${DENY2_ID}'")"
    if [ "${n}" = "0" ]; then
      pass "AUDIT-TYPES identical repeat denial within the window produced no row (suppressed)"
    else
      fail "AUDIT-TYPES suppressed repeat" "expected 0 rows for the repeat request id, got ${n:-none}"
    fi
  fi
else
  echo "AUDIT-TYPES SKIPPED: suppressed-repeat assertion (repeat returned HTTP ${DENY2_CODE:-none}, not 403)"
fi

# d. summary row: same request_id as the first denial, suppressed_count = 1.
if [ "${DENY1_CODE}" = "403" ] && [ "${DENY2_CODE}" = "403" ]; then
  if ! valid_rid "${DENY1_ID}"; then
    fail "AUDIT-TYPES suppression summary" "no X-Request-ID header"
  else
    summary_wait=$((SMOKE_SUPPRESS_WINDOW_SECS * 2 + 30))
    if wait_q_eq "select count(*) from audit_events where request_id='${DENY1_ID}' and event_type='denial' and suppressed_count=1" 1 "${summary_wait}"; then
      pass "AUDIT-TYPES suppression summary row written with suppressed_count=1 (migration 000003 and sweeper live)"
    else
      fail "AUDIT-TYPES suppression summary" "no row with suppressed_count=1 for the first denial within ${summary_wait}s, last count ${LAST_Q:-none}"
    fi
  fi
else
  echo "AUDIT-TYPES SKIPPED: suppression summary assertion (repeat returned HTTP ${DENY2_CODE:-none}); the summary row is proven only by hermetic TestE2ESuppressionAndSummary and TestPipelineSweeper"
fi

# e. unauthenticated request: an anonymous denial row.
if ! valid_rid "${UNAUTH_ID}"; then
  fail "AUDIT-TYPES unauthenticated denial" "no X-Request-ID header"
elif wait_q_eq "select count(*) from audit_events where request_id='${UNAUTH_ID}'" 1 30; then
  rows="$(psql_q "select event_type||'|'||decision||'|'||http_status||'|'||principal_kind from audit_events where request_id='${UNAUTH_ID}'")"
  if [ "${rows}" = "denial|deny|401|anonymous" ]; then
    pass "AUDIT-TYPES unauthenticated request has a denial row (401, anonymous)"
  else
    fail "AUDIT-TYPES unauthenticated denial" "row=[${rows:-none}]"
  fi
else
  fail "AUDIT-TYPES unauthenticated denial" "no row for the unauthenticated request within 30s, last count ${LAST_Q:-none}"
fi

# f. the gateway's own counters.
metrics="$(dc exec -T "${GW}" curl -s http://127.0.0.1:9091/metrics 2>/dev/null | tr -d '\r' || true)"
metric_value() {
  printf '%s\n' "${metrics}" | awk -v k="$1" '$1 == k { v = $2 } END { print v }'
}
for kind in completion denial; do
  v="$(metric_value "aegis_audit_records_written_total{kind=\"${kind}\"}")"
  if [ -n "${v}" ] && awk -v v="${v}" 'BEGIN { exit !(v + 0 > 0) }'; then
    pass "AUDIT-TYPES gateway counter aegis_audit_records_written_total{kind=${kind}} is ${v}"
  else
    fail "AUDIT-TYPES gateway counter" "aegis_audit_records_written_total{kind=${kind}} is '${v:-missing}', expected > 0"
  fi
done
v="$(metric_value aegis_audit_degraded)"
if [ "${v}" = "0" ]; then
  pass "AUDIT-TYPES aegis_audit_degraded 0"
else
  fail "AUDIT-TYPES aegis_audit_degraded" "value '${v:-missing}', expected 0"
fi

# --- 4c. DEDUPE: cursor deleted, worker restarted, no new rows -------------
echo "=== DEDUPE: delete the cursor, restart the worker, row count must not change (AUD-03) ==="
for w in "${WORKERS[@]}"; do
  # Quiesce: the row count must be unchanged across 3 consecutive 1-second samples.
  prev=""
  stable=0
  for _ in $(seq 1 60); do
    cur="$(audit_count)"
    if [ -n "${cur}" ] && [ "${cur}" = "${prev}" ]; then
      stable=$((stable + 1))
    else
      stable=0
    fi
    prev="${cur}"
    if [ "${stable}" -ge 2 ]; then
      break
    fi
    sleep 1
  done
  if [ "${stable}" -lt 2 ]; then
    fail "DEDUPE ${w}" "audit_events count never stayed constant for 3 samples within 60s (last ${prev:-none})"
    continue
  fi
  before="${prev}"

  seg_before="$(cursor_field "${w}" segment_file)"
  off_before="$(cursor_field "${w}" offset)"
  case "${off_before}" in
    ''|*[!0-9]*) off_before=0 ;;
  esac
  if [ -z "${seg_before}" ] || [ "${off_before}" -le 0 ]; then
    fail "DEDUPE ${w}" "nothing to replay: cursor segment '${seg_before:-none}' offset ${off_before}"
    continue
  fi

  if ! dc exec -T "${w}" rm -f "${WAL_CURSOR}" >/dev/null 2>&1; then
    fail "DEDUPE ${w}" "rm unavailable in worker container (assumption A2)"
    continue
  fi
  if ! dc restart "${w}" >/dev/null 2>&1; then
    fail "DEDUPE ${w}" "docker compose restart failed"
    continue
  fi

  seg_after=""
  for _ in $(seq 1 30); do
    seg_after="$(cursor_field "${w}" segment_file)"
    if [ -n "${seg_after}" ]; then
      break
    fi
    sleep 1
  done
  sleep 5
  after="$(audit_count)"
  if [ -z "${seg_after}" ]; then
    fail "DEDUPE ${w}" "wal.cursor was not recreated within 30s of the restart"
  elif [[ "${seg_after}" < "${seg_before}" ]]; then
    fail "DEDUPE ${w}" "recreated cursor segment ${seg_after} sorts before the earlier ${seg_before}"
  elif [ "${after}" != "${before}" ]; then
    fail "DEDUPE ${w}" "audit_events count changed from ${before} to ${after:-none} after replay"
  else
    pass "DEDUPE ${w} cursor deleted, worker restarted, replay absorbed (count unchanged at ${before})"
  fi
done

# --- 4d. ROTATION: multi-segment delivery (only when requested) --------------
echo "=== ROTATION: multi-segment delivery (AUD-03) ==="
if [ "${SMOKE_ROTATION_REQUESTS}" -le 0 ]; then
  echo "ROTATION SKIPPED: set SMOKE_ROTATION_REQUESTS=300 and start the stack with AEGIS_SPOOL_SEGMENT_BYTES=65536 to exercise rotation (rotation is NOT claimed)"
elif [ "${PROFILE}" != "mvp" ]; then
  echo "ROTATION SKIPPED: the rotation check is only implemented for the mvp profile (rotation is NOT claimed)"
else
  rot_worker="${WORKERS[0]:-}"
  seg_seq() { sed -n 's/^wal-[0-9]*-\([0-9]*\)\.log$/\1/p'; }
  seq_a="$(cursor_field "${rot_worker}" segment_file | seg_seq)"
  if [ -z "${rot_worker}" ] || [ -z "${seq_a}" ]; then
    fail "ROTATION" "could not read a wal-<nanos>-<seq>.log segment name from the worker cursor"
  else
    seq_a=$((10#${seq_a}))
    rot_ids=()
    rot_no_id=0
    rot_non200=0
    tok=""
    for i in $(seq 1 "${SMOKE_ROTATION_REQUESTS}"); do
      if [ -z "${tok}" ] || [ $(((i - 1) % 100)) -eq 0 ]; then
        tok="$(dev_token)"
      fi
      if [ -z "${tok}" ]; then
        rot_non200=$((rot_non200 + 1))
        continue
      fi
      probe="$(gw_probe "${tok}" /api/orders)"
      code="${probe%% *}"
      rid="${probe#* }"
      if [ "${code}" != "200" ]; then
        rot_non200=$((rot_non200 + 1))
      elif valid_rid "${rid}"; then
        rot_ids+=("${rid}")
      else
        rot_no_id=$((rot_no_id + 1))
      fi
      sleep 0.02
    done
    rot_n="${#rot_ids[@]}"
    echo "ROTATION sent ${SMOKE_ROTATION_REQUESTS} requests: ${rot_n} with 200 and a request id, ${rot_non200} non-200, ${rot_no_id} 200 without request id"
    if [ "${rot_n}" -eq 0 ] || [ "${rot_no_id}" -gt 0 ]; then
      fail "ROTATION" "cannot verify delivery: ${rot_n} usable 200 responses, ${rot_no_id} 200 responses without X-Request-ID"
    else
      rot_list="$(printf "'%s'," "${rot_ids[@]}")"
      rot_list="${rot_list%,}"
      rot_ok="false"
      if wait_q_eq "select count(distinct request_id) from audit_events where event_type='completion' and request_id in (${rot_list})" "${rot_n}" 90; then
        rot_ok="true"
      fi
      seq_b="${seq_a}"
      for _ in $(seq 1 15); do
        seg_b="$(cursor_field "${rot_worker}" segment_file | seg_seq)"
        if [ -n "${seg_b}" ]; then
          seq_b=$((10#${seg_b}))
        fi
        if [ $((seq_b - seq_a)) -ge 2 ]; then
          break
        fi
        sleep 1
      done
      ready_code="$(curl -s -o /dev/null -w '%{http_code}' "${GATEWAY_URL}/readyz" 2>/dev/null || true)"
      if [ "${rot_ok}" != "true" ]; then
        fail "ROTATION" "only ${LAST_Q:-none} of ${rot_n} completion rows arrived within 90s"
      elif [ $((seq_b - seq_a)) -lt 2 ]; then
        fail "ROTATION" "all ${rot_n} rows present but segment seq ${seq_a} -> ${seq_b}; segment size too large to rotate; start the stack with a smaller AEGIS_SPOOL_SEGMENT_BYTES"
      elif [ "${ready_code}" != "200" ]; then
        fail "ROTATION" "gateway /readyz returned HTTP ${ready_code:-none} after the burst (spool saturated?)"
      else
        pass "ROTATION ${rot_n} requests delivered across >=2 segment rotations (seq ${seq_a} -> ${seq_b})"
      fi
    fi
  fi
fi

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
