# Phase 1: MVP Secure Vertical Slice — Research

**Researched:** 2026-10-06  
**Domain:** Go Reverse Proxy, Embedded OPA Policy Engine, Zero-Trust Ingress Security  
**Confidence:** HIGH (All package versions, runtime capabilities, and architectural integrations verified against Go 1.25.5, OPA v1.21.1, and project contracts)

<user_constraints>
## User Constraints (from Project Contracts & Invariants)

### Locked Decisions (Non-Negotiable)
- **Go Standard Library ReverseProxy**: Use Go 1.20+ `httputil.ReverseProxy.Rewrite` hook receiving `*httputil.ProxyRequest`. The deprecated `Director` hook is strictly prohibited.
- **Strict Zero-Repair Path Validation**: Immediate `HTTP 400 Bad Request` on any path traversal sequence (`..`, `/..`), encoded separators (`%2f`, `%2F`, `%5c`, `%5C`), duplicate slashes (`//`), NUL bytes (`%00`), or invalid percent encodings. Never call `path.Clean()` to "repair" or normalize malicious paths before evaluation. The policy engine and upstream proxy must evaluate and forward the exact identical validated byte sequence (Invariant 7).
- **Embedded In-Memory OPA Engine**: Precompile Rego v1 queries once during startup using `rego.PrepareForEval(ctx)`. Evaluate typed request contexts entirely in-memory with sub-2ms latency (<0.2ms typical) without making external network calls (ADR-0002, Invariant 1).
- **Default-Deny Authorization**: Unmapped routes, evaluation errors, or missing permissions fail closed with `HTTP 403 Forbidden` and explicit reason codes (e.g., `DENIED_DEFAULT`, `DENIED_DEVELOPER_ADMIN_FORBIDDEN`) (Invariant 1).
- **Ingress Header Scrubbing**: Unconditionally strip all incoming client-supplied `X-Aegis-*`, `Forwarded`, `X-Forwarded-*`, and RFC 7230 hop-by-hop headers at ingress. Derive identity and routing context exclusively from cryptographically verified credentials (Invariant 8).
- **Ingress Concurrency Limiting**: Bounded request concurrency limit on unauthenticated ingress before identity resolution to prevent CPU and memory exhaustion (REV-02).
- **Request Limits**: Enforce `MaxHeaderBytes = 16 * 1024` (16 KiB) and max body limit of `1 MiB` (`http.MaxBytesReader`). Assign cryptographically random UUID to `X-Request-ID` (GW-01).
- **JWT Authentication & Demo Issuer**: Validate user Bearer JWTs against pinned issuer, audience, and algorithm allowlist (rejecting symmetric HMAC and `alg: none`) with 30s clock skew tolerance (AUTH-01). Local demo issuer (`cmd/demo-issuer`) seeds `developer`, `finance`, and `application-admin` credentials with login rate throttling (AUTH-04).
- **Docker Compose MVP Network Isolation**: All three backend microservices (`orders`, `payments`, `admin`) run with zero published host ports, accessible exclusively via the gateway over an internal Docker bridge network (BYP-01).
- **Structured Completion Auditing**: Every completed request (whether allowed, denied, or malformed) emits a structured JSON audit event containing request ID, principal, route, decision, reason code, HTTP status, duration ms, and error details (AUD-04).

### Planner's Discretion
- Choice of asymmetric algorithm for demo JWT issuer: Ed25519 (`EdDSA`) or `RS256` (Ed25519 recommended for microsecond signing/verification and stdlib purity).
- Exact unauthenticated concurrency capacity: 1,000 concurrent requests default.
- Microservice framework for demo backends: minimal Go stdlib `net/http` or `github.com/go-chi/chi/v5` handlers returning deterministic JSON payloads.

### Deferred Ideas (OUT OF SCOPE for Phase 1)
- Dedicated workload mTLS listener (`:9443`) and SPIFFE URI SAN validation (Deferred to Phase 2: GW-05, AUTH-02, AUTH-03).
- Gateway-to-backend mTLS and short-lived signed assertion JWTs (`X-Aegis-Assertion`) (Deferred to Phase 2: AUTH-05, BYP-02, BYP-03).
- Control Plane PostgreSQL storage, monotonic signed snapshots, gRPC streaming, and 10s freshness leases (Deferred to Phase 3: CTRL-01 through CTRL-06).
- Redis atomic token bucket rate limiting and JTI revocation / principal quarantine (Deferred to Phase 3: REV-01, REV-03, REV-04).
- Durable local disk WAL spool with `fsync()` and background async worker (Deferred to Phase 3: AUD-01, AUD-02, AUD-03).
- Operator React dashboard and `/control/v1` REST management APIs (Deferred to Phase 4).
</user_constraints>

<architectural_responsibility_map>
## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Ingress Listener & Limits (GW-01) | Gateway Edge Data Plane (`cmd/gateway`) | Go Standard Library (`net/http`) | Edge transport limits (16 KiB header, 1 MiB body, UUID assignment) must be enforced at socket arrival. |
| Zero-Repair Path Validation (GW-02) | Gateway Ingress Middleware (`internal/proxy/sanitizer.go`) | — | Rejects traversal/encoding anomalies immediately (HTTP 400) before routing, authentication, or proxying. |
| Unauthenticated Concurrency Bounds (REV-02) | Gateway Ingress Middleware (`internal/proxy/limiter.go`) | Go Runtime Semaphores | Throttles incoming flood before identity decoding or cryptographic verification to prevent CPU/memory DoS. |
| User Bearer JWT Authentication (AUTH-01) | Gateway Identity Domain (`internal/identity/jwt.go`) | `golang-jwt/jwt/v5` | Authenticates caller, validates pinned algorithm allowlist and claims, extracts principal ID and roles. |
| Local Demo JWT Issuer (AUTH-04) | Development Auth Service (`cmd/demo-issuer`) | Asymmetric Crypto (`crypto/ed25519`) | Mocks external identity provider for developer, finance, and admin roles with login rate throttling. |
| Deterministic Upstream Routing (GW-03) | Gateway Routing Domain (`internal/proxy/router.go`) | Static Snapshot Data (`policies/data/routes.json`) | Maps (HTTP method, canonical path) strictly to target private backend services without trusting client `Host`. |
| In-Memory OPA Policy Engine (POL-01–04) | Gateway Policy Domain (`internal/policy/engine.go`) | Embedded OPA SDK (`open-policy-agent/opa/rego`) | In-memory evaluation against precompiled query using typed input schemas in <0.2ms with zero network hops. |
| Header Scrubbing & Proxy Dispatch (GW-04) | Gateway Proxy Domain (`internal/proxy/proxy.go`) | Go Standard Library (`httputil.ReverseProxy`) | Strips client `X-Aegis-*` and hop-by-hop headers via `Rewrite` hook, dispatches request to target upstream. |
| Structured Completion Auditing (AUD-04) | Gateway Audit Domain (`internal/audit/logger.go`) | Go Standard Library (`log/slog`) | Emits structured JSON events recording principal, route, decision, status, duration, and error codes. |
| Private Backend Microservices (BYP-01) | Demo Microservice Tier (`services/{orders,payments,admin}`) | Go Standard Library (`net/http`) | Internal HTTP services exposing business endpoints without external ports. |
| Network Isolation (BYP-01) | Deployment Infrastructure (`deployments/compose/docker-compose.mvp.yml`) | Docker Bridge Network | Prohibits direct access from host by omitting published host ports for backends. |
</architectural_responsibility_map>

