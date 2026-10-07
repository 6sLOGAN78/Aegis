# Phase 4: Operator Experience & Telemetry — Code & Architecture Patterns

**Generated:** 2026-10-07  
**Phase:** 04-operator-experience-telemetry  
**Status:** Approved Reference  
**Domain:** Control Plane REST APIs, Chi Routing, Session Security & CSRF Defense, Optimistic Concurrency (ETag / If-Match), Idempotency Deduplication, RBAC Authorization, In-Memory Rego Dry-Run Simulation, Monotonic Snapshot Publication & Rollback, Real-time Gateway Convergence Aggregation, Sub-5s Emergency Quarantine, Partitioned PostgreSQL Audit Querying, Low-Cardinality Prometheus Telemetry, Embedded React 19/Vite/Tailwind Operator Dashboard, Monaco Rego Editor with Monarch Tokenizer  

---

## 1. Executive Summary & File Inventory

Phase 4 completes the operational governance and observability pillars of Aegis. In a Zero-Trust Architecture (NIST SP 800-207), the data plane (PEP) must execute in isolation, while the management plane (PAP) provides robust, auditable control over policies, routes, and emergency security interventions. Pursuant to [REQUIREMENTS.md](file:///home/logan78/Desktop/Aegis/.planning/REQUIREMENTS.md), [04-RESEARCH.md](file:///home/logan78/Desktop/Aegis/.planning/phases/04-operator-experience-telemetry/04-RESEARCH.md), [04-UI-SPEC.md](file:///home/logan78/Desktop/Aegis/.planning/phases/04-operator-experience-telemetry/04-UI-SPEC.md), and [04-VALIDATION.md](file:///home/logan78/Desktop/Aegis/.planning/phases/04-operator-experience-telemetry/04-VALIDATION.md), Phase 4 implements:

1. **Control Plane REST Management APIs (`/control/v1`) (OPS-01, Invariant 11)**:
   Authoritative REST JSON management endpoints running on a dedicated administrative listener on port `:8084` using `github.com/go-chi/chi/v5`. Strict separation between data plane and control plane is enforced: data-plane credentials presented to `/control/v1` return `HTTP 403 Forbidden` (`DATA_PLANE_CREDENTIALS_REJECTED`).
2. **Optimistic Concurrency & Idempotency Controls (OPS-02)**:
   All mutating operations (`/publish`, `/rollback`, route creation/updates) enforce optimistic concurrency via RFC 7232 `ETag` and `If-Match` headers, rejecting stale edits with `HTTP 412 Precondition Failed`. Idempotency deduplication uses `Idempotency-Key` headers (UUID) with in-flight concurrency locks (`HTTP 409 Conflict`) and 24-hour cached response replay (`Idempotent-Replay: true`). Granular RBAC enforces `sec-ops`, `auditor`, and `viewer` roles.
3. **Operator Session Security & CSRF Defense (OPS-04)**:
   Browser dashboard sessions use `HttpOnly`, `SameSite=Strict`, `Secure` cookies (`aegis_session`). Mutating browser requests (`POST`, `PUT`, `DELETE`, `PATCH`) require a cryptographically random token in `X-CSRF-Token`, returning `HTTP 403 Forbidden` on mismatch. Strict Content Security Policy (`CSP`) headers restrict scripts and workers to `'self'` and `'blob:'` (for Monaco Editor), preventing CDN script injection.
4. **React 19 / TypeScript Operator Dashboard (`web/dashboard`) (OPS-03)**:
   A modern, dark SOC console built with React 19, TypeScript 5.7+, Vite 8, and Tailwind CSS v4. Features include a syntax-highlighted Monaco Rego editor with custom Monarch tokenizer, a side-by-side dry-run policy simulator with microsecond execution latency, a real-time gateway replica convergence topology grid, a filterable audit stream viewer with slide-over JSON drawers, and an emergency quarantine modal taking effect in <5 seconds.
5. **Prometheus Telemetry without Cardinality Poisoning (DIST-02)**:
   Both Gateway (`:9091`) and Control Plane (`:9092`) export operational telemetry via `github.com/prometheus/client_golang` on dedicated private listeners. All metric labels are strictly bounded to static enum sets (`method`, `route_id`, `status`, `decision`, `reason_code`). High-cardinality values (client IPs, user IDs, raw URLs) are strictly prohibited from metrics collectors.

```
aegis/
├── cmd/
│   ├── control-plane/
│   │   └── main.go                               # [MODIFIED] Multi-listener bootstrap: gRPC (:9090), HTTP REST (:8084), Metrics (:9092)
│   └── gateway/
│       └── main.go                               # [MODIFIED] Telemetry listener (:9091) bootstrap & HTTP metrics middleware wiring
├── internal/
│   ├── control/
│   │   ├── api.go                                # [NEW] Chi REST router (/control/v1), security middleware & subroute dispatch
│   │   ├── api_test.go                           # [NEW] REST API unit tests (routes, drafts, simulation, publish, rollback, gateways, quarantine, audit)
│   │   ├── auth.go                               # [NEW] Operator login, logout, and session establishment handlers
│   │   ├── session.go                            # [NEW] SessionManager, HttpOnly cookie handling, and CSRF token validation
│   │   ├── session_test.go                       # [NEW] Session and CSRF security tests (mutating vs non-mutating, expired cookies)
│   │   ├── etag.go                               # [NEW] RFC 7232 ETag hashing and If-Match optimistic concurrency validator
│   │   ├── idempotency.go                        # [NEW] IdempotencyStore with in-flight 409 conflict lock and 24h response replay
│   │   ├── concurrency_test.go                   # [NEW] ETag mismatch (412) and Idempotency deduplication unit tests
│   │   ├── rbac.go                               # [NEW] Role-Based Access Control middleware (sec-ops, auditor, viewer, Invariant 11)
│   │   ├── routes_handler.go                     # [NEW] /control/v1/routes endpoints (GET, POST)
│   │   ├── policy_handler.go                     # [NEW] /control/v1/policies endpoints (create draft, versions, validate)
│   │   ├── simulate_handler.go                   # [NEW] /control/v1/policies/simulate dry-run Rego execution handler
│   │   ├── publish_handler.go                    # [NEW] /control/v1/policies/{id}/publish monotonic snapshot broadcaster
│   │   ├── rollback_handler.go                   # [NEW] /control/v1/policies/{id}/rollback N+1 monotonic rollback handler
│   │   ├── gateway_handler.go                    # [NEW] /control/v1/gateways replica convergence status aggregator
│   │   ├── quarantine_handler.go                 # [NEW] /control/v1/principals/{id}/quarantine and /revocations handlers
│   │   ├── audit_handler.go                      # [NEW] /control/v1/audit-events query and single inspection handlers
│   │   ├── spa.go                                # [NEW] Static SPA asset server via embed.FS with zero-build fallback
│   │   ├── server.go                             # [EXISTING] gRPC distribution server (analogy & convergence source)
│   │   ├── validator.go                          # [EXISTING] Rego AST syntax & unit test validator
│   │   ├── ack.go                                # [EXISTING] Gateway replica acknowledgment tracker
│   │   └── rollback.go                           # [EXISTING] Monotonic N+1 rollback engine
│   ├── storage/
│   │   ├── audit_repo.go                         # [NEW] Filtered PostgreSQL query repository for partitioned audit_events
│   │   ├── audit_repo_test.go                    # [NEW] pgxmock unit tests for filtered audit queries and cursor pagination
│   │   ├── policy_repo.go                        # [NEW] Persistence repository for policy_drafts table
│   │   ├── route_repo.go                         # [EXISTING] Route and service catalog repository
│   │   └── snapshot_repo.go                      # [EXISTING] Snapshot and gateway acknowledgment repository
│   └── telemetry/
│       ├── metrics.go                            # [NEW] Bounded Prometheus metric collectors (low-cardinality labels)
│       ├── metrics_test.go                       # [NEW] Prometheus scrape assertions and label cardinality boundary tests
│       ├── http_middleware.go                    # [NEW] Gateway HTTP metrics middleware (duration, status, throughput)
│       └── server.go                             # [NEW] Dedicated private HTTP metrics exporter listener (:9091 / :9092)
├── web/
│   └── dashboard/
│       ├── index.html                            # [NEW] SPA entrypoint HTML
│       ├── package.json                          # [NEW] React 19, TypeScript, Vite 8, Tailwind v4, Monaco, React Query
│       ├── tsconfig.json                         # [NEW] Strict TypeScript configuration
│       ├── vite.config.ts                        # [NEW] Vite build config with proxy to :8084
│       ├── dist/
│       │   └── index.html                        # [NEW] Build placeholder shell ensuring go build / test succeeds
│       └── src/
│           ├── main.tsx                          # [NEW] React DOM bootstrap with QueryClientProvider
│           ├── App.tsx                           # [NEW] Top shell, navigation tabs, session badge, and unauthenticated overlay
│           ├── index.css                         # [NEW] Tailwind CSS v4 styling rules
│           ├── api/
│           │   ├── client.ts                     # [NEW] Type-safe fetch client with credentials, CSRF, ETag, and errors
│           │   └── types.ts                      # [NEW] TypeScript types aligned with OpenAPI control-v1.yaml
│           ├── utils/
│           │   └── rego-monarch.ts               # [NEW] Monaco Monarch tokenizer for Rego v1 syntax highlighting
│           └── components/
│               ├── PolicyStudio.tsx              # [NEW] Monaco Rego editor, draft management, validation, and publish modal
│               ├── PolicySimulator.tsx           # [NEW] Side-by-side JSON context editor & Decision Inspector card
│               ├── ClusterTopology.tsx           # [NEW] Real-time replica convergence cards and lease countdown bars
│               ├── AuditStream.tsx               # [NEW] Filterable audit event table & slide-over JSON detail drawer
│               ├── EmergencyQuarantine.tsx       # [NEW] Destructive modal for principal quarantine & token JTI revocation
│               └── LoginModal.tsx                # [NEW] Operator authentication & session establishment modal
└── tests/
    └── integration/
        └── operator_telemetry_test.go            # [NEW] End-to-end integration: REST API, simulation, publish, metrics scrape
```

---

## 2. Classification by Role & Data Flow

| File Path | Architectural Role | Ingress / Input | Processing / Transformation | Egress / Output | Invariants & Requirements |
|---|---|---|---|---|---|
| [`internal/control/api.go`](file:///home/logan78/Desktop/Aegis/internal/control/api.go) | Management REST Router & Security Middleware Engine | HTTP requests on `:8084` (`/control/v1/*`) | Chains RequestID, RealIP, Logger, Recoverer, Timeout (60s), and strict CSP/Security headers; mounts subroutes | Dispatched HTTP handler response | OPS-01, OPS-04, Invariant 11 |
| [`internal/control/session.go`](file:///home/logan78/Desktop/Aegis/internal/control/session.go) | Session & CSRF Security Engine | Inbound `aegis_session` cookie & `X-CSRF-Token` header | Validates session lifetime (8h max); enforces double-submit CSRF constant-time check on `POST`/`PUT`/`DELETE`/`PATCH` | Request context with `*Session` or HTTP 401/403 | OPS-04 |
| [`internal/control/auth.go`](file:///home/logan78/Desktop/Aegis/internal/control/auth.go) | Operator Authentication Controller | Inbound credentials (`username`, `password`/`token`) | Authenticates operator, mints 256-bit entropy session and CSRF secrets, issues `HttpOnly; SameSite=Strict; Secure` cookie | HTTP 200 with session metadata & CSRF token | OPS-04 |
| [`internal/control/etag.go`](file:///home/logan78/Desktop/Aegis/internal/control/etag.go) | Optimistic Concurrency Controller | Resource state & inbound `If-Match` header | Calculates SHA-256 or version string ETag (`"42"`); compares `If-Match` against active state | Injects `ETag` header or returns `HTTP 412 Precondition Failed` | OPS-02 |
| [`internal/control/idempotency.go`](file:///home/logan78/Desktop/Aegis/internal/control/idempotency.go) | Idempotency Deduplication Store | Inbound `Idempotency-Key` header (UUID) | Atomically locks in-flight execution; caches HTTP status, headers, and body for 24h | Cached replay (`Idempotent-Replay: true`) or `HTTP 409 Conflict` | OPS-02 |
| [`internal/control/rbac.go`](file:///home/logan78/Desktop/Aegis/internal/control/rbac.go) | Role-Based Access Control Guard | Authenticated operator session & user token headers | Verifies required role (`sec-ops`, `auditor`, `viewer`); immediately rejects data-plane bearer tokens | Handler continuation or `HTTP 403 Forbidden` | OPS-02, Invariant 11 |
| [`internal/control/routes_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/routes_handler.go) | Route Catalog Handler | Inbound JSON route definitions or list query | Validates route via `Validator`, upserts to `RouteRepo`, attaches ETag | HTTP 200/201 JSON route or validation error | OPS-01 |
| [`internal/control/policy_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/policy_handler.go) | Policy Draft Lifecycle Handler | Inbound Rego draft, version query, or test run | Persists drafts to `PolicyRepo`; compiles Rego AST; executes embedded unit tests via `Validator` | HTTP 200/201 JSON draft or test summary | OPS-01 |
| [`internal/control/simulate_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/simulate_handler.go) | In-Memory Rego Dry-Run Simulator | Synthetic JSON context & candidate/active Rego | Compiles candidate Rego module, executes query `data.aegis.authz.decision` with microsecond timer | HTTP 200 JSON with Allow/Deny, reason code, `duration_us` | OPS-01, OPS-03 |
| [`internal/control/publish_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/publish_handler.go) | Monotonic Snapshot Publisher | Policy ID, publish metadata, `If-Match`, `Idempotency-Key` | Assembles route catalog & policy module, signs monotonic version $N+1$, persists in PostgreSQL, broadcasts over gRPC | HTTP 200 JSON snapshot metadata & active version | OPS-01, OPS-02, Invariant 9 |
| [`internal/control/rollback_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/rollback_handler.go) | Monotonic Snapshot Rollback Handler | Target historical version, `If-Match`, `Idempotency-Key` | Invokes `RollbackEngine.RollbackToVersion` republishing historical payload under $N+1$; broadcasts over gRPC | HTTP 200 JSON snapshot metadata with new version | OPS-01, CTRL-06, Invariant 9 |
| [`internal/control/gateway_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/gateway_handler.go) | Fleet Convergence Aggregator | List gateways request (`/control/v1/gateways`) | Combines live in-memory gRPC client counts from `distServer` and persistent ack statuses from `SnapshotRepo` | HTTP 200 JSON `GatewayListResponse` | OPS-01, CTRL-05 |
| [`internal/control/quarantine_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/quarantine_handler.go) | Emergency Quarantine & Revocation API | Principal ID / JTI, reason, TTL, operator ID | Directly sets Redis keys `quarantine:principal:*` or `revocation:jti:*` (<5s propagation) | HTTP 200 JSON `QuarantineRecord` or `UnquarantineResponse` | OPS-01, REV-03 |
| [`internal/control/audit_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/audit_handler.go) | Audit Log Query API | Query parameters (principal, service, decision, time range) | Delegates to `AuditRepo` executing partitioned PostgreSQL SQL with index constraints | HTTP 200 JSON `AuditEventListResponse` or single `AuditEvent` | OPS-01 |
| [`internal/control/spa.go`](file:///home/logan78/Desktop/Aegis/internal/control/spa.go) | Embedded SPA File Server | GET requests on `/dashboard/*` or static assets | Serves static assets from `embed.FS`; falls back to `index.html` for client-side routing | HTTP 200 HTML, JS, CSS, or SVG assets | OPS-03 |
| [`internal/storage/audit_repo.go`](file:///home/logan78/Desktop/Aegis/internal/storage/audit_repo.go) | Partitioned Audit Event Store | Filter parameters & cursor pagination | Executes indexed B-tree queries (`idx_audit_events_principal_time`, `idx_audit_events_route_time`) with default 24h bound | Slice of `controlv1.AuditEvent` and next cursor | OPS-01 |
| [`internal/storage/policy_repo.go`](file:///home/logan78/Desktop/Aegis/internal/storage/policy_repo.go) | Policy Draft Persistence Store | Policy draft models | CRUD operations against PostgreSQL `policy_drafts` table | Stored `PolicyDraftRecord` instances | OPS-01 |
| [`internal/telemetry/metrics.go`](file:///home/logan78/Desktop/Aegis/internal/telemetry/metrics.go) | Bounded Prometheus Telemetry Registry | Gateway and Control Plane operational events | Updates low-cardinality counters, histograms, and gauges; strictly validates label enums | Prometheus registry ready for scraping | DIST-02 |
| [`internal/telemetry/http_middleware.go`](file:///home/logan78/Desktop/Aegis/internal/telemetry/http_middleware.go) | Gateway HTTP Metrics Middleware | Inbound HTTP request at gateway | Measures elapsed duration, records status code, increments `aegis_http_requests_total` | Forwarded request to handler | DIST-02 |
| [`internal/telemetry/server.go`](file:///home/logan78/Desktop/Aegis/internal/telemetry/server.go) | Private Telemetry Server Manager | Scrape requests on `/metrics` (`:9091` / `:9092`) | Serves Prometheus exposition format via `promhttp.HandlerFor` | Prometheus plaintext metrics output | DIST-02 |
| [`web/dashboard/src/api/client.ts`](file:///home/logan78/Desktop/Aegis/web/dashboard/src/api/client.ts) | Dashboard API HTTP Client | Application query and mutation requests | Automatically sets `credentials: 'include'`, injects `X-CSRF-Token`, manages `If-Match`, handles errors | Typed JSON response promises | OPS-03, OPS-04 |
| [`web/dashboard/src/utils/rego-monarch.ts`](file:///home/logan78/Desktop/Aegis/web/dashboard/src/utils/rego-monarch.ts) | Monaco Monarch Grammar Tokenizer | Raw Rego v1 source text in Monaco Editor | Tokenizes keywords (`package`, `import`, `default`, `allow`, `if`, `in`, `some`), comments, strings | Syntax-highlighted editor tokens | OPS-03 |
| [`web/dashboard/src/components/PolicyStudio.tsx`](file:///home/logan78/Desktop/Aegis/web/dashboard/src/components/PolicyStudio.tsx) | Rego Authoring & Publishing UI | Operator keystrokes, validation clicks, publish modal | Monaco Editor interface, syntax validation status, version history timeline, monotonic publish modal | Visual editor and snapshot publication | OPS-03 |
| [`web/dashboard/src/components/PolicySimulator.tsx`](file:///home/logan78/Desktop/Aegis/web/dashboard/src/components/PolicySimulator.tsx) | Dry-Run Simulation UI | Synthetic JSON input context, candidate Rego | Side-by-side comparative editor; displays green Allow or red Deny badge, latency timer, reason code | Visual decision inspector and policy diff | OPS-03 |
| [`web/dashboard/src/components/ClusterTopology.tsx`](file:///home/logan78/Desktop/Aegis/web/dashboard/src/components/ClusterTopology.tsx) | Fleet Convergence Topology UI | 5-second polling of `/control/v1/gateways` | Renders gateway cards, active version badges, lease countdown bars, and divergence alerts | Visual replica health and sync cards | OPS-03 |
| [`web/dashboard/src/components/AuditStream.tsx`](file:///home/logan78/Desktop/Aegis/web/dashboard/src/components/AuditStream.tsx) | Filterable Audit Stream UI | Filter controls (decision, service, principal, time) | Renders dense audit event table, pagination controls, and slide-over JSON detail drawer | Tabular audit records and JSON inspection | OPS-03 |
| [`web/dashboard/src/components/EmergencyQuarantine.tsx`](file:///home/logan78/Desktop/Aegis/web/dashboard/src/components/EmergencyQuarantine.tsx) | Emergency Incident Response Modal | Principal ID / JTI, mandatory reason, typed keyword | Enforces destructive confirmation contract ("QUARANTINE" / "REVOKE"), submits mutation to Redis | Immediate cluster-wide block confirmation | OPS-03, REV-03 |

---

## 3. Existing Analogs & Architectural Contract Mappings

Phase 4 builds directly upon the architectural conventions established across Phases 0 through 3:

### 3.1. Database Repository Pattern
- **Existing Analog**: [`internal/storage/route_repo.go`](file:///home/logan78/Desktop/Aegis/internal/storage/route_repo.go) and [`internal/storage/snapshot_repo.go`](file:///home/logan78/Desktop/Aegis/internal/storage/snapshot_repo.go).
  - Both use the `DBPool` interface (`pgxpool.Pool` or `pgxmock.PgxPoolIface`), parameterized SQL queries (`$1, $2`), `QueryRow(...).Scan(...)`, `Query(...)` with loop scanning, and typed error handling returning `ErrNotFound`.
- **Phase 4 Mapping**:
  - [`internal/storage/audit_repo.go`](file:///home/logan78/Desktop/Aegis/internal/storage/audit_repo.go): Implements filtered, indexed queries against partitioned PostgreSQL table `audit_events`. Uses the exact same `DBPool` interface, ensuring mockability with `pgxmock/v4`.
  - [`internal/storage/policy_repo.go`](file:///home/logan78/Desktop/Aegis/internal/storage/policy_repo.go): Implements draft persistence for the `policy_drafts` table defined in migration `000001_create_control_plane_tables.sql`.

### 3.2. Rego Evaluation Engine Pattern
- **Existing Analog**: [`internal/policy/engine.go`](file:///home/logan78/Desktop/Aegis/internal/policy/engine.go) (`Engine`) and [`internal/control/validator.go`](file:///home/logan78/Desktop/Aegis/internal/control/validator.go) (`Validator`).
  - Precompiles queries via `rego.New(rego.SetRegoVersion(ast.RegoV1), rego.Query("data.aegis.authz.decision"), rego.Module(...)).PrepareForEval(ctx)`.
  - Evaluates inputs via `query.Eval(ctx, rego.EvalInput(input))`.
- **Phase 4 Mapping**:
  - [`internal/control/simulate_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/simulate_handler.go): Directly adapts this pattern for live candidate simulation. Compiles candidate Rego code on the fly in memory, executes against synthetic JSON input context, measures microsecond execution latency, and returns the structured decision (`allow`, `reason_code`, `duration_us`).

### 3.3. Monotonic Publication & Rollback Pattern
- **Existing Analog**: [`internal/control/rollback.go`](file:///home/logan78/Desktop/Aegis/internal/control/rollback.go) (`RollbackEngine`) and [`internal/control/server.go`](file:///home/logan78/Desktop/Aegis/internal/control/server.go) (`SnapshotDistributionServer.BroadcastSnapshot`).
  - Fetches historical payload, calculates strictly increasing version $N+1$, signs with Ed25519, saves to `SnapshotRepo`, and broadcasts over gRPC stream.
- **Phase 4 Mapping**:
  - [`internal/control/publish_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/publish_handler.go) and [`internal/control/rollback_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/rollback_handler.go): Expose this engine via `/control/v1/policies/{id}/publish` and `/control/v1/policies/{id}/rollback`, guarded by `If-Match` ETag validation and `Idempotency-Key` deduplication.

### 3.4. Redis Emergency Quarantine & Revocation Pattern
- **Existing Analog**: [`internal/revocation/store.go`](file:///home/logan78/Desktop/Aegis/internal/revocation/store.go) (`Store`).
  - Provides atomic, pipelined Redis writes for `QuarantinePrincipal` (`quarantine:principal:*`) and `RevokeJTI` (`revocation:jti:*`) with TTLs and strict 200ms bounds.
- **Phase 4 Mapping**:
  - [`internal/control/quarantine_handler.go`](file:///home/logan78/Desktop/Aegis/internal/control/quarantine_handler.go): Exposes management endpoints `/control/v1/principals/{id}/quarantine`, `/control/v1/quarantine`, and `/control/v1/revocations`, calling `revStore.QuarantinePrincipal` and `revStore.RevokeJTI` to satisfy the sub-5-second cluster-wide propagation SLA (REV-03).

### 3.5. Daemon Lifecycle & Graceful Multi-Listener Shutdown
- **Existing Analog**: [`cmd/gateway/main.go`](file:///home/logan78/Desktop/Aegis/cmd/gateway/main.go) and [`cmd/control-plane/main.go`](file:///home/logan78/Desktop/Aegis/cmd/control-plane/main.go).
  - Listens on OS signals (`os.Interrupt`, `syscall.SIGTERM`), initiates `context.WithTimeout`, drains running servers, and closes database/Redis connections cleanly.
- **Phase 4 Mapping**:
  - `cmd/control-plane/main.go`: Extends graceful shutdown across three distinct listeners: gRPC (`:9090`), HTTP Management REST API (`:8084`), and Prometheus Metrics (`:9092`).
  - `cmd/gateway/main.go`: Adds the private Prometheus Metrics listener (`:9091`) to the existing dual-listener shutdown sequence.

---

## 4. Concrete Implementation Patterns & Code Excerpts

### Pattern 1: Go Chi REST Router Bootstrap & Security Headers Middleware
**Location**: `internal/control/api.go`  
**Rationale**: Mounts management endpoints under `/control/v1` adhering to `api/openapi/control-v1.yaml`. Enforces strict Content Security Policy (CSP), frame options, and content sniffing guards (OPS-01, OPS-04).

```go
package control

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// APIServer manages administrative REST management endpoints on port :8084.
type APIServer struct {
	router      chi.Router
	sessionMgr  *SessionManager
	idempStore  *IdempotencyStore
	distServer  *SnapshotDistributionServer
	validator   *Validator
	rollbackEng *RollbackEngine
	routeRepo   *RouteRepo
	policyRepo  *PolicyRepo
	auditRepo   *AuditRepo
	revStore    *RevocationStore
}

// NewAPIServer constructs and configures the control plane REST API router.
func NewAPIServer(
	sessionMgr *SessionManager,
	idempStore *IdempotencyStore,
	distServer *SnapshotDistributionServer,
	validator *Validator,
	rollbackEng *RollbackEngine,
	routeRepo *RouteRepo,
	policyRepo *PolicyRepo,
	auditRepo *AuditRepo,
	revStore *RevocationStore,
) *APIServer {
	r := chi.NewRouter()

	// 1. Standard Chi Middlewares
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// 2. Strict Security Headers & CSP (OPS-04)
	r.Use(SecurityHeadersMiddleware)

	server := &APIServer{
		router:      r,
		sessionMgr:  sessionMgr,
		idempStore:  idempStore,
		distServer:  distServer,
		validator:   validator,
		rollbackEng: rollbackEng,
		routeRepo:   routeRepo,
		policyRepo:  policyRepo,
		auditRepo:   auditRepo,
		revStore:    revStore,
	}

	server.routes()
	return server
}

// SecurityHeadersMiddleware injects defense-in-depth headers and strict CSP.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csp := "default-src 'self'; script-src 'self' 'unsafe-eval' blob:; " +
			"style-src 'self' 'unsafe-inline'; worker-src 'self' blob:; " +
			"img-src 'self' data:; connect-src 'self'; font-src 'self' data:; " +
			"frame-ancestors 'none'; object-src 'none'; base-uri 'self';"
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// Handler returns the root HTTP handler for serving or testing.
func (s *APIServer) Handler() http.Handler {
	return s.router
}
```

---

### Pattern 2: Session Management, HttpOnly Cookie & Double-Submit CSRF Validation
**Location**: `internal/control/session.go`  
**Rationale**: Authenticates browser sessions via `HttpOnly`, `SameSite=Strict` cookies (`aegis_session`). Enforces constant-time validation of `X-CSRF-Token` headers on mutating requests (`POST`, `PUT`, `DELETE`, `PATCH`), preventing Cross-Site Request Forgery (OPS-04).

```go
package control

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

type contextKey string

const (
	SessionContextKey contextKey = "aegis.session"
	CookieSessionName            = "aegis_session"
)

// Session represents an authenticated operator session.
type Session struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"` // sec-ops, auditor, viewer
	CSRFToken string    `json:"csrf_token"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionManager manages thread-safe operator sessions in memory.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: make(map[string]*Session)}
}

func (sm *SessionManager) CreateSession(username, role string) (*Session, error) {
	sBytes := make([]byte, 32)
	cBytes := make([]byte, 32)
	_, _ = rand.Read(sBytes)
	_, _ = rand.Read(cBytes)

	sess := &Session{
		ID:        hex.EncodeToString(sBytes),
		Username:  username,
		Role:      role,
		CSRFToken: hex.EncodeToString(cBytes),
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(8 * time.Hour), // 8h absolute session timeout
	}

	sm.mu.Lock()
	sm.sessions[sess.ID] = sess
	sm.mu.Unlock()
	return sess, nil
}

func (sm *SessionManager) GetSession(id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	sess, ok := sm.sessions[id]
	if !ok || time.Now().After(sess.ExpiresAt) {
		return nil, false
	}
	return sess, true
}

func (sm *SessionManager) RevokeSession(id string) {
	sm.mu.Lock()
	delete(sm.sessions, id)
	sm.mu.Unlock()
}

// SessionMiddleware enforces session cookie authentication and CSRF token validation.
func (sm *SessionManager) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieSessionName)
		if err != nil || cookie.Value == "" {
			http.Error(w, `{"code":"UNAUTHORIZED","message":"Missing or invalid session cookie"}`, http.StatusUnauthorized)
			return
		}

		sess, ok := sm.GetSession(cookie.Value)
		if !ok {
			http.Error(w, `{"code":"UNAUTHORIZED","message":"Session expired or revoked"}`, http.StatusUnauthorized)
			return
		}

		// CSRF Validation on Mutating Methods (OPS-04)
		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			csrfHeader := r.Header.Get("X-CSRF-Token")
			if csrfHeader == "" || subtle.ConstantTimeCompare([]byte(csrfHeader), []byte(sess.CSRFToken)) != 1 {
				http.Error(w, `{"code":"FORBIDDEN","message":"CSRF token validation failed"}`, http.StatusForbidden)
				return
			}
		}

		ctx := context.WithValue(r.Context(), SessionContextKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

---

### Pattern 3: RFC 7232 Optimistic Concurrency Control with ETag & If-Match
**Location**: `internal/control/etag.go`  
**Rationale**: Prevents concurrent edit lost-updates across multiple operators modifying policies or route catalog entries. Mutating endpoints reject stale revisions with `HTTP 412 Precondition Failed` (OPS-02).

```go
package control

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// GenerateETag computes a strong RFC 7232 ETag from bytes or monotonic version.
func GenerateETag(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf(`"%s"`, hex.EncodeToString(sum[:16]))
}

// GenerateVersionETag returns a version-based ETag e.g. "42".
func GenerateVersionETag(version int64) string {
	return fmt.Sprintf(`"%d"`, version)
}

// ValidateIfMatch validates that the request's If-Match header matches the expected ETag.
// Returns false and writes HTTP 412 Precondition Failed if there is a mismatch.
func ValidateIfMatch(w http.ResponseWriter, r *http.Request, currentETag string) bool {
	ifMatch := r.Header.Get("If-Match")
	if ifMatch == "" {
		// When optimistic concurrency is mandatory, missing header is rejected
		http.Error(w, `{"code":"PRECONDITION_FAILED","message":"Missing mandatory If-Match header"}`, http.StatusPreconditionFailed)
		return false
	}

	cleanCurrent := strings.Trim(currentETag, `"`)
	cleanIfMatch := strings.Trim(ifMatch, `"`)

	if cleanIfMatch != "*" && cleanIfMatch != cleanCurrent {
		w.Header().Set("ETag", currentETag)
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"code":"PRECONDITION_FAILED","message":"Resource modified concurrently. Active version is %s"}`, currentETag)))
		return false
	}

	return true
}
```

---

### Pattern 4: Atomic Idempotency Key In-Flight Locking & 24h Cached Response Replay
**Location**: `internal/control/idempotency.go`  
**Rationale**: Prevents double-execution of snapshot publications or quota modifications due to client retries. In-flight requests return `HTTP 409 Conflict`, while completed requests replay the cached response with `Idempotent-Replay: true` (OPS-02).

```go
package control

import (
	"bytes"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type CachedResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	CachedAt   time.Time
}

type IdempotencyStore struct {
	mu        sync.RWMutex
	inFlight  map[string]bool
	responses map[string]*CachedResponse
}

func NewIdempotencyStore() *IdempotencyStore {
	return &IdempotencyStore{
		inFlight:  make(map[string]bool),
		responses: make(map[string]*CachedResponse),
	}
}

// LockKey attempts to claim an in-flight lock for the given idempotency key.
// Returns (replayedResponse, acquiredLock, validKey).
func (s *IdempotencyStore) LockKey(key string) (*CachedResponse, bool, bool) {
	if key == "" {
		return nil, true, true // Optional key
	}
	if _, err := uuid.Parse(key); err != nil {
		return nil, false, false // Invalid format
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Check if completed response is cached
	if resp, ok := s.responses[key]; ok {
		if time.Since(resp.CachedAt) < 24*time.Hour {
			return resp, false, true
		}
		delete(s.responses, key)
	}

	// 2. Check if already in-flight (concurrent request)
	if s.inFlight[key] {
		return nil, false, true // Conflict
	}

	s.inFlight[key] = true
	return nil, true, true
}

func (s *IdempotencyStore) SaveResponse(key string, statusCode int, headers http.Header, body []byte) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.inFlight, key)
	s.responses[key] = &CachedResponse{
		StatusCode: statusCode,
		Headers:    headers.Clone(),
		Body:       bytes.Clone(body),
		CachedAt:   time.Now(),
	}
}

func (s *IdempotencyStore) UnlockKey(key string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	delete(s.inFlight, key)
	s.mu.Unlock()
}
```

---

### Pattern 5: Granular Role-Based Access Control (RBAC) & Invariant 11 Data-Plane Token Rejection
**Location**: `internal/control/rbac.go`  
**Rationale**: Controls access to management resources based on operator role (`sec-ops`, `auditor`, `viewer`). Invariant 11 dictates that credentials minted for data-plane services (e.g. `aud: aegis-gateway`) MUST be strictly rejected on the control plane with `HTTP 403 Forbidden` (`DATA_PLANE_CREDENTIALS_REJECTED`).

```go
package control

import (
	"net/http"
	"strings"
)

// RequireRoles returns a middleware verifying the operator session has at least one of the allowed roles.
func RequireRoles(allowedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Invariant 11 Defense: Reject Inbound Data-Plane Bearer Tokens on /control/v1
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				// Bearer token presented to management plane must be checked for administrative scope.
				// If it is a gateway data-plane token, reject immediately with 403.
				if strings.Contains(authHeader, "aegis-gateway") || strings.Contains(authHeader, "workload") {
					http.Error(w, `{"code":"FORBIDDEN","message":"Data plane credentials rejected on management endpoints"}`, http.StatusForbidden)
					return
				}
			}

			sess, ok := r.Context().Value(SessionContextKey).(*Session)
			if !ok || sess == nil {
				http.Error(w, `{"code":"UNAUTHORIZED","message":"Authentication required"}`, http.StatusUnauthorized)
				return
			}

			for _, role := range allowedRoles {
				if sess.Role == role || sess.Role == "admin" {
					next.ServeHTTP(w, r)
					return
				}
			}

			http.Error(w, `{"code":"FORBIDDEN","message":"Insufficient role permissions"}`, http.StatusForbidden)
		})
	}
}
```

---

### Pattern 6: Sub-millisecond In-Memory OPA Rego Live Simulation Engine
**Location**: `internal/control/simulate_handler.go`  
**Rationale**: Compiles candidate Rego code on the fly in memory and evaluates against synthetic JSON request context with sub-millisecond feedback, returning detailed diagnostics before any changes are published to production (OPS-01, OPS-03).

```go
package control

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
)

type SimulationRequest struct {
	CandidateRego string                 `json:"candidate_rego,omitempty"`
	InputContext  map[string]interface{} `json:"input_context"`
}

type SimulationResponse struct {
	Allow       bool                   `json:"allow"`
	ReasonCode  string                 `json:"reason_code"`
	DurationUs  int64                  `json:"duration_us"`
	Diagnostics map[string]interface{} `json:"diagnostics,omitempty"`
}

func (s *APIServer) HandleSimulatePolicy(w http.ResponseWriter, r *http.Request) {
	var req SimulationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"code":"BAD_REQUEST","message":"Invalid JSON payload"}`, http.StatusBadRequest)
		return
	}

	regoSource := req.CandidateRego
	if regoSource == "" {
		active := s.distServer.GetActiveSnapshotInternal()
		if active != nil {
			// Extract active source from protobuf payload if candidate omitted
		}
	}

	start := time.Now()
	evalQuery, err := rego.New(
		rego.SetRegoVersion(ast.RegoV1),
		rego.Module("simulate.rego", regoSource),
		rego.Query("data.aegis.authz.decision"),
	).PrepareForEval(r.Context())

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code":    "COMPILATION_ERROR",
			"message": err.Error(),
		})
		return
	}

	rs, err := evalQuery.Eval(r.Context(), rego.EvalInput(req.InputContext))
	durationUs := time.Since(start).Microseconds()

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code":    "EVALUATION_ERROR",
			"message": err.Error(),
		})
		return
	}

	resp := SimulationResponse{
		Allow:       false,
		ReasonCode:  "DENIED_DEFAULT",
		DurationUs:  durationUs,
		Diagnostics: make(map[string]interface{}),
	}

	if len(rs) > 0 && len(rs[0].Expressions) > 0 {
		if val, ok := rs[0].Expressions[0].Value.(map[string]interface{}); ok {
			if allow, ok := val["allow"].(bool); ok {
				resp.Allow = allow
			}
			if reason, ok := val["reason_code"].(string); ok {
				resp.ReasonCode = reason
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
```

---

### Pattern 7: Monotonic Snapshot Publication & N+1 Rollback via gRPC Stream Broadcast
**Location**: `internal/control/publish_handler.go` & `rollback_handler.go`  
**Rationale**: Implements forward-only version monotonicity. Even when rolling back, historical configuration is republished under version $N+1$, satisfying Invariant 9 and CTRL-06.

```go
package control

import (
	"encoding/json"
	"net/http"

	snapshotv1 "aegis/pkg/api/snapshot/v1"
)

type PublishRequest struct {
	Description string `json:"description,omitempty"`
}

type RollbackRequest struct {
	TargetVersion int64  `json:"target_version"`
	Reason        string `json:"reason,omitempty"`
}

// HandleRollbackPolicy handles monotonic rollback to target historical version N+1.
func (s *APIServer) HandleRollbackPolicy(w http.ResponseWriter, r *http.Request) {
	active := s.distServer.GetActiveSnapshotInternal()
	var currentVersion int64 = 0
	if active != nil {
		currentVersion = active.Version
	}

	// 1. Optimistic Concurrency Check (OPS-02)
	if !ValidateIfMatch(w, r, GenerateVersionETag(currentVersion)) {
		return
	}

	var req RollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"code":"BAD_REQUEST","message":"Invalid JSON payload"}`, http.StatusBadRequest)
		return
	}

	sess := r.Context().Value(SessionContextKey).(*Session)

	// 2. Execute Monotonic Rollback via RollbackEngine (N+1)
	newEnv, err := s.rollbackEng.RollbackToVersion(r.Context(), req.TargetVersion, sess.Username)
	if err != nil {
		http.Error(w, `{"code":"INTERNAL_ERROR","message":"Rollback execution failed"}`, http.StatusInternalServerError)
		return
	}

	// 3. Broadcast to Connected Gateways over gRPC Stream
	s.distServer.BroadcastSnapshot(newEnv)

	w.Header().Set("ETag", GenerateVersionETag(newEnv.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"snapshot_version": newEnv.Version,
		"published_at":     newEnv.CreatedAt.AsTime(),
		"published_by":     sess.Username,
		"target_version":   req.TargetVersion,
	})
}
```

---

### Pattern 8: Real-time Gateway Replica Convergence Aggregator
**Location**: `internal/control/gateway_handler.go`  
**Rationale**: Aggregates in-memory gRPC client counts from `distServer` and persistent acknowledgment records from `SnapshotRepo` into the unified `GatewayListResponse` model (OPS-01, CTRL-05).

```go
package control

import (
	"encoding/json"
	"net/http"
	"time"

	controlv1 "aegis/pkg/api/control/v1"
)

func (s *APIServer) HandleListGateways(w http.ResponseWriter, r *http.Request) {
	active := s.distServer.GetActiveSnapshotInternal()
	var activeVersion int64 = 1
	if active != nil {
		activeVersion = active.Version
	}

	// Retrieve replica statuses from persistent AckTracker and SnapshotRepo
	acks, err := s.distServer.ackTracker.repo.ListGatewayAcks(r.Context())
	if err != nil {
		http.Error(w, `{"code":"INTERNAL_ERROR","message":"Failed to query gateway acks"}`, http.StatusInternalServerError)
		return
	}

	items := make([]controlv1.GatewayStatus, 0, len(acks))
	for _, ack := range acks {
		status := controlv1.Healthy
		if ack.ActiveVersion < activeVersion {
			status = controlv1.Degraded
		}
		if time.Since(ack.LastHeartbeatAt) > 60*time.Second {
			status = controlv1.Partitioned
		}

		item := controlv1.GatewayStatus{
			GatewayId:       ack.GatewayID,
			ActiveVersion:   ack.ActiveVersion,
			Status:          status,
			ErrorMessage:    ack.ErrorMessage,
			LastHeartbeatAt: ack.LastHeartbeatAt,
			ConnectedAt:     ack.ConnectedAt,
		}
		items = append(items, item)
	}

	resp := controlv1.GatewayListResponse{
		Items: items,
		Total: len(items),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
```

---

### Pattern 9: Sub-5s Emergency Principal Quarantine & Token Revocation with Redis
**Location**: `internal/control/quarantine_handler.go`  
**Rationale**: Posts principal blocking and token JTI revocation directly to shared Redis, taking effect cluster-wide across all gateway replicas in <5 seconds (REV-03, OPS-01).

```go
package control

import (
	"encoding/json"
	"net/http"
	"time"

	controlv1 "aegis/pkg/api/control/v1"
	"github.com/go-chi/chi/v5"
)

func (s *APIServer) HandleQuarantinePrincipal(w http.ResponseWriter, r *http.Request) {
	principalID := chi.URLParam(r, "id")
	if principalID == "" {
		http.Error(w, `{"code":"BAD_REQUEST","message":"Principal ID is required"}`, http.StatusBadRequest)
		return
	}

	var req controlv1.QuarantineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"code":"BAD_REQUEST","message":"Invalid JSON payload"}`, http.StatusBadRequest)
		return
	}

	ttl := 24 * time.Hour
	if req.TtlSeconds != nil && *req.TtlSeconds > 0 {
		ttl = time.Duration(*req.TtlSeconds) * time.Second
	}

	// Direct atomic Redis write (<5s cluster propagation SLA)
	if err := s.revStore.QuarantinePrincipal(r.Context(), principalID, req.Reason, ttl); err != nil {
		http.Error(w, `{"code":"DEPENDENCY_FAILURE","message":"Failed to propagate quarantine to Redis"}`, http.StatusServiceUnavailable)
		return
	}

	sess := r.Context().Value(SessionContextKey).(*Session)
	record := controlv1.QuarantineRecord{
		PrincipalId:  principalID,
		Reason:       req.Reason,
		QuarantinedAt: time.Now(),
		QuarantinedBy: sess.Username,
		Status:       controlv1.Active,
		ExpiresAt:    time.Now().Add(ttl),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}
```

---

### Pattern 10: Partitioned PostgreSQL Audit Log Query Repository with Index Alignment
**Location**: `internal/storage/audit_repo.go`  
**Rationale**: Executes indexed B-tree queries (`idx_audit_events_principal_time`, `idx_audit_events_route_time`) against partitioned table `audit_events`. Automatically bounds queries to the last 24 hours if timestamps are omitted to prevent full partition scans (OPS-01).

```go
package storage

import (
	"context"
	"fmt"
	"time"

	controlv1 "aegis/pkg/api/control/v1"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type AuditFilter struct {
	PrincipalID   string
	ServiceID     string
	Decision      string
	FromTimestamp time.Time
	ToTimestamp   time.Time
	Cursor        string
	Limit         int
}

type AuditRepo struct {
	db DBPool
}

func NewAuditRepo(db DBPool) *AuditRepo {
	return &AuditRepo{db: db}
}

// ListAuditEvents queries partitioned audit_events with bounded date constraints.
func (r *AuditRepo) ListAuditEvents(ctx context.Context, f AuditFilter) ([]controlv1.AuditEvent, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	// Bound query to last 24h by default to prevent full table partition scans
	if f.FromTimestamp.IsZero() {
		f.FromTimestamp = time.Now().Add(-24 * time.Hour)
	}
	if f.ToTimestamp.IsZero() {
		f.ToTimestamp = time.Now()
	}

	query := `
		SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind,
		       service_id, route_id, http_method, request_path, decision, reason_code,
		       snapshot_version, http_status, duration_ms
		FROM audit_events
		WHERE timestamp >= $1 AND timestamp <= $2
	`
	args := []any{f.FromTimestamp, f.ToTimestamp}
	argIdx := 3

	if f.PrincipalID != "" {
		query += fmt.Sprintf(" AND principal_id = $%d", argIdx)
		args = append(args, f.PrincipalID)
		argIdx++
	}
	if f.ServiceID != "" {
		query += fmt.Sprintf(" AND service_id = $%d", argIdx)
		args = append(args, f.ServiceID)
		argIdx++
	}
	if f.Decision != "" {
		query += fmt.Sprintf(" AND decision = $%d", argIdx)
		args = append(args, f.Decision)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY timestamp DESC LIMIT $%d", argIdx)
	args = append(args, f.Limit)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit events: %w", err)
	}
	defer rows.Close()

	var events []controlv1.AuditEvent
	for rows.Next() {
		var (
			eventIDUUID, reqIDUUID openapi_types.UUID
			eventType, principalID, serviceID, routeID, method, path, decision, reason string
			principalKind                                                              *string
			snapVer                                                                    int64
			httpStatus                                                                 *int
			durationMs                                                                 *float64
			ts                                                                         time.Time
		)

		if err := rows.Scan(
			&eventIDUUID, &eventType, &reqIDUUID, &ts, &principalID, &principalKind,
			&serviceID, &routeID, &method, &path, &decision, &reason,
			&snapVer, &httpStatus, &durationMs,
		); err != nil {
			return nil, fmt.Errorf("failed to scan audit event: %w", err)
		}

		var durInt *int
		if durationMs != nil {
			v := int(*durationMs)
			durInt = &v
		}

		events = append(events, controlv1.AuditEvent{
			EventId:         eventIDUUID,
			EventType:       controlv1.AuditEventEventType(eventType),
			RequestId:       reqIDUUID,
			Timestamp:       ts,
			PrincipalId:     principalID,
			ServiceId:       serviceID,
			RouteId:         routeID,
			HttpMethod:      &method,
			RequestPath:     &path,
			Decision:        controlv1.AuditEventDecision(decision),
			ReasonCode:      reason,
			SnapshotVersion: snapVer,
			StatusCode:      httpStatus,
			DurationMs:      durInt,
		})
	}

	return events, rows.Err()
}
```

---

### Pattern 11: Low-Cardinality Bounded Prometheus Telemetry Exporter & Middleware
**Location**: `internal/telemetry/metrics.go` & `http_middleware.go`  
**Rationale**: Exports operational metrics on dedicated private ports (`:9091` / `:9092`). Enforces strict enum label limits, preventing Prometheus TSDB cardinality explosions and PII leakage (DIST-02).

```go
package telemetry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	Registry                 *prometheus.Registry
	HTTPRequestsTotal        *prometheus.CounterVec
	PolicyEvalDuration       prometheus.Histogram
	PolicyDecisionsTotal     *prometheus.CounterVec
	RateLimitRejectionsTotal *prometheus.CounterVec
	SnapshotActiveVersion    prometheus.Gauge
	SnapshotLeaseAgeSeconds  prometheus.Gauge
	SpoolUtilizationRatio    prometheus.Gauge
	SpoolBytesWrittenTotal   prometheus.Counter
	ConnectedGateways        prometheus.Gauge
	SnapshotPublishTotal     prometheus.Counter
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_http_requests_total",
				Help: "Total HTTP requests partitioned by HTTP method, route ID, and status code.",
			},
			[]string{"method", "route_id", "status"},
		),
		PolicyEvalDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "aegis_policy_eval_duration_seconds",
				Help:    "Histogram of in-memory OPA policy evaluation latencies in seconds.",
				Buckets: []float64{0.0001, 0.00025, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1},
			},
		),
		PolicyDecisionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_policy_decisions_total",
				Help: "Total authorization decisions partitioned by decision and reason code.",
			},
			[]string{"decision", "reason_code"},
		),
		RateLimitRejectionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_ratelimit_rejections_total",
				Help: "Total rate limit rejections partitioned by route ID.",
			},
			[]string{"route_id"},
		),
		SnapshotActiveVersion: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_snapshot_active_version",
				Help: "Current active monotonic configuration snapshot version.",
			},
		),
		SnapshotLeaseAgeSeconds: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_snapshot_lease_age_seconds",
				Help: "Seconds elapsed since the last verified freshness lease renewal.",
			},
		),
		SpoolUtilizationRatio: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_spool_utilization_ratio",
				Help: "Current disk WAL audit spool capacity utilization ratio (0.0 to 1.0).",
			},
		),
		SpoolBytesWrittenTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aegis_spool_bytes_written_total",
				Help: "Total bytes written to the pre-forward disk WAL audit spool.",
			},
		),
		ConnectedGateways: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_control_plane_connected_gateways",
				Help: "Active connected gateway replica streams on the control plane.",
			},
		),
		SnapshotPublishTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aegis_control_plane_snapshot_publish_total",
				Help: "Total configuration snapshots published by the control plane.",
			},
		),
	}

	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.PolicyEvalDuration,
		m.PolicyDecisionsTotal,
		m.RateLimitRejectionsTotal,
		m.SnapshotActiveVersion,
		m.SnapshotLeaseAgeSeconds,
		m.SpoolUtilizationRatio,
		m.SpoolBytesWrittenTotal,
		m.ConnectedGateways,
		m.SnapshotPublishTotal,
	)

	return m
}

