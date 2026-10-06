---
phase: 02-workload-identity-enforced-bypass-prevention
plan: 02-02
status: complete
requirements:
  - GW-05
  - AUTH-05
completed_at: 2026-10-06T16:45:00Z
---

# Plan 02-02: Signed Backend Assertion JWTs & Gateway mTLS Transport Summary

**Objective:** Implement short-lived signed backend assertion JWT generator (`aegis-gateway`) with Ed25519 cryptography and configure the gateway reverse proxy upstream transport with mutual TLS connection pooling.

---

### Executed Tasks

| Task ID | Description | Commits | Key Deliverables |
|---|---|---|---|
| **02-02-01** | Signed backend assertion JWT minter & verifier with Ed25519 | `86503e4` | `internal/identity/assertion.go`, `internal/identity/assertion_test.go` |
| **02-02-02** | Reverse proxy upstream mTLS transport with connection pooling and `Rewrite` assertion injection | `2074186` | `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go` |
| **02-02-03** | Gateway binary integration with dual listeners and assertion negative security test suite | `b7fdc67` | `cmd/gateway/main.go`, `tests/security/assertion_security_test.go` |

---

### Deliverables & Verification Details

1. **Short-Lived Signed Assertion Minting & Verification (`internal/identity/assertion.go`)**:
   - Mints short-lived (<= 15 seconds) Ed25519 JWT assertions with issuer `aegis-gateway`.
   - Binds assertion claims to recipient backend service (`aud`), HTTP method (`method`), canonical path template (`path`), request correlation UUID (`req_id`), and principal (`sub`).
   - Verifier checks pinned algorithm `EdDSA`, issuer, audience, and 5-second clock skew tolerance.
   - Verified via unit test: `go test -v -race ./internal/identity/ -run TestAssertionToken`.

2. **Upstream Mutual TLS ReverseProxy (`internal/proxy/proxy.go`)**:
   - `CreateUpstreamTransport` initializes a single, shared, connection-pooled `*http.Transport` with gateway client certificate (`spiffe://aegis.local/ns/gateway/sa/aegis-gateway`), Root CA verification pool, and `InsecureSkipVerify: false`.
   - `NewReverseProxyWithAssertion` utilizes Go 1.20+ `Rewrite` hook to scrub untrusted client `Authorization` and `X-Aegis-*` headers, mint assertion JWTs, and inject `X-Aegis-Assertion`.
   - Verified via unit test: `go test -v -race ./internal/proxy/ -run TestMTLSForwarding`.

3. **Gateway Main & Security Assertion Suite (`cmd/gateway/main.go`, `tests/security/assertion_security_test.go`)**:
   - Wires both `gatewayHandler` (user bearer tokens on port `:8080`) and `workloadHandler` (SPIFFE mTLS on port `:9443`) into `proxy.NewDualServer`.
   - Evaluates in-memory OPA policy with workload principal context, strips incoming credentials, mints assertions, and dispatches via pooled upstream transport.
   - Comprehensive negative security tests cover signature forgery, `alg: none`, HMAC algorithm confusion, audience substitution, method tampering, path tampering, and expired assertion rejection.
   - Verified via: `go build ./cmd/gateway && go test -v -race ./tests/security/ -run TestAssertionSecurity`.

---

### Traceability

- **`GW-05`**: Satisfied by reverse proxy upstream mTLS transport presenting gateway client cert and verifying backends without skip-verify.
- **`AUTH-05`**: Satisfied by short-lived Ed25519-signed `X-Aegis-Assertion` minted for every forwarded upstream request.