<research_summary>
## Summary

Phase 1 delivers the foundational secure vertical slice of Aegis: a single-replica Go gateway reverse proxy enforcing default-deny authorization for human users authenticating with short-lived JWTs, routing requests across three private backend microservices (`orders`, `payments`, `admin`) in Docker Compose with zero published backend host ports.

The implementation centers on three core pillars:
1. **Defensive Edge Reverse Proxying**: Built on Go 1.20+ `httputil.ReverseProxy.Rewrite`. The gateway decouples inbound client requests (`pr.In`) from outbound requests (`pr.Out`), strips all client-supplied `X-Aegis-*`, `Forwarded`, and RFC 7230 hop-by-hop headers, and enforces strict zero-repair path validation. By inspecting the raw wire bytes (`r.RequestURI` and `r.URL.RawPath`), the gateway immediately rejects dot segments (`..`), encoded slashes (`%2f`, `%5c`), duplicate slashes (`//`), and NUL bytes (`%00`) with `HTTP 400 Bad Request`, permanently eliminating CWE-22 and CWE-444 path desynchronization vulnerabilities.
2. **Sub-Millisecond In-Memory Policy Evaluation**: Using the official embedded OPA Go SDK (`github.com/open-policy-agent/opa v1.21.1`), the gateway compiles `policies/rego/authz.rego` once at startup using `rego.PrepareForEval(ctx)`. Requests map incoming JWT claims, canonical paths, and HTTP verbs to typed Go input documents conforming to `policies/schemas/input.schema.json`. Evaluation runs purely in-memory in ~0.15ms (<2ms p99 budget) with zero network calls, returning structured reason codes and defaulting to fail-closed on unmapped routes or evaluation errors.
3. **Reproducible Security Demonstration & Isolation**: In Docker Compose (`docker-compose.mvp.yml`), backend microservices have zero published host ports. Only the gateway port (`:8080`) is exposed. A lightweight mock issuer (`cmd/demo-issuer`) signs 5-minute tokens for seeded accounts (`developer`, `finance`, `application-admin`). Automated negative security tests prove that developers can read orders and payments, but are blocked from administrative routes (HTTP 403), that path traversal attempts fail (HTTP 400), that spoofed headers are stripped, and that direct connections to backend ports fail (connection refused).

**Primary recommendation:** Build thin, decoupled domain packages in `internal/` (`internal/proxy`, `internal/policy`, `internal/identity`, `internal/audit`), precompile the OPA query on startup, inspect raw wire bytes in `r.RequestURI` before route resolution, and enforce strict test-driven verification using `httptest` and Docker Compose.
</research_summary>

<standard_stack>
## Standard Stack

### Core Technologies
| Library | Version | Purpose | Why Standard / Legitimacy |
|---------|---------|---------|---------------------------|
| **Go** | `1.25.5` (runtime), `go 1.25.5` in `go.mod` | Gateway, Demo Issuer, and Microservice runtime | Cloud-native systems standard; Go 1.22+ ServeMux routing, `log/slog` structured logging, lock-free primitives, sub-millisecond GC. Verified installed on host. |
| **Go Reverse Proxy (`net/http` & `httputil.ReverseProxy`)** | Go Standard Library | Edge HTTP termination, path validation, header scrubbing, and upstream dispatch | Built-in standard library with zero third-party dependencies. Modern Go 1.20+ `Rewrite` hook decouples `r.In` from `r.Out`, removes hop-by-hop headers automatically, and avoids legacy `Director` path desynchronization bugs. |
| **Embedded OPA Engine (`open-policy-agent/opa`)** | `v1.21.1` (`github.com/open-policy-agent/opa/rego`) | In-memory declarative policy evaluation on the request hot path | CNCF graduated standard. Precompiled Rego queries (`rego.PrepareForEval(ctx)`) evaluate locally in <0.2ms, satisfying the <2ms p99 budget without remote network hops or external daemon failure modes. Rego v1 syntax supported. |
| **JWT Cryptography (`golang-jwt/jwt/v5`)** | `v5.3.1` (`github.com/golang-jwt/jwt/v5`) | Ingress access token validation and demo issuer token minting | RFC 8725 compliant, actively maintained. Enforces pinned algorithm allowlists (`RS256`, `ES256`, `EdDSA`), explicit audience/issuer validation, and prevents algorithm confusion (`alg: none` and symmetric key substitution). |
| **UUID Generator (`google/uuid`)** | `v1.6.0` (`github.com/google/uuid`) | Cryptographically random `X-Request-ID` and audit event IDs | RFC 4122 compliant UUID v4 generation using `crypto/rand`. |
| **Structured Logging (`log/slog`)** | Go Standard Library | Structured completion audit logging (AUD-04) | High-performance JSON logging with zero heap allocations for static fields; native to Go 1.21+. |

### Supporting Libraries
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| **`github.com/stretchr/testify`** | `v1.12.1` | Unit, security, and integration assertions | Testing token parsing, path canonicalization, policy precedence, and negative security tests (`assert`, `require`, `suite`). |
| **`github.com/go-chi/chi/v5`** | `v5.3.2` | REST router for demo microservices | Backend demo microservices (`orders`, `payments`, `admin`) and demo issuer endpoints. 100% `net/http` compatible. |
| **`golang.org/x/sync/semaphore`** | Standard subrepo (or stdlib buffered channels) | Concurrency limiting on unauthenticated ingress (REV-02) | Bounding concurrent in-flight requests before identity parsing to protect CPU. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff / Why Not |
|------------|-----------|--------------------|
| `httputil.ReverseProxy.Rewrite` | `httputil.ReverseProxy.Director` | Deprecated in Go 1.20+. Fails to sanitize hop-by-hop headers, leaks client headers, and causes path desynchronization between `Path` and `RawPath`. |
| Embedded OPA (`github.com/open-policy-agent/opa`) | External OPA Daemon (`http://opa:8181`) | Adds 2–10ms network round-trip overhead; introduces a network failure point; crashes cause fail-open risk or widespread gateway 503s. Violates <2ms p99 budget. |
| Strict Zero-Repair Rejection (400) | `path.Clean()` on raw ingress path | Attempting to "repair" paths with dot segments or encoded slashes causes impedance mismatch with backend routers, enabling path-traversal bypasses (CWE-22/CWE-444). |
| Ed25519 / RS256 for Demo Tokens | HMAC-SHA256 (`HS256`) | Symmetric keys allow any service possessing the verification key to forge tokens for any user or role. Asymmetric cryptography enforces strict issuer separation. |
| Docker network isolation (no ports) | Published ports with firewall rules | Publishing ports (`8081:8081`) exposes services to the host network and adjacent containers, violating bypass prevention (BYP-01). |

