---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 03
subsystem: dashboard
tags: [policy-studio, idempotency, dist]
requires: []
provides: [save-then-publish flow, fresh Idempotency-Key per mutation, rebuilt embedded dist]
key-files:
  modified:
    - web/dashboard/src/components/PolicyStudio.tsx
    - web/dashboard/src/components/EmergencyQuarantine.tsx
    - web/dashboard/dist
decisions:
  - Publish key held in a useRef and re-minted on every success or error; draft/rollback/quarantine keys minted inside mutationFn.
metrics:
  completed: 2026-10-11
---

# Phase 10 Plan 03: Dashboard save-then-publish Summary

Policy Studio now defaults to `mvp-authz`, saves the editor content as a draft (POST /control/v1/policies, name authz.rego) before publishing, shows 422 errors, and every mutating action (publish, rollback, create-draft, quarantine, unquarantine, revoke) sends a fresh `crypto.randomUUID()` Idempotency-Key. The embedded `web/dashboard/dist` was rebuilt: it contains `mvp-authz` and no `pol_authz_core`.

## Commits
- d2fc766: PolicyStudio changes
- 6d149c4: EmergencyQuarantine keys and rebuilt dist

## Deviations from Plan
None. `client.ts` delete already accepted options. `npm run build` passes (node_modules temporarily symlinked from the main checkout, removed after build). Manual UAT (edit, validate, publish returns 200) not run.

## Self-Check: PASSED
