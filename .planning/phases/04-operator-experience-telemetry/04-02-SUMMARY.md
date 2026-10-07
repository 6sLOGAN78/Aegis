---
phase: 04-operator-experience-telemetry
plan: 02
subsystem: ui
tags: [react, vite, tailwindcss, monaco, rego, monarch, spa, embed, dry-run, audit]

# Dependency graph
requires:
  - phase: 04-operator-experience-telemetry
    plan: 01
    provides: /control/v1 REST management APIs, session cookies, CSRF defense, and candidate Rego simulation
provides:
  - React 19 / TypeScript Operator Dashboard (web/dashboard) with dark SOC design system
  - In-binary embedded SPA static file server via embed.FS with client-side fallback routing (internal/control/spa.go, web/embed.go)
  - Type-safe apiClient with automatic HttpOnly cookie delivery and X-CSRF-Token header injection
  - Client-side Monaco Monarch tokenizer for Rego v1 syntax highlighting without external CDNs
  - Policy Studio with in-memory Rego AST validation and monotonic snapshot publication/rollback modals (N to N+1)
  - Dry-run Policy Simulator with synthetic request context editor, presets, and microsecond decision latency card
  - Filterable PostgreSQL Audit Stream viewer with cursor pagination and slide-over JSON record drawer
affects: [04-03-PLAN, operator-console, integration-tests]

# Tech tracking
tech-stack:
  added:
    - react 19.3.0
    - vite 8.3.3
    - tailwindcss 4.3.3
    - "@monaco-editor/react 4.7.0"
    - "monaco-editor 0.57.0"
    - "@tanstack/react-query 5.104.1"
    - "lucide-react 1.52.0"
  patterns:
    - Local Vite bundling with pure client-side Monarch tokenizer (zero CDN dependency)
    - Double-submit CSRF propagation in type-safe fetch client
    - Monotonic N+1 snapshot publication confirmation dialogs
    - Slide-over detail drawer for JSON telemetry inspection
    - Go embed.FS SPA static asset server with client-side HTML fallback

key-files:
  created:
    - web/embed.go
    - web/dashboard/.gitignore
    - web/dashboard/package.json
    - web/dashboard/vite.config.ts
    - web/dashboard/tsconfig.json
    - web/dashboard/index.html
    - web/dashboard/src/main.tsx
    - web/dashboard/src/index.css
    - web/dashboard/src/App.tsx
    - web/dashboard/src/api/types.ts
    - web/dashboard/src/api/client.ts
    - web/dashboard/src/utils/rego-monarch.ts
    - web/dashboard/src/components/Shell.tsx
    - web/dashboard/src/components/LoginModal.tsx
    - web/dashboard/src/components/PolicyStudio.tsx
    - web/dashboard/src/components/PolicySimulator.tsx
    - web/dashboard/src/components/AuditStream.tsx
    - web/dashboard/dist/index.html
    - internal/control/spa.go
  modified:
    - internal/control/api.go

key-decisions:
  - "Used dedicated web/embed.go package to embed web/dashboard/dist cleanly, avoiding Go's standard library //go:embed illegal relative '..' path constraint."
  - "Configured pure in-browser Monaco Monarch tokenizer for Rego v1, satisfying strict CSP (script-src 'self') without remote CDN or LSP dependencies."
  - "Built type-safe ApiClient with automatic credentials: 'include', ambient session cookie delivery, and X-CSRF-Token injection on mutating HTTP methods."
  - "Implemented monotonic publish confirmation modal displaying current version (N), next monotonic version (N+1), and active ETag hash."
  - "Enforced cursor-based pagination and default 24h bounding on PostgreSQL audit logs with high-contrast Allow/Deny badges and slide-over JSON drawers."

patterns-established:
  - "Pattern: Zero-CDN Monaco Monarch syntax highlighting for declarative policy authoring"
  - "Pattern: Ambient cookie + X-CSRF-Token double-submit propagation across frontend mutations"
  - "Pattern: Monotonic confirmation dialogs preventing version regression during publishing or rollback"
  - "Pattern: Embedded Go SPA serving with client-side routing fallback to index.html"

requirements-completed:
  - OPS-03
  - OPS-04

# Metrics
duration: 35min
completed: 2026-10-07
---