### Package Verification & Installation
```bash
# Add Phase 1 Go module dependencies
go get github.com/open-policy-agent/opa@v1.21.1
go get github.com/golang-jwt/jwt/v5@v5.3.1
go get github.com/google/uuid@v1.6.0
go get github.com/stretchr/testify@v1.12.1
go get github.com/go-chi/chi/v5@v5.3.2
```
</standard_stack>

<architecture_patterns>
## Architecture Patterns

### System Architecture Diagram (Phase 1 Request Lifecycle)

```mermaid
sequenceDiagram
    autonumber
    actor Client as External Client / Developer
    participant Ingress as Gateway Ingress (:8080)
    participant Limiter as Concurrency Limiter (REV-02)
    participant PathValidator as Zero-Repair Path Validator (GW-02)
    participant Authenticator as JWT Authenticator (AUTH-01)
    participant Router as Upstream Router (GW-03)
    participant OPA as Embedded OPA Engine (POL-01)
    participant Proxy as ReverseProxy Rewrite (GW-04)
    participant Backend as Private Backend (orders/payments/admin)
    participant Audit as Structured Audit Logger (AUD-04)

    Client->>Ingress: HTTP Request (Method, Path, Bearer JWT, Headers)
    Note over Ingress: Assign UUID X-Request-ID<br/>Enforce limits (16KB headers, 1MB body)
    
    Ingress->>Limiter: Acquire concurrency slot
    alt Slot unavailable (saturated)
        Limiter-->>Client: 429 Too Many Requests (or 503)
        Limiter->>Audit: Emit completion audit (status: 429)
    end
    
    Limiter->>PathValidator: Validate raw wire path bytes (r.RequestURI)
    alt Contains traversal (..), %2f, %5c, //, %00
        PathValidator-->>Client: 400 Bad Request (Zero-Repair Rejection)
        PathValidator->>Audit: Emit completion audit (status: 400, reason: INVALID_PATH)
    end
    
    PathValidator->>Authenticator: Extract & verify Bearer JWT
    alt Invalid token / alg: none / expired / missing
        Authenticator-->>Client: 401 Unauthorized (RFC 7807)
        Authenticator->>Audit: Emit completion audit (status: 401, reason: AUTH_FAILED)
    end
    
    Authenticator->>Router: Match (Method, Canonical Path) against route catalog
    alt Route unmapped
        Router-->>Client: 404 Not Found (or 403 Forbidden)
        Router->>Audit: Emit completion audit (status: 404, reason: ROUTE_NOT_FOUND)
    end
    
    Router->>OPA: In-Memory Eval (Principal, Resource, Request, Context)
    alt Decision: allow == false or evaluation error
        OPA-->>Client: 403 Forbidden (with reason_code)
        OPA->>Audit: Emit completion audit (status: 403, reason: DENIED_*)
    end
    
    OPA->>Proxy: Forward authorized request
    Note over Proxy: Strip client X-Aegis-* & hop-by-hop headers<br/>Inject X-Request-ID<br/>Set target backend URL
    Proxy->>Backend: HTTP Request (internal Docker bridge)
    Backend-->>Proxy: HTTP Response (200 OK + JSON payload)
    
    Proxy-->>Client: HTTP Response (200 OK + payload + X-Request-ID)
    Proxy->>Audit: Emit completion audit (status: 200, duration_ms, reason: ALLOWED_*)
```

### Recommended Project Structure

```
aegis/
├── cmd/
│   ├── gateway/                  # Gateway binary entrypoint (PEP / Embedded PDP)
│   │   └── main.go
│   └── demo-issuer/              # Development-only JWT issuer (seeded developer, finance, admin)
│       └── main.go
├── internal/
│   ├── config/                   # Gateway configuration (port, limits, routes file, issuer keys)
│   │   └── config.go
│   ├── identity/                 # JWT verification, pinned algorithms, claims extraction
│   │   ├── jwt.go
│   │   ├── claims.go
│   │   └── jwt_test.go
│   ├── policy/                   # OPA/Rego compilation, evaluator interface, input schemas
│   │   ├── engine.go
│   │   ├── types.go
│   │   └── engine_test.go
│   ├── proxy/                    # Reverse proxy engine, path validator, header scrubber, router
│   │   ├── handler.go            # Core gateway HTTP handler pipeline
│   │   ├── proxy.go              # httputil.ReverseProxy with Rewrite hook
│   │   ├── router.go             # Deterministic route table matcher
│   │   ├── sanitizer.go          # Strict zero-repair path validator
│   │   ├── limiter.go            # Unauthenticated ingress concurrency limiter
│   │   └── proxy_test.go
│   └── audit/                    # Structured completion audit logger
│       ├── event.go
│       ├── logger.go
│       └── logger_test.go
├── services/                     # Private backend demo microservices (BYP-01)
│   ├── orders/                   # Orders service (:8081)
│   │   └── main.go
│   ├── payments/                 # Payments service (:8082)
│   │   └── main.go
│   └── admin/                    # Admin service (:8083)
│       └── main.go
├── policies/                     # Seed Rego policies, input schemas, and data
│   ├── rego/
│   │   └── authz.rego            # Authoritative Rego v1 authorization policy
│   ├── schemas/
│   │   ├── input.schema.json     # Typed policy input contract
│   │   └── output.schema.json    # Typed policy output contract
│   ├── data/
│   │   ├── routes.json           # Catalog of private backend routes
│   │   └── seed_roles.json       # Seed RBAC role-to-permission mapping
│   └── tests/
│       └── authz_test.rego       # OPA unit tests (8 passing)
├── deployments/
│   └── compose/
│       └── docker-compose.mvp.yml # MVP compose profile: gateway, demo-issuer, 3 private backends
├── tests/
│   ├── integration/              # End-to-end and Docker Compose integration tests
│   │   └── mvp_test.go
│   └── security/                 # Automated penetration & negative security tests
│       ├── path_traversal_test.go
│       ├── header_spoofing_test.go
│       ├── jwt_negative_test.go
│       └── rbac_matrix_test.go
├── go.mod
├── go.sum
└── Makefile                      # Build, test, and demo targets
```

---

### Pattern 1: Go 1.20+ `httputil.ReverseProxy.Rewrite` Hook

**What:** Uses the modern `Rewrite` hook receiving `*httputil.ProxyRequest` to safely configure outbound proxy requests without mutating the inbound client request. Automatically purges hop-by-hop headers, unconditionally deletes client-supplied `X-Aegis-*` and `Forwarded` headers, and maps the validated canonical path to the upstream backend.  
**When to use:** Every request forwarded through the gateway.

