---
phase: 4
slug: operator-experience-telemetry
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-10-07
---

# Phase 4 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go Test CLI (`go test -race`) + `httptest` + `miniredis/v2` + `pgxmock/v4` + Vite/TypeScript Compiler (`tsc && vite build`) |
| **Config file** | `api/openapi/control-v1.yaml`, `web/dashboard/package.json` |
| **Quick run command** | `go test -v -race ./internal/control/... ./internal/telemetry/... ./internal/storage/...` |
| **Full suite command** | `go test -race ./... && cd web/dashboard && npm run build` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick run command (`go test -v -race ./internal/control/... ./internal/telemetry/... ./internal/storage/...`)
- **After every plan wave:** Run full suite command (`go test -race ./... && (cd web/dashboard && npm run build)`)
- **Before `/gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 04-01-01 | 01 | 1 | OPS-01, OPS-04 | T-04-01, T-04-03 | Chi REST router mounts `/control/v1` routes with HttpOnly cookies, CSRF validation, and CSP headers | unit/security | `go test -v -race ./internal/control/ -run TestSessionAndCSRF` | ❌ W0 | ⬜ pending |
| 04-01-02 | 01 | 1 | OPS-02 | T-04-02, T-04-06 | Optimistic concurrency (`ETag`, `If-Match` 412) and Idempotency key deduplication (409 conflict, 24h cached replay) | unit/security | `go test -v -race ./internal/control/ -run TestConcurrencyAndIdempotency` | ❌ W0 | ⬜ pending |
| 04-01-03 | 01 | 1 | OPS-01, OPS-02 | T-04-01, T-04-07 | Policy draft validation, dry-run Rego simulation, monotonic publish/rollback, and RBAC authorization | unit | `go test -v -race ./internal/control/ -run TestPolicyAndSimulationAPI` | ❌ W0 | ⬜ pending |
| 04-02-01 | 02 | 2 | OPS-03, OPS-04 | T-04-03 | React 19 / Vite / Tailwind dashboard scaffolded with authenticated shell and CSP compatibility | build | `cd web/dashboard && npm run build` | ❌ W0 | ⬜ pending |
| 04-02-02 | 02 | 2 | OPS-03 | T-04-02 | Monaco Rego editor with custom Monarch grammar, syntax checks, and dry-run policy simulator component | unit/build | `cd web/dashboard && npm run build` | ❌ W0 | ⬜ pending |
| 04-02-03 | 02 | 2 | OPS-01, OPS-03 | T-04-04 | PostgreSQL audit log query repository, `/control/v1/audit-events` API, and filterable audit stream UI | unit | `go test -v -race ./internal/storage/ -run TestAuditRepo && go test -v -race ./internal/control/ -run TestAuditEventsAPI` | ❌ W0 | ⬜ pending |
| 04-03-01 | 03 | 3 | OPS-01, OPS-03 | T-04-04 | Real-time cluster convergence endpoint `/control/v1/gateways`, fleet grid view, and emergency quarantine UI | unit/integration | `go test -v -race ./internal/control/ -run TestConvergenceAndQuarantine` | ❌ W0 | ⬜ pending |
| 04-03-02 | 03 | 3 | DIST-02 | T-04-05 | Prometheus telemetry metrics exported on private listener (:9091/:9092) with strictly bounded label cardinality | unit/security | `go test -v -race ./internal/telemetry/ -run TestPrometheusMetrics` | ❌ W0 | ⬜ pending |
| 04-03-03 | 03 | 3 | OPS-03, DIST-02 | T-04-01, T-04-05 | Gateway and Control Plane entrypoints wired with REST API, SPA embed fallback, and metrics scraping | integration | `go test -v -race ./tests/integration/ -run TestOperatorAndTelemetryIntegration` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Go dependencies installed:
  - `github.com/go-chi/chi/v5`
  - `github.com/prometheus/client_golang`
- [ ] Frontend directory scaffolding:
  - `web/dashboard/` initialized with React 19, Vite, TypeScript, Tailwind CSS, Monaco Editor
- [ ] Go packages scaffolding:
  - `internal/control/` extended with REST router and middleware
  - `internal/telemetry/` initialized with Prometheus metrics collectors
  - `internal/storage/audit_repo.go` for audit log queries

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| None | N/A | N/A | All Phase 4 behaviors have automated verification via Go test suite, httptest, pgxmock, miniredis, and TypeScript compiler / Vite build. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-10-07
