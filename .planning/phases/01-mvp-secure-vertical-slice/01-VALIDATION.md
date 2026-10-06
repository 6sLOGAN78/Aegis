---
phase: 1
slug: mvp-secure-vertical-slice
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-10-06
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go Test CLI (`go test -race`) + OPA Test CLI (`opa test`) + Docker Compose |
| **Config file** | `deployments/compose/docker-compose.mvp.yml`, `policies/rego/authz.rego` |
| **Quick run command** | `go test -race ./internal/... && opa test policies/rego policies/tests -v` |
| **Full suite command** | `go test -race ./... && opa test policies/rego policies/tests -v && docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build && go test -v -race ./tests/integration/... && docker compose -f deployments/compose/docker-compose.mvp.yml down` |
| **Estimated runtime** | ~25 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick run command (`go test -race ./internal/... && opa test policies/rego policies/tests -v`)
- **After every plan wave:** Run full suite command
- **Before `/gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 25 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 01-01-01 | 01 | 1 | GW-01 | T-01-08 | 16 KiB header and 1 MiB body limits enforced; UUID X-Request-ID assigned | unit | `go test -v -race ./internal/proxy/ -run TestListenerLimits` | ❌ W0 | ⬜ pending |
| 01-01-02 | 01 | 1 | GW-02 | T-01-03 | Zero-repair path validator rejects `..`, `%2f`, `%5c`, `//`, `%00` with 400 | security | `go test -v -race ./tests/security/ -run TestZeroRepairPathValidation` | ❌ W0 | ⬜ pending |
| 01-01-03 | 01 | 1 | GW-04 | T-01-01 | RFC 7230 hop-by-hop and client `X-Aegis-*` headers stripped before forward | security | `go test -v -race ./tests/security/ -run TestHeaderScrubbing` | ❌ W0 | ⬜ pending |
| 01-01-04 | 01 | 1 | REV-02 | T-01-07 | Counting semaphore limits unauthenticated in-flight requests with 429 | unit | `go test -v -race ./internal/proxy/ -run TestConcurrencyLimiter` | ❌ W0 | ⬜ pending |
| 01-02-01 | 02 | 1 | POL-01 | T-01-09 | Embedded OPA prepares and evaluates query in-memory in <0.2ms per op | benchmark | `go test -bench=BenchmarkOPAEval -benchmem ./internal/policy/` | ❌ W0 | ⬜ pending |
| 01-02-02 | 02 | 1 | POL-02 | T-01-09 | Default-deny returns `allow: false` and `DENIED_DEFAULT` on unmapped routes | unit | `go test -v -race ./internal/policy/ -run TestDefaultDeny` | ❌ W0 | ⬜ pending |
| 01-02-03 | 02 | 1 | POL-03 | T-01-09 | Developer/finance/admin roles enforce exact RBAC matrix with reason codes | security | `go test -v -race ./tests/security/ -run TestRBACMatrix` | ❌ W0 | ⬜ pending |
| 01-02-04 | 02 | 1 | POL-04 | T-01-09 | Anonymous or invalid identities fail closed with 401/403 without forward | unit | `go test -v -race ./internal/policy/ -run TestFailClosed` | ❌ W0 | ⬜ pending |
| 01-03-01 | 03 | 2 | AUTH-01 | T-01-02 | Pinned algorithm allowlist (EdDSA, RS256, ES256) rejects `alg: none` and HMAC | security | `go test -v -race ./tests/security/ -run TestJWTNegativeSecurity` | ❌ W0 | ⬜ pending |
| 01-03-02 | 03 | 2 | AUTH-04 | T-01-02 | Demo issuer signs 5-min role tokens and rate limits login endpoint | unit | `go test -v -race ./cmd/demo-issuer/ -run TestDemoIssuer` | ❌ W0 | ⬜ pending |
| 01-03-03 | 03 | 2 | GW-03 | T-01-04 | Upstream router maps authorized requests to private mock services | unit | `go test -v -race ./internal/proxy/ -run TestRouteResolution` | ❌ W0 | ⬜ pending |
| 01-03-04 | 03 | 2 | BYP-01 | T-01-10 | Compose mvp runs services with 0 host ports; direct backend access refused | integration | `go test -v -race ./tests/integration/ -run TestBackendBypassPrevention` | ❌ W0 | ⬜ pending |
| 01-04-01 | 04 | 3 | AUD-04 | T-01-05 | Structured completion audit logs 100% of responses with duration & reason | unit | `go test -v -race ./internal/audit/ -run TestCompletionAuditLogging` | ❌ W0 | ⬜ pending |
| 01-04-02 | 04 | 3 | GW-01..BYP-01 | T-01-01..10 | End-to-end integration and negative security suite in Docker Compose | integration | `go test -v -race ./tests/integration/ -run TestMVPEndToEnd` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Directory scaffolding:
  - `cmd/gateway`, `cmd/demo-issuer`, `cmd/services/{orders,payments,admin}`
  - `internal/{proxy,policy,audit,auth}`
  - `tests/{security,integration}`
  - `deployments/compose`
- [ ] Go dependencies installed:
  - `github.com/open-policy-agent/opa` (v1.21.1)
  - `github.com/golang-jwt/jwt/v5` (v5.3.1)
  - `github.com/go-chi/chi/v5` (v5.3.2)
  - `github.com/stretchr/testify` (v1.12.1)
  - `github.com/google/uuid` (v1.6.0)
- [ ] Docker Compose mvp profile file: `deployments/compose/docker-compose.mvp.yml`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| None | All | N/A | All Phase 1 gateway, auth, policy, and network isolation behaviors have automated test commands. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 25s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending 2026-10-06
