---
status: complete
phase: 01-mvp-secure-vertical-slice
source: [01-01-SUMMARY.md, 01-02-SUMMARY.md, 01-03-SUMMARY.md, 01-04-SUMMARY.md]
started: 2026-10-08T00:00:00Z
updated: 2026-11-17T00:00:00Z
---

## Current Test

[testing complete]

## Tests

### 1. Cold Start Smoke Test
expected: Stop the MVP stack, rebuild the images from current source, and start it fresh with `make compose-down && make compose-build && make compose-up`. All five containers (gateway, demo-issuer, orders, payments, admin) stay up in `docker compose -f deployments/compose/docker-compose.mvp.yml ps`. The gateway log shows no startup errors, and `curl -i http://localhost:8085/health` returns 200.
result: pass — 2026-11-17 MVP stack from images built at HEAD 7c6edf0 (fresh `down -v`, `up --wait`): all 9 services running (topology now also has control-plane, redis, postgres, audit-worker since Phase 7), 0 error/fatal lines in gateway log, demo-issuer healthy.

### 2. Get a Demo Token
expected: `curl -s -X POST http://localhost:8085/login -H 'Content-Type: application/json' -d '{"role":"developer"}'` returns JSON containing a non-empty `access_token`. The same works for `finance` and `application-admin`.
result: pass (updated mechanics) — since Phase 9 the issuer takes seeded username/password (`{"username":"developer","password":...}`); developer, finance and application-admin all return a non-empty access_token. A role-only body now returns 400 by design (AUTH-04).

### 3. Developer Reads Orders
expected: With the developer token, `curl -i http://localhost:8080/api/orders -H "Authorization: Bearer $TOKEN"` returns HTTP 200 with the orders service's JSON payload.
result: pass — developer GET /api/orders → 200.

### 4. Developer Denied Admin
expected: With the developer token, `curl -i http://localhost:8080/api/admin/users -H "Authorization: Bearer $TOKEN"` returns HTTP 403 with a problem-details JSON body naming reason code `DENIED_DEVELOPER_ADMIN_FORBIDDEN`. `POST /api/payments` with the same token also returns 403.
result: pass — developer GET /api/admin/users → 403 problem JSON with `detail: DENIED_DEVELOPER_ADMIN_FORBIDDEN`; developer POST /api/payments → 403.

### 5. Finance Permissions
expected: With the finance token, `curl -i -X POST http://localhost:8080/api/payments -H "Authorization: Bearer $TOKEN"` returns HTTP 200, and `GET /api/orders` with the same token returns HTTP 403.
result: pass — finance POST /api/payments → 200; finance GET /api/orders → 403.

### 6. Admin Reads Admin Users
expected: With the application-admin token, `curl -i http://localhost:8080/api/admin/users -H "Authorization: Bearer $TOKEN"` returns HTTP 200 with the admin service's payload.
result: pass — application-admin GET /api/admin/users → 200.

### 7. Missing or Bad Token Rejected
expected: `curl -i http://localhost:8080/api/orders` with no Authorization header returns HTTP 401. A garbage token (`-H "Authorization: Bearer abc.def.ghi"`) also returns 401. Neither reaches a backend.
result: pass — no Authorization → 401; `Bearer abc.def.ghi` → 401.

### 8. Path Traversal Rejected
expected: `curl -i --path-as-is "http://localhost:8080/api/orders/../admin/users" -H "Authorization: Bearer $TOKEN"` returns HTTP 400 Bad Request. So do `/api/orders/..%2fadmin`, `/api//orders`, and `/api/orders%00`. The path is never silently cleaned into a valid one.
result: pass — `/api/orders/../admin/users`, `/api/orders/..%2fadmin`, `/api//orders`, `/api/orders%00` all → 400.

### 9. Spoofed Headers Ignored
expected: With the developer token, adding `-H "X-Aegis-User: usr_admin_01" -H "X-Aegis-Roles: application-admin" -H "X-Forwarded-For: 10.0.0.1"` to `GET /api/admin/users` still returns HTTP 403. Injected headers grant nothing.
result: pass — spoofed X-Aegis-User / X-Aegis-Roles / X-Forwarded-For with developer token on /api/admin/users → 403.

