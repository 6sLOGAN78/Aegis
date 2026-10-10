---
status: testing
phase: 01-mvp-secure-vertical-slice
source: [01-01-SUMMARY.md, 01-02-SUMMARY.md, 01-03-SUMMARY.md, 01-04-SUMMARY.md]
started: 2026-10-08T00:00:00Z
updated: 2026-10-08T00:00:00Z
---

## Current Test
<!-- OVERWRITE each test - shows where we are -->

number: 1
name: Cold Start Smoke Test
expected: |
  Stop the MVP stack, rebuild the images from current source, and start it fresh:
  `make compose-down && make compose-build && make compose-up`
  All five containers (gateway, demo-issuer, orders, payments, admin) stay up in `docker compose -f deployments/compose/docker-compose.mvp.yml ps`. The gateway log shows no startup errors, and `curl -i http://localhost:8085/health` returns 200.
awaiting: user response

## Tests

### 1. Cold Start Smoke Test
expected: Stop the MVP stack, rebuild the images from current source, and start it fresh with `make compose-down && make compose-build && make compose-up`. All five containers (gateway, demo-issuer, orders, payments, admin) stay up in `docker compose -f deployments/compose/docker-compose.mvp.yml ps`. The gateway log shows no startup errors, and `curl -i http://localhost:8085/health` returns 200.
result: [pending]

### 2. Get a Demo Token
expected: `curl -s -X POST http://localhost:8085/login -H 'Content-Type: application/json' -d '{"role":"developer"}'` returns JSON containing a non-empty `access_token`. The same works for `finance` and `application-admin`.
result: [pending]

### 3. Developer Reads Orders
expected: With the developer token, `curl -i http://localhost:8080/api/orders -H "Authorization: Bearer $TOKEN"` returns HTTP 200 with the orders service's JSON payload.
result: [pending]

### 4. Developer Denied Admin
expected: With the developer token, `curl -i http://localhost:8080/api/admin/users -H "Authorization: Bearer $TOKEN"` returns HTTP 403 with a problem-details JSON body naming reason code `DENIED_DEVELOPER_ADMIN_FORBIDDEN`. `POST /api/payments` with the same token also returns 403.
result: [pending]

### 5. Finance Permissions
expected: With the finance token, `curl -i -X POST http://localhost:8080/api/payments -H "Authorization: Bearer $TOKEN"` returns HTTP 200, and `GET /api/orders` with the same token returns HTTP 403.
result: [pending]

### 6. Admin Reads Admin Users
expected: With the application-admin token, `curl -i http://localhost:8080/api/admin/users -H "Authorization: Bearer $TOKEN"` returns HTTP 200 with the admin service's payload.
result: [pending]

### 7. Missing or Bad Token Rejected
expected: `curl -i http://localhost:8080/api/orders` with no Authorization header returns HTTP 401. A garbage token (`-H "Authorization: Bearer abc.def.ghi"`) also returns 401. Neither reaches a backend.
result: [pending]

### 8. Path Traversal Rejected
expected: `curl -i --path-as-is "http://localhost:8080/api/orders/../admin/users" -H "Authorization: Bearer $TOKEN"` returns HTTP 400 Bad Request. So do `/api/orders/..%2fadmin`, `/api//orders`, and `/api/orders%00`. The path is never silently cleaned into a valid one.
result: [pending]

### 9. Spoofed Headers Ignored
expected: With the developer token, adding `-H "X-Aegis-User: usr_admin_01" -H "X-Aegis-Roles: application-admin" -H "X-Forwarded-For: 10.0.0.1"` to `GET /api/admin/users` still returns HTTP 403. Injected headers grant nothing.
result: [pending]

### 10. Backends Unreachable Directly
expected: `curl -i http://localhost:8081/`, `http://localhost:8082/`, and `http://localhost:8083/` all fail with connection refused. `docker compose -f deployments/compose/docker-compose.mvp.yml ps` shows no published host ports for orders, payments, or admin.
result: [pending]

### 11. Request ID on Every Response
expected: Every gateway response (the 200, 400, 401 and 403 cases above) carries an `X-Request-ID` header containing a UUID. A client-supplied `X-Request-ID` is replaced, not echoed.
result: [pending]

### 12. Completion Audit Log
expected: After a few requests, `docker compose -f deployments/compose/docker-compose.mvp.yml logs gateway` shows one JSON line per request with request ID, principal, method, path, HTTP status, duration in ms, decision and reason code. Denied and malformed requests are logged as well as allowed ones.
result: [pending]

### 13. Oversized Request Rejected
expected: A request with a body over 1 MiB (`head -c 2000000 /dev/zero | curl -i -X POST http://localhost:8080/api/payments -H "Authorization: Bearer $FINANCE_TOKEN" --data-binary @-`) is rejected with HTTP 413 and is not forwarded. A request with more than 16 KiB of headers is rejected with HTTP 431.
result: [pending]

### 14. Login Throttling
expected: Sending more than 20 login requests within a minute from one machine (`for i in $(seq 1 25); do curl -s -o /dev/null -w "%{http_code}\n" -X POST http://localhost:8085/login -H 'Content-Type: application/json' -d '{"role":"developer"}'; done`) starts returning HTTP 429 after roughly the 20th.
result: [pending]

### 15. Security Test Suite
expected: `make test-security` runs the path traversal, header spoofing, JWT negative and RBAC matrix suites and finishes with all tests passing.
result: [pending]

## Summary

total: 15
passed: 0
issues: 0
pending: 15
skipped: 0
blocked: 0

## Gaps

[none yet]
