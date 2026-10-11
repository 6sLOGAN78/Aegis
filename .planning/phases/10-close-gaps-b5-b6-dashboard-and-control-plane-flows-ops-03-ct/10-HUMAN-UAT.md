---
status: diagnosed
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
source: [10-VERIFICATION.md]
started: 2026-10-11T06:25:23Z
updated: 2026-10-11T06:48:31Z
---

## Current Test

[testing complete — 2 issues found]

## Tests

### 1. Compose smoke with idle window and control-plane restart (B5 / CR-01)
expected: `make compose-smoke` passes including the 65s idle check; after restarting the control plane, gateways reconnect and stay "converged" (never flip to "partitioned").
result: pass (partial)
notes: Distributed profile (3 gateways). Restarted control-plane at 12:16:38; at t+30/60/90s gateway-1/2/3 all status=healthy, version=7, last_ack advancing each 30s. CR-01 confirmed live. compose-smoke.sh could not run: DISK pre-flight aborted (Docker root 87.9% full > 85% limit).

### 2. Dashboard end-to-end over plain HTTP (B6 / CR-03 / OPS-02 / OPS-03)
expected: From both http://localhost and http://<host-ip>:8084, Policy Studio renders; author → validate → publish succeeds; convergence view shows the new version; quarantine/revoke/rollback succeed with no crypto.randomUUID errors in the console.
result: issue
notes: localhost:8084 — PASS: login, validate (200), save draft POST /policies (201) then publish (200, v6, no 404 — B6 fixed), all 3 live gateways converged on v6, rollback to v5 → v7 (200), quarantine/revoke/release (200). No randomUUID errors. LAN IP 192.170.9.86:8084 — isSecureContext=false, crypto.randomUUID undefined, Policy Studio renders without crash (CR-03 fixed), BUT login 200 is followed by 401 on every call: session cookie is Secure (internal/control/auth.go:79) and browsers drop it on plain-HTTP non-localhost origins. Both origins: Monaco editor never loads (CDN loader blocked by CSP script-src 'self'), so the Rego cannot be edited in the UI.

## Summary

total: 2
passed: 1
issues: 1
pending: 0
skipped: 0
blocked: 0

## Gaps

- truth: "Operator can authenticate and use the dashboard over plain HTTP from a LAN IP (http://<host-ip>:8084)"
  status: failed
  reason: "Session cookie set with Secure=true (internal/control/auth.go:79,106); browser discards it on non-secure origin, every API call after login returns 401"
  severity: major
  test: 2
- truth: "Operator can author/edit Rego in Policy Studio"
  status: failed
  reason: "@monaco-editor/react loads loader.js from cdn.jsdelivr.net; CSP script-src 'self' blocks it; editor stuck on 'Loading...' on every origin. Fix: bundle monaco locally (loader.config({ monaco }))"
  severity: major
  test: 2
- truth: "Convergence view shows only live replicas"
  status: failed
  reason: "Dead gateway records from a previous stack (random IDs, last heartbeat minutes old) stay listed as PARTITIONED and count against convergence (3/6). Cosmetic; no eviction/TTL"
  severity: minor
  test: 2