### 10. Backends Unreachable Directly
expected: `curl -i http://localhost:8081/`, `http://localhost:8082/`, and `http://localhost:8083/` all fail with connection refused. `docker compose -f deployments/compose/docker-compose.mvp.yml ps` shows no published host ports for orders, payments, or admin.
result: pass — localhost:8081/8082/8083 connection refused; no published host ports for orders/payments/admin; TestBackendBypassPrevention 7/7 live.

### 11. Request ID on Every Response
expected: Every gateway response (the 200, 400, 401 and 403 cases above) carries an `X-Request-ID` header containing a UUID. A client-supplied `X-Request-ID` is replaced, not echoed.
result: pass — every response carries a UUID X-Request-ID; a client-supplied `X-Request-ID: spoofed-123` is replaced.

### 12. Completion Audit Log
expected: After a few requests, `docker compose -f deployments/compose/docker-compose.mvp.yml logs gateway` shows one JSON line per request with request ID, principal, method, path, HTTP status, duration in ms, decision and reason code. Denied and malformed requests are logged as well as allowed ones.
result: pass (updated mechanics) — since Phase 8 completion/denial records go to the durable spool → Postgres, not gateway stdout. audit_events (last 60 min): decision/allow 335, completion/allow 335, denial/deny 41 incl. 20 unauthenticated/malformed (400/401) denials.

### 13. Oversized Request Rejected
expected: A request with a body over 1 MiB (`head -c 2000000 /dev/zero | curl -i -X POST http://localhost:8080/api/payments -H "Authorization: Bearer $FINANCE_TOKEN" --data-binary @-`) is rejected with HTTP 413 and is not forwarded. A request with more than 16 KiB of headers is rejected with HTTP 431.
result: issue — 16 KiB+ headers → 431 (pass), but bodies over 1 MiB are NOT rejected: 1,048,577 and 2,000,000-byte POST /api/payments with a finance token both returned 200 from the payments backend. MaxBytesReader only trips if the upstream reads past the limit; the mock backend replies first, so the oversized request is forwarded.
re-test (2026-11-17, after quick task 261117-rvo): pass — 2,000,000-byte bodies get 413 problem+json with Content-Length and chunked; normal requests 200.

### 14. Login Throttling
expected: Sending more than 20 login requests within a minute from one machine (`for i in $(seq 1 25); do curl -s -o /dev/null -w "%{http_code}\n" -X POST http://localhost:8085/login -H 'Content-Type: application/json' -d '{"role":"developer"}'; done`) starts returning HTTP 429 after roughly the 20th.
result: pass — 25 bad-password logins from one source: 401 ×16 then 429 ×9 (throttle engages at the configured burst).

### 15. Security Test Suite
expected: `make test-security` runs the path traversal, header spoofing, JWT negative and RBAC matrix suites and finishes with all tests passing.
result: pass — `make test-security`: 24 tests, 0 failures (race on).

## Summary

total: 15
passed: 15
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps

- truth: "A request body over 1 MiB is rejected with HTTP 413 and is not forwarded (GW-01)"
  status: resolved
  resolved_by: "quick task 261117-rvo (054fe69, 03491f5, 46d6629, d7116f1)"
  retest: "2026-11-17 hardened stack, rebuilt gateway: 2,000,000-byte body → 413 problem+json (body-too-large) with Content-Length and with chunked transfer; normal GET → 200."
  reason: "Live: 2,000,000-byte POST /api/payments returned 200 from the backend. http.MaxBytesReader only errors when the upstream reads past the limit; there is no Content-Length pre-check and the proxy does not map *http.MaxBytesError to 413."
  severity: major
  test: 13
  artifacts: [internal/proxy/server.go]
  missing: ["413 pre-check on Content-Length > MaxBodyBytes on all three ingress handlers", "proxy ErrorHandler maps *http.MaxBytesError to 413 for chunked bodies", "regression test with a backend that responds without reading the body"]