# Plan 04-02: React/TypeScript Operator Dashboard with Monaco Rego Editor, Dry-Run Simulator, and Audit Log Viewer Summary

**Delivered the modern, dark SOC operator console (`web/dashboard`) built with React 19, Vite 8, and Tailwind CSS v4, embedded into the control plane binary (`internal/control/spa.go`) with client-side Rego v1 syntax highlighting, monotonic publishing, dry-run simulation, and filterable audit inspection.**

## Performance

- **Duration:** 35 min
- **Started:** 2026-10-07T04:40:00Z
- **Completed:** 2026-10-07T05:15:00Z
- **Tasks:** 3
- **Files modified/created:** 20

## Accomplishments

- Scaffolding & SPA Embed Server: Bootstrapped React 19, TypeScript 5.7+, Vite 8, Tailwind CSS v4, Monaco Editor, Lucide React, and React Query in `web/dashboard`. Embedded the compiled distribution assets into the Go binary with fallback routing via `internal/control/spa.go` and `web/embed.go`.
- Session Management & Shell: Implemented `Shell.tsx` and `LoginModal.tsx` enforcing authenticated access, session status monitoring, live connection pulse indicator, role badges (`sec-ops`, `auditor`, `viewer`), and session termination.
- Type-Safe API Client: Created `ApiClient` with ambient cookie management, double-submit `X-CSRF-Token` header injection on mutating operations, `If-Match` ETag propagation, `Idempotency-Key` headers, and 401 interception.
- Monaco Rego v1 Editor: Implemented custom client-side Monarch tokenizer (`rego-monarch.ts`) for Rego v1 keywords, operators, strings, comments, and brackets with zero external CDN dependencies.
- Policy Studio: Built split-screen workspace with Monaco Rego editor, in-memory AST syntax validation feedback, version history list, and monotonic publish/rollback confirmation modals ($N \to N+1$).
- Policy Simulator: Built side-by-side comparative simulation workspace with synthetic JSON request context editor, pre-configured presets (developer orders allow, developer admin deny, workload payments allow), microsecond latency timer, and high-contrast Allow/Deny decision cards.
- Audit Stream Viewer: Built high-density PostgreSQL audit log inspection interface with decision, service, principal, and time range filters, cursor-based pagination, and slide-over raw JSON event drawer.

## Task Commits

Each task was committed atomically:

1. **Task 1: Dashboard Scaffolding, SPA Embed Server, API Client, and Shell Layout** - `325b58b` (feat)
2. **Task 2: Monaco Rego Editor with Monarch Tokenizer, Policy Studio, and Monotonic Publishing Workflow** - `d375340` (feat)
3. **Task 3: Dry-Run Policy Simulator and Filterable Audit Stream Viewer** - `22fbe36` (feat)

## Files Created/Modified

- `web/embed.go` - Go package embedding `dashboard/dist` assets
- `internal/control/spa.go` - SPA file server with fallback to `index.html` on client-side routes
- `internal/control/api.go` - Mounted SPA routes onto Chi management router
- `web/dashboard/.gitignore` - Ignored `node_modules` and `*.tsbuildinfo`
- `web/dashboard/package.json` - Declared dependencies (React 19, Monaco, Tailwind v4, React Query, Lucide)
- `web/dashboard/package-lock.json` - Pinned dependency lockfile
- `web/dashboard/vite.config.ts` - Vite 8 configuration with React and Tailwind v4 plugins and `/control/v1` proxy
- `web/dashboard/tsconfig.json` - Strict TypeScript configuration
- `web/dashboard/index.html` - HTML entrypoint with root container
- `web/dashboard/dist/index.html` - Embedded distribution build placeholder shell
- `web/dashboard/dist/assets/*` - Compiled production JavaScript and CSS assets
- `web/dashboard/src/main.tsx` - React DOM bootstrap with `QueryClientProvider`
- `web/dashboard/src/index.css` - Tailwind CSS v4 styling rules and JetBrains Mono monospace fonts
- `web/dashboard/src/App.tsx` - Root application component orchestrating session state, modals, and tab navigation
- `web/dashboard/src/api/types.ts` - TypeScript interfaces aligned with `api/openapi/control-v1.yaml`
- `web/dashboard/src/api/client.ts` - Type-safe fetch client with credentials, CSRF, ETag, and 401 interceptor
- `web/dashboard/src/utils/rego-monarch.ts` - Monaco Monarch tokenizer definition and language configuration for Rego v1
- `web/dashboard/src/components/Shell.tsx` - Application shell with top header, navigation tabs, role badges, and logout
- `web/dashboard/src/components/LoginModal.tsx` - Operator login modal dialog with error diagnostics
- `web/dashboard/src/components/PolicyStudio.tsx` - Monaco Rego editor, draft authoring, AST validation, and monotonic publish modal
- `web/dashboard/src/components/PolicySimulator.tsx` - Side-by-side JSON context editor, presets, and decision outcome card
- `web/dashboard/src/components/AuditStream.tsx` - Filterable PostgreSQL audit table and slide-over JSON drawer

