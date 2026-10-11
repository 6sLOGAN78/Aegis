---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 11
subsystem: web/dashboard
tags: [monaco, csp, vite, policy-studio]
requirements: [OPS-03, CTRL-01]
key-files:
  created: [web/dashboard/src/utils/monaco-setup.ts]
  modified: [web/dashboard/package.json, web/dashboard/package-lock.json, web/dashboard/vite.config.ts, web/dashboard/src/components/PolicyStudio.tsx, web/dashboard/dist]
decisions:
  - "monaco-editor pinned to exact 0.57.0"
  - "Vite plugin neutralizes the dead default CDN literal in @monaco-editor/loader so dist has no jsdelivr string"
metrics:
  completed: 2026-10-11
---

# Phase 10 Plan 11: Local Monaco bundle and latest-source hydration Summary

Monaco is bundled locally via `loader.config({ monaco })` with a Vite-built editor worker, so the Rego editor no longer depends on cdn.jsdelivr.net (CSP in internal/control/api.go untouched); the editor also hydrates once from the latest stored policy source.

## Tasks
1. Local Monaco + worker (commit 561543a): `monaco-setup.ts` imports monaco-editor, the editor worker (`monaco-editor/editor/editor.worker?worker`, because the 0.57 exports map rejects the `esm/vs/...` path), and calls `loader.config`. Imported as side effect in PolicyStudio.
2. Hydration (commit 561543a): one-time effect guarded by `hydratedRef` and `userEditedRef` (set in `handleCodeChange`); no comparison with the default template; no re-hydration on the 10s refetch.
3. Rebuilt tracked dist (commit 8c66e68): stale hashed assets removed, new assets added; `grep -r jsdelivr dist` empty; `editor.worker-*.js` present; `go build ./...` passes.

## Deviations
- [Rule 3] The `?worker` import path in the plan failed to resolve; used the exports-map form.
- [Rule 3] @monaco-editor/loader's default config contains the jsdelivr URL as dead code, which would fail the "no CDN reference in dist" criterion; added a small `strip-monaco-loader-cdn` Vite plugin in vite.config.ts.
- `npm install --package-lock-only` added unrelated optional-dep noise, so it was reverted and the lockfile's root monaco-editor spec was edited by hand to `0.57.0`.
- Tasks 1 and 2 share one commit since both touch PolicyStudio.tsx.
- `go test -run 'SPA|Dashboard'` matched no tests ("no tests to run"); build passes.

## Bundle size
Full `monaco-editor` import bundles all language workers (ts.worker ~6.9 MB, css ~1.1 MB, html ~0.7 MB, json ~0.4 MB); dist is now ~15 MB (was 3 files). Only the base editor worker is used for Rego; trimming to `editor.api` plus selected contributions is a possible follow-up.

## Human verification pending
Policy Studio editor leaves "Loading..." and shows the active policy on http://localhost:8084/dashboard/ with no CSP console errors; retyping the default template and waiting 10s+ does not overwrite the editor. No unit test runner exists in web/dashboard, so hydration has no automated test.

## Self-Check: PASSED