```go
package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func NewReverseProxy(targetURL *url.URL, canonicalPath string, requestID string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// 1. Point outbound request to target backend URL
			pr.SetURL(targetURL)

			// 2. Set exact canonical validated path (prevent path desynchronization)
			pr.Out.URL.Path = canonicalPath
			pr.Out.URL.RawPath = ""

			// 3. Strip all client-supplied X-Aegis-* headers
			for k := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(k), "x-aegis-") {
					pr.Out.Header.Del(k)
				}
			}

			// 4. Strip client-supplied Forwarded and X-Forwarded-* headers
			pr.Out.Header.Del("Forwarded")
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
			pr.Out.Header.Del("X-Forwarded-Server")

			// 5. Ingress user token is stripped: backends run in private network
			// (In Phase 2, this is replaced by signed assertion X-Aegis-Assertion)
			pr.Out.Header.Del("Authorization")

			// 6. Inject gateway tracking context
			pr.Out.Header.Set("X-Request-ID", requestID)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Structured RFC 7807 Bad Gateway error response
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"type":"https://aegis.local/errors/bad-gateway","title":"Bad Gateway","status":502,"detail":"Upstream backend unreachable"}`))
		},
	}
}
```

---

### Pattern 2: Strict Zero-Repair Path Canonicalization

**What:** Inspects the raw wire path string from `r.RequestURI` before any URL unescaping or route lookup. Checks for dot-segments (`..`), encoded slashes (`%2f`, `%5c`), duplicate slashes (`//`), NUL bytes (`%00`), and invalid percent encodings. Immediately returns `HTTP 400 Bad Request` without attempting `path.Clean()` or automatic repair.  
**When to use:** Ingress middleware executed before authentication and routing on all incoming requests.

```go
package proxy

import (
	"errors"
	"net/http"
	"strings"
)

var (
	ErrPathContainsTraversal   = errors.New("path contains traversal dot-segments")
	ErrPathContainsEncodedSlash = errors.New("path contains encoded directory separators")
	ErrPathContainsDoubleSlash = errors.New("path contains duplicate slashes")
	ErrPathContainsNulByte     = errors.New("path contains NUL bytes")
	ErrPathInvalidEncoding     = errors.New("path contains invalid percent encoding")
)

// ValidatePathZeroRepair performs strict zero-repair rejection on raw wire bytes.
func ValidatePathZeroRepair(r *http.Request) (string, error) {
	// r.RequestURI contains the exact raw string sent in the HTTP request line
	rawURI := r.RequestURI
	rawPath := rawURI
	if idx := strings.IndexByte(rawURI, '?'); idx != -1 {
		rawPath = rawURI[:idx]
	}
	if idx := strings.IndexByte(rawPath, '#'); idx != -1 {
		rawPath = rawPath[:idx]
	}

	lower := strings.ToLower(rawPath)

	// 1. Detect duplicate slashes
	if strings.Contains(rawPath, "//") {
		return "", ErrPathContainsDoubleSlash
	}

	// 2. Detect NUL bytes (raw or encoded)
	if strings.Contains(rawPath, "\x00") || strings.Contains(lower, "%00") {
		return "", ErrPathContainsNulByte
	}

	// 3. Detect encoded slashes and backslashes
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(rawPath, "\\") {
		return "", ErrPathContainsEncodedSlash
	}

	// 4. Detect dot-segments (literal and encoded)
	if strings.Contains(rawPath, "/../") || strings.HasSuffix(rawPath, "/..") || rawPath == ".." ||
		strings.Contains(rawPath, "/./") || strings.HasSuffix(rawPath, "/.") ||
		strings.Contains(lower, "%2e%2e") || strings.Contains(lower, ".%2e") || strings.Contains(lower, "%2e.") {
		return "", ErrPathContainsTraversal
	}

	// 5. Verify prefix format: Aegis expects valid absolute paths starting with /
	if !strings.HasPrefix(rawPath, "/") {
		return "", ErrPathInvalidEncoding
	}

	// Validated exact path bytes to use for route matching and policy evaluation
	return rawPath, nil
}
```

---

### Pattern 3: Embedded Precompiled OPA Engine (`open-policy-agent/opa/rego`)

**What:** Loads `policies/rego/authz.rego` and precompiles the query `data.aegis.authz.decision` once during gateway startup using `rego.PrepareForEval(ctx)`. Evaluates typed Go input structs in-memory per request in ~0.15ms. Evaluates to `allow: false` with reason codes on unmapped routes or evaluation errors.  
**When to use:** Authorization hot path after JWT authentication and route resolution.

```go
package policy

import (
	"context"
	"fmt"
	"github.com/open-policy-agent/opa/rego"
)

type Engine struct {
	pq rego.PreparedEvalQuery
}

type PolicyInput struct {
	Principal       PrincipalInput `json:"principal"`
	Resource        ResourceInput  `json:"resource"`
	Request         RequestInput   `json:"request"`
	Context         ContextInput   `json:"context"`
	SnapshotVersion int64          `json:"snapshot_version"`
}

type PrincipalInput struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"`
	Roles []string `json:"roles"`
}

type ResourceInput struct {
	Service string `json:"service"`
	Route   string `json:"route"`
}

type RequestInput struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type ContextInput struct {
	RiskScore int    `json:"risk_score"`
	RiskState string `json:"risk_state"`
}

type Decision struct {
	Allow           bool   `json:"allow"`
	ReasonCode      string `json:"reason_code"`
	SnapshotVersion int64  `json:"snapshot_version"`
}

func NewEngine(ctx context.Context, regoCode string) (*Engine, error) {
	query, err := rego.New(
		rego.Query("data.aegis.authz.decision"),
		rego.Module("authz.rego", regoCode),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to precompile rego query: %w", err)
	}
	return &Engine{pq: query}, nil
}

func (e *Engine) Evaluate(ctx context.Context, input PolicyInput) (Decision, error) {
	rs, err := e.pq.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		// Fail closed on evaluation error
		return Decision{Allow: false, ReasonCode: "EVALUATION_ERROR", SnapshotVersion: input.SnapshotVersion}, err
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return Decision{Allow: false, ReasonCode: "DENIED_EMPTY_RESULT", SnapshotVersion: input.SnapshotVersion}, nil
	}

	val, ok := rs[0].Expressions[0].Value.(map[string]interface{})
	if !ok {
		return Decision{Allow: false, ReasonCode: "MALFORMED_DECISION", SnapshotVersion: input.SnapshotVersion}, nil
	}

	allow, _ := val["allow"].(bool)
	reasonCode, _ := val["reason_code"].(string)

	return Decision{
		Allow:           allow,
		ReasonCode:      reasonCode,
		SnapshotVersion: input.SnapshotVersion,
	}, nil
}
```

---

### Pattern 4: Unauthenticated Ingress Concurrency Bounding (REV-02)

**What:** Bounded counting semaphore wrapping the ingress HTTP handler before JWT signature verification or route resolution. Protects CPU from asymmetric cryptographic load during volumetric flood attacks.  
**When to use:** First middleware in the gateway HTTP processing pipeline.

```go
package proxy