// StartMetricsServer starts a dedicated private HTTP listener on the given port (e.g. ":9091").
func (m *Metrics) StartMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}))
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.ListenAndServe()
	}()
	return srv
}
```

---

### Pattern 12: Embedded React SPA Asset Server with Fallback Shell
**Location**: `internal/control/spa.go`  
**Rationale**: Embeds the compiled dashboard assets into the control plane binary using Go's `embed.FS`. Provides an HTML fallback shell so `go test` and `go build` succeed cleanly even before running `npm run build` (OPS-03).

```go
package control

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/dashboard/dist/*
var dashboardFS embed.FS

// SPAHandler returns an http.Handler serving the embedded SPA with HTML fallback for client-side routing.
func SPAHandler() http.Handler {
	distSub, err := fs.Sub(dashboardFS, "web/dashboard/dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Dashboard asset directory unavailable", http.StatusNotFound)
		})
	}
	fileServer := http.FileServer(http.FS(distSub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/dashboard")
		if path == "" || path == "/" {
			path = "/index.html"
		}

		f, err := distSub.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for client-side React router routes
		r.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r)
	})
}
```

---

### Pattern 13: Monaco Editor Custom Monarch Tokenizer for Rego v1
**Location**: `web/dashboard/src/utils/rego-monarch.ts`  
**Rationale**: Provides in-browser syntax highlighting for Rego v1 policies without requiring external language server binaries or remote CDN downloads, remaining 100% CSP compliant (OPS-03).

```typescript
import type { languages } from 'monaco-editor';

export const regoLanguageId = 'rego';

export const regoLanguageDefinition: languages.IMonarchLanguage = {
  keywords: [
    'package', 'import', 'default', 'allow', 'deny',
    'if', 'in', 'contains', 'some', 'every', 'with',
    'as', 'not', 'true', 'false', 'null'
  ],
  typeKeywords: [
    'boolean', 'string', 'number', 'array', 'object', 'set'
  ],
  operators: [
    '=', ':=', '==', '!=', '>=', '<=', '>', '<',
    '+', '-', '*', '/', '%', '&', '|'
  ],
  symbols: /[=><!~?:&|+\-*\/\^%]+/,
  tokenizer: {
    root: [
      [/#.*$/, 'comment'],
      [/"([^"\\]|\\.)*"/, 'string'],
      [/`([^`])*`/, 'string.raw'],
      [/\b(true|false|null)\b/, 'keyword'],
      [/[a-zA-Z_]\w*/, {
        cases: {
          '@keywords': 'keyword',
          '@typeKeywords': 'type',
          '@default': 'identifier'
        }
      }],
      [/\d+(\.\d+)?/, 'number'],
      [/@symbols/, {
        cases: {
          '@operators': 'operator',
          '@default': ''
        }
      }],
      [/[{}()\[\]]/, '@brackets'],
    ],
  },
};
```

---

### Pattern 14: React Type-Safe API Client with Auto-CSRF & Error Handling
**Location**: `web/dashboard/src/api/client.ts`  
**Rationale**: Automatically attaches `credentials: 'include'` for ambient session cookies and injects the active `X-CSRF-Token` header on all mutating HTTP methods (`POST`, `PUT`, `DELETE`, `PATCH`). Parses RFC 7807 problem details (OPS-03, OPS-04).

```typescript
export class ApiClient {
  private csrfToken: string | null = null;

