---
phase: 02-workload-identity-enforced-bypass-prevention
plan: 01
subsystem: identity-pki-proxy
tags: [pki, spiffe, mtls, dual-listener, confused-deputy, x509, ecdsa, rfc7807]

requires: [01-01, 01-02, 01-03, 01-04]
provides:
  - Pure Go in-memory PKI generator (<5ms) for Root CA, workload mTLS client certificates, and server certificates
  - Strict SPIFFE URI SAN identity extractor ignoring CN and ingress headers, enforcing trust domains
  - Physical dual-listener architecture separating user traffic (:8080) and workload mTLS traffic (:9443)
  - Workload ingress guard rejecting Authorization: Bearer tokens with HTTP 401 (AUTH-03)
  - Configuration support for workload port and certificate file paths
affects: [02-02-PLAN, 02-03-PLAN, 02-04-PLAN]

tech-stack:
  added: []
  patterns:
    - pure-go-pki-generator
    - spiffe-uri-san-extraction
    - dual-listener-credential-isolation
    - confused-deputy-bearer-rejection

key-files:
  created:
    - internal/pki/pki.go
    - internal/pki/pki_test.go
    - internal/identity/spiffe.go
    - internal/identity/spiffe_test.go
    - internal/proxy/server_test.go
    - tests/security/workload_identity_test.go
  modified:
    - internal/config/config.go
    - internal/config/config_test.go
    - internal/proxy/server.go

key-decisions:
  - "Generate pure Go ECDSA P-256 PKI artifacts in under 5ms to avoid external OpenSSL/cfssl dependencies during local execution and testing"
  - "Extract workload identity exclusively from peer certificate SPIFFE URI SANs (r.TLS.PeerCertificates[0].URIs), strictly ignoring Common Name and headers (Invariant 2)"
  - "Physically separate user ingress (:8080) and workload ingress (:9443 mTLS) to enforce distinct transport security boundaries (ADR-0003)"
  - "Immediately reject inbound Bearer tokens on the workload listener (:9443) with HTTP 401 Unauthorized to eliminate Confused Deputy vulnerabilities (AUTH-03)"

patterns-established:
  - "Pattern 1: Pure Go in-memory PKI generation for ECDSA P-256 Root CAs, server certs, and SPIFFE client certs"
  - "Pattern 2: Strict SPIFFE URI SAN identity parsing and trust domain validation"
  - "Pattern 5: Dual ingress physical listener separation with mutual TLS and Bearer rejection"

requirements-completed:
  - AUTH-02
  - AUTH-03

duration: 18min
completed: 2026-10-06
---

# Phase 02 Plan 01: Local PKI Generation & Dedicated Workload mTLS Listener Summary

**Pure Go development PKI generator, strict SPIFFE URI SAN identity extraction, and physical dual-listener server manager (:8080 user / :9443 workload mTLS) with Confused Deputy mitigation rejecting Bearer tokens on workload ingress.**

## Performance

- **Duration:** 18 min
- **Started:** 2026-10-06T16:12:37Z
- **Completed:** 2026-10-06T16:30:00Z
- **Tasks:** 3
- **Files modified:** 9

## Accomplishments

- Implemented pure Go PKI certificate and key generator in `internal/pki/pki.go` producing ECDSA P-256 Root CAs, workload client certificates with SPIFFE URI SANs (`spiffe://aegis.local/...`), and server certificates with DNS and IP SANs in <10ms without external CLI dependencies.
- Implemented strict SPIFFE URI SAN identity extractor in `internal/identity/spiffe.go` that inspects verified peer certificates (`r.TLS.PeerCertificates[0].URIs`), enforces trust domain matching, rejects malformed paths, and strictly ignores Common Name and untrusted request headers.
- Extended gateway configuration in `internal/config/config.go` with workload listener port (`WorkloadPort`, default 9443), client certificate/key paths, CA certificate paths, and assertion key parameters.
- Implemented `DualServer` in `internal/proxy/server.go` managing both User Ingress (`:8080`) and Workload Ingress (`:9443` mTLS with `tls.RequireAndVerifyClientCert`), wrapping both pipelines with concurrency limits, header limits (16 KiB), body limits (1 MiB), and UUID v4 `X-Request-ID` tracing.
- Enforced AUTH-03 Confused Deputy prevention: requests arriving on `:9443` with an `Authorization: Bearer` header are immediately rejected with HTTP 401 Unauthorized and RFC 7807 problem details (`https://aegis.local/errors/ambiguous-credentials`).
- Created automated security suite in `tests/security/workload_identity_test.go` verifying mTLS handshake rejection without client cert or with rogue CA, successful workload authentication with legitimate cert, Bearer token rejection on workload port, and user listener credential handling.

## Task Commits

Each task was committed atomically:

1. **Task 1: Pure Go PKI Certificate & Key Generator** - `79afb88` (feat)
2. **Task 2: Strict SPIFFE URI SAN Extractor and Trust Domain Validator** - `300dc9f` (feat)
3. **Task 3: Dual-Listener Server Manager and Workload mTLS Ingress Guard (:9443)** - `c6e52e2` (feat)

## Files Created/Modified

- `internal/pki/pki.go` - Pure Go PKI generator for CA, workload certificates, and server certificates
- `internal/pki/pki_test.go` - Unit tests for CA generation, workload cert issuing, server cert issuing, PEM encoding, and in-memory mTLS handshake
- `internal/identity/spiffe.go` - Strict SPIFFE URI SAN identity extractor and trust domain validator
- `internal/identity/spiffe_test.go` - Table-driven unit tests for SPIFFE URI parsing, domain validation, and negative edge cases
- `internal/config/config.go` - Config model with workload port, cert paths, and assertion key path parameters
- `internal/config/config_test.go` - Unit tests for config defaults and environment variable overrides
- `internal/proxy/server.go` - DualServer manager coordinating user (:8080) and workload (:9443 mTLS) listeners with Bearer rejection guard
- `internal/proxy/server_test.go` - DualServer tests for startup, shutdown, ambiguous credentials rejection, and header/body bounding
- `tests/security/workload_identity_test.go` - End-to-end security test suite verifying mTLS enforcement, rogue CA rejection, and Confused Deputy prevention

## Decisions Made

- Pure Go in-memory PKI using `crypto/ecdsa`, `crypto/elliptic.P256()`, and `crypto/x509` executes mutual TLS handshakes in ~7ms, eliminating `openssl` or Docker daemon requirements during unit testing.
- Followed Invariant 2 and ADR-0003 strictly: Common Name is ignored completely; workload identity is sourced exclusively from `cert.URIs`.
- Ingress `Authorization` headers on the workload port (:9443) are rejected immediately prior to downstream dispatch to eliminate principal ambiguity and Confused Deputy attacks (AUTH-03).
- DualServer manages both listeners concurrently with unified graceful shutdown aggregating errors from both servers.

## Verification Results

All automated tests executed with code 0 under race detection:

```bash
go test -v -race ./internal/pki/ -run TestPKI
go test -v -race ./internal/identity/ -run TestSPIFFE
go test -v -race ./internal/config/ -run TestConfig
go test -v -race ./internal/proxy/ -run TestDualServer
go test -v -race ./tests/security/ -run TestWorkloadIdentityListener
go test -v -race -count=1 ./...
```