import (
	"net/http"
)

type ConcurrencyLimiter struct {
	sem chan struct{}
}

func NewConcurrencyLimiter(maxConcurrent int) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		sem: make(chan struct{}, maxConcurrent),
	}
}

func (l *ConcurrencyLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case l.sem <- struct{}{}:
			defer func() { <-l.sem }()
			next.ServeHTTP(w, r)
		default:
			// Capacity exceeded: fail closed with 429
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"type":"https://aegis.local/errors/concurrency-exceeded","title":"Too Many Requests","status":429,"detail":"Ingress concurrency limit exceeded"}`))
		}
	})
}
```
</architecture_patterns>

<dont_hand_roll>
## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why Hand-Rolling Fails |
|---------|-------------|-------------|------------------------|
| **JWT Signature & Claims Validation** | Custom base64 decoding and JSON unmarshaling | `github.com/golang-jwt/jwt/v5` with `jwt.WithValidMethods` | Custom token parsers frequently fall victim to `alg: none` exploits, key confusion attacks (treating RSA public keys as HMAC secrets), timing attacks in signature checks, and missing clock skew tolerance (RFC 8725). |
| **Declarative Authorization Policy** | Custom Go `switch/case` role and path checkers | Embedded OPA SDK (`open-policy-agent/opa/rego`) | Hardcoded Go RBAC logic tightly couples policies to application code, cannot be tested with standard tools (`opa test`), lacks formal default-deny guarantees, and cannot be dynamically updated without recompilation. |
| **HTTP Reverse Proxy Forwarding** | Custom `http.Client.Do` dispatch in handler | `net/http/httputil.ReverseProxy` with `Rewrite` | Manual forwarding mishandles chunked transfer encodings, forgets to remove RFC 7230 hop-by-hop headers (`Connection`, `Upgrade`), leaks idle TCP sockets, and introduces race conditions on request/response body streams. |
| **Path Cleaning / Auto-Repair** | Calling `path.Clean()` on raw ingress paths | Strict Zero-Repair rejection (`HTTP 400`) | Standard `path.Clean()` resolves `/..` and merges `//`, but upstream backends often parse raw uncleaned paths or decode `%2F` differently. This impedance mismatch causes critical path traversal bypasses (CWE-22/CWE-444). |
| **Cryptographically Secure IDs** | `math/rand` string generation | `github.com/google/uuid` (UUID v4) | Non-cryptographic random generators produce predictable sequences, allowing attackers to guess `X-Request-ID` and audit correlation tokens. |

**Key insight:** In security gateways, subtle edge cases (e.g. `%2e%2e`, `alg: none`, undrained TCP sockets) are where 90% of zero-day vulnerabilities live. Established libraries have undergone years of formal security audits and CVE hardening.
</dont_hand_roll>

<common_pitfalls>
## Common Pitfalls

### Pitfall 1: Go `r.URL.Path` Auto-Decoding Masking Traversal Sequences
**What goes wrong:** The developer checks `strings.Contains(r.URL.Path, "%2f")` or `strings.Contains(r.URL.Path, "..")`. The check passes because Go's standard HTTP server automatically URL-decodes path segments before populating `r.URL.Path`. An attacker requesting `/api/orders/%2e%2e/admin/users` has `%2e%2e` decoded to `..`, which might be resolved or bypassed before the check executes.  
**Why it happens:** Go's `http.Request` has both `r.URL.Path` (unreserved and decoded) and `r.RequestURI` (the exact raw bytes from the HTTP wire). Checking only `r.URL.Path` creates parser disparity.  
**How to avoid:** Always parse and validate `r.RequestURI` (or `r.URL.RawPath`) in the zero-repair validator. Inspect the raw wire string for `%2e`, `%2f`, `%5c`, `%00`, and `..` before touching `r.URL.Path`.  
**Warning signs:** A request with `%2e%2e` reaches the router without triggering a 400 error.

### Pitfall 2: ReverseProxy Rewrite Upstream URL and Host Desynchronization
**What goes wrong:** Using `httputil.ReverseProxy.Rewrite`, the developer sets `pr.Out.URL.Path = canonicalPath` but forgets `pr.SetURL(targetURL)`. The request is dispatched with mismatched `Host` headers or fails to connect to the backend container.  
**Why it happens:** In `ProxyRequest`, `SetURL(target)` rewrites the scheme, host, and port, and updates `pr.Out.Host`. If modified manually in the wrong order, the proxy connects to the wrong host.  
**How to avoid:** Call `pr.SetURL(targetURL)` first, then explicitly assign `pr.Out.URL.Path = canonicalPath` and `pr.Out.URL.RawPath = ""` to enforce identical validated path bytes.  
**Warning signs:** Upstream backends log `404 Not Found` or proxy logs show connection errors to `localhost` instead of the Docker service name.

### Pitfall 3: Goroutine and Connection Leaks from Undrained Upstream Error Bodies
**What goes wrong:** When a backend returns a 500 error or the reverse proxy intercepts an error, failing to read and close `resp.Body` prevents the underlying TCP socket from being reused. Ephemeral sockets accumulate in `CLOSE_WAIT` until the gateway runs out of file descriptors.  
**Why it happens:** In Go's `net/http.Transport`, connection reuse requires the response body to be read to EOF (`io.Copy(io.Discard, resp.Body)`) and closed (`resp.Body.Close()`).  
**How to avoid:** When customizing proxy response handling or error logging, always drain and close response bodies. Configure explicit `MaxIdleConnsPerHost = 100` and `ResponseHeaderTimeout = 5 * time.Second` on the proxy transport.  
**Warning signs:** Goroutine count in `pprof` steadily climbs under load; `netstat` shows high socket counts in `TIME_WAIT` / `CLOSE_WAIT`.

### Pitfall 4: OPA AST Recompilation on Every Request
**What goes wrong:** The developer creates a new `rego.New(...)` object and calls `r.Eval(ctx)` inside the HTTP request handler. CPU utilization spikes to 100% at only 50 RPS, and request latency surges from <1ms to >300ms due to continuous Rego AST parsing and garbage collection churn.  
**Why it happens:** Compiling Rego source code is computationally expensive (~2–10ms).  
**How to avoid:** Call `rego.New(...).PrepareForEval(ctx)` once during startup or snapshot load. The resulting `rego.PreparedEvalQuery` is completely thread-safe. Concurrent requests pass only `rego.EvalInput(input)` to `Eval(ctx)`.  
**Warning signs:** `BenchmarkOPAEval` reports >1ms per operation and high heap allocations per op.