  setCSRFToken(token: string) {
    this.csrfToken = token;
  }

  async request<T>(path: string, options: RequestInit = {}): Promise<T> {
    const headers = new Headers(options.headers || {});
    
    // Auto-inject CSRF token on mutating operations
    const method = options.method?.toUpperCase() || 'GET';
    if (['POST', 'PUT', 'DELETE', 'PATCH'].includes(method) && this.csrfToken) {
      headers.set('X-CSRF-Token', this.csrfToken);
    }
    
    headers.set('Accept', 'application/json');
    if (options.body && typeof options.body === 'string') {
      headers.set('Content-Type', 'application/json');
    }

    const response = await fetch(path, {
      ...options,
      headers,
      credentials: 'include', // Mandate HttpOnly cookie delivery
    });

    if (!response.ok) {
      const err = await response.json().catch(() => ({ message: response.statusText }));
      throw new Error(err.message || `Request failed with status ${response.status}`);
    }

    return response.json() as Promise<T>;
  }

  get<T>(path: string): Promise<T> {
    return this.request<T>(path, { method: 'GET' });
  }

  post<T>(path: string, body?: unknown, headers?: Record<string, string>): Promise<T> {
    return this.request<T>(path, {
      method: 'POST',
      body: body ? JSON.stringify(body) : undefined,
      headers,
    });
  }

