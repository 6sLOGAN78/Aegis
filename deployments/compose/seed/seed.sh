#!/bin/sh
# Aegis compose seed: loads routes + policy into the control plane via its REST API
# and publishes a signed snapshot. Workaround for POL-03 (no binary reads
# policies/data/routes.json or policies/rego/authz.rego); it does not close POL-03.
#
# Environment:
#   AEGIS_CP_URL            control plane base URL (default http://control-plane:8084)
#   AEGIS_SEED_USER         sec-ops operator username (REQUIRED, no default)
#   AEGIS_SEED_PASSWORD     operator password (REQUIRED, no default, never printed)
#   AEGIS_SEED_POLICY_DIR   directory holding data/routes.json and rego/authz.rego
#                           (default /app/policies)
#   AEGIS_SEED_POLICY_ID    policy id for the draft (default mvp-authz)
#   AEGIS_SEED_WAIT_SECONDS seconds to wait for the control plane (default 60)
#   AEGIS_SEED_DRY_RUN      when 1: print transformed route payloads and exit 0,
#                           before any credential check or network call
#
# Behavior notes:
#   - The session cookie is Secure, so curl would drop it over plain HTTP. The
#     cookie and CSRF token are therefore sent as explicit headers; no cookie jar
#     is used and cookie flags are never weakened.
#   - routes.json has burst < rps on every route and the API rejects that, so the
#     posted burst is max(burst, rps).
#   - Re-runs against a persisted postgres_data volume work: routes and policy
#     drafts are upserts, and each run publishes one new snapshot version
#     (publish retries once with the ETag from a 412 response).
#   - Any non-2xx response exits non-zero so a dependent gateway
#     (service_completed_successfully) never starts on a half-seeded snapshot.

set -eu

AEGIS_CP_URL="${AEGIS_CP_URL:-http://control-plane:8084}"
AEGIS_SEED_POLICY_DIR="${AEGIS_SEED_POLICY_DIR:-/app/policies}"
AEGIS_SEED_POLICY_ID="${AEGIS_SEED_POLICY_ID:-mvp-authz}"
AEGIS_SEED_WAIT_SECONDS="${AEGIS_SEED_WAIT_SECONDS:-60}"
AEGIS_SEED_DRY_RUN="${AEGIS_SEED_DRY_RUN:-0}"

banner() {
  echo "=== $* ===" >&2
}

# Step 1: transform routes.json into RouteCreateRequest objects, one per line.
transform_routes() {
  jq -c '.[] | {
    route_id,
    service_id,
    http_method,
    path_template,
    upstream_url,
    upstream_spiffe_id,
    rate_limit_rps: .rate_limit.requests_per_second,
    rate_limit_burst: ([.rate_limit.burst, .rate_limit.requests_per_second] | max),
    timeout_ms: .timeout.upstream_timeout_ms,
    requires_workload_mtls
  }' "$AEGIS_SEED_POLICY_DIR/data/routes.json"
}

# Step 0(a): dry-run exits before any credential check or network call.
if [ "$AEGIS_SEED_DRY_RUN" = "1" ]; then
  transform_routes
  exit 0
fi

# Step 0(b): credential check before any file read or network call.
if [ -z "${AEGIS_SEED_USER:-}" ]; then
  echo "seed: required environment variable AEGIS_SEED_USER is not set" >&2
  exit 2
fi
if [ -z "${AEGIS_SEED_PASSWORD:-}" ]; then
  echo "seed: required environment variable AEGIS_SEED_PASSWORD is not set" >&2
  exit 2
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

SID=""
CSRF=""

# cp_call METHOD PATH BODYFILE [extra curl args...]
# Writes response headers to $WORK/resp.h, body to $WORK/resp.b, prints the HTTP code.
cp_call() {
  _method="$1"
  _path="$2"
  _body="$3"
  shift 3
  if [ -n "$_body" ]; then
    curl -s --max-time 15 -X "$_method" \
      -D "$WORK/resp.h" -o "$WORK/resp.b" -w '%{http_code}' \
      -H "Cookie: aegis_session=$SID" \
      -H "X-CSRF-Token: $CSRF" \
      -H "Content-Type: application/json" \
      "$@" \
      --data-binary "@$_body" \
      "$AEGIS_CP_URL$_path"
  else
    curl -s --max-time 15 -X "$_method" \
      -D "$WORK/resp.h" -o "$WORK/resp.b" -w '%{http_code}' \
      -H "Cookie: aegis_session=$SID" \
      -H "X-CSRF-Token: $CSRF" \
      "$@" \
      "$AEGIS_CP_URL$_path"
  fi
}