### Pitfall 5: JWT Clock Skew Invalidation
**What goes wrong:** Valid tokens minted by the demo issuer are rejected by the gateway with `401 Unauthorized (token is not valid yet or expired)` due to subtle millisecond differences between system clocks.  
**Why it happens:** Standard JWT validators check `exp` and `nbf` against `time.Now()` with zero tolerance unless configured with leeway.  
**How to avoid:** Configure `jwt.WithLeeway(30 * time.Second)` in `golang-jwt/jwt/v5` parser options (NIST SP 800-207 and RFC 8725 recommendation).  
**Warning signs:** Flaky authentication failures in integration test suites right around token issuance timestamps.

### Pitfall 6: Inadvertently Exposing Backend Ports in Docker Compose
**What goes wrong:** The developer writes `ports: ["8081:8081"]` in `docker-compose.mvp.yml` for the `orders` service to "make testing easier." This allows external callers to bypass the gateway entirely by querying `http://localhost:8081`.  
**Why it happens:** Confusing `ports` (published to host) with `expose` (available only to containers on the internal network).  
**How to avoid:** Enforce BYP-01: Backend services (`orders`, `payments`, `admin`) MUST NOT define `ports:`. They must only connect to the `aegis-internal` network. Only the gateway publishes port `8080` (and `demo-issuer` port `8085` for test token minting).  
**Warning signs:** `curl http://localhost:8081/api/orders` returns 200 from the host machine.
</common_pitfalls>

<code_examples>
## Code Examples

### 1. JWT Bearer Token Authenticator with Pinned Algorithm Allowlist (AUTH-01)
```go
package identity

import (
	"crypto"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrMissingAuthHeader = errors.New("missing or malformed authorization header")
	ErrInvalidAlgorithm  = errors.New("unsupported or insecure token signing algorithm")
	ErrTokenExpired       = errors.New("token is expired")
	ErrInvalidClaims     = errors.New("token claims invalid or missing required fields")
)

type TokenValidator struct {
	issuer        string
	audience      string
	publicKey     crypto.PublicKey
	allowedAlgos  []string
}

type UserClaims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles"`
}

func NewTokenValidator(issuer, audience string, publicKey crypto.PublicKey) *TokenValidator {
	return &TokenValidator{
		issuer:       issuer,
		audience:     audience,
		publicKey:    publicKey,
		allowedAlgos: []string{"EdDSA", "RS256", "ES256"}, // Pinned allowlist; strictly reject none and HS*
	}
}

func (v *TokenValidator) ValidateBearerToken(authHeader string) (*UserClaims, error) {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, ErrMissingAuthHeader
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	claims := &UserClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		// Enforce algorithm allowlist
		validMethod := false
		for _, algo := range v.allowedAlgos {
			if token.Method.Alg() == algo {
				validMethod = true
				break
			}
		}
		if !validMethod {
			return nil, fmt.Errorf("%w: %s", ErrInvalidAlgorithm, token.Method.Alg())
		}
		return v.publicKey, nil
	},
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithLeeway(30*time.Second), // Clock skew tolerance
	)

	if err != nil || !token.Valid {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	if claims.Subject == "" {
		return nil, ErrInvalidClaims
	}

	return claims, nil
}
```

### 2. Demo JWT Issuer with Seeded Accounts and Throttling (AUTH-04)
```go
package main

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type SeedUser struct {
	UserID string   `json:"user_id"`
	Roles  []string `json:"roles"`
}

var seedDatabase = map[string]SeedUser{
	"developer":         {UserID: "usr_developer_01", Roles: []string{"developer"}},
	"finance":           {UserID: "usr_finance_01", Roles: []string{"finance"}},
	"application-admin": {UserID: "usr_admin_01", Roles: []string{"application-admin"}},
}

type IssuerServer struct {
	privateKey ed25519.PrivateKey
	issuer     string
	audience   string
	mu         sync.Mutex
	attempts   map[string]int
}

func (s *IssuerServer) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Login throttling (AUTH-04)
	clientIP := r.RemoteAddr
	s.mu.Lock()
	if s.attempts[clientIP] > 20 {
		s.mu.Unlock()
		http.Error(w, `{"error":"RATE_LIMITED"}`, http.StatusTooManyRequests)
		return
	}
	s.attempts[clientIP]++
	s.mu.Unlock()

	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	user, ok := seedDatabase[req.Role]
	if !ok {
		http.Error(w, `{"error":"UNKNOWN_ROLE"}`, http.StatusUnauthorized)
		return
	}

	now := time.Now()
	claims := struct {
		jwt.RegisteredClaims
		Roles []string `json:"roles"`
	}{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Audience:  jwt.ClaimStrings{s.audience},
			Subject:   user.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)), // Short-lived (5 min)
		},
		Roles: user.Roles,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signedToken, err := token.SignedString(s.privateKey)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": signedToken,
		"token_type":   "Bearer",
		"expires_in":   300,
	})
}
```

### 3. Structured Completion Audit Logging (AUD-04)
```go
package audit

import (
	"log/slog"
	"os"
	"time"
)

type CompletionEvent struct {
	EventID         string    `json:"event_id"`
	Timestamp       time.Time `json:"timestamp"`
	RequestID       string    `json:"request_id"`
	PrincipalID     string    `json:"principal_id"`
	PrincipalKind   string    `json:"principal_kind"`
	PrincipalRoles  []string  `json:"principal_roles"`
	ClientIP        string    `json:"client_ip"`
	HTTPMethod      string    `json:"http_method"`
	CanonicalPath   string    `json:"canonical_path"`
	RouteID         string    `json:"route_id"`
	ServiceID       string    `json:"service_id"`
	Decision        string    `json:"decision"`
	ReasonCode      string    `json:"reason_code"`
	HTTPStatus      int       `json:"http_status"`
	DurationMS      float64   `json:"duration_ms"`
	SnapshotVersion int64     `json:"snapshot_version"`
	ErrorCode       string    `json:"error_code,omitempty"`
}

type Logger struct {
	logger *slog.Logger
}

func NewLogger() *Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return &Logger{logger: slog.New(handler)}
}

func (l *Logger) LogCompletion(event CompletionEvent) {
	l.logger.Info("audit_completion",
		"event_id", event.EventID,
		"request_id", event.RequestID,
		"principal_id", event.PrincipalID,
		"principal_kind", event.PrincipalKind,
		"principal_roles", event.PrincipalRoles,
		"client_ip", event.ClientIP,
		"method", event.HTTPMethod,
		"path", event.CanonicalPath,
		"route_id", event.RouteID,
		"service_id", event.ServiceID,
		"decision", event.Decision,
		"reason_code", event.ReasonCode,
		"status", event.HTTPStatus,
		"duration_ms", event.DurationMS,
		"snapshot_version", event.SnapshotVersion,
		"error_code", event.ErrorCode,
	)
}
```
</code_examples>

<sota_updates>
## State of the Art (2024–2026)

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `httputil.ReverseProxy.Director` | `httputil.ReverseProxy.Rewrite` | Go 1.20+ (2023) | `Director` modified `r.In` directly, failing to strip hop-by-hop headers and leading to path divergence between `Path` and `RawPath`. `Rewrite` cleanly isolates inbound from outbound requests. |
| External OPA Daemon (`http://opa:8181`) | Embedded OPA Go Engine (`github.com/open-policy-agent/opa/rego`) | Established production pattern | Eliminates 2–10ms network round-trip per request, removes an external runtime dependency on the request path, and avoids fail-open risks. |
| Rego legacy syntax (`allow { ... }`) | Rego v1 syntax (`allow if { ... }`) | OPA v1.0+ (2024–2026) | Explicit keywords (`if`, `in`, `contains`) prevent subtle ambiguity in boolean evaluation rules and enable strict linting. |
| Path normalization (`path.Clean`) | Zero-Repair Path Rejection (400) | RFC 9110 / Modern Zero Trust | "Repairing" paths causes impedance mismatch with backend routers. Rejecting malformed paths immediately eliminates CWE-22 and CWE-444. |
| `github.com/golang-jwt/jwt/v4` | `github.com/golang-jwt/jwt/v5` | 2023–2026 | v5 enforces strict RFC 8725 type checks, algorithm pinning, and safer claims validation APIs. |