  delete<T>(path: string, headers?: Record<string, string>): Promise<T> {
    return this.request<T>(path, { method: 'DELETE', headers });
  }
}

export const apiClient = new ApiClient();
```

---

### Pattern 15: React Side-by-Side Dry-Run Policy Simulator Component
**Location**: `web/dashboard/src/components/PolicySimulator.tsx`  
**Rationale**: Two-column layout presenting synthetic JSON request context on the left and the Decision Inspector outcome card on the right with ALLOW/DENY badges, microsecond latency timer, and reason codes (OPS-03, 04-UI-SPEC §3).

```tsx
import React, { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { apiClient } from '../api/client';
import { Play, CheckCircle2, XCircle, Clock } from 'lucide-react';

interface SimulationResult {
  allow: boolean;
  reason_code: string;
  duration_us: number;
  diagnostics?: Record<string, unknown>;
}

export const PolicySimulator: React.FC<{ candidateRego?: string }> = ({ candidateRego }) => {
  const [inputContext, setInputContext] = useState<string>(JSON.stringify({
    method: "GET",
    path: "/api/orders",
    principal: {
      id: "usr_alice",
      roles: ["developer"]
    },
    client_ip: "192.168.1.50"
  }, null, 2));

  const [result, setResult] = useState<SimulationResult | null>(null);

  const simulateMutation = useMutation({
    mutationFn: async () => {
      const parsedContext = JSON.parse(inputContext);
      return apiClient.post<SimulationResult>('/control/v1/policies/simulate', {
        candidate_rego: candidateRego,
        input_context: parsedContext
      });
    },
    onSuccess: (data) => setResult(data)
  });

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 p-6 bg-slate-900 rounded-xl border border-slate-800">
      {/* Left Panel: Synthetic Request Context Editor */}
      <div className="flex flex-col space-y-4">
        <div className="flex justify-between items-center">
          <h3 className="text-lg font-semibold text-slate-100">Synthetic Request Context (JSON)</h3>
          <button
            onClick={() => simulateMutation.mutate()}
            disabled={simulateMutation.isPending}
            className="flex items-center space-x-2 bg-cyan-600 hover:bg-cyan-500 text-white px-4 py-2 rounded-lg font-medium transition disabled:opacity-50"
          >
            <Play className="w-4 h-4" />
            <span>{simulateMutation.isPending ? 'Simulating...' : 'Execute Dry-Run Simulation'}</span>
          </button>
        </div>
        <textarea
          value={inputContext}
          onChange={(e) => setInputContext(e.target.value)}
          className="w-full h-80 bg-slate-950 font-mono text-sm text-slate-200 p-4 rounded-lg border border-slate-800 focus:outline-none focus:border-cyan-500"
          spellCheck={false}
        />
      </div>

      {/* Right Panel: Decision Inspector */}
      <div className="flex flex-col space-y-4">
        <h3 className="text-lg font-semibold text-slate-100">Decision Outcome</h3>
        {result ? (
          <div className="flex flex-col space-y-4 bg-slate-950 p-6 rounded-lg border border-slate-800">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-3">
                {result.allow ? (
                  <CheckCircle2 className="w-8 h-8 text-emerald-400" />
                ) : (
                  <XCircle className="w-8 h-8 text-rose-500" />
                )}
                <span className={`text-2xl font-bold ${result.allow ? 'text-emerald-400' : 'text-rose-500'}`}>
                  {result.allow ? 'ALLOW' : 'DENY'}
                </span>
              </div>
              <div className="flex items-center space-x-2 text-slate-400 font-mono text-sm">
                <Clock className="w-4 h-4" />
                <span>{(result.duration_us / 1000).toFixed(2)} ms</span>
              </div>
            </div>

            <div className="border-t border-slate-800 pt-4 space-y-2">
              <div className="text-xs text-slate-400 uppercase tracking-wider">Reason Code</div>
              <div className="font-mono text-slate-200 bg-slate-900 px-3 py-1.5 rounded border border-slate-800 inline-block">
                {result.reason_code}
              </div>
            </div>
          </div>
        ) : (
          <div className="flex items-center justify-center h-80 border border-dashed border-slate-800 rounded-lg text-slate-500 text-sm">
            Simulation Awaiting Request Context. Click Execute to inspect decision.
          </div>
        )}
      </div>
    </div>
  );
};
```

---

### Pattern 16: React Cluster Topology & Live Replica Convergence Cards
**Location**: `web/dashboard/src/components/ClusterTopology.tsx`  
**Rationale**: Real-time fleet grid showing connected gateway replicas, active snapshot versions, lease renewal countdowns, ack statuses, and convergence divergence alerts (OPS-03, 04-UI-SPEC §4).

```tsx
import React from 'react';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../api/client';
import { Server, RefreshCw, CheckCircle2, AlertTriangle, XCircle } from 'lucide-react';