banner "Preparing route payloads"
transform_routes > "$WORK/routes.ndjson"
EXPECTED_ROUTES="$(jq 'length' "$AEGIS_SEED_POLICY_DIR/data/routes.json")"

banner "Waiting for control plane at $AEGIS_CP_URL"
waited=0
until curl -s --max-time 5 -o /dev/null "$AEGIS_CP_URL/control/v1/auth/me"; do
  waited=$((waited + 1))
  if [ "$waited" -ge "$AEGIS_SEED_WAIT_SECONDS" ]; then
    echo "seed: control plane not reachable after ${AEGIS_SEED_WAIT_SECONDS}s" >&2
    exit 1
  fi
  sleep 1
done

banner "Logging in"
jq -n --arg u "$AEGIS_SEED_USER" --arg p "$AEGIS_SEED_PASSWORD" \
  '{username: $u, password: $p}' > "$WORK/login.json"
code="$(curl -s --max-time 15 -X POST \
  -D "$WORK/login.h" -o "$WORK/login.b" -w '%{http_code}' \
  -H "Content-Type: application/json" \
  --data-binary "@$WORK/login.json" \
  "$AEGIS_CP_URL/control/v1/auth/login")"
rm -f "$WORK/login.json"
if [ "$code" != "200" ]; then
  echo "seed: login failed: HTTP $code" >&2
  exit 1
fi
CSRF="$(jq -r '.csrf_token // empty' "$WORK/login.b")"
SID="$(tr -d '\r' < "$WORK/login.h" \
  | grep -i '^set-cookie: *aegis_session=' \
  | head -n 1 \
  | sed 's/^[^=]*=//; s/;.*$//')"
if [ -z "$CSRF" ] || [ -z "$SID" ]; then
  echo "seed: login response missing session cookie or csrf token" >&2
  exit 1
fi

banner "Seeding routes"
posted=0
while read -r route; do
  [ -n "$route" ] || continue
  printf '%s' "$route" > "$WORK/route.json"
  rid="$(printf '%s' "$route" | jq -r '.route_id')"
  code="$(cp_call POST /control/v1/routes "$WORK/route.json")"
  if [ "$code" != "201" ] && [ "$code" != "200" ]; then
    echo "route $rid failed: HTTP $code" >&2
    cat "$WORK/resp.b" >&2
    echo >&2
    exit 1
  fi
  posted=$((posted + 1))
done < "$WORK/routes.ndjson"
if [ "$posted" -ne "$EXPECTED_ROUTES" ]; then
  echo "seed: posted $posted routes, expected $EXPECTED_ROUTES" >&2
  exit 1
fi

banner "Seeding policy"
jq -n --arg id "$AEGIS_SEED_POLICY_ID" \
  --rawfile s "$AEGIS_SEED_POLICY_DIR/rego/authz.rego" \
  '{policy_id: $id, name: "authz.rego", source_rego: $s}' > "$WORK/policy.json"
code="$(cp_call POST /control/v1/policies "$WORK/policy.json")"
if [ "$code" != "201" ] && [ "$code" != "200" ]; then
  echo "policy $AEGIS_SEED_POLICY_ID failed: HTTP $code" >&2
  cat "$WORK/resp.b" >&2
  echo >&2
  exit 1
fi

banner "Publishing snapshot"
PUBLISH_PATH="/control/v1/policies/$AEGIS_SEED_POLICY_ID/publish"
code="$(cp_call POST "$PUBLISH_PATH" "" -H 'If-Match: "1"')"
if [ "$code" = "412" ]; then
  etag="$(tr -d '\r' < "$WORK/resp.h" \
    | grep -i '^etag:' \
    | head -n 1 \
    | sed 's/^[^:]*: *//; s/"//g')"
  if [ -z "$etag" ]; then
    echo "seed: publish returned 412 without an ETag" >&2
    exit 1
  fi
  code="$(cp_call POST "$PUBLISH_PATH" "" -H "If-Match: \"$etag\"")"
fi
if [ "$code" != "200" ]; then
  echo "publish failed: HTTP $code" >&2
  cat "$WORK/resp.b" >&2
  echo >&2
  exit 1
fi
version="$(tr -d '\r' < "$WORK/resp.h" \
  | grep -i '^etag:' \
  | head -n 1 \
  | sed 's/^[^:]*: *//; s/"//g')"

echo "seed complete: $posted routes, policy $AEGIS_SEED_POLICY_ID, snapshot version ${version:-unknown}" >&2
exit 0