**New tools/patterns to consider:**
- Go 1.21+ `log/slog` structured logging: zero-allocation structured JSON logging built into stdlib.
- Rego v1 compiler enforcement: `import rego.v1` in all policy modules.
</sota_updates>

<environment_availability>
## Environment Availability

All necessary development runtimes and orchestration tools are confirmed available and verified on the local host:
- **Go Runtime**: `go version go1.25.5 linux/amd64` (installed and functional)
- **Docker & Docker Compose**: `Docker Compose version v5.0.0` (installed and functional)
- **OPA Engine**: `Version: 1.2.0` CLI installed; verified passing 8/8 existing Rego tests via `opa test policies/rego policies/tests -v`
- **Port Allocation for Phase 1**:
  - Gateway Ingress Listener: `8080` (mapped to host)
  - Demo JWT Issuer: `8085` (mapped to host for tests)
  - Orders Service: `8081` (internal bridge network only, NO host mapping)
  - Payments Service: `8082` (internal bridge network only, NO host mapping)
  - Admin Service: `8083` (internal bridge network only, NO host mapping)
</environment_availability>

<validation_architecture>
## Validation Architecture

### Test Framework
- **Unit & Security Tests**: Standard Go `testing` package with `github.com/stretchr/testify` (`assert`, `require`, `suite`).
- **Race Detection**: `go test -race ./...` mandatory for verifying concurrency limits and proxy handlers.
- **Policy Verification**: `opa test policies/rego policies/tests -v` for Rego rule correctness.
- **E2E Integration**: Docker Compose (`deployments/compose/docker-compose.mvp.yml`) executing parameterized curl/HTTP tests in `tests/integration/mvp_test.go`.

### Execution Commands & Feedback Latency
- **Quick Run Command** (< 2 seconds):
  ```bash
  go test -race ./internal/... && opa test policies/rego policies/tests -v
  ```
- **Full Suite Command** (< 25 seconds):
  ```bash
  go test -race ./... && opa test policies/rego policies/tests -v && docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build && go test -v -race ./tests/integration/... && docker compose -f deployments/compose/docker-compose.mvp.yml down
  ```

### Task-Level Requirement-to-Test Mapping

| Requirement ID | Description | Test File | Test Case & Assertion |
|----------------|-------------|-----------|------------------------|
| **GW-01** | Listener limits & UUIDs | `internal/proxy/proxy_test.go` | Assert request header >16 KiB rejected with 431/400; body >1 MiB truncated/rejected with 413/400; response contains valid UUID in `X-Request-ID`. |
| **GW-02** | Zero-repair path rejection | `tests/security/path_traversal_test.go` | Parameterized test table: `/api/orders/../admin`, `/api/orders/%2e%2e/admin`, `/api/orders/%2fadmin`, `//api/orders`, `/api/orders/%00`. Assert immediate `HTTP 400 Bad Request` with zero path repair and zero upstream dispatch. |
| **GW-03** | Upstream route resolution | `internal/proxy/proxy_test.go` | Assert `GET /api/orders` routes to orders service; `POST /api/payments` routes to payments service; unmapped `/api/unknown` returns `HTTP 404/403`. |
| **GW-04** | Header sanitization | `tests/security/header_spoofing_test.go` | Inject `X-Aegis-User: root`, `X-Aegis-Roles: admin`, `Forwarded: for=evil`, `Connection: close, X-Hop`. Mock upstream asserts all `X-Aegis-*` and forwarding headers are absent. |
| **AUTH-01** | JWT validation | `tests/security/jwt_negative_test.go` | Assert expired token -> 401; `alg: none` -> 401; HMAC with public key -> 401; invalid signature -> 401; token within 30s clock skew window -> accepted (200). |
| **AUTH-04** | Demo issuer & throttling | `cmd/demo-issuer/main_test.go` | Assert logins for `developer`, `finance`, `application-admin` yield valid signed tokens; >20 logins/min from same IP yields 429 Too Many Requests. |
| **POL-01** | In-memory OPA evaluation | `internal/policy/engine_test.go` | `BenchmarkOPAEval`: Assert evaluation of prepared query takes <0.2ms per op; verify zero network calls during evaluation. |
| **POL-02** | Default-deny authorization | `internal/policy/engine_test.go` | Assert unmapped routes or evaluation errors fail closed (`allow: false`, reason code `DENIED_DEFAULT`). |
| **POL-03** | RBAC matrix enforcement | `tests/security/rbac_matrix_test.go` | Developer token: `GET /api/orders` -> 200, `GET /api/payments` -> 200, `POST /api/payments` -> 403, `GET /api/admin/users` -> 403 (`DENIED_DEVELOPER_ADMIN_FORBIDDEN`). Finance token: `POST /api/payments` -> 200, `GET /api/orders` -> 403. Admin token: `GET /api/admin/users` -> 200. |
| **POL-04** | Fail-closed on auth failure | `internal/proxy/proxy_test.go` | Assert policy engine never authorizes anonymous or unauthenticated requests to protected backend routes. |
| **REV-02** | Ingress concurrency bounds | `internal/proxy/limiter_test.go` | Flood gateway with concurrent requests exceeding capacity; assert excess requests immediately return 429 Too Many Requests without server crash. |
| **AUD-04** | Structured completion audit | `internal/audit/logger_test.go` | Intercept log output; assert every 200, 400, 401, 403 response emits structured JSON containing request ID, principal, route, decision, status, duration ms. |
| **BYP-01** | Private backend isolation | `tests/integration/mvp_test.go` | From test runner (host): `curl http://localhost:8081` fails (connection refused); `curl http://localhost:8080/api/orders` via gateway succeeds (200). |
</validation_architecture>

<security_domain>
## Security Domain