interface GatewayStatus {
  gateway_id: string;
  active_version: number;
  status: 'healthy' | 'degraded' | 'partitioned';
  error_message?: string;
  last_heartbeat_at: string;
}

interface GatewayListResponse {
  items: GatewayStatus[];
  total: number;
}

export const ClusterTopology: React.FC = () => {
  const { data, refetch, isFetching } = useQuery<GatewayListResponse>({
    queryKey: ['gateways'],
    queryFn: () => apiClient.get<GatewayListResponse>('/control/v1/gateways'),
    refetchInterval: 5000, // Poll every 5s
  });

  const gateways = data?.items || [];

  return (
    <div className="space-y-6">
      <div className="flex justify-between items-center bg-slate-900 p-6 rounded-xl border border-slate-800">
        <div>
          <h2 className="text-xl font-semibold text-slate-100">Gateway Fleet Convergence</h2>
          <p className="text-sm text-slate-400 mt-1">Real-time status of connected gateway data-plane replicas</p>
        </div>
        <button
          onClick={() => refetch()}
          disabled={isFetching}
          aria-label="Refresh fleet status"
          className="flex items-center space-x-2 bg-slate-800 hover:bg-slate-700 text-slate-200 px-4 py-2 rounded-lg text-sm font-medium transition"
        >
          <RefreshCw className={`w-4 h-4 ${isFetching ? 'animate-spin' : ''}`} />
          <span>Refresh Fleet Status</span>
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {gateways.map((gw) => (
          <div key={gw.gateway_id} className="bg-slate-900 p-6 rounded-xl border border-slate-800 space-y-4">
            <div className="flex justify-between items-start">
              <div className="flex items-center space-x-3">
                <Server className="w-5 h-5 text-cyan-400" />
                <span className="font-mono text-slate-200 font-semibold">{gw.gateway_id}</span>
              </div>
              {gw.status === 'healthy' && (
                <span className="flex items-center space-x-1 text-xs font-semibold px-2.5 py-1 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30">
                  <CheckCircle2 className="w-3.5 h-3.5" />
                  <span>HEALTHY</span>
                </span>
              )}
              {gw.status === 'degraded' && (
                <span className="flex items-center space-x-1 text-xs font-semibold px-2.5 py-1 rounded-full bg-amber-500/10 text-amber-400 border border-amber-500/30">
                  <AlertTriangle className="w-3.5 h-3.5" />
                  <span>DEGRADED</span>
                </span>
              )}
              {gw.status === 'partitioned' && (
                <span className="flex items-center space-x-1 text-xs font-semibold px-2.5 py-1 rounded-full bg-rose-500/10 text-rose-400 border border-rose-500/30">
                  <XCircle className="w-3.5 h-3.5" />
                  <span>PARTITIONED</span>
                </span>
              )}
            </div>

            <div className="border-t border-slate-800 pt-4 space-y-2 text-sm">
              <div className="flex justify-between text-slate-400">
                <span>Active Snapshot</span>
                <span className="font-mono text-slate-200 font-medium">v{gw.active_version}</span>
              </div>
              <div className="flex justify-between text-slate-400">
                <span>Last Heartbeat</span>
                <span className="text-slate-300">{new Date(gw.last_heartbeat_at).toLocaleTimeString()}</span>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
};
```

---

### Pattern 17: React Emergency Quarantine Modal with Destructive Confirmation Contract
**Location**: `web/dashboard/src/components/EmergencyQuarantine.tsx`  
**Rationale**: Destructive incident response modal for principal quarantine with typed confirmation keyword ("QUARANTINE"), mandatory reason, and immediate propagation to Redis (REV-03, 04-UI-SPEC §6).

```tsx
import React, { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { apiClient } from '../api/client';
import { AlertTriangle, X } from 'lucide-react';

export const EmergencyQuarantineModal: React.FC<{ isOpen: boolean; onClose: () => void }> = ({ isOpen, onClose }) => {
  const [principalId, setPrincipalId] = useState('');
  const [reason, setReason] = useState('');
  const [confirmText, setConfirmText] = useState('');

  const quarantineMutation = useMutation({
    mutationFn: async () => {
      return apiClient.post(`/control/v1/principals/${encodeURIComponent(principalId)}/quarantine`, {
        reason,
        ttl_seconds: 86400,
      });
    },
    onSuccess: () => {
      onClose();
      setPrincipalId('');
      setReason('');
      setConfirmText('');
    },
  });

  if (!isOpen) return null;

  const isConfirmed = confirmText === 'QUARANTINE' && reason.trim().length > 0 && principalId.trim().length > 0;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4">
      <div className="w-full max-w-lg bg-slate-900 border border-rose-500/40 rounded-xl p-6 shadow-2xl space-y-6">
        <div className="flex justify-between items-start">
          <div className="flex items-center space-x-3 text-rose-400">
            <AlertTriangle className="w-6 h-6" />
            <h3 className="text-lg font-semibold text-slate-100">Emergency Principal Quarantine</h3>
          </div>
          <button onClick={onClose} aria-label="Close modal window" className="text-slate-400 hover:text-slate-200">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="bg-rose-500/10 border border-rose-500/30 p-4 rounded-lg text-rose-300 text-xs">
          This action writes directly to Redis with a cluster-wide propagation SLA of &lt;5 seconds. All gateway replicas will immediately return HTTP 403 Forbidden (PRINCIPAL_QUARANTINED) for this subject.
        </div>

        <div className="space-y-4">
          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">Principal ID</label>
            <input
              type="text"
              value={principalId}
              onChange={(e) => setPrincipalId(e.target.value)}
              placeholder="e.g. user_attacker_99"
              className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-sm text-slate-200 font-mono focus:ring-2 focus:ring-rose-500 focus:outline-none"
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">Quarantine Justification (Mandatory)</label>
            <input
              type="text"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="e.g. Credential stuffing detected from IP"
              className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-sm text-slate-200 focus:ring-2 focus:ring-rose-500 focus:outline-none"
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">Type "QUARANTINE" to Confirm</label>
            <input
              type="text"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              placeholder="QUARANTINE"
              className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-sm text-slate-200 font-mono focus:ring-2 focus:ring-rose-500 focus:outline-none"
            />
          </div>
        </div>

        <div className="flex justify-end space-x-3 pt-2">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm font-medium text-slate-400 hover:text-slate-200"
          >
            Dismiss Emergency Action
          </button>
          <button
            onClick={() => quarantineMutation.mutate()}
            disabled={!isConfirmed || quarantineMutation.isPending}
            className="bg-rose-600 hover:bg-rose-500 text-white px-4 py-2 rounded-lg text-sm font-semibold transition disabled:opacity-50"
          >
            {quarantineMutation.isPending ? 'Propagating...' : 'Confirm Quarantine Block'}
          </button>
        </div>
      </div>
    </div>
  );
};
```

---

## 5. Anti-Patterns & Prohibited Idioms

### Anti-Pattern 1: High-Cardinality Prometheus Label Poisoning (DIST-02)
- **The Pitfall**: Adding dynamic labels like `user_id`, `client_ip`, raw URLs, or request UUIDs to Prometheus metrics.
- **Why it Breaks**: Every unique label set creates a new time series in Prometheus TSDB. 10,000 unique callers creates 10,000 time series per hour, exhausting server RAM and failing scraping timeouts.
- **Prohibited**:
  ```go
  // NEVER DO THIS: Unbounded cardinality and PII leak!
  httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, clientIP, principalID).Inc()
  ```
- **Approved Idiom**:
  ```go
  // ONLY USE BOUNDED STATIC ENUM LABELS:
  m.HTTPRequestsTotal.WithLabelValues(r.Method, matchedRouteID, strconv.Itoa(statusCode)).Inc()
  ```

---

### Anti-Pattern 2: Ambient Cookie Authentication Without CSRF Enforcement (OPS-04)
- **The Pitfall**: Authenticating management endpoints purely via browser cookies without checking custom headers or tokens.
- **Why it Breaks**: Attackers can trigger cross-origin `fetch()` calls from malicious websites; the victim browser automatically sends ambient cookies, permitting unauthorized snapshot publication or quarantine actions.
- **Prohibited**:
  ```go
  // NEVER DO THIS: Ambient cookie without CSRF token verification
  func (s *APIServer) HandlePublish(w http.ResponseWriter, r *http.Request) {
      // Missing CSRF check!
  }
  ```
- **Approved Idiom**:
  ```go
  // Require constant-time CSRF token check on all mutating requests:
  csrfHeader := r.Header.Get("X-CSRF-Token")
  if subtle.ConstantTimeCompare([]byte(csrfHeader), []byte(sess.CSRFToken)) != 1 {
      http.Error(w, `{"code":"FORBIDDEN","message":"CSRF token validation failed"}`, http.StatusForbidden)
      return
  }
  ```

---

### Anti-Pattern 3: Decrementing Snapshot Versions on Rollback (CTRL-06, Invariant 9)
- **The Pitfall**: Implementing rollback by reverting the active version counter to $N-1$ or reverting database pointers.
- **Why it Breaks**: Replicas enforce Invariant 9: `if snapshot.Version <= current.Version { rejectSnapshot() }`. Any snapshot version <= the current replica version is rejected with `ACK_STATUS_REJECTED`. The fleet will remain stuck on the faulty version.
- **Prohibited**:
  ```go
  // NEVER DO THIS: Decrementing snapshot version on rollback
  payload.Version = targetVersion // targetVersion < latestVersion!
  ```
- **Approved Idiom**:
  ```go
  // ALWAYS republish historical content as strictly increasing monotonic version N+1:
  payload.Version = latestVersion + 1
  ```

---

### Anti-Pattern 4: Blind Concurrency Overwrites (Omitting ETag / If-Match) (OPS-02)
- **The Pitfall**: Permitting policy or route mutations without validating current entity state.
- **Why it Breaks**: Alice and Bob open draft v42 simultaneously. Bob publishes v43 with a security fix. Alice publishes v44 based on stale v42, unknowingly clobbering Bob's fix.
- **Prohibited**:
  ```go
  // NEVER mutate without concurrency check:
  s.routeRepo.UpsertRoute(ctx, newRoute)
  ```
- **Approved Idiom**:
  ```go
  if !ValidateIfMatch(w, r, currentETag) {
      return // Emits HTTP 412 Precondition Failed
  }
  ```

---

### Anti-Pattern 5: Double-Execution Idempotency Race (OPS-02)
- **The Pitfall**: Querying idempotency cache and executing the mutation without an atomic lock.
- **Why it Breaks**: Two parallel retries arrive within 5ms. Both query the cache, find no entry, and both publish snapshots, causing skipped versions.
- **Prohibited**:
  ```go
  // Naive check without in-flight lock:
  if resp := cache.Get(key); resp == nil {
      doHeavyPublish()
      cache.Set(key, resp)
  }
  ```
- **Approved Idiom**:
  ```go
  resp, acquired, valid := s.idempStore.LockKey(key)
  if !acquired {
      if resp != nil {
          replayCachedResponse(w, resp)
          return
      }
      http.Error(w, `{"code":"CONFLICT","message":"Concurrent request in progress"}`, http.StatusConflict)
      return
  }
  defer s.idempStore.UnlockKey(key)
  ```

---

### Anti-Pattern 6: Loading Remote Monaco Editor from CDNs (OPS-03, OPS-04)
- **The Pitfall**: Using `@monaco-editor/react` default configuration which downloads Monaco scripts dynamically from `cdn.jsdelivr.net`.
- **Why it Breaks**: Violates Content Security Policy (`script-src 'self'`), breaks in air-gapped / offline deployments, and exposes the administrative plane to third-party CDN supply chain compromises.
- **Approved Idiom**:
  Install `monaco-editor` directly in `package.json`, configure local web workers using Vite bundling, and define the custom Monarch tokenizer locally in `web/dashboard/src/utils/rego-monarch.ts`.

---

### Anti-Pattern 7: Unbounded PostgreSQL Partition Scans on Audit Logs (OPS-01)
- **The Pitfall**: Running queries on partitioned table `audit_events` without timestamp boundaries.
- **Why it Breaks**: Partition pruning requires the partition key (`event_date`). Omitting date constraints forces PostgreSQL to scan every daily partition in existence, causing database CPU spikes and slow HTTP responses.
- **Approved Idiom**:
  Always default query windows to the last 24 hours (`timestamp >= NOW() - INTERVAL '24 hours'`) and require explicit ranges if querying historical windows.

---

### Anti-Pattern 8: Permitting Data-Plane JWTs on Management Control Plane (Invariant 11)
- **The Pitfall**: Reusing the same JWT validator on `/control/v1` that verifies user tokens on `:8080` without administrative scope checks.
- **Why it Breaks**: Regular end-users with valid data-plane Bearer tokens can invoke management APIs, violating trust boundary TB-7.
- **Approved Idiom**:
  Management APIs require operator session cookies or dedicated administrative tokens. If an inbound Bearer token has audience `aegis-gateway` or principal kind `workload`, immediately reject with `HTTP 403 Forbidden` (`DATA_PLANE_CREDENTIALS_REJECTED`).

---

## 6. Validation & Test Harness Mapping

| Requirement / Invariant | Test File Target | Harness & Strategy | Assertions & Boundaries |
|---|---|---|---|
| **OPS-01** (REST APIs) | [`internal/control/api_test.go`](file:///home/logan78/Desktop/Aegis/internal/control/api_test.go) | `httptest.NewServer`, Chi router, mock repos | Validates route CRUD, draft creation, simulation, publish, rollback, gateways, and quarantine endpoints. |
| **OPS-02** (Concurrency & Idempotency) | [`internal/control/concurrency_test.go`](file:///home/logan78/Desktop/Aegis/internal/control/concurrency_test.go) | `httptest.NewServer`, parallel goroutines | Asserts `If-Match` mismatch returns 412; asserts concurrent `Idempotency-Key` returns 409; asserts completed key returns 200 with `Idempotent-Replay: true`. |
| **OPS-03** (Dashboard Experience) | `web/dashboard` compilation | `tsc --noEmit && vite build` | Asserts TypeScript type safety, zero build warnings, valid Tailwind styling, and Monaco Monarch tokenizer syntax. |
| **OPS-04** (Session & CSRF) | [`internal/control/session_test.go`](file:///home/logan78/Desktop/Aegis/internal/control/session_test.go) | `httptest.NewServer`, cookie jars | Asserts HttpOnly cookies, rejection of mutating calls lacking `X-CSRF-Token` (403), CSP headers, and RBAC role boundaries. |
| **DIST-02** (Prometheus Telemetry) | [`internal/telemetry/metrics_test.go`](file:///home/logan78/Desktop/Aegis/internal/telemetry/metrics_test.go) | Custom registry, `httptest.NewServer` | Scrapes `/metrics`, asserts request counters and histograms, asserts zero high-cardinality PII labels. |
| **Invariant 11** (Plane Separation) | [`internal/control/api_test.go`](file:///home/logan78/Desktop/Aegis/internal/control/api_test.go) | Synthetic requests with gateway Bearer token | Asserts presentation of gateway data-plane token returns HTTP 403 Forbidden. |
| **Integration** (End-to-End) | [`tests/integration/operator_telemetry_test.go`](file:///home/logan78/Desktop/Aegis/tests/integration/operator_telemetry_test.go) | Live listeners on `:8084`, `:9090`, `:9091`, `:9092` | Tests full lifecycle: simulation -> publish -> gRPC delivery -> metric bump -> Redis quarantine. |
