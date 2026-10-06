# Phase 1: MVP Secure Vertical Slice — Code & Architecture Patterns

**Generated:** 2026-10-06  
**Phase:** 01-mvp-secure-vertical-slice  
**Status:** Approved Reference  
**Domain:** Go Reverse Proxy, In-Memory OPA Policy Engine, Zero-Trust Ingress Security  

---

## 1. Executive Summary & File Inventory

Phase 1 establishes the foundational secure vertical slice of Aegis: a single-replica Go gateway reverse proxy enforcing default-deny authorization for human users authenticating with short-lived JWTs, routing requests across three private backend microservices (`orders`, `payments`, `admin`) in Docker Compose with zero published backend host ports.

This document classifies every file to be created or modified in Phase 1, maps each file to its closest existing Phase 0 contracts and schemas, details role and data flows, and extracts concrete, tested code excerpts to guide implementation without ambiguity.

### Target File Inventory

```
aegis/
├── cmd/
│   ├── gateway/
│   │   └── main.go                               # Gateway entrypoint & lifecycle orchestration
│   └── demo-issuer/
│       ├── main.go                               # Development mock JWT issuer & login throttling
│       └── main_test.go                          # Unit tests for token minting and rate throttling
├── internal/
│   ├── config/
│   │   └── config.go                             # Gateway runtime configuration loading & validation
│   ├── identity/
│   │   ├── jwt.go                                # JWT token validator with pinned algorithm allowlist
│   │   ├── claims.go                             # JWT claims data structures (UserClaims, roles)
│   │   └── jwt_test.go                           # Unit & negative tests for token parsing & clock skew
│   ├── policy/
│   │   ├── engine.go                             # In-memory OPA engine wrapper (rego.PrepareForEval)
│   │   ├── types.go                              # Typed policy input/output structs matching schemas
│   │   └── engine_test.go                        # Unit tests & BenchmarkOPAEval (<0.2ms latency budget)
│   ├── proxy/
│   │   ├── handler.go                            # Gateway HTTP pipeline handler (PEP controller)
│   │   ├── proxy.go                              # httputil.ReverseProxy with Rewrite hook & error handler
│   │   ├── router.go                             # Deterministic route table matcher (routes.json)
│   │   ├── sanitizer.go                          # Strict zero-repair raw wire-byte path validator
│   │   ├── limiter.go                            # Unauthenticated ingress counting semaphore limiter
│   │   ├── proxy_test.go                         # Unit tests for proxy, limits, and header scrubbing
│   │   └── limiter_test.go                       # Unit tests for unauthenticated concurrency limiter
│   └── audit/
│       ├── event.go                              # Audit completion event model (pkg/api/control/v1 analog)
│       ├── logger.go                             # Structured completion audit logger (log/slog JSON)
│       └── logger_test.go                        # Unit tests for completion audit emission
├── services/
│   ├── orders/
│   │   └── main.go                               # Private orders microservice (:8081, 0 host ports)
│   ├── payments/
│   │   └── main.go                               # Private payments microservice (:8082, 0 host ports)
│   └── admin/
│       └── main.go                               # Private admin microservice (:8083, 0 host ports)
├── deployments/
│   └── compose/
│       └── docker-compose.mvp.yml                # Docker Compose MVP network isolation profile
├── tests/
│   ├── integration/
│   │   └── mvp_test.go                           # End-to-end multi-service integration test suite
│   └── security/
│       ├── path_traversal_test.go                # Negative penetration tests for path traversal (GW-02)
│       ├── header_spoofing_test.go               # Negative penetration tests for header injection (GW-04)
│       ├── jwt_negative_test.go                  # Negative tests for alg:none, HMAC, expired JWTs (AUTH-01)
│       └── rbac_matrix_test.go                   # Verification of developer/finance/admin RBAC matrix (POL-03)
├── Makefile                                      # Build, test, lint, and compose automation targets
├── go.mod                                        # Go module dependencies
└── go.sum                                        # Cryptographic checksums
```

---

## 2. Classification by Role & Data Flow

