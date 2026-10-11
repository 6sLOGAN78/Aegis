---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 09
subsystem: control-plane-auth
tags: [cookie, session, compose, gap-closure]
requires: []
provides:
  - "Env-configurable Secure flag on aegis_session cookie (AEGIS_COOKIE_SECURE, default secure)"
affects: [internal/control, deployments/compose]
key-files:
  modified:
    - internal/control/auth.go
    - internal/control/api.go
    - internal/control/session_test.go
    - deployments/compose/docker-compose.mvp.yml
    - deployments/compose/docker-compose.distributed.yml
decisions:
  - "Only AEGIS_COOKIE_SECURE=false/0 disables Secure; unrecognised values stay secure"
  - "Hardened compose profile left unchanged"
requirements: [OPS-03, CTRL-01]
metrics:
  tasks: 2
  completed: 2026-10-11
---

# Phase 10 Plan 09: Configurable session cookie Secure flag Summary

Session cookie Secure attribute is now driven by AEGIS_COOKIE_SECURE (default true, only "false"/"0" disables) on both login and logout cookies, with demo compose profiles opting out so LAN HTTP login works.

## Tasks

1. Configurable Secure flag (commit f31fcc8): `secureCookie` field, `WithSecureCookie`, `cookieSecureFromEnv`; api.go wires env and logs a warning via `log.Printf` when disabled. Tests added for secure/insecure login+logout and env parsing.
2. Compose opt-out (commit bab4893): `AEGIS_COOKIE_SECURE=false` with warning comment in mvp and distributed control-plane env; hardened untouched.

## Verification

`go vet ./internal/control/` clean; `go test ./internal/control/ -count=1` passes. docker compose config not run.

## Deviations from Plan

- Worktree base was behind the expected base commit, so it was reset to 0087394 per the startup instruction. Otherwise none.
- The existing tests reference "Secure: true" alignment as `Secure:   true,`; both replaced by `h.secureCookie`.

## Notes

Pre-existing gofmt differences in api_test.go and server.go were left alone (out of scope).

## Self-Check: PASSED
