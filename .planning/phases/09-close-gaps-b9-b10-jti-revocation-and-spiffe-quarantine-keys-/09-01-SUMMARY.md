---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
plan: 01
subsystem: demo-issuer
tags: [auth, jwt, jti, compose]
provides: [credential-checked /login, jti on every token and in login response, password env vars in all compose profiles]
key-files:
  modified:
    - cmd/demo-issuer/main.go
    - cmd/demo-issuer/main_test.go
    - deployments/compose/docker-compose.mvp.yml
    - deployments/compose/docker-compose.hardened.yml
    - deployments/compose/docker-compose.distributed.yml
    - tests/compose/compose_test.go
requirements: [AUTH-04, REV-03]
completed: 2026-10-10
---

# Phase 9 Plan 01: Issuer login with credentials and jti Summary

Demo issuer now requires username and password (constant-time sha256 digest compare, identical 401 INVALID_CREDENTIALS for unknown user or bad password) and mints a UUID v4 jti on every token, returned as `jti` in the login response.

## Tasks
1. Issuer login with jti: commit 24d14a3
2. Compose password env vars + TestDemoIssuerCredentials: commit 7512d74

Demo defaults: demo-developer-pass, demo-finance-pass, demo-admin-pass; compose sets the same values explicitly so no default warning fires.

## Deviations from Plan
None. TDD red/green were combined in one commit per task.

## Notes
- Plan files were untracked in the main checkout, so they were read from there (read-only).
- Rate limit check still precedes body decode, so failed logins count toward the 20/min limit.

## Self-Check: PASSED