## Decisions Made

- Created `web/embed.go` containing `//go:embed all:dashboard/dist` to avoid Go's restriction against relative `..` path patterns in `//go:embed`.
- Implemented Rego syntax highlighting purely in TypeScript using Monaco's Monarch engine; avoids running WebAssembly or external LSP network requests in the browser, adhering to CSP.
- Ensured monotonic version progression ($N \to N+1$) is visibly reinforced in the publish and rollback confirmation modals with explicit warning notices.
- Configured React Query with `refetchOnWindowFocus: false` and bounded intervals for predictable polling and low background network load.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Build Alignment] @vitejs/plugin-react version alignment with Vite 8**
- **Found during:** Task 1 (`npm install`)
- **Issue:** The plan referenced `@vitejs/plugin-react: ^4.3.4`, which is designed for Vite 4–7 and conflicted with `vite: ^8.3.3` peer dependencies.
- **Fix:** Updated `package.json` to `@vitejs/plugin-react: ^6.1.2`, which officially supports Vite 8.
- **Files modified:** `web/dashboard/package.json`
- **Verification:** `npm install` and `npm run build` completed with exit code 0.
- **Committed in:** `325b58b` (Task 1 commit)

**2. [Rule 1 - Go Embed Constraint] Embedding frontend assets across packages**
- **Found during:** Task 1 (SPA server embedding)
- **Issue:** Go's `//go:embed` directive prohibits relative `..` paths, preventing `internal/control/spa.go` from directly embedding `../../web/dashboard/dist`.
- **Fix:** Created `web/embed.go` in package `web` to embed `dashboard/dist`, which is consumed by `internal/control/spa.go` via `fs.Sub(web.DashboardDist, "dashboard/dist")`.
- **Files modified:** `web/embed.go`, `internal/control/spa.go`
- **Verification:** `go test -v -race ./internal/control/...` passed with exit code 0.
- **Committed in:** `325b58b` (Task 1 commit)

**3. [Rule 1 - Linter Fix] Unused refetch variable in AuditStream**
- **Found during:** Task 3 (`npm run build`)
- **Issue:** `refetch` destructured from `useQuery` in `AuditStream.tsx` was unused, triggering TypeScript `TS6133` error under strict compilation.
- **Fix:** Removed unused `refetch` from destructuring.
- **Files modified:** `web/dashboard/src/components/AuditStream.tsx`
- **Verification:** `npm run build` succeeded in 206ms with zero errors.
- **Committed in:** `22fbe36` (Task 3 commit)

---

**Total deviations:** 3 auto-fixed (all Rule 1 build alignments / linter fixes)
**Impact on plan:** Essential for zero-error compilation across Go and TypeScript. Zero scope creep.

## Issues Encountered
None that were not resolved via automatic fixes.

## User Setup Required
None. Frontend builds directly via `npm run build` in `web/dashboard` and static assets are embedded into the Go binary.

## Next Phase Readiness
- React/TypeScript Operator Dashboard is scaffolded, styled, and verified.
- Policy Studio, Policy Simulator, and Audit Stream are operational.
- Ready for Plan 04-03: Real-time Gateway Convergence Topology grid, Emergency Quarantine modal (<5s SLA), and Bounded Prometheus Telemetry collectors.

---
*Phase: 04-operator-experience-telemetry*
*Completed: 2026-10-07*
