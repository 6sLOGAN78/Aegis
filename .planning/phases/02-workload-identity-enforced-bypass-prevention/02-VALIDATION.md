---
phase: 2
slug: workload-identity-enforced-bypass-prevention
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-10-06
---

# Phase 2 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go Test CLI (`go test -race`) + Docker Compose |
| **Config file** | `deployments/compose/docker-compose.mvp.yml` |
| **Quick run command** | `go test -v -race ./internal/identity/... ./internal/pki/... ./internal/proxy/... ./services/middleware/... ./tests/security/...` |
| **Full suite command** | `go test -v -race ./... && docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build && go test -v -race ./tests/integration/... && docker compose -f deployments/compose/docker-compose.mvp.yml down` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick run command (`go test -v -race ./internal/identity/... ./internal/pki/... ./internal/proxy/... ./services/middleware/... ./tests/security/...`)
- **After every plan wave:** Run full suite command
- **Before `/gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 02-01-01 | 01 | 1 | AUTH-02 | T-02-01 | Pure Go PKI generator generates valid Root CA, intermediate CA, and workload x509 certs with SPIFFE URI SANs | unit | `go test -v -race ./internal/pki/ -run TestPKI` | ❌ W0 | ⬜ pending |
| 02-01-02 | 01 | 1 | AUTH-02 | T-02-01 | SPIFFE URI extractor strictly extracts and parses `spiffe://aegis.local/workload/...` from verified TLS peer certs | unit | `go test -v -race ./internal/identity/ -run TestSPIFFE` | ❌ W0 | ⬜ pending |
| 02-01-03 | 01 | 1 | AUTH-02, AUTH-03 | T-02-02, T-02-03 | Workload mTLS listener on :9443 requires verified client cert and rejects `Authorization: Bearer` with HTTP 401 | security | `go test -v -race ./tests/security/ -run TestWorkloadIdentityListener` | ❌ W0 | ⬜ pending |
| 02-02-01 | 02 | 2 | AUTH-05 | T-02-04, T-02-05 | Gateway mints Ed25519-signed assertion JWT with <=15s expiry bound to recipient service, method, canonical path, req ID | unit | `go test -v -race ./internal/identity/ -run TestAssertionToken` | ❌ W0 | ⬜ pending |
| 02-02-02 | 02 | 2 | GW-05, AUTH-05 | T-02-01, T-02-06 | Reverse proxy forwards via mTLS client transport, injects `X-Aegis-Assertion`, and scrubs client `Authorization` | unit | `go test -v -race ./internal/proxy/ -run TestMTLSForwarding` | ❌ W0 | ⬜ pending |
| 02-02-03 | 02 | 2 | AUTH-05 | T-02-04, T-02-05 | Assertion negative security suite validates signature forgery, expiry, path tampering, and audience substitution rejection | security | `go test -v -race ./tests/security/ -run TestAssertionSecurity` | ❌ W0 | ⬜ pending |
| 02-03-01 | 03 | 3 | BYP-02 | T-02-02, T-02-07 | Reusable backend middleware validates Gateway SPIFFE peer cert and verifies `X-Aegis-Assertion` claims | unit | `go test -v -race ./services/middleware/ -run TestAuthMiddleware` | ❌ W0 | ⬜ pending |
| 02-03-02 | 03 | 3 | BYP-02, BYP-03 | T-02-07, T-02-08 | Microservices (`orders`, `payments`, `admin`) enforce HTTPS + auth middleware; `/health` is exempted | unit | `go test -v -race ./cmd/services/...` | ❌ W0 | ⬜ pending |
| 02-03-03 | 03 | 3 | BYP-03 | T-02-08 | Certificate provisioning script generates dev certs and Compose config configures TLS without host ports | integration | `go test -v -race ./cmd/services/...` | ❌ W0 | ⬜ pending |
| 02-04-01 | 04 | 4 | AUTH-02, POL-03 | T-02-09 | Authenticated workload `orders` can call `/api/payments` but is rejected from `/api/admin/users` (HTTP 403) | security | `go test -v -race ./tests/security/ -run TestWorkloadPolicyEnforcement` | ❌ W0 | ⬜ pending |
| 02-04-02 | 04 | 4 | BYP-02, BYP-03 | T-02-07, T-02-08 | Direct calls without gateway client cert or assertion fail with TLS handshake failure or 401/403 | integration | `go test -v -race ./tests/integration/ -run TestBackendBypassPrevention` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Directory scaffolding:
  - `internal/pki`
  - `services/middleware`
  - `scripts/certificates`
- [ ] Certificate generation utilities for unit test fixtures in Go standard library (`crypto/x509`, `crypto/ecdsa`, `crypto/tls`)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| None | All | N/A | All phase behaviors have automated verification. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-10-06
