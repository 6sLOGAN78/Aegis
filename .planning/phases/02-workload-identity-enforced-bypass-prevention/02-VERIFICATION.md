---
phase: 02-workload-identity-enforced-bypass-prevention
verified: 2026-10-06T18:05:00Z
status: passed
score: 6/6 requirements verified, 4/4 observable truths verified
---

# Phase 2: Workload Identity & Enforced Bypass Prevention Verification Report

**Phase Goal:** Implement mutual TLS workload authentication on a dedicated listener, enforce gateway client identity verification and short-lived signed assertions on backends, and eliminate direct microservice bypass.
**Verified:** 2026-10-06T18:05:00Z
**Status:** passed

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Workload presenting a verified client certificate with SPIFFE URI SAN `spiffe://aegis.local/workload/orders` on port `:9443` successfully invokes `/api/payments` via the gateway (HTTP 200); requests presenting user bearer tokens on this listener are rejected with HTTP 401 Unauthorized (`AMBIGUOUS_CREDENTIALS`) (AUTH-02, AUTH-03) | ✓ VERIFIED | `internal/proxy/server.go`, `internal/identity/spiffe.go`, verified via `tests/security/workload_identity_test.go` and `tests/integration/bypass_test.go` |
| 2 | Gateway mints a short-lived (<=15s) Ed25519-signed JWT assertion (`X-Aegis-Assertion`, issuer `aegis-gateway`) bound to target service audience, canonical path, HTTP method, and correlation request ID on every forwarded request over mTLS (AUTH-05, GW-05) | ✓ VERIFIED | `internal/identity/assertion.go`, `internal/proxy/proxy.go`, verified via `tests/security/assertion_security_test.go` and `internal/identity/assertion_test.go` |
| 3 | Backend middleware validates gateway mTLS client certificate (`spiffe://aegis.local/ns/gateway/sa/aegis-gateway`) and verifies assertion claims; direct HTTP/HTTPS calls from outside or peer containers without gateway certificate and assertion fail immediately (HTTP 401/403 or TLS alert) (BYP-02, BYP-03) | ✓ VERIFIED | `services/middleware/auth_middleware.go`, verified via `services/middleware/auth_middleware_test.go` and `tests/integration/bypass_test.go` |
| 4 | Automated security tests prove that an authenticated workload (`orders`) cannot access administrative endpoints (`/api/admin/users`), confirming workload role policy enforcement with `DENIED_WORKLOAD_ADMIN_FORBIDDEN` (HTTP 403) | ✓ VERIFIED | `policies/rego/authz.rego` Rule 5, verified via `tests/security/workload_identity_test.go` |

**Score:** 4/4 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|---|---|---|---|
| `internal/pki/pki.go` | Pure Go PKI certificate generator | ✓ EXISTS + SUBSTANTIVE | In-memory ECDSA P-256 Root CA, SPIFFE client certs, server certs |
| `internal/identity/spiffe.go` | Strict SPIFFE URI SAN parser | ✓ EXISTS + SUBSTANTIVE | Extracts exclusively from peer cert URIs, validates scheme & trust domain |
| `internal/identity/assertion.go` | Short-lived signed assertion minter & verifier | ✓ EXISTS + SUBSTANTIVE | Ed25519-signed JWTs, <=15s expiry, audience/method/path binding |
| `internal/proxy/server.go` | Dual-listener HTTP & mTLS manager | ✓ EXISTS + SUBSTANTIVE | Port 8080 (user) vs 9443 (mTLS workload), ambiguous credential rejection |
| `internal/proxy/proxy.go` | Upstream mTLS reverse proxy | ✓ EXISTS + SUBSTANTIVE | Pooled transport with client cert, `Rewrite` assertion injection |
| `services/middleware/auth_middleware.go` | Reusable microservice auth middleware | ✓ EXISTS + SUBSTANTIVE | Transport SPIFFE ID verification + application JWT assertion claims check |
| `scripts/certificates/main.go` | Development PKI generator CLI script | ✓ EXISTS + SUBSTANTIVE | Generates dev certs for Docker Compose environment |
| `deployments/compose/docker-compose.mvp.yml` | Hardened Docker Compose deployment | ✓ EXISTS + SUBSTANTIVE | Zero published host ports for microservices, internal network isolation |
| `tests/security/workload_identity_test.go` | Workload RBAC & listener security suite | ✓ EXISTS + SUBSTANTIVE | 100% PASS with race detector enabled |
| `tests/security/assertion_security_test.go` | Negative assertion security suite | ✓ EXISTS + SUBSTANTIVE | Forgery, tampering, audience substitution, expiry rejection verified |
| `tests/integration/bypass_test.go` | Direct and lateral bypass prevention suite | ✓ EXISTS + SUBSTANTIVE | Direct host dials fail, lateral container calls fail closed |

---

### Requirements Traceability

| Requirement | Statement | Plans | Verification Status |
|---|---|---|---|
| **GW-05** | Gateway reverse proxy forwards validated requests over verified mutual TLS using Go `Rewrite` hooks | 02-02, 02-04 | ✓ PASS (`tests/proxy/`, `tests/integration/bypass_test.go`) |
| **AUTH-02** | Dedicated mTLS listener (`:9443`) terminates workload connections and extracts authenticated SPIFFE URI SAN identities | 02-01, 02-04 | ✓ PASS (`tests/security/workload_identity_test.go`) |
| **AUTH-03** | Workload mTLS listener rejects user bearer credentials to prevent ambiguous principal selection | 02-01, 02-04 | ✓ PASS (`tests/security/workload_identity_test.go`) |
| **AUTH-05** | Gateway mints and signs short-lived (<=15s) backend assertion JWTs bound to target service, method, path, req ID | 02-02, 02-04 | ✓ PASS (`tests/security/assertion_security_test.go`) |
| **BYP-02** | Backend middleware enforces mTLS and validates gateway identity and signed assertion JWT | 02-03, 02-04 | ✓ PASS (`services/middleware/auth_middleware_test.go`) |
| **BYP-03** | Direct calls from external clients or peer workloads to protected endpoints fail immediately (401/403) | 02-03, 02-04 | ✓ PASS (`tests/integration/bypass_test.go`) |

---

### Verification Summary
All 6 requirements and 4 observable truths are verified with automated tests passing under `-race`. Direct bypass is completely eliminated, workload authentication is strictly enforced on dedicated listener `:9443`, and short-lived signed assertions protect backend microservices.