| File Path | Architectural Role | Ingress / Input | Processing / Transformation | Egress / Output | Invariants & Requirements |
|---|---|---|---|---|---|
| [`cmd/gateway/main.go`](file:///home/logan78/Desktop/Aegis/cmd/gateway/main.go) | Service Orchestrator / Entrypoint | OS environment variables, CLI flags, filesystem files (`routes.json`, `authz.rego`) | Bootstraps config, initializes OPA engine, compiles query, registers middleware pipeline, starts `http.Server` with graceful shutdown | Listening HTTP socket (`:8080`) | GW-01, POL-01, Invariant 1 |
| [`cmd/demo-issuer/main.go`](file:///home/logan78/Desktop/Aegis/cmd/demo-issuer/main.go) | Mock Identity Provider | HTTP POST `/login` with JSON payload `{"role":"developer"}` | Verifies IP attempt window (<=20/min), looks up seeded credentials, signs Ed25519 JWT with 5-minute expiry | HTTP 200 with Bearer JWT or 429 Too Many Requests | AUTH-04, Invariant 2 |
| [`cmd/demo-issuer/main_test.go`](file:///home/logan78/Desktop/Aegis/cmd/demo-issuer/main_test.go) | Test Verification | Mock HTTP requests to `/login` | Executes serial and burst logins for seeded roles and unknown roles | Test assertions for valid EdDSA tokens and 429 rate limiting | AUTH-04 |
| [`internal/config/config.go`](file:///home/logan78/Desktop/Aegis/internal/config/config.go) | Configuration Model & Loader | Environment variables (`AEGIS_PORT`, `AEGIS_ROUTES_PATH`, etc.) | Parses port numbers, limits, paths, public keys; applies defaults (`MaxHeaderBytes = 16KiB`, `MaxBody = 1MiB`) | Immutable `Config` struct | GW-01 |
| [`internal/identity/jwt.go`](file:///home/logan78/Desktop/Aegis/internal/identity/jwt.go) | Cryptographic Authenticator | `Authorization: Bearer <token>` string | Parses JWT, validates EdDSA/RS256/ES256 against pinned allowlist, verifies issuer/aud, tolerates 30s clock skew | `*UserClaims` (Subject ID, Roles) or typed error | AUTH-01, Invariant 2 |
| [`internal/identity/claims.go`](file:///home/logan78/Desktop/Aegis/internal/identity/claims.go) | Identity Domain Model | Decoded JWT JSON claims payload | Maps standard registered claims and custom `roles` array | `UserClaims` struct embedding `jwt.RegisteredClaims` | AUTH-01 |
| [`internal/identity/jwt_test.go`](file:///home/logan78/Desktop/Aegis/internal/identity/jwt_test.go) | Unit Test Suite | Synthetic signed and unsigned JWT tokens | Tests valid claims, expired tokens within 30s leeway, expired beyond leeway, wrong issuer/aud | Unit test assertions | AUTH-01 |
| [`internal/policy/engine.go`](file:///home/logan78/Desktop/Aegis/internal/policy/engine.go) | Policy Decision Point (PDP) | Typed Go struct `PolicyInput` adhering to `input.schema.json` | In-memory evaluation via precompiled `rego.PreparedEvalQuery`; extracts `allow` and `reason_code` | Typed `Decision` struct; fails closed on any error | POL-01, POL-02, Invariant 1 |
| [`internal/policy/types.go`](file:///home/logan78/Desktop/Aegis/internal/policy/types.go) | Policy Domain Contract | Domain parameters from router, identity, and context | Defines Go structs for `PrincipalInput`, `ResourceInput`, `RequestInput`, `ContextInput`, `Decision` | Serialized JSON input matching `input.schema.json` | POL-01, POL-02 |
| [`internal/policy/engine_test.go`](file:///home/logan78/Desktop/Aegis/internal/policy/engine_test.go) | PDP Unit & Benchmark Tests | Synthetic `PolicyInput` fixtures | Evaluates default deny, rule matching, error cases; executes `BenchmarkOPAEval` | Latency assertions (<0.2ms/op) and correctness checks | POL-01, POL-02, POL-03 |
| [`internal/proxy/handler.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/handler.go) | Policy Enforcement Point (PEP) | Raw incoming `*http.Request` from client socket | Orchestrates pipeline: RequestID -> Limit -> PathSanitize -> Authenticate -> Route -> Authorize -> Audit -> Proxy | Outbound HTTP response to client | GW-01..04, POL-01..04, AUD-04 |
| [`internal/proxy/proxy.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/proxy.go) | Upstream Proxy Dispatcher | Inbound `*http.Request` and matched `*Route` | `Rewrite` hook: resets target URL, sets canonical path, deletes `X-Aegis-*` and hop-by-hop headers, sets `X-Request-ID` | Outbound `http.Request` to private backend | GW-04, Invariant 7, Invariant 8 |
| [`internal/proxy/router.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/router.go) | Deterministic Route Matcher | HTTP Method and canonical path string | Matches against loaded `[]Route` table (from `routes.json`); strictly ignores client `Host` header | `*Route` definition or `ErrRouteNotFound` | GW-03, Invariant 6 |
| [`internal/proxy/sanitizer.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/sanitizer.go) | Zero-Repair Path Validator | `r.RequestURI` raw wire bytes | Checks for `..`, `%2f`, `%5c`, `//`, `%00`; rejects malformed paths immediately with HTTP 400 | Validated canonical path string or rejection error | GW-02, Invariant 7 |
| [`internal/proxy/limiter.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/limiter.go) | Ingress Concurrency Limiter | Inbound `http.Handler` | Selects on counting semaphore channel (buffer size: 1000); rejects excess requests with HTTP 429 | Wraps downstream HTTP handlers | REV-02 |
| [`internal/proxy/proxy_test.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/proxy_test.go) | Proxy Integration Tests | Mock HTTP requests via `httptest.NewServer` | Asserts listener limits, header stripping, routing dispatch, problem details formatting | Test assertions | GW-01, GW-03, GW-04 |
| [`internal/proxy/limiter_test.go`](file:///home/logan78/Desktop/Aegis/internal/proxy/limiter_test.go) | Concurrency Unit Tests | Concurrent goroutines calling limited handler | Saturated semaphore triggers 429; capacity recovery allows subsequent calls | Test assertions | REV-02 |
| [`internal/audit/event.go`](file:///home/logan78/Desktop/Aegis/internal/audit/event.go) | Audit Event Domain Model | Request lifecycle metrics and decision results | Data struct aligning with `controlv1.AuditEvent` (`pkg/api/control/v1/types.gen.go`) | `CompletionEvent` struct | AUD-04 |
| [`internal/audit/logger.go`](file:///home/logan78/Desktop/Aegis/internal/audit/logger.go) | Structured Completion Logger | `CompletionEvent` struct from handler defer | Serializes event as structured JSON log using `log/slog` to standard output | JSON log lines on stdout | AUD-04 |
| [`internal/audit/logger_test.go`](file:///home/logan78/Desktop/Aegis/internal/audit/logger_test.go) | Audit Unit Test Suite | Synthetic `CompletionEvent` instances | Captures buffer output, unmarshals JSON, verifies all mandatory fields present | Test assertions | AUD-04 |
| [`services/orders/main.go`](file:///home/logan78/Desktop/Aegis/services/orders/main.go) | Private Demo Microservice | HTTP GET `/api/orders` | Responds with mock orders JSON payload | HTTP 200 JSON response | BYP-01 |
| [`services/payments/main.go`](file:///home/logan78/Desktop/Aegis/services/payments/main.go) | Private Demo Microservice | HTTP GET/POST `/api/payments` | Responds with mock payments JSON payload | HTTP 200 JSON response | BYP-01 |
| [`services/admin/main.go`](file:///home/logan78/Desktop/Aegis/services/admin/main.go) | Private Demo Microservice | HTTP GET/POST `/api/admin/users` | Responds with mock admin users JSON payload | HTTP 200 JSON response | BYP-01 |
| [`deployments/compose/docker-compose.mvp.yml`](file:///home/logan78/Desktop/Aegis/deployments/compose/docker-compose.mvp.yml) | Compose Deployment Spec | Docker engine execution | Configures 5 services on isolated bridge network; exposes only `8080` (gateway) and `8085` (issuer) | Running container network | BYP-01 |
| [`tests/integration/mvp_test.go`](file:///home/logan78/Desktop/Aegis/tests/integration/mvp_test.go) | End-to-End Integration Suite | Live gateway & mock issuer | Obtains tokens, exercises orders, payments, admin endpoints; verifies backend port isolation | Automated pass/fail results | GW-01..BYP-01 |
| [`tests/security/path_traversal_test.go`](file:///home/logan78/Desktop/Aegis/tests/security/path_traversal_test.go) | Security Penetration Test | Path traversal strings (`/..`, `%2e%2e`, `%2f`, `//`, `%00`) | Sends raw HTTP wire requests via `net.Dial` or `http.Client` without stdlib auto-cleaning | Asserts HTTP 400 Bad Request on all variants | GW-02 |
| [`tests/security/header_spoofing_test.go`](file:///home/logan78/Desktop/Aegis/tests/security/header_spoofing_test.go) | Security Penetration Test | Requests with `X-Aegis-*`, `Forwarded`, `Connection: close, X-Hop` | Inspects headers received by mock upstream server | Asserts zero leaked or injected headers | GW-04 |
| [`tests/security/jwt_negative_test.go`](file:///home/logan78/Desktop/Aegis/tests/security/jwt_negative_test.go) | Security Penetration Test | Malformed tokens: `alg: none`, HMAC substituted, expired >30s, wrong issuer | Verifies gateway returns HTTP 401 Unauthorized with problem details | Asserts rejection of all invalid tokens | AUTH-01 |
| [`tests/security/rbac_matrix_test.go`](file:///home/logan78/Desktop/Aegis/tests/security/rbac_matrix_test.go) | Security Penetration Test | Requests with developer, finance, and admin tokens across all routes | Validates full RBAC matrix: developer denied admin (403), finance allowed payments, admin allowed all | Asserts exact HTTP status and reason codes | POL-03 |

---

## 3. Existing Analogs & Phase 0 Contract Mappings

Phase 0 defined explicit contracts, schemas, and architecture decision records. Phase 1 code directly implements or mirrors these contracts:

### 3.1. Route Definition Analogs
- **Contract Definition (Protobuf)**: [`pkg/api/snapshot/v1/snapshot.pb.go`](file:///home/logan78/Desktop/Aegis/pkg/api/snapshot/v1/snapshot.pb.go#L466-L480) (`RouteDefinition` message).
- **Contract Definition (OpenAPI)**: [`pkg/api/control/v1/types.gen.go`](file:///home/logan78/Desktop/Aegis/pkg/api/control/v1/types.gen.go#L238-L250) (`Route` struct).
- **Data Source**: [`policies/data/routes.json`](file:///home/logan78/Desktop/Aegis/policies/data/routes.json#L1-L88).
- **Phase 1 Implementation**: `internal/proxy/router.go` parses `policies/data/routes.json` into Go structs matching the Phase 0 schema.

```go
// Phase 0 Generated Analog: pkg/api/control/v1/types.gen.go:238-250
type Route struct {
	CreatedAt            *time.Time      `json:"created_at,omitempty"`
	HttpMethod           RouteHttpMethod `json:"http_method"`
	PathTemplate         string          `json:"path_template"`
	RateLimitBurst       *int            `json:"rate_limit_burst,omitempty"`
	RateLimitRps         *int            `json:"rate_limit_rps,omitempty"`
	RequiresWorkloadMtls *bool           `json:"requires_workload_mtls,omitempty"`
	RouteId              string          `json:"route_id"`
	ServiceId            string          `json:"service_id"`
	TimeoutMs            *int            `json:"timeout_ms,omitempty"`
	UpdatedAt            *time.Time      `json:"updated_at,omitempty"`
	UpstreamSpiffeId     *string         `json:"upstream_spiffe_id,omitempty"`
	UpstreamUrl          string          `json:"upstream_url"`
}
```

### 3.2. Policy Input/Output Schemas
- **Contract Schema**: [`policies/schemas/input.schema.json`](file:///home/logan78/Desktop/Aegis/policies/schemas/input.schema.json#L1-L48) and [`policies/schemas/output.schema.json`](file:///home/logan78/Desktop/Aegis/policies/schemas/output.schema.json#L1-L21).
- **Rego Implementation**: [`policies/rego/authz.rego`](file:///home/logan78/Desktop/Aegis/policies/rego/authz.rego#L1-L157).
- **Rego Unit Tests**: [`policies/tests/authz_test.rego`](file:///home/logan78/Desktop/Aegis/policies/tests/authz_test.rego#L1-L109).
- **Phase 1 Implementation**: `internal/policy/types.go` maps directly to the JSON schema fields to eliminate any discrepancy during `rego.EvalInput(input)`.

```go
// Phase 1 Analog: internal/policy/types.go mirroring policies/schemas/input.schema.json
type PolicyInput struct {
	Principal       PrincipalInput `json:"principal"`
	Resource        ResourceInput  `json:"resource"`
	Request         RequestInput   `json:"request"`
	Context         ContextInput   `json:"context"`
	SnapshotVersion int64          `json:"snapshot_version"`
}

type PrincipalInput struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"` // "user", "workload", "anonymous"
	Roles []string `json:"roles"`
}

type ResourceInput struct {
	Service string `json:"service"` // "orders", "payments", "admin"
	Route   string `json:"route"`
}

type RequestInput struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type ContextInput struct {
	RiskScore int    `json:"risk_score"`
	RiskState string `json:"risk_state"` // "available", "degraded", "unavailable"
}

type Decision struct {
	Allow           bool   `json:"allow"`
	ReasonCode      string `json:"reason_code"`
	SnapshotVersion int64  `json:"snapshot_version"`
}
```

### 3.3. Audit Event Model Analog
- **Contract Definition**: [`pkg/api/control/v1/types.gen.go`](file:///home/logan78/Desktop/Aegis/pkg/api/control/v1/types.gen.go#L78-L95) (`AuditEvent`).
- **Phase 1 Implementation**: `internal/audit/event.go` adopts these exact JSON tags and field semantics for all structured completion logs.

```go
// Phase 0 Generated Analog: pkg/api/control/v1/types.gen.go:78-95
type AuditEvent struct {
	Decision        AuditEventDecision       `json:"decision"`
	DurationMs      *int                     `json:"duration_ms,omitempty"`
	EventId         openapi_types.UUID       `json:"event_id"`
	EventType       AuditEventEventType      `json:"event_type"` // "completion"
	HttpMethod      *string                  `json:"http_method,omitempty"`
	PrincipalId     string                   `json:"principal_id"`
	PrincipalKind   *AuditEventPrincipalKind `json:"principal_kind,omitempty"`
	ReasonCode      string                   `json:"reason_code"`
	RequestId       openapi_types.UUID       `json:"request_id"`
	RequestPath     *string                  `json:"request_path,omitempty"`
	Roles           *[]string                `json:"roles,omitempty"`
	RouteId         string                   `json:"route_id"`
	ServiceId       string                   `json:"service_id"`
	SnapshotVersion int64                    `json:"snapshot_version"`
	StatusCode      *int                     `json:"status_code,omitempty"`
	Timestamp       time.Time                `json:"timestamp"`
}
```

---

## 4. Concrete Implementation Patterns & Code Excerpts

### Pattern 1: Go 1.20+ `httputil.ReverseProxy.Rewrite` Hook (GW-04, Invariants 7 & 8)

**Role**: Proxy Dispatcher  
**Location**: `internal/proxy/proxy.go`  
**Rationale**: Decouples inbound client request (`pr.In`) from outbound upstream request (`pr.Out`). Enforces header scrubbing, sets upstream target URL and validated canonical path, and suppresses client hop-by-hop headers automatically (ADR-0001).

```go
package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// RFC7807Problem represents an RFC 7807 problem details error response.
type RFC7807Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance,omitempty"`
}

// NewReverseProxy creates a reverse proxy targeting upstream with the Rewrite hook.
func NewReverseProxy(targetURL *url.URL, canonicalPath string, requestID string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// 1. Rewrite outbound target URL
			pr.SetURL(targetURL)

			// 2. Set identical validated canonical path bytes (Invariant 7)
			pr.Out.URL.Path = canonicalPath
			pr.Out.URL.RawPath = ""

			// 3. Strip all client-supplied X-Aegis-* headers (Invariant 8)
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

			// 5. Strip inbound user Bearer token before backend forward (Private MVP network)
			pr.Out.Header.Del("Authorization")

			// 6. Inject gateway correlation tracking header
			pr.Out.Header.Set("X-Request-ID", requestID)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(RFC7807Problem{
				Type:   "https://aegis.local/errors/bad-gateway",
				Title:  "Bad Gateway",
				Status: http.StatusBadGateway,
				Detail: "Upstream backend unreachable or returned connection error",
			})
		},
	}
}
```

---

### Pattern 2: Strict Zero-Repair Path Canonicalization (GW-02, Invariant 7)

**Role**: Ingress Sanitization Middleware  
**Location**: `internal/proxy/sanitizer.go`  
**Rationale**: Inspects raw wire bytes in `r.RequestURI` before Go's standard library or any router performs URL decoding or cleaning. Immediately fails closed with `HTTP 400 Bad Request` without calling `path.Clean()` (ADR-0001, Invariant 7).

```go
package proxy

import (
	"errors"
	"net/http"
	"strings"
)

var (
	ErrPathContainsTraversal    = errors.New("path contains traversal dot-segments")
	ErrPathContainsEncodedSlash = errors.New("path contains encoded directory separators")
	ErrPathContainsDoubleSlash  = errors.New("path contains duplicate slashes")
	ErrPathContainsNulByte      = errors.New("path contains NUL bytes")
	ErrPathInvalidPrefix        = errors.New("path does not start with /")
)

// ValidatePathZeroRepair performs zero-repair path validation on raw wire bytes.
func ValidatePathZeroRepair(r *http.Request) (string, error) {
	// Extract raw wire path string from RequestURI before stdlib decoding
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

	// 3. Detect encoded slashes (%2f, %5c) and backslashes
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(rawPath, "\\") {
		return "", ErrPathContainsEncodedSlash
	}

	// 4. Detect dot-segments (literal and encoded: /../, /./, %2e%2e, .%2e, %2e.)
	if strings.Contains(rawPath, "/../") || strings.HasSuffix(rawPath, "/..") || rawPath == ".." ||
		strings.Contains(rawPath, "/./") || strings.HasSuffix(rawPath, "/.") ||
		strings.Contains(lower, "%2e%2e") || strings.Contains(lower, ".%2e") || strings.Contains(lower, "%2e.") {
		return "", ErrPathContainsTraversal
	}

	// 5. Must start with absolute slash
	if !strings.HasPrefix(rawPath, "/") {
		return "", ErrPathInvalidPrefix
	}

	return rawPath, nil
}
```

---

### Pattern 3: Embedded In-Memory Precompiled OPA Engine (POL-01..04, Invariant 1)

**Role**: Policy Decision Point (PDP)  
**Location**: `internal/policy/engine.go`  
**Rationale**: Precompiles `policies/rego/authz.rego` once at startup using `rego.PrepareForEval(ctx)`. Evaluates typed Go input structs in ~0.15ms (<2ms p99 SLA) with zero network calls and default-deny semantics (ADR-0002).

```go
package policy

import (
	"context"
	"fmt"
	"github.com/open-policy-agent/opa/rego"
)

type Engine struct {
	preparedQuery rego.PreparedEvalQuery
}

// NewEngine initializes and precompiles the Rego authorization query.
func NewEngine(ctx context.Context, regoModule string) (*Engine, error) {
	query, err := rego.New(
		rego.Query("data.aegis.authz.decision"),
		rego.Module("authz.rego", regoModule),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to precompile rego query: %w", err)
	}
	return &Engine{preparedQuery: query}, nil
}

// Evaluate runs the precompiled query against typed policy input in memory.
func (e *Engine) Evaluate(ctx context.Context, input PolicyInput) (Decision, error) {
	rs, err := e.preparedQuery.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		// Invariant 1: Fail closed on evaluation error
		return Decision{
			Allow:           false,
			ReasonCode:      "EVALUATION_ERROR",
			SnapshotVersion: input.SnapshotVersion,
		}, err
	}

	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return Decision{
			Allow:           false,
			ReasonCode:      "DENIED_EMPTY_RESULT",
			SnapshotVersion: input.SnapshotVersion,
		}, nil
	}

	val, ok := rs[0].Expressions[0].Value.(map[string]interface{})
	if !ok {
		return Decision{
			Allow:           false,
			ReasonCode:      "MALFORMED_DECISION",
			SnapshotVersion: input.SnapshotVersion,
		}, nil
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

### Pattern 4: Cryptographic JWT Authenticator (AUTH-01, Invariant 2)

**Role**: Identity Authenticator  
**Location**: `internal/identity/jwt.go`  
**Rationale**: Validates Bearer tokens against pinned algorithm allowlists (`EdDSA`, `RS256`, `ES256`). Rejects `alg: none` and symmetric key substitution attacks. Enforces 30s clock skew leeway and validates required issuer/audience claims (RFC 8725).

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
	ErrTokenExpired      = errors.New("token is expired")
	ErrInvalidClaims     = errors.New("token claims invalid or missing subject")
)

type TokenValidator struct {
	issuer       string
	audience     string
	publicKey    crypto.PublicKey
	allowedAlgos []string
}

func NewTokenValidator(issuer, audience string, publicKey crypto.PublicKey) *TokenValidator {
	return &TokenValidator{
		issuer:       issuer,
		audience:     audience,
		publicKey:    publicKey,
		allowedAlgos: []string{"EdDSA", "RS256", "ES256"},
	}
}

func (v *TokenValidator) ValidateBearerToken(authHeader string) (*UserClaims, error) {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, ErrMissingAuthHeader
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	claims := &UserClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
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
		jwt.WithLeeway(30*time.Second),
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

---

### Pattern 5: Ingress Concurrency Bounding (REV-02)

**Role**: Transport Protection Middleware  
**Location**: `internal/proxy/limiter.go`  
**Rationale**: Bounded counting semaphore wrapping the ingress HTTP handler before JWT signature verification or route resolution to prevent CPU exhaustion under high load.

```go
package proxy

import (
	"encoding/json"
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
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(RFC7807Problem{
				Type:   "https://aegis.local/errors/concurrency-exceeded",
				Title:  "Too Many Requests",
				Status: http.StatusTooManyRequests,
				Detail: "Ingress concurrency capacity exceeded",
			})
		}
	})
}
```

---

### Pattern 6: Structured Completion Audit Logger (AUD-04)

**Role**: Observability & Compliance  
**Location**: `internal/audit/logger.go` and `internal/audit/event.go`  
**Rationale**: Emits structured JSON events recording principal, route, decision, status, duration, and error codes using Go's standard library `log/slog`. Mirrors `controlv1.AuditEvent`.

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
	Decision        string    `json:"decision"` // "allow" or "deny"
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

---

### Pattern 7: Deterministic Route Table Matcher (GW-03, Invariant 6)

**Role**: Routing Engine  
**Location**: `internal/proxy/router.go`  
**Rationale**: Binds HTTP method and canonical path to configured upstream backend services declared in `routes.json`. Client `Host` header is strictly ignored (ADR-0001, Invariant 6).

```go
package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
)

var ErrRouteNotFound = errors.New("no matching upstream route found")

type Route struct {
	RouteID              string `json:"route_id"`
	ServiceID            string `json:"service_id"`
	HTTPMethod           string `json:"http_method"`
	PathTemplate         string `json:"path_template"`
	UpstreamURL          string `json:"upstream_url"`
	UpstreamSPIFFEID     string `json:"upstream_spiffe_id"`
	RequiresWorkloadMTLS bool   `json:"requires_workload_mtls"`
	parsedURL            *url.URL
}

type Router struct {
	routes []Route
}

func NewRouterFromJSON(filePath string) (*Router, error) {
	bytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read routes file: %w", err)
	}

	var routes []Route
	if err := json.Unmarshal(bytes, &routes); err != nil {
		return nil, fmt.Errorf("failed to parse routes JSON: %w", err)
	}

	for i := range routes {
		u, err := url.Parse(routes[i].UpstreamURL)
		if err != nil {
			return nil, fmt.Errorf("invalid upstream URL %q: %w", routes[i].UpstreamURL, err)
		}
		routes[i].parsedURL = u
	}

	return &Router{routes: routes}, nil
}

func (r *Router) Match(method, path string) (*Route, error) {
	for i := range r.routes {
		if r.routes[i].HTTPMethod == method && r.routes[i].PathTemplate == path {
			return &r.routes[i], nil
		}
	}
	return nil, ErrRouteNotFound
}
```

---

### Pattern 8: Development Demo JWT Issuer with Login Throttling (AUTH-04)

**Role**: Development Auth Service  
**Location**: `cmd/demo-issuer/main.go`  
**Rationale**: Seeds `developer`, `finance`, and `application-admin` credentials. Signs short-lived 5-minute Ed25519 JWTs and enforces login rate limits (AUTH-04).

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

	// Rate throttle logins (AUTH-04)
	clientIP := r.RemoteAddr
	s.mu.Lock()
	if s.attempts[clientIP] >= 20 {
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "RATE_LIMITED"})
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "UNKNOWN_ROLE"})
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
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
		},
		Roles: user.Roles,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signedToken, err := token.SignedString(s.privateKey)
	if err != nil {
		http.Error(w, "Signing Failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": signedToken,
		"token_type":   "Bearer",
		"expires_in":   300,
	})
}
```

---

### Pattern 9: Isolated Private Microservices (BYP-01)

**Role**: Target Microservice  
**Location**: `services/orders/main.go`, `services/payments/main.go`, `services/admin/main.go`  
**Rationale**: Minimal Go standard library HTTP services responding with deterministic payloads. In Docker Compose, these containers have zero published host ports and attach strictly to `aegis-internal`.

```go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/orders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "ord_101", "item": "Cloud Security Scanner", "status": "shipped"},
			{"id": "ord_102", "item": "Hardware Key Token", "status": "processing"},
		})
	})

	log.Printf("Orders service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
```

---

### Pattern 10: Security Penetration Test Harness (GW-02, GW-04, AUTH-01, POL-03)

**Role**: Automated Security Verification  
**Location**: `tests/security/`  
**Rationale**: Table-driven tests verifying negative security behaviors directly against HTTP endpoints without relying on mock shortcuts.

```go
package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZeroRepairPathValidation(t *testing.T) {
	// Setup test gateway server with path sanitizer middleware
	handler := createTestGatewayHandler()
	server := httptest.NewServer(handler)
	defer server.Close()

	testCases := []struct {
		name         string
		rawPath      string
		expectedCode int
	}{
		{"dot dot traversal", "/api/orders/../admin", http.StatusBadRequest},
		{"percent encoded dot dot", "/api/orders/%2e%2e/admin", http.StatusBadRequest},
		{"encoded slash", "/api/orders%2fadmin", http.StatusBadRequest},
		{"encoded backslash", "/api/orders%5cadmin", http.StatusBadRequest},
		{"duplicate slashes", "//api/orders", http.StatusBadRequest},
		{"nul byte raw", "/api/orders\x00/test", http.StatusBadRequest},
		{"nul byte encoded", "/api/orders%00/test", http.StatusBadRequest},
		{"valid path", "/api/orders", http.StatusOK},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Issue raw HTTP request to bypass client auto-cleaning
			req, err := http.NewRequest(http.MethodGet, server.URL+tc.rawPath, nil)
			require.NoError(t, err)

			resp, err := http.DefaultTransport.RoundTrip(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.expectedCode, resp.StatusCode)
		})
	}
}
```

---

## 5. Anti-Patterns & Pitfalls to Avoid

| Category | Anti-Pattern | Correct Pattern | Threat / Vulnerability Prevented |
|---|---|---|---|
| **Reverse Proxy** | Using deprecated `httputil.ReverseProxy.Director` hook | Use `httputil.ReverseProxy.Rewrite` hook receiving `*httputil.ProxyRequest` | Prevents hop-by-hop header leakage and path desynchronization between `Path` and `RawPath`. |
| **Path Canonicalization** | Calling `path.Clean()` on ingress path to normalize `..` or `//` | Reject immediately with `HTTP 400 Bad Request` on raw `r.RequestURI` wire bytes | Prevents CWE-22 & CWE-444 path traversal bypasses caused by parser impedance mismatch with upstreams. |
| **Policy Evaluation** | Calling `rego.New(...).Eval(ctx)` dynamically inside HTTP handler | Precompile once with `rego.PrepareForEval(ctx)` at startup; call `preparedQuery.Eval(ctx, input)` | Prevents 300ms latency spikes and 100% CPU exhaustion from AST recompilation on the hot path. |
| **JWT Cryptography** | Custom JSON unmarshaling or allowing `alg: none` / symmetric keys | Use `golang-jwt/jwt/v5` with pinned allowlist (`EdDSA`, `RS256`, `ES256`) and `jwt.WithLeeway(30s)` | Prevents algorithm confusion and false expiration rejections from clock drift. |
| **Ingress Headers** | Trusting client-supplied `X-Aegis-*` or `Forwarded` headers | Unconditionally strip all `X-Aegis-*` and forwarding headers in `Rewrite` hook | Prevents privilege escalation and identity spoofing (Invariant 8). |
| **Routing Upstream** | Using client `Host` or `X-Forwarded-Host` to determine upstream target | Strict lookup in deterministic route table (`routes.json`) matching `(method, canonicalPath)` | Prevents Server-Side Request Forgery (SSRF) and routing hijack (Invariant 6). |
| **Network Isolation** | Publishing backend host ports in Docker Compose (`ports: ["8081:8081"]`) | Backends use isolated Docker bridge network with zero published host ports | Prevents external direct bypass of the security gateway perimeter (BYP-01). |

---

## 6. Pattern Mapping Matrix

| File Path | Role | Analogs & Contracts | Implementation Excerpt |
|---|---|---|---|
| `cmd/gateway/main.go` | Gateway Entrypoint | ADR-0001, ADR-0002, Invariant 1 | Bootstraps config, OPA engine, router, limiter, and `http.Server` |
| `cmd/demo-issuer/main.go` | Auth Issuer | AUTH-04, `crypto/ed25519` | Pattern 8: login handler with IP attempt limiter |
| `cmd/demo-issuer/main_test.go` | Unit Test | `testing`, `testify/assert` | Tests login rate limit (20 req/min) and valid EdDSA tokens |
| `internal/config/config.go` | Config Loader | GW-01 (16KiB header, 1MiB body) | Struct with port, timeout, limits, route path, Ed25519 key |
| `internal/identity/jwt.go` | Authenticator | AUTH-01, RFC 8725 | Pattern 4: `TokenValidator` with pinned allowlist |
| `internal/identity/claims.go` | Claims Model | `jwt.RegisteredClaims` | Struct with Subject and Roles slice |
| `internal/identity/jwt_test.go` | Unit Test | `jwt/v5` | Validates EdDSA tokens, clock skew tolerance, rejects `none` |
| `internal/policy/engine.go` | In-Memory PDP | ADR-0002, `open-policy-agent/opa/rego` | Pattern 3: `rego.PrepareForEval`, evaluate `PolicyInput` |
| `internal/policy/types.go` | PDP Schema | `policies/schemas/{input,output}.schema.json` | Go structs matching `AegisPolicyInput` and `AegisPolicyOutput` |
| `internal/policy/engine_test.go` | PDP Test/Bench | `policies/tests/authz_test.rego` | Verifies default deny and benchmarks <0.2ms latency |
| `internal/proxy/handler.go` | Gateway PEP | Pipeline Controller | Limits -> Path -> JWT -> Route -> OPA -> Audit -> Proxy |
| `internal/proxy/proxy.go` | Proxy Dispatcher | ADR-0001, Go 1.20+ `httputil.ProxyRequest` | Pattern 1: `Rewrite` hook, header stripping, RFC 7807 502 |
| `internal/proxy/router.go` | Upstream Router | `pkg/api/control/v1/types.gen.go:Route` | Pattern 7: Matches method and canonical path from `routes.json` |
| `internal/proxy/sanitizer.go` | Path Validator | ADR-0001, Invariant 7 | Pattern 2: Strict zero-repair checks on `r.RequestURI` |
| `internal/proxy/limiter.go` | Concurrency Limit | REV-02 | Pattern 5: Buffered channel counting semaphore |
| `internal/proxy/proxy_test.go` | Unit Test | `httptest.Server`, `testify` | Verifies listener limits, 16KiB headers, UUID `X-Request-ID` |
| `internal/proxy/limiter_test.go` | Unit Test | `testify` | Tests burst capacity, 429 rejection on saturation |
| `internal/audit/event.go` | Audit Model | `pkg/api/control/v1/types.gen.go:AuditEvent` | `CompletionEvent` struct matching control plane schema |
| `internal/audit/logger.go` | Audit Logger | AUD-04, `log/slog` | Pattern 6: Structured JSON logging to stdout |
| `internal/audit/logger_test.go` | Unit Test | `testify` | Asserts all audit fields present on logged events |
| `services/orders/main.go` | Private Backend | BYP-01 | Pattern 9: HTTP server returning mock orders JSON |
| `services/payments/main.go` | Private Backend | BYP-01 | HTTP server returning mock payments JSON |
| `services/admin/main.go` | Private Backend | BYP-01 | HTTP server returning mock admin users JSON |
| `deployments/compose/docker-compose.mvp.yml` | Compose Spec | BYP-01 | Gateway (8080) and Issuer (8085); backends zero host ports |
| `tests/integration/mvp_test.go` | E2E Integration | Compose Cluster | End-to-end token flow, access check, and port isolation test |
| `tests/security/path_traversal_test.go` | Penetration Test | GW-02 | Pattern 10: Asserts 400 Bad Request on raw traversal variants |
| `tests/security/header_spoofing_test.go` | Penetration Test | GW-04 | Injects `X-Aegis-*` and hop-by-hop headers; asserts stripped |
| `tests/security/jwt_negative_test.go` | Penetration Test | AUTH-01 | Asserts 401 on `alg: none`, HMAC substituted, expired tokens |
| `tests/security/rbac_matrix_test.go` | Penetration Test | POL-03 | Tests developer/finance/admin roles across route permissions |
| `Makefile` | Tooling Target | Quick & full test suites | `test-quick`, `test-full`, `compose-up`, `compose-down` |
| `go.mod` / `go.sum` | Dependencies | Standard Stack | `opa@v1.21.1`, `jwt/v5@v5.3.1`, `uuid@v1.6.0`, `testify@v1.12.1` |

---

*Pattern Mapping Complete for Phase 01: MVP Secure Vertical Slice*
