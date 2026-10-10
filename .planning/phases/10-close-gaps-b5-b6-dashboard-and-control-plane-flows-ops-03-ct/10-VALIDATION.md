---
phase: 10
slug: close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-10-11
---

# Phase 10 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` + testify + pgxmock; in-process gRPC (`tests/chaos` TestCluster); dashboard: `tsc -b && vite build` (no unit runner) |
| **Config file** | none (Makefile targets) |
| **Quick run command** | `go test -race ./internal/control/... ./internal/snapshot/...` |
| **Full suite command** | `make test-full` (+ `cd web/dashboard && npm run build` for dashboard touches) |
| **Estimated runtime** | ~90 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test -race ./internal/control/... ./internal/snapshot/...`
- **After every plan wave:** Run `make test-full` (+ dashboard build if touched)
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 90 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| TBD | TBD | TBD | CTRL-05 / OPS-03 (B5) | — | Heartbeat ack echoes last real status (REJECTED not masked) | unit/in-process | `go test -race ./internal/snapshot -run Heartbeat` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | CTRL-05 / OPS-03 (B5) | — | Control plane records Pong | unit | `go test -race ./internal/control -run Pong` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | OPS-03 (B5) | — | Idle gateway stays healthy; >60s silent → partitioned | unit | `go test ./internal/control -run TestGatewayConvergence` | extend | ⬜ pending |
| TBD | TBD | TBD | OPS-03 (B6) | — | Save-then-publish 200 for `mvp-authz`; unknown id 404 | unit (pgxmock) | `go test ./internal/control -run TestPublish` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | OPS-03 (B6) | — | Dashboard default id = seeded id | build + grep | `cd web/dashboard && npm run build && grep -n mvp-authz src/components/PolicyStudio.tsx` | ✅ | ⬜ pending |
| TBD | TBD | TBD | CTRL-01 | — | Invalid Rego / failing test → 422, no sign/persist/broadcast | unit | `go test ./internal/control -run TestPublishValidation` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | CTRL-02 | — | Published payload carries IdentityMappings; preserved across publish + rollback | unit + in-process | `go test -race ./internal/control ./internal/snapshot -run IdentityMappings` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | OPS-01 | — | Every router route has an OpenAPI op; POST/DELETE ref IdempotencyKey | unit (drift) | `go test ./internal/control -run TestOpenAPIDrift` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | OPS-02 | — | Same-key replay returns cached response; missing If-Match → 412 | unit | `go test ./internal/control -run "Idempot|IfMatch"` | partial | ⬜ pending |
| TBD | TBD | TBD | OPS-02 (dashboard) | — | Dashboard sends fresh idempotency key per mutation attempt | build + grep | `grep -rn idempotencyKey web/dashboard/src/components` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/control/publish_handler_test.go` — B6, CTRL-01, CTRL-02 publish behaviours (reuse `setupTestHarness` from `api_test.go`)
- [ ] `internal/control/openapi_drift_test.go` — OPS-01
- [ ] heartbeat + rejection-preservation tests in `internal/snapshot/client_test.go`
- [ ] Pong test in `internal/control/control_test.go`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Idle gateway shows healthy past 60s in live dashboard | OPS-03 (B5) | Needs running compose stack + browser | `make compose-up PROFILE=mvp`; wait >60s with no publish; open convergence view; all gateways `healthy` |
| Dashboard author → validate → publish → swap end to end | OPS-03 (B6) | No dashboard test runner | In Policy Studio, edit `mvp-authz`, validate, publish; confirm 200 and gateways converge on new version |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