### Applicable OWASP ASVS 4.0 Categories
- **V1: Architecture, Design and Threat Modeling (V1.1, V1.4)**: Default-deny across all routes (Invariant 1); zero-repair path rejection (Invariant 7).
- **V2: Authentication (V2.1, V2.8)**: Pinned JWT algorithm allowlists (RS256/ES256/EdDSA); rejection of `alg: none` and symmetric key substitution; issuer, audience, and expiration validation with 30s clock skew.
- **V4: Access Control (V4.1, V4.2, V4.3)**: In-memory declarative policy enforcement (OPA); role-based access control matrix (developer, finance, admin); path traversal protection.
- **V5: Validation, Sanitization and Encoding (V5.1)**: Zero-repair path validation rejecting `..`, `%2f`, `%5c`, `//`, and `%00`.
- **V8: Data Protection (V8.3)**: Unconditional stripping of incoming client `X-Aegis-*` and forwarding headers; credentials stripped before backend forward.
- **V13: API and Web Service (V13.1)**: Enforced listener limits (16 KiB headers, 1 MiB body); unauthenticated ingress concurrency throttling.
- **V14: Configuration (V14.1, V14.2)**: Backend microservices published on isolated Docker bridge network with zero host ports.

### STRIDE Threat Model & Mitigations for Phase 1

| Threat Category | Specific Threat Scenario | Mitigation Mechanism | Verification Assertion |
|-----------------|---------------------------|----------------------|------------------------|
| **Spoofing (S)** | Client sends `X-Aegis-User: admin` or `Forwarded: for=127.0.0.1`. | `httputil.ReverseProxy.Rewrite` unconditionally strips all `X-Aegis-*` and forwarding headers. Identity is derived strictly from verified JWT claims. | Negative test verifying backend never sees injected headers. |
| **Spoofing (S)** | Client presents token with `alg: none` or symmetric HMAC signature. | JWT validator enforces pinned algorithm allowlist (`EdDSA`, `RS256`, `ES256`). Rejects `none` and symmetric keys. | Test sending `alg: none` token returns HTTP 401. |
| **Tampering (T)** | Path traversal (`/api/orders/..%2fadmin/users`) to reach admin service. | Zero-repair path validator rejects dot segments, `%2f`, `%5c`, `//`, `%00` immediately with HTTP 400. No `path.Clean()` repair. | Fuzz test table asserting HTTP 400 on all traversal variants. |
| **Tampering (T)** | Client supplies `Host: evil.com` or absolute URI to trigger SSRF. | Upstream router maps strictly to fixed backend URLs declared in `routes.json`. Client `Host` header ignored for dispatch. | Test sending arbitrary `Host` header reaches configured target only. |
| **Repudiation (R)** | Malicious access attempts or errors go unrecorded. | Structured completion audit logging (AUD-04) logs 100% of responses (200, 400, 401, 403, 502) with duration and reason code. | Test verifying audit logs capture all requests. |
| **Information Disclosure (I)** | Upstream errors leak backend stack traces or internal IP addresses. | Reverse proxy error handler intercepts backend connection errors and formats sanitized RFC 7807 problem details. | Negative test triggering upstream 502 inspecting sanitized response body. |
| **Denial of Service (D)** | Unauthenticated flood exhausts CPU via cryptographic token parsing. | Concurrency limiter bounds unauthenticated in-flight requests before identity parsing, returning 429. | Concurrency flood test asserting fast 429 rejection. |
| **Denial of Service (D)** | Slowloris or oversized header attack exhausts server memory. | `http.Server` configured with `MaxHeaderBytes: 16 KiB`, `ReadHeaderTimeout: 5s`, `MaxBytesReader: 1 MiB`. | Test sending 32 KiB header returns 431/400. |
| **Elevation of Privilege (E)** | User with `developer` role accesses `/api/admin/users`. | Embedded OPA policy evaluates in-memory against `authz.rego`, yielding `allow: false` and `DENIED_DEVELOPER_ADMIN_FORBIDDEN`. | Negative RBAC test asserting HTTP 403 Forbidden. |
| **Elevation of Privilege (E)** | Client accesses backend microservices directly, bypassing gateway. | Backend microservices publish zero host ports in Docker Compose (BYP-01). | Test attempting `curl http://localhost:8081` fails with connection refused. |
</security_domain>

<sources>
## Sources

### Primary (HIGH confidence)
- **Go Standard Library Documentation**: `net/http/httputil.ReverseProxy` and `ProxyRequest.Rewrite` API specification — https://pkg.go.dev/net/http/httputil#ReverseProxy
- **Open Policy Agent (OPA) Documentation**: *Go SDK Integration and Precompiled Query Evaluation* (`rego.PrepareForEval`) — https://www.openpolicyagent.org/docs/latest/integration/#integrating-with-go
- **RFC 8725**: *JSON Web Token Best Current Practices* (Algorithm allowlists, key confusion prevention, clock skew recommendations) — https://www.rfc-editor.org/rfc/rfc8725.html
- **RFC 7230 / RFC 9112**: *HTTP/1.1 Message Syntax and Routing* (Hop-by-hop header removal requirements)
- **Aegis Architectural Specification & Threat Model**: `spec.md`, `docs/threat-model/threat-model.md`, `docs/adr/0001-reverse-proxy-architecture.md`, `docs/adr/0002-in-memory-opa-embedding.md`

### Secondary (MEDIUM confidence)
- **OWASP Application Security Verification Standard (ASVS) 4.0**: V1, V2, V4, V5, V8, V13, V14 requirements.
- **CWE-22 & CWE-444**: *Path Traversal* and *Inconsistent Interpretation of HTTP Requests* prevention strategies.

### Tertiary (LOW confidence)
- None — all architectural patterns, dependencies, and commands verified directly against local toolchains and project artifacts.
</sources>

<metadata>
## Metadata

**Research scope:**
- Core technologies: Go `net/http`, `httputil.ReverseProxy.Rewrite`, embedded OPA SDK (`v1.21.1`), `golang-jwt/jwt/v5` (`v5.3.1`)
- Security patterns: Zero-repair path canonicalization, ingress header scrubbing, RBAC matrix enforcement, unauthenticated concurrency limits, Docker network isolation
- Requirements covered: GW-01, GW-02, GW-03, GW-04, AUTH-01, AUTH-04, POL-01, POL-02, POL-03, POL-04, REV-02, AUD-04, BYP-01

**Confidence breakdown:**
- Standard stack: HIGH — verified against installed Go 1.25.5, OPA v1.21.1, and package repositories
- Architecture patterns: HIGH — derived from verified ADR-0001, ADR-0002, and Go 1.20+ ReverseProxy specifications
- Common pitfalls: HIGH — grounded in verified CWE patterns and Go HTTP parser behaviors
- Code examples: HIGH — verified against Go stdlib and OPA APIs
- Validation architecture: HIGH — verified using local OPA CLI and Go test runners

**Research date:** 2026-10-06  
**Valid until:** 2026-11-06 (30 days)
</metadata>

---
*Phase: 01-mvp-secure-vertical-slice*  
*Research completed: 2026-10-06*  
*Ready for planning: yes*
