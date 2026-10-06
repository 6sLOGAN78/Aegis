# Phase 2: Workload Identity & Enforced Bypass Prevention — Code & Architecture Patterns

**Generated:** 2026-10-06  
**Phase:** 02-workload-identity-enforced-bypass-prevention  
**Status:** Approved Reference  
**Domain:** Mutual TLS (mTLS), SPIFFE X.509 Workload Identity, Dual-Listener Ingress Separation, Cryptographic Backend Assertions, Microservice Bypass Prevention  

---

## 1. Executive Summary & File Inventory

Phase 2 elevates Aegis from an edge reverse proxy into a zero-trust distributed access fabric. Pursuant to [ADR-0003](file:///home/logan78/Desktop/Aegis/docs/adr/0003-dual-listener-identity-separation.md), Phase 2 implements:
1. **Dual Physical Listeners on Ingress**: Separates human user traffic (`:8080` HTTP / `:8443` TLS) from machine workload traffic (`:9443` mTLS). The workload listener mandates `tls.RequireAndVerifyClientCert` against the internal Aegis Workload Root CA and strictly extracts identities from SPIFFE URI SANs (`spiffe://aegis.local/workload/{service}`).
2. **Confused Deputy Prevention**: The workload listener immediately rejects inbound `Authorization: Bearer` user tokens (`HTTP 401 Unauthorized` / `AMBIGUOUS_CREDENTIALS`), preventing principal precedence ambiguity.
3. **Cryptographic Backend Assertions**: Upon OPA policy authorization, the gateway mints a short-lived (<=15s) Ed25519-signed assertion JWT (`X-Aegis-Assertion`) cryptographically bound to the target backend service, method, canonical path, and request ID.
4. **Upstream Mutual TLS**: The gateway reverse proxy forwards requests to private backends over mTLS presenting the gateway client certificate (`spiffe://aegis.local/ns/gateway/sa/aegis-gateway`) with `InsecureSkipVerify: false`.
5. **Airtight Backend Defense-in-Depth**: Private backend microservices (`orders`, `payments`, `admin`) run reusable authentication middleware validating both transport identity (Gateway SPIFFE certificate) and application assertion (`X-Aegis-Assertion`). Direct internal or external calls fail with TLS handshake failure, HTTP 401, or HTTP 403.

This document catalogues every file to be created or modified in Phase 2, maps each file to existing Phase 0/1 analogs, classifies data flows, and extracts concrete, verified code patterns.

### Target File Inventory

```
aegis/
├── cmd/
│   ├── gateway/
│   │   └── main.go                               # [MODIFIED] Dual listener bootstrap, assertion minter, mTLS transport
│   └── services/
│       ├── orders/
│       │   └── main.go                           # [MODIFIED] Orders service (:8081) with HTTPS mTLS & auth middleware
│       ├── payments/
│       │   └── main.go                           # [MODIFIED] Payments service (:8082) with HTTPS mTLS & auth middleware
│       └── admin/
│           └── main.go                           # [MODIFIED] Admin service (:8083) with HTTPS mTLS & auth middleware
├── internal/
│   ├── config/
│   │   └── config.go                             # [MODIFIED] Workload port (:9443), cert paths, assertion key config
│   ├── identity/
│   │   ├── spiffe.go                             # [NEW] SPIFFE URI SAN parser and trust domain validator (AUTH-02)
│   │   ├── spiffe_test.go                        # [NEW] Unit tests for SPIFFE URI extraction and domain enforcement
│   │   ├── assertion.go                          # [NEW] Signed assertion JWT minter & verifier (Ed25519) (AUTH-05)
│   │   └── assertion_test.go                     # [NEW] Unit tests for assertion signing, expiry, leeway, and claims
│   ├── pki/
│   │   ├── pki.go                                # [NEW] Pure Go in-memory PKI generator (CA, workload & server certs)
│   │   └── pki_test.go                           # [NEW] Unit tests for fast in-memory cert generation (<5ms)
│   └── proxy/
│       ├── server.go                             # [MODIFIED] Dual-listener server manager (ports 8080 and 9443)
│       ├── proxy.go                              # [MODIFIED] ReverseProxy with upstream mTLS transport & assertion injection
│       └── proxy_test.go                         # [MODIFIED] Unit tests for upstream mTLS transport & header purging
├── services/
│   └── middleware/
│       ├── auth_middleware.go                    # [NEW] Reusable microservice mTLS + assertion verification middleware (BYP-02)
│       └── auth_middleware_test.go               # [NEW] Unit tests for direct bypass rejection and assertion claims
├── scripts/
│   └── certificates/
│       └── main.go                               # [NEW] Dev CLI utility generating local certs/keys to deployments/certs/
├── deployments/
│   └── compose/
│       ├── docker-compose.mvp.yml                # [MODIFIED] Gateway ports 8080 & 9443; backends with 0 host ports
│       └── Dockerfile                            # [MODIFIED] Multi-stage build support for cert generation and services
└── tests/
    ├── security/
    │   ├── workload_identity_test.go             # [NEW] Workload mTLS, dual-listener isolation, OPA rule 5 RBAC tests
    │   └── assertion_security_test.go            # [NEW] Assertion signature forgery, audience mismatch, method/path tests
    └── integration/
        └── bypass_test.go                        # [MODIFIED] Container cluster tests: peer container bypass rejection (BYP-03)
```

---

## 2. Classification by Role & Data Flow

| File Path | Architectural Role | Ingress / Input | Processing / Transformation | Egress / Output | Invariants & Requirements |
|---|---|---|---|---|---|
| [`internal/pki/pki.go`](file:///home/logan78/Desktop/Aegis/internal/pki/pki.go) | PKI Cryptography Provider | Trust domain string, common names, DNS/IP SANs | Generates ECDSA P-256 / Ed25519 key pairs, constructs X.509 templates with SPIFFE URI SANs (`template.URIs`), signs certs via Root CA | `*pki.CA`, `tls.Certificate`, `*x509.CertPool` | AUTH-02, Invariant 2 |
| [`internal/pki/pki_test.go`](file:///home/logan78/Desktop/Aegis/internal/pki/pki_test.go) | PKI Unit Test Suite | Memory buffers | Generates CA, workload certs, server certs; performs synthetic TLS handshake | Test assertions (<5ms execution) | AUTH-02 |
| [`internal/identity/spiffe.go`](file:///home/logan78/Desktop/Aegis/internal/identity/spiffe.go) | SPIFFE Identity Extractor | `*x509.Certificate` from `r.TLS.PeerCertificates[0]` | Inspects `cert.URIs`, validates scheme (`spiffe`), verifies trust domain (`aegis.local`), extracts workload ID | SPIFFE ID string or typed error (`ErrInvalidTrustDomain`, etc.) | AUTH-02, Invariant 2 |
| [`internal/identity/spiffe_test.go`](file:///home/logan78/Desktop/Aegis/internal/identity/spiffe_test.go) | SPIFFE Unit Test Suite | Synthetic x509 cert fixtures | Tests valid SPIFFE URIs, untrusted trust domains, missing URIs, invalid schemes | Test assertions | AUTH-02 |
| [`internal/identity/assertion.go`](file:///home/logan78/Desktop/Aegis/internal/identity/assertion.go) | Cryptographic Assertion Engine | Principal ID, roles, target service, method, path, req ID, Ed25519 keys | **Minter:** Signs JWT (`iss: aegis-gateway`, `exp: <=15s`, `EdDSA`).<br>**Verifier:** Checks signature, audience, method, path, 5s clock leeway | Signed JWT string or parsed `*AssertionClaims` | AUTH-05, Invariant 5 |
| [`internal/identity/assertion_test.go`](file:///home/logan78/Desktop/Aegis/internal/identity/assertion_test.go) | Assertion Unit Test Suite | Synthetic claims, valid & invalid tokens | Tests minting, signature verification, expiry, clock skew leeway, audience mismatch | Test assertions | AUTH-05 |
| [`services/middleware/auth_middleware.go`](file:///home/logan78/Desktop/Aegis/services/middleware/auth_middleware.go) | Backend Defense-in-Depth Middleware | Inbound backend `*http.Request` (`r.TLS` + `X-Aegis-Assertion`) | **Layer 1:** Verifies peer cert SPIFFE URI matches Gateway SPIFFE.<br>**Layer 2:** Verifies assertion signature, audience, method, path | Request forwarded with claims context or HTTP 401/403 rejection | BYP-02, Invariant 5, Invariant 12 |
| [`services/middleware/auth_middleware_test.go`](file:///home/logan78/Desktop/Aegis/services/middleware/auth_middleware_test.go) | Backend Middleware Unit Tests | Synthetic requests via `httptest.NewServer` | Tests plain HTTP rejection (401), untrusted cert rejection (403), missing assertion rejection (401), valid assertion pass-through | Test assertions | BYP-02 |
| [`internal/config/config.go`](file:///home/logan78/Desktop/Aegis/internal/config/config.go) | Configuration Model & Loader | Environment variables (`AEGIS_WORKLOAD_PORT`, `AEGIS_TLS_*`, etc.) | Adds `WorkloadPort` (default 9443), certificate paths, CA cert paths, assertion private/public key configuration | Loaded `*Config` struct | GW-01, AUTH-02 |
| [`internal/proxy/server.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/server.go) | Dual-Listener Server Manager | User listener (`:8080`) & Workload listener (`:9443`) | Configures `tls.RequireAndVerifyClientCert` on `:9443`; filters incoming `Authorization: Bearer` on `:9443` (HTTP 401) | Managed dual `http.Server` instances with synchronized shutdown | AUTH-02, AUTH-03, ADR-0003 |
| [`internal/proxy/proxy.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/proxy.go) | Upstream mTLS Reverse Proxy | Inbound request, target URL, canonical path, signed assertion JWT | In `Rewrite`: configures mTLS transport, strips inbound `Authorization` and client `X-Aegis-*`, injects `X-Aegis-Assertion` | Forwarded HTTPS request over mTLS to backend | GW-05, AUTH-05, Invariant 4 |
| [`internal/proxy/proxy_test.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/proxy_test.go) | Upstream Proxy Unit Tests | Mock mTLS backend server | Asserts upstream mTLS handshake, assertion header injection, header scrubbing | Test assertions | GW-05, AUTH-05 |
| [`cmd/gateway/main.go`](file:///home/logan78/Desktop/Aegis/cmd/gateway/main.go) | Gateway Lifecycle & Orchestration | Environment, routes JSON, Rego policy, dual ports (`:8080`, `:9443`) | Dispatches user requests on 8080 (JWT auth) and workload requests on 9443 (SPIFFE auth), evaluates OPA, mints assertions, proxies upstream | Running Gateway service with dual listeners | GW-05, AUTH-02, AUTH-03, AUTH-05 |
| [`cmd/services/orders/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/orders/main.go) | Private Orders Microservice | Port 8081 HTTPS mTLS, `X-Aegis-Assertion` | Applies `BackendAuthMiddleware` with audience `"orders"`, serves `/api/orders` and unauthenticated `/health` | HTTP 200 JSON orders payload or 401/403 | BYP-01, BYP-02 |
| [`cmd/services/payments/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/payments/main.go) | Private Payments Microservice | Port 8082 HTTPS mTLS, `X-Aegis-Assertion` | Applies `BackendAuthMiddleware` with audience `"payments"`, serves GET/POST `/api/payments` and `/health` | HTTP 200 JSON payments payload or 401/403 | BYP-01, BYP-02 |
| [`cmd/services/admin/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/admin/main.go) | Private Admin Microservice | Port 8083 HTTPS mTLS, `X-Aegis-Assertion` | Applies `BackendAuthMiddleware` with audience `"admin"`, serves GET/POST `/api/admin/users` and `/health` | HTTP 200 JSON admin payload or 401/403 | BYP-01, BYP-02 |
| [`scripts/certificates/main.go`](file:///home/logan78/Desktop/Aegis/scripts/certificates/main.go) | Certificate Generation CLI | Command line invocation | Uses `internal/pki` to generate Root CA, gateway client cert, workload certs, backend server certs, and assertion keys to disk | PEM files in `deployments/certs/` | PKI Setup |
| [`deployments/compose/docker-compose.mvp.yml`](file:///home/logan78/Desktop/Aegis/deployments/compose/docker-compose.mvp.yml) | Multi-Container Cluster Spec | Docker Engine execution | Publishes gateway ports 8080 and 9443; backends have 0 published ports; mounts cert volumes | Isolated bridge network cluster | BYP-01, BYP-03 |
| [`tests/security/workload_identity_test.go`](file:///home/logan78/Desktop/Aegis/tests/security/workload_identity_test.go) | Workload Security Suite | Live test servers with client certs | Tests mTLS enforcement, SPIFFE URI extraction, Bearer token rejection on 9443, OPA rule 5 RBAC | Test assertions | AUTH-02, AUTH-03, POL-03 |
| [`tests/security/assertion_security_test.go`](file:///home/logan78/Desktop/Aegis/tests/security/assertion_security_test.go) | Assertion Security Suite | Synthetic and modified assertion tokens | Tests signature forgery, audience replay attack, method tampering, path tampering, expired assertions | Test assertions | AUTH-05, BYP-02 |
| [`tests/integration/bypass_test.go`](file:///home/logan78/Desktop/Aegis/tests/integration/bypass_test.go) | End-to-End Bypass Suite | Docker Compose running cluster | Tests peer container direct lateral calls (orders calling payments), direct dials without cert/assertion, mediated workload calls | Test assertions | BYP-02, BYP-03 |

---

## 3. Existing Analogs & Phase 0/1 Contract Mappings

Phase 2 builds directly upon the architectural conventions established in Phase 0 and Phase 1:

### 3.1. Identity Extraction & Cryptographic Validation
- **Existing Analog**: [`internal/identity/jwt.go`](file:///home/logan78/Desktop/Aegis/internal/identity/jwt.go#L30-L96) (`TokenValidator`).
  - `TokenValidator` validates ingress user Bearer JWTs against pinned algorithm allowlists (`EdDSA`, `RS256`, `ES256`), issuer, audience, and clock skew leeway.
- **Phase 2 Mapping**:
  - [`internal/identity/spiffe.go`](file:///home/logan78/Desktop/Aegis/internal/identity/spiffe.go): Symmetrical authenticator for machine workloads. Extracts identity from `*x509.Certificate` rather than `Authorization` headers. Validates scheme (`spiffe://`), trust domain (`aegis.local`), and path format (`/workload/{service}`).
  - [`internal/identity/assertion.go`](file:///home/logan78/Desktop/Aegis/internal/identity/assertion.go): Inversion of the validator. The gateway becomes the *issuer* (`AssertionMinter`) minting short-lived assertions with Ed25519 (`jwt.SigningMethodEdDSA`), and backends become the *verifier* (`AssertionVerifier`).

### 3.2. Reverse Proxy & Upstream Forwarding
- **Existing Analog**: [`internal/proxy/proxy.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/proxy.go#L10-L64) (`NewReverseProxy`).
  - In Phase 1, `NewReverseProxy` used the Go 1.20+ `Rewrite` hook to scrub client headers (`X-Aegis-*`, `Forwarded`), strip `Authorization`, and forward canonical path bytes over plain HTTP.
- **Phase 2 Mapping**:
  - `internal/proxy/proxy.go` retains the exact same header sanitization rules, but attaches a shared `*http.Transport` configured with `TLSClientConfig` presenting the Gateway SPIFFE client certificate (`spiffe://aegis.local/ns/gateway/sa/aegis-gateway`) and verifying backend certificates against the internal Root CA (`InsecureSkipVerify: false`).
  - Injects `pr.Out.Header.Set("X-Aegis-Assertion", signedAssertionJWT)`.

### 3.3. Ingress Server & Pipeline Wrapping
- **Existing Analog**: [`internal/proxy/server.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/server.go#L13-L100) (`Server`).
  - Wraps `http.Server` with concurrency limits, header limits (16 KiB), body limits (1 MiB), and UUID v4 `X-Request-ID` injection.
- **Phase 2 Mapping**:
  - `internal/proxy/server.go` evolves to manage dual listeners: User Ingress (`:8080`) and Workload Ingress (`:9443`).
  - Port `:9443` introduces mandatory TLS client verification (`tls.RequireAndVerifyClientCert`) and an ingress guard immediately rejecting requests containing `Authorization: Bearer` with `HTTP 401 Unauthorized` (`AMBIGUOUS_CREDENTIALS`).

### 3.4. Backend Microservice Architecture
- **Existing Analogs**: [`cmd/services/orders/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/orders/main.go), [`payments/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/payments/main.go), [`admin/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/admin/main.go).
  - Minimal standard library HTTP handlers responding with sample JSON fixtures, with an unauthenticated `/health` endpoint.
- **Phase 2 Mapping**:
  - Microservices migrate from `http.ListenAndServe` to `http.Server.ListenAndServeTLS`.
  - Protected business routes (`/api/*`) are wrapped with [`services/middleware.BackendAuthMiddleware`](file:///home/logan78/Desktop/Aegis/services/middleware/auth_middleware.go).
  - Health probe (`/health`) remains unauthenticated for container orchestration liveness checks.

### 3.5. OPA Policy Evaluation for Workloads
- **Existing Analog**: [`policies/rego/authz.rego`](file:///home/logan78/Desktop/Aegis/policies/rego/authz.rego#L103-L140) (Rule 5 & Workload Denials).
  - Rule 5 authorizes `principal.kind == "workload"` with `principal.id == "spiffe://aegis.local/workload/orders"` to invoke `POST /api/payments`.
  - Explicit denial `DENIED_WORKLOAD_ADMIN_FORBIDDEN` rejects workload calls to `admin`.
- **Phase 2 Mapping**:
  - [`cmd/gateway/main.go`](file:///home/logan78/Desktop/Aegis/cmd/gateway/main.go) passes workload identity attributes (`Kind: "workload"`, `ID: spiffeID`) into `policy.PolicyInput` to activate these rules.

---

## 4. Concrete Implementation Patterns & Code Excerpts

### Pattern 1: Pure Go PKI Certificate & Key Generator (`internal/pki/pki.go`)

**Role**: Development & Testing PKI Provider  
**Location**: `internal/pki/pki.go`  
**Rationale**: Eliminates external dependencies (`openssl`, `cfssl`) for CI and local test execution. Generates self-signed Root CA, workload client certificates with SPIFFE URI SANs (`template.URIs`), and backend server certificates in <5ms.

```go
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"time"
)

// CA represents a Certificate Authority containing root certificate, private key, and pool.
type CA struct {
	Certificate *x509.Certificate
	PrivateKey  *ecdsa.PrivateKey
	CertPool    *x509.CertPool
}

// NewCA creates a self-signed Root Certificate Authority.
func NewCA(commonName string) (*CA, error) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Aegis Zero-Trust PKI"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	return &CA{Certificate: cert, PrivateKey: privKey, CertPool: pool}, nil
}

// IssueWorkloadCert issues an X.509 client certificate with a SPIFFE URI SAN.
func (ca *CA) IssueWorkloadCert(spiffeURIStr string) (tls.Certificate, error) {
	spiffeURI, err := url.Parse(spiffeURIStr)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("invalid SPIFFE URI %q: %w", spiffeURIStr, err)
	}

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   spiffeURI.Path,
			Organization: []string{"Aegis Workload"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:                  []*url.URL{spiffeURI},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, ca.Certificate, &privKey.PublicKey, ca.PrivateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to issue workload cert: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  privKey,
	}, nil
}

// IssueServerCert issues a server TLS certificate with DNS and IP SANs.
func (ca *CA) IssueServerCert(commonName string, dnsNames []string, ipAddresses []net.IP) (tls.Certificate, error) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Aegis Service"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, ca.Certificate, &privKey.PublicKey, ca.PrivateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to issue server cert: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  privKey,
	}, nil
}
```

---

### Pattern 2: Strict SPIFFE URI SAN Identity Extraction (`internal/identity/spiffe.go`)

**Role**: Machine Workload Identity Extractor  
**Location**: `internal/identity/spiffe.go`  
**Rationale**: Invariant 2 mandates extracting workload identity exclusively from verified peer certificates. Common Name (CN) is deprecated and ignored. Ingress headers like `X-Client-Cert-SAN` are prohibited.

```go
package identity

import (
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNoPeerCertificate  = errors.New("no peer certificate presented")
	ErrMissingSPIFFEID    = errors.New("certificate does not contain a SPIFFE URI SAN")
	ErrInvalidTrustDomain = errors.New("certificate SPIFFE trust domain mismatch")
	ErrMalformedSPIFFEURI = errors.New("malformed SPIFFE URI structure")
)

// ExtractSPIFFEID extracts and validates the SPIFFE ID from an authenticated peer certificate.
// It verifies the URI scheme is "spiffe" and host matches the expected trust domain.
func ExtractSPIFFEID(peerCert *x509.Certificate, expectedTrustDomain string) (string, error) {
	if peerCert == nil {
		return "", ErrNoPeerCertificate
	}

	for _, uri := range peerCert.URIs {
		if uri == nil {
			continue
		}
		if uri.Scheme == "spiffe" {
			if expectedTrustDomain != "" && !strings.EqualFold(uri.Host, expectedTrustDomain) {
				return "", fmt.Errorf("%w: got %q, expected %q", ErrInvalidTrustDomain, uri.Host, expectedTrustDomain)
			}
			if uri.Path == "" || uri.Path == "/" {
				return "", ErrMalformedSPIFFEURI
			}
			return uri.String(), nil
		}
	}

	return "", ErrMissingSPIFFEID
}
```

---

### Pattern 3: Short-Lived Audience-Bound Signed Assertion JWT (`internal/identity/assertion.go`)

**Role**: Gateway Assertion Minter & Backend Assertion Verifier  
**Location**: `internal/identity/assertion.go`  
**Rationale**: AUTH-05 & ADR-0003 require gateway-signed assertions bound to recipient microservice, method, canonical path, and request ID with <=15-second bounded lifetime. Signed with Ed25519 (`EdDSA`) for constant-time, microsecond execution.

```go
package identity

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	AssertionIssuer  = "aegis-gateway"
	AssertionLifetime = 15 * time.Second
	ClockSkewLeeway   = 5 * time.Second
)

// AssertionClaims defines claims embedded in gateway-to-backend assertions.
type AssertionClaims struct {
	jwt.RegisteredClaims
	Method        string   `json:"method"`
	Path          string   `json:"path"`
	RequestID     string   `json:"req_id"`
	PrincipalKind string   `json:"principal_kind"`
	Roles         []string `json:"roles,omitempty"`
	SnapshotVer   int64    `json:"snapshot_ver,omitempty"`
}

// AssertionMinter mints short-lived signed assertion JWTs at the gateway edge.
type AssertionMinter struct {
	privateKey ed25519.PrivateKey
	issuer     string
}

// NewAssertionMinter initializes a minter with the gateway's private Ed25519 key.
func NewAssertionMinter(privateKey ed25519.PrivateKey) *AssertionMinter {
	return &AssertionMinter{
		privateKey: privateKey,
		issuer:     AssertionIssuer,
	}
}

// MintAssertion generates an Ed25519-signed assertion valid for 15 seconds.
func (m *AssertionMinter) MintAssertion(
	principalID string,
	principalKind string,
	roles []string,
	targetService string,
	method string,
	canonicalPath string,
	requestID string,
	snapshotVer int64,
) (string, error) {
	now := time.Now()
	claims := AssertionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   principalID,
			Audience:  jwt.ClaimStrings{targetService},
			ExpiresAt: jwt.NewNumericDate(now.Add(AssertionLifetime)),
			NotBefore: jwt.NewNumericDate(now.Add(-ClockSkewLeeway)), // Clock skew allowance
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
		Method:        method,
		Path:          canonicalPath,
		RequestID:     requestID,
		PrincipalKind: principalKind,
		Roles:         roles,
		SnapshotVer:   snapshotVer,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(m.privateKey)
}

// AssertionVerifier validates assertions on private backend microservices.
type AssertionVerifier struct {
	publicKey      ed25519.PublicKey
	expectedIssuer string
	serviceID      string
}

// NewAssertionVerifier creates a verifier pinned to the backend service ID.
func NewAssertionVerifier(publicKey ed25519.PublicKey, serviceID string) *AssertionVerifier {
	return &AssertionVerifier{
		publicKey:      publicKey,
		expectedIssuer: AssertionIssuer,
		serviceID:      serviceID,
	}
}

// VerifyAssertion validates signature, audience, method, and canonical path.
func (v *AssertionVerifier) VerifyAssertion(tokenStr, expectedMethod, expectedPath string) (*AssertionClaims, error) {
	if tokenStr == "" {
		return nil, errors.New("missing assertion token")
	}

	claims := &AssertionClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != "EdDSA" {
			return nil, fmt.Errorf("unexpected algorithm: %s", token.Method.Alg())
		}
		return v.publicKey, nil
	},
		jwt.WithIssuer(v.expectedIssuer),
		jwt.WithAudience(v.serviceID),
		jwt.WithLeeway(ClockSkewLeeway),
	)

	if err != nil {
		return nil, fmt.Errorf("assertion validation failed: %w", err)
	}

	if !token.Valid {
		return nil, errors.New("invalid assertion token")
	}

	if claims.Method != expectedMethod {
		return nil, fmt.Errorf("assertion method mismatch: expected %q, got %q", expectedMethod, claims.Method)
	}

	if claims.Path != expectedPath {
		return nil, fmt.Errorf("assertion path mismatch: expected %q, got %q", expectedPath, claims.Path)
	}

	return claims, nil
}
```

---

### Pattern 4: Reusable Backend Defense-in-Depth Middleware (`services/middleware/auth_middleware.go`)

**Role**: Microservice Security Middleware  
**Location**: `services/middleware/auth_middleware.go`  
**Rationale**: BYP-02 mandates two independent security layers before handling backend requests:
1. Transport layer: Connecting client certificate SPIFFE ID matches Gateway SPIFFE ID (`spiffe://aegis.local/ns/gateway/sa/aegis-gateway`). Peer microservices connecting directly fail here.
2. Application layer: `X-Aegis-Assertion` signature, audience, method, and canonical path are verified.

```go
package middleware

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"

	"aegis/internal/identity"
)

type contextKey string

const AssertionClaimsKey contextKey = "aegis.assertion_claims"

// BackendAuthMiddleware wraps microservice routes with dual-layer mTLS and assertion verification.
func BackendAuthMiddleware(
	authorizedGatewaySPIFFE string,
	expectedServiceID string,
	gatewayAssertionPubKey ed25519.PublicKey,
	exemptPaths ...string,
) func(http.Handler) http.Handler {
	verifier := identity.NewAssertionVerifier(gatewayAssertionPubKey, expectedServiceID)
	exemptMap := make(map[string]bool)
	for _, p := range exemptPaths {
		exemptMap[p] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt unauthenticated routes (e.g. /health for container liveness)
			if exemptMap[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Layer 1: Verify mTLS peer certificate presented and issued to Aegis Gateway
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				writeError(w, http.StatusUnauthorized, "mTLS client certificate required")
				return
			}

			peerCert := r.TLS.PeerCertificates[0]
			spiffeID, err := identity.ExtractSPIFFEID(peerCert, "aegis.local")
			if err != nil || spiffeID != authorizedGatewaySPIFFE {
				writeError(w, http.StatusForbidden, "unauthorized client identity: peer is not the Aegis Gateway")
				return
			}

			// Layer 2: Verify X-Aegis-Assertion header
			assertionHeader := r.Header.Get("X-Aegis-Assertion")
			if assertionHeader == "" {
				writeError(w, http.StatusUnauthorized, "missing required X-Aegis-Assertion header")
				return
			}

			claims, err := verifier.VerifyAssertion(assertionHeader, r.Method, r.URL.Path)
			if err != nil {
				writeError(w, http.StatusForbidden, "invalid assertion: "+err.Error())
				return
			}

			// Inject verified assertion claims into request context and proceed
			ctx := context.WithValue(r.Context(), AssertionClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext extracts verified AssertionClaims from request context.
func FromContext(ctx context.Context) (*identity.AssertionClaims, bool) {
	claims, ok := ctx.Value(AssertionClaimsKey).(*identity.AssertionClaims)
	return claims, ok
}

func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"type":   "https://aegis.local/errors/unauthorized",
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}
```

---

### Pattern 5: Dual Ingress Listener Isolation (`internal/proxy/server.go`)

**Role**: Transport Ingress Controller  
**Location**: `internal/proxy/server.go`  
**Rationale**: AUTH-02, AUTH-03, and ADR-0003 require physically separate listener ports. Workload listener binds to `:9443` with `tls.RequireAndVerifyClientCert`. Crucially, requests presenting `Authorization: Bearer` on `:9443` are rejected immediately before application processing.

```go
package proxy

import (
	"crypto/tls"
	"fmt"
	"net/http"

	"aegis/internal/config"
	"github.com/google/uuid"
)

// DualServer manages both User (:8080) and Workload (:9443) ingress HTTP servers.
type DualServer struct {
	userServer     *http.Server
	workloadServer *http.Server
	cfg            *config.Config
	limiter        *ConcurrencyLimiter
}

// NewDualServer constructs the dual-listener setup with physical credential isolation.
func NewDualServer(
	cfg *config.Config,
	userHandler http.Handler,
	workloadHandler http.Handler,
	workloadTLSConfig *tls.Config,
) *DualServer {
	limiter := NewConcurrencyLimiter(cfg.MaxConcurrentRequests)

	// Pipeline wrapper for User Listener (:8080)
	userIngress := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.NewString()
		r.Header.Set("X-Request-ID", reqID)
		w.Header().Set("X-Request-ID", reqID)

		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		}
		userHandler.ServeHTTP(w, r)
	})

	// Pipeline wrapper for Workload Listener (:9443 mTLS)
	workloadIngress := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.NewString()
		r.Header.Set("X-Request-ID", reqID)
		w.Header().Set("X-Request-ID", reqID)

		// AUTH-03: Strictly reject inbound Bearer tokens on workload port
		if r.Header.Get("Authorization") != "" {
			WriteProblemDetails(
				w,
				http.StatusUnauthorized,
				"Unauthorized",
				"Ambiguous credentials: Bearer tokens are prohibited on the workload listener",
				"https://aegis.local/errors/ambiguous-credentials",
				reqID,
			)
			return
		}

		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		}
		workloadHandler.ServeHTTP(w, r)
	})

	userSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           limiter.Wrap(userIngress),
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	workloadSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.WorkloadPort),
		Handler:           limiter.Wrap(workloadIngress),
		TLSConfig:         workloadTLSConfig, // Must have ClientAuth: tls.RequireAndVerifyClientCert
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	return &DualServer{
		userServer:     userSrv,
		workloadServer: workloadSrv,
		cfg:            cfg,
		limiter:        limiter,
	}
}
```

---

### Pattern 6: Gateway Upstream Mutual TLS ReverseProxy (`internal/proxy/proxy.go`)

**Role**: Upstream Forwarder with Client mTLS & Assertion Injection  
**Location**: `internal/proxy/proxy.go`  
**Rationale**: GW-05 & Invariant 4 require requests forwarded to backends over mutual TLS with verified server certificates (`InsecureSkipVerify: false`). Inbound `Authorization` is stripped; signed `X-Aegis-Assertion` is injected.

```go
package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// CreateUpstreamTransport instantiates a shared, connection-pooled mTLS http.Transport.
// Reusing a single transport avoids ephemeral port exhaustion under high load (Pitfall 4).
func CreateUpstreamTransport(clientCert tls.Certificate, caPool *x509.CertPool) *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			RootCAs:      caPool,
			MinVersion:   tls.VersionTLS13,
			InsecureSkipVerify: false, // Invariant 4: Never disable verification
		},
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
}

// NewReverseProxyWithMTLS creates a reverse proxy configured with mTLS transport and assertion injection.
func NewReverseProxyWithMTLS(
	targetURL *url.URL,
	canonicalPath string,
	requestID string,
	assertionJWT string,
	transport *http.Transport,
) *httputil.ReverseProxy {
	rp := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(targetURL)
			pr.Out.URL.Path = canonicalPath
			pr.Out.URL.RawPath = ""

			// Scrub all client-supplied X-Aegis-* headers
			for k := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(k), "x-aegis-") {
					pr.Out.Header.Del(k)
				}
			}

			// Scrub forwarding headers
			pr.Out.Header.Del("Forwarded")
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")

			// Scrub inbound Authorization token
			pr.Out.Header.Del("Authorization")

			// Inject correlation tracking ID
			if requestID != "" {
				pr.Out.Header.Set("X-Request-ID", requestID)
			}

			// Inject signed gateway assertion JWT (AUTH-05)
			if assertionJWT != "" {
				pr.Out.Header.Set("X-Aegis-Assertion", assertionJWT)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			WriteProblemDetails(
				w,
				http.StatusBadGateway,
				"Bad Gateway",
				"Upstream backend unreachable or TLS handshake failed",
				"https://aegis.local/errors/bad-gateway",
				requestID,
			)
		},
	}
	return rp
}
```

---

### Pattern 7: Private Backend Microservice mTLS Server Integration (`cmd/services/orders/main.go`)

**Role**: Private Microservice Ingress  
**Location**: `cmd/services/{orders,payments,admin}/main.go`  
**Rationale**: BYP-01 & BYP-02 mandate backend microservices run over HTTPS with verified client certificates, wrapped with `BackendAuthMiddleware`.

```go
package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"aegis/services/middleware"
)

const authorizedGatewaySPIFFE = "spiffe://aegis.local/ns/gateway/sa/aegis-gateway"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	// Load Server TLS Certificate & CA Pool
	serverCert, err := tls.LoadX509KeyPair(os.Getenv("TLS_CERT_PATH"), os.Getenv("TLS_KEY_PATH"))
	if err != nil {
		log.Fatalf("Failed to load server TLS certificate: %v", err)
	}

	caCertBytes, err := os.ReadFile(os.Getenv("CA_CERT_PATH"))
	if err != nil {
		log.Fatalf("Failed to read CA certificate: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCertBytes)

	// Load Gateway Assertion Public Key (Ed25519)
	gatewayAssertionPubKey := loadGatewayPublicKey()

	// Build Protected Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/api/orders", HandleOrders)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Wrap with 2-layer defense-in-depth middleware (exempting /health)
	authMW := middleware.BackendAuthMiddleware(
		authorizedGatewaySPIFFE,
		"orders",
		gatewayAssertionPubKey,
		"/health",
	)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: authMW(mux),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientCAs:    caPool,
			ClientAuth:   tls.RequireAndVerifyClientCert, // Layer 1: Mandatory mTLS
			MinVersion:   tls.VersionTLS13,
		},
	}

	log.Printf("Orders microservice listening on :%s (HTTPS mTLS)", port)
	if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Orders service failed: %v", err)
	}
}
```

---

### Pattern 8: Workload Ingress Pipeline & OPA Evaluation (`cmd/gateway/main.go`)

**Role**: Gateway Pipeline Orchestration  
**Location**: `cmd/gateway/main.go`  
**Rationale**: Workload requests arriving on port `:9443` undergo zero-repair path validation, SPIFFE URI SAN extraction, route resolution, OPA policy evaluation (`kind: "workload"`), assertion minting, and mTLS proxy forwarding.

```go
// Workload Ingress Request Pipeline on Port :9443
workloadHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	ac := audit.FromContext(r.Context())

	// 1. Strict Zero-Repair Path Validation (GW-02)
	canonicalPath, err := proxy.ValidatePathZeroRepair(r)
	if err != nil {
		proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
			"https://aegis.local/errors/invalid-path", reqID)
		return
	}

	// 2. SPIFFE Identity Extraction from TLS Peer Certificate (AUTH-02)
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", "Client certificate required",
			"https://aegis.local/errors/unauthorized", reqID)
		return
	}
	spiffeID, err := identity.ExtractSPIFFEID(r.TLS.PeerCertificates[0], "aegis.local")
	if err != nil {
		proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
			"https://aegis.local/errors/invalid-workload-identity", reqID)
		return
	}

	if ac != nil {
		ac.SetPrincipal(spiffeID, "workload", []string{"workload"})
		ac.SetCanonicalPath(canonicalPath)
	}

	// 3. Deterministic Route Resolution (GW-03)
	route, err := router.Match(r.Method, canonicalPath)
	if err != nil {
		proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching route found",
			"https://aegis.local/errors/not-found", reqID)
		return
	}

	// 4. In-Memory OPA Policy Evaluation (POL-01, Rule 5 in authz.rego)
	input := policy.PolicyInput{
		Principal: policy.PrincipalInput{
			ID:    spiffeID,
			Kind:  "workload",
			Roles: []string{"workload"},
		},
		Resource: policy.ResourceInput{
			Service: route.ServiceID,
			Route:   route.RouteID,
		},
		Request: policy.RequestInput{
			Method: r.Method,
			Path:   canonicalPath,
		},
		Context: policy.ContextInput{RiskScore: 0, RiskState: "available"},
		SnapshotVersion: snapshotVersion,
	}

	decision, err := policyEngine.Evaluate(r.Context(), input)
	if err != nil || !decision.Allow {
		reason := "DENIED_WORKLOAD_FORBIDDEN"
		if decision.ReasonCode != "" {
			reason = decision.ReasonCode
		}
		proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", reason,
			"https://aegis.local/errors/forbidden", reqID)
		return
	}

	// 5. Mint Backend Assertion JWT (AUTH-05)
	assertionJWT, err := assertionMinter.MintAssertion(
		spiffeID,
		"workload",
		[]string{"workload"},
		route.ServiceID,
		r.Method,
		canonicalPath,
		reqID,
		snapshotVersion,
	)
	if err != nil {
		proxy.WriteProblemDetails(w, http.StatusInternalServerError, "Internal Server Error", "Failed to mint assertion",
			"https://aegis.local/errors/internal-error", reqID)
		return
	}

	// 6. Forward over Mutual TLS to Backend (GW-05)
	rp := proxy.NewReverseProxyWithMTLS(route.ParsedURL(), canonicalPath, reqID, assertionJWT, upstreamTransport)
	rp.ServeHTTP(w, r)
})
```

---

### Pattern 9: Automated Security & Bypass Test Harness (`tests/security/`, `tests/integration/bypass_test.go`)

**Role**: Security Verification  
**Location**: `tests/security/workload_identity_test.go` and `tests/integration/bypass_test.go`  
**Rationale**: Table-driven automated verification testing:
1. Workload mTLS and dual-listener isolation in memory (<1.5s).
2. Assertion tampering and audience substitution in memory (<1.5s).
3. Live container lateral bypass rejection in Docker Compose (<15s).

```go
func TestWorkloadIdentityListener(t *testing.T) {
	// Setup fast in-memory PKI
	ca, err := pki.NewCA("Aegis Root CA")
	require.NoError(t, err)

	gatewayCert, err := ca.IssueServerCert("gateway", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	ordersCert, err := ca.IssueWorkloadCert("spiffe://aegis.local/workload/orders")
	require.NoError(t, err)

	// Start test dual server...
	t.Run("workload listener accepts valid SPIFFE client cert", func(t *testing.T) {
		client := createMTLSClient(ordersCert, ca.CertPool)
		req, _ := http.NewRequest(http.MethodPost, workloadURL+"/api/payments", nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("workload listener rejects Bearer JWT header (AMBIGUOUS_CREDENTIALS)", func(t *testing.T) {
		client := createMTLSClient(ordersCert, ca.CertPool)
		req, _ := http.NewRequest(http.MethodPost, workloadURL+"/api/payments", nil)
		req.Header.Set("Authorization", "Bearer eyJhbGciOiJFZERTQ...")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("peer microservice direct call is rejected (BYPASS_PREVENTED)", func(t *testing.T) {
		// orders calling payments directly presenting orders cert instead of gateway cert
		client := createMTLSClient(ordersCert, ca.CertPool)
		req, _ := http.NewRequest(http.MethodPost, paymentsBackendURL+"/api/payments", nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}
```

---

## 5. Anti-Patterns & Pitfalls to Avoid

| Category | Anti-Pattern | Correct Pattern | Threat / Vulnerability Prevented |
|---|---|---|---|
| **SPIFFE Extraction** | Inspecting `cert.Subject.CommonName` for workload identity | Extract strictly from `cert.URIs` matching `spiffe://aegis.local/workload/*` | Spoofed or mismatched CNs; non-compliance with RFC 6125 and SPIFFE standard (Pitfall 1). |
| **Ingress Headers** | Trusting `X-Client-Cert-SAN` or `X-Forwarded-Client-Cert` headers | Extract exclusively from active `*tls.ConnectionState` (`r.TLS.PeerCertificates[0]`) | Identity spoofing via client header injection from external or perimeter callers (STRIDE: Spoofing). |
| **Listener Isolation** | Allowing `Authorization: Bearer` tokens on Workload port `:9443` | Immediately reject with `HTTP 401 Unauthorized` (`AMBIGUOUS_CREDENTIALS`) | Confused Deputy attacks; principal precedence ambiguity between cert and user JWT (ADR-0003, Pitfall 2). |
| **User Listener** | Evaluating client certificates on User Ingress port `:8080` | Port `:8080` evaluates only Bearer tokens; ignores or rejects client certificates | Unauthorized elevation from user to workload permissions (ADR-0003). |
| **Backend Assertion** | Forwarding client Bearer tokens directly to backend microservices | Gateway mints short-lived (15s) audience-bound assertion JWT (`X-Aegis-Assertion`) | Leaked user tokens allowing lateral impersonation across backend microservices (AUTH-05). |
| **Assertion Scope** | Omitting audience (`aud`) or method/path claims in backend assertion | Assertion binds `aud: <service>`, `method`, canonical `path`, and `req_id` | Replay attacks where an assertion minted for `orders` is stolen and reused against `admin` (Pitfall 3). |
| **Clock Skew** | Verifying 15-second assertion tokens without clock leeway | Set `nbf: now - 5s` and configure `jwt.WithLeeway(5 * time.Second)` in verifier | False 401 rejections caused by slight clock drift between containers (Pitfall 6). |
| **Reverse Proxy** | Instantiating a new `http.Transport` for every request or route | Use a single, shared `*http.Transport` with connection pooling | Ephemeral port exhaustion and socket `TIME_WAIT` saturation under load (Pitfall 4). |
| **TLS Verification** | Setting `InsecureSkipVerify: true` in upstream proxy or tests | Generate certificates with `DNSNames: []string{service, "localhost"}` and `IPAddresses` | Man-in-the-middle attacks on backend communication; violates Invariant 4 (Pitfall 5). |
| **Backend Security** | Relying solely on Docker network isolation or mTLS without assertion | Implement dual-layer verification: Gateway SPIFFE client cert + Ed25519 assertion JWT | Direct lateral bypass by compromised containers on the shared Docker bridge network (BYP-02, BYP-03). |

---

## 6. Pattern Mapping Matrix

| File Path | Architectural Role | Analogs & Contracts | Implementation Excerpt |
|---|---|---|---|
| [`internal/pki/pki.go`](file:///home/logan78/Desktop/Aegis/internal/pki/pki.go) | PKI Generator | Go stdlib `crypto/x509`, `crypto/tls` | Pattern 1: `NewCA`, `IssueWorkloadCert`, `IssueServerCert` |
| [`internal/pki/pki_test.go`](file:///home/logan78/Desktop/Aegis/internal/pki/pki_test.go) | PKI Unit Tests | `testing`, `testify/require` | Fast in-memory certificate generation and TLS handshake verification |
| [`internal/identity/spiffe.go`](file:///home/logan78/Desktop/Aegis/internal/identity/spiffe.go) | SPIFFE Extractor | Analog to [`jwt.go`](file:///home/logan78/Desktop/Aegis/internal/identity/jwt.go), AUTH-02 | Pattern 2: `ExtractSPIFFEID` inspecting `cert.URIs` |
| [`internal/identity/spiffe_test.go`](file:///home/logan78/Desktop/Aegis/internal/identity/spiffe_test.go) | SPIFFE Unit Tests | `testify/assert` | Tests valid SPIFFE URIs, untrusted trust domains, missing URIs |
| [`internal/identity/assertion.go`](file:///home/logan78/Desktop/Aegis/internal/identity/assertion.go) | Assertion Crypto | Analog to [`jwt.go`](file:///home/logan78/Desktop/Aegis/internal/identity/jwt.go), AUTH-05, ADR-0003 | Pattern 3: `AssertionMinter` and `AssertionVerifier` with Ed25519 |
| [`internal/identity/assertion_test.go`](file:///home/logan78/Desktop/Aegis/internal/identity/assertion_test.go) | Assertion Unit Tests | `jwt/v5`, `testify` | Tests minting, signature checking, 5s leeway, audience mismatch |
| [`services/middleware/auth_middleware.go`](file:///home/logan78/Desktop/Aegis/services/middleware/auth_middleware.go) | Backend Middleware | BYP-02, Invariant 5 & 12 | Pattern 4: `BackendAuthMiddleware` enforcing mTLS + assertion |
| [`services/middleware/auth_middleware_test.go`](file:///home/logan78/Desktop/Aegis/services/middleware/auth_middleware_test.go) | Middleware Tests | `httptest.Server`, `testify` | Tests plain HTTP, untrusted peer cert, and assertion validation |
| [`internal/config/config.go`](file:///home/logan78/Desktop/Aegis/internal/config/config.go) | Config Model | GW-01, AUTH-02 | Loads `WorkloadPort` (9443), cert paths, assertion key config |
| [`internal/proxy/server.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/server.go) | Dual Listener Server | ADR-0003, AUTH-02, AUTH-03 | Pattern 5: `DualServer` managing :8080 and :9443 mTLS |
| [`internal/proxy/proxy.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/proxy.go) | Upstream mTLS Proxy | GW-05, AUTH-05, Invariant 4 | Pattern 6: `NewReverseProxyWithMTLS` injecting assertion |
| [`internal/proxy/proxy_test.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/proxy_test.go) | Upstream Proxy Tests | `httptest.NewUnstartedServer` | Asserts client mTLS presentation and `X-Aegis-Assertion` injection |
| [`cmd/gateway/main.go`](file:///home/logan78/Desktop/Aegis/cmd/gateway/main.go) | Gateway Entrypoint | ADR-0003, POL-01, Rule 5 | Pattern 8: Wires dual listeners, workload OPA eval, assertion minting |
| [`cmd/services/orders/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/orders/main.go) | Private Orders Service | BYP-01, BYP-02 | Pattern 7: HTTPS mTLS listener with `BackendAuthMiddleware` |
| [`cmd/services/payments/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/payments/main.go) | Private Payments Service | BYP-01, BYP-02 | Pattern 7: HTTPS mTLS listener with `BackendAuthMiddleware` |
| [`cmd/services/admin/main.go`](file:///home/logan78/Desktop/Aegis/cmd/services/admin/main.go) | Private Admin Service | BYP-01, BYP-02 | Pattern 7: HTTPS mTLS listener with `BackendAuthMiddleware` |
| [`scripts/certificates/main.go`](file:///home/logan78/Desktop/Aegis/scripts/certificates/main.go) | PKI Dev Script | Pure Go CLI | Generates local development Root CA and PEM keys to disk |
| [`deployments/compose/docker-compose.mvp.yml`](file:///home/logan78/Desktop/Aegis/deployments/compose/docker-compose.mvp.yml) | Compose Spec | BYP-01, BYP-03 | Publishes gateway ports 8080 & 9443; backends have 0 published ports |
| [`deployments/compose/Dockerfile`](file:///home/logan78/Desktop/Aegis/deployments/compose/Dockerfile) | Container Build | Docker multi-stage | Builds gateway and services with TLS cert mounting |
| [`tests/security/workload_identity_test.go`](file:///home/logan78/Desktop/Aegis/tests/security/workload_identity_test.go) | Security Test | AUTH-02, AUTH-03, POL-03 | Pattern 9: Tests workload mTLS, Bearer rejection on 9443, OPA rule 5 |
| [`tests/security/assertion_security_test.go`](file:///home/logan78/Desktop/Aegis/tests/security/assertion_security_test.go) | Security Test | AUTH-05, BYP-02 | Tests assertion tampering, expiration, audience replay attacks |
| [`tests/integration/bypass_test.go`](file:///home/logan78/Desktop/Aegis/tests/integration/bypass_test.go) | E2E Bypass Test | BYP-03, Invariant 12 | Tests peer microservice direct call rejection in Docker Compose |

---

*Pattern Mapping Complete for Phase 02: Workload Identity & Enforced Bypass Prevention*
