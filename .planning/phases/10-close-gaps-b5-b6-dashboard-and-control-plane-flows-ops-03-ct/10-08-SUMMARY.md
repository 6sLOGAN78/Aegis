---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 08
subsystem: dashboard
tags: [react, idempotency, insecure-context, gap-closure]
requires: []
provides:
  - newIdempotencyKey() helper safe on non-secure (plain HTTP) origins
affects: [PolicyStudio, EmergencyQuarantine]
tech-stack:
  added: []
  patterns: [getRandomValues RFC4122 v4 fallback, lazy useRef init]
key-files:
  created: [web/dashboard/src/api/idempotency.ts]
  modified:
    - web/dashboard/src/components/PolicyStudio.tsx
    - web/dashboard/src/components/EmergencyQuarantine.tsx
    - web/dashboard/dist
key-decisions:
  - "dist is tracked in git, so the rebuilt bundle is committed"
requirements-completed: [OPS-02, OPS-03]
duration: 10min
completed: 2026-10-11
---

# Phase 10 Plan 08: Idempotency key helper (CR-03) Summary

Shared `newIdempotencyKey()` uses `crypto.randomUUID` when available and otherwise falls back to a `crypto.getRandomValues` RFC4122 v4 key, so Policy Studio and emergency quarantine/revoke/rollback work over plain HTTP on :8084.

## Tasks
1. Helper plus all call sites replaced (commit 6656fa0). `publishKeyRef` is now `useRef<string | null>(null)` with a guarded lazy init. `randomUUID` remains only in idempotency.ts. `tsc -b` is clean.
2. Behavior verified and dist rebuilt (commit in git log: "build(10-08)"). A scratchpad script (not committed) confirmed that, with randomUUID absent, 1000 keys all match the v4 regex and are unique, and that the randomUUID-present path is used. The built bundle contains getRandomValues and has zero unguarded `randomUUID()` calls.

## Deviations from Plan
- [Rule 3] `node_modules` was absent, so I ran `npm ci` from the lockfile (no new packages).
- [Rule 1] `publishKeyRef.current` typed `string | null` broke tsc at the publish call. I used `publishKeyRef.current ?? newIdempotencyKey()` there.
- The minified bundle expresses the guard as `if(typeof crypto.randomUUID==...)return crypto.randomUUID()` rather than a ternary. I widened the scratch regex to accept both forms.
- `dist/` is tracked (not gitignored), so the rebuild appears as a diff and was committed.

## Known Stubs
None.

## Self-Check: PASSED
