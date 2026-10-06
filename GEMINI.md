<!-- GSD:project-start source:PROJECT.md -->
## Project

**Aegis — Distributed Zero-Trust Access Gateway**

Aegis is an independently designed, cloud-native distributed zero-trust access gateway and control plane in Go. It enforces identity-based authorization, workload authentication via mTLS, request-time OPA/Rego policy evaluation, rate limiting, and durable audit event streaming for private backend microservices without requiring a full service mesh. Built as an SDE internship portfolio showcase and a reference system that can be hardened for controlled production deployments.

**Core Value:** Default-deny, fail-closed authorization and workload authentication where invalid credentials, stale security state, or dependency failures never produce implicit authorization.

### Constraints

- **Language & Runtime**: Go for gateway, control plane, audit worker, and demo services; React/TypeScript for operator dashboard.
- **Architecture**: In-memory OPA snapshot evaluation on the gateway hot path; no synchronous PostgreSQL or external OPA network calls during authorization.
- **Security Invariants**: 12 mandatory invariants (default-deny, verified identity only, no upstream URL tampering, atomic snapshot activation, pre-forward durable audit, mTLS to backends with signed assertions).
- **Failure Semantics**: Fail-closed (503) on Redis revocation outage, expired snapshot lease (>60s), or spool saturation (>90%).
- **Verification**: Strict automated test suite with negative tests, race detection, fault injection, and reproducible benchmarks.
<!-- GSD:project-end -->

<!-- GSD:stack-start source:research/STACK.md -->
## Technology Stack

## Recommended Stack
### Core Technologies
| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| **Go** | `1.25+` (`go1.25.5` runtime, `go 1.24+` in `go.mod`) | Primary runtime for Gateway, Control Plane, Audit Worker, and Backend microservices | Standard systems language for cloud-native networking. Go 1.22+ enhanced ServeMux routing, `log/slog` structured logging, lock-free `sync/atomic.Pointer`, and low-latency garbage collection ensure predictable <2ms policy evaluation budgets. |
| **Go Reverse Proxy (`net/http` & `httputil.ReverseProxy`)** | Go Standard Library | Edge HTTP/mTLS termination, path validation, header scrubbing, and upstream dispatch | Spec requirement (§2, §5). Zero third-party runtime overhead. Uses modern Go 1.20+ `Rewrite` hook (instead of deprecated `Director`) to safely decouple inbound `r.In` from outbound `r.Out`, preventing header injection, hop-by-hop leaks, and path divergence. |
| **Embedded OPA Engine (`open-policy-agent/opa`)** | `v1.21.1` (`github.com/open-policy-agent/opa/v1/rego`) | In-memory declarative policy evaluation on the request hot path | Spec requirement (§2, §6). Precompiled Rego queries (`rego.PrepareForEval(ctx)`) evaluate locally in <0.2ms, satisfying the <2ms p99 budget without remote network hops or external daemon failure modes. Rego v1 syntax is enabled by default. |
| **gRPC & Protobuf (`grpc-go`)** | gRPC `v1.83.2` (`google.golang.org/grpc`), Protobuf `v1.36.12` (`google.golang.org/protobuf`) | Control Plane to Gateway snapshot distribution, freshness leases, and acknowledgment telemetry | Spec requirement (§2, §6). Multiplexed HTTP/2 bidirectional streaming delivers signed monotonic configuration snapshots and 10-second freshness heartbeats with reconnect jitter and low CPU overhead. |
| **PostgreSQL Driver & Pool (`pgx/v5`)** | `v5.9.2` (`github.com/jackc/pgx/v5/pgxpool`) | Relational persistence for Control Plane metadata and batch audit ingestion | Spec requirement (§2, §8). High-performance pure Go driver with binary protocol encoding, statement caching, JSONB support, and native `pgx.Batch` / `COPY` for high-throughput audit log ingestion. |
| **Redis Client (`go-redis/v9`)** | `v9.22.0` (`github.com/redis/go-redis/v9`) | Shared atomic token bucket rate limiting, JTI token revocation, and principal quarantine | Spec requirement (§2, §4, §9). Supports connection pooling, automatic pipelining, atomic Lua scripts (`EvalSha`), and context cancellation for enforcing strict 200ms dependency deadlines and fail-closed semantics. |
| **JWT Cryptography (`golang-jwt/jwt/v5`)** | `v5.3.1` (`github.com/golang-jwt/jwt/v5`) | Ingress access token validation and gateway-to-backend assertion minting | Spec requirement (§4, §5, §17). RFC 8725 compliant, actively maintained. Enforces pinned algorithm allowlists (RS256, ES256, EdDSA), explicit audience/issuer validation, and prevents algorithm confusion (`alg: none`). |
| **Metrics Observability (`client_golang`)** | `v1.24.1` (`github.com/prometheus/client_golang`) | Low-overhead gateway and control plane telemetry export | Spec requirement (§12). CNCF standard for Prometheus instrumentation. Thread-safe counters/histograms with fixed label sets to prevent metric cardinality explosion. |
| **Distributed Tracing (`opentelemetry-go`)** | `v1.47.0` (`go.opentelemetry.io/otel`, SDK `v1.47.0`) | Distributed request tracing across gateway ingress, policy checks, spooling, and upstreams | Spec requirement (§2, §12). W3C TraceContext propagation across network hops; bounded non-blocking sampling protects gateway throughput during ingress surges. |
| **Operator Dashboard (React / Vite / TypeScript)** | React `19.3.0`, Vite `8.3.2`, TypeScript `5.7+` | Web-based management console for policy authoring, dry-run simulation, convergence, and quarantine | Spec requirement (§2, §7). Sub-second HMR with Vite, strict compile-time types for OpenAPI contracts, and responsive operational UI. |
### Supporting Libraries
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| **`github.com/go-chi/chi/v5`** | `v5.3.2` | REST router and middleware engine | Control Plane management APIs (`/control/v1`) and private backend demo microservices (`orders`, `payments`, `admin`). 100% `net/http` compatible. |
| **`github.com/pressly/goose/v3`** | `v3.27.3` | Schema migrations with forward/backward rollback | Bootstrapping PostgreSQL tables (`migrations/`) via CLI or Go binary embedding (`embed.FS`) during automated test setups and `make bootstrap`. |
| **`github.com/oapi-codegen/oapi-codegen/v2`** | `v2.5.0` | OpenAPI 3.0/3.1 contract code generator | Generating Go server interfaces, models, and client bindings from `api/openapi/control-v1.yaml` to prevent contract drift. |
| **`google.golang.org/grpc/cmd/protoc-gen-go-grpc`** | `v1.6.2` | Protobuf gRPC Go generator | Generating gRPC client/server stubs from `api/proto/snapshot/v1/snapshot.proto`. |
| **`google.golang.org/protobuf/cmd/protoc-gen-go`** | `v1.36.12` | Protobuf message Go generator | Generating strongly typed Go structs for snapshot payloads and lease messages. |
| **`github.com/google/uuid`** | `v1.6.0` | Cryptographically random UUIDs | Generating unique `X-Request-ID` correlation identifiers, audit event IDs, and idempotency tokens. |
| **`github.com/go-redis/redis_rate/v10`** | `v10.0.1` | Generic Cell Rate Algorithm (GCRA) token bucket rate limiting | Implementing atomic per-principal and per-route rate limiting over Redis with automatic `Retry-After` calculation. |
| **Standard Library `crypto/ed25519`** | Go Stdlib | Digital signatures for configuration snapshots and assertions | Signing and verifying monotonic configuration snapshots and backend assertion tokens with high performance and constant-time execution. |
| **Standard Library `os.File.Sync` & `hash/crc32`** | Go Stdlib | Local append-only Write-Ahead Log (WAL) audit spool | Appending pre-forward decision records with forced `fsync()` and CRC32 checksums before upstream network dispatch, with zero CGO dependencies. |
| **`@tanstack/react-query`** | `v5.104.1` | Dashboard async state management and cache | Polling replica convergence, live health status, audit event streams, and managing mutation states. |
| **`tailwindcss`** | `4.3.3` | Utility-first dashboard styling | Responsive dashboard layout, dark/light theme, alert banners, and topology maps. |
| **`lucide-react`** | `1.52.0` | Dashboard SVG icon set | Rendering status indicators (healthy, degraded, quarantined), arrows, and action icons. |
| **`@monaco-editor/react`** | `v4.7.0` | In-browser Rego policy editor and simulator | Live policy authoring, syntax highlighting, and JSON input/output dry-run simulation in the dashboard. |
| **`github.com/stretchr/testify`** | `v1.12.1` | Unit, security, and integration assertions | Testing token parsing, path canonicalization, policy precedence, and negative security tests (`assert`, `require`, `suite`). |
| **Grafana `k6`** | `v2.3.0` | Load generation and latency SLO benchmarking | Executing reproducible load tests at 1,000+ RPS, measuring p50/p95/p99 latency, and recording convergence metrics. |
### Development Tools
| Tool | Purpose | Notes |
|------|---------|-------|
| **Go Toolchain (`go`)** | Compilation, race detection, fuzzing, testing | Run with `-race` in all CI and integration runs to verify lock-free snapshot atomic swaps and worker concurrency. |
| **Docker & Docker Compose (v2.29+)** | Multi-profile local orchestration | Provides discrete profiles: `mvp`, `hardened`, `distributed`, `observability` without requiring cloud infrastructure. |
| **`golangci-lint` (v1.64+)** | Static analysis and linter | Enforces code formatting, error checking (`errcheck`), security analysis (`gosec`), and deprecated API warnings. |
| **`cfssl` or Go CA scripts (`scripts/certificates`)** | Development PKI and mTLS certificate generator | Generates Root CA, Intermediate CA, workload certificates (`spiffe://aegis.local/...`), and gateway certificates. Never commits private keys. |
| **`curl` / `httpie`** | Manual CLI verification and demo scripts | Validating positive and negative authorization flows (`make demo`). |
## Installation
### Core Go Dependencies
# Initialize Go module
# Core HTTP, OPA, gRPC, Persistence, Cache, and Crypto
### Supporting Go Libraries
# REST Router, Rate Limiting, Migrations, UUIDs, and Testing
### Development Tools & Code Generators
# Install Protobuf and OpenAPI code generators
### Frontend Dashboard Dependencies (`web/dashboard`)
# Navigate to dashboard directory
# Core React & Vite
# Development dependencies
## Alternatives Considered
| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| **Embedded OPA (`open-policy-agent/opa/v1/rego`)** | External OPA Daemon (`http://opa:8181/v1/data`) | Use external daemon only when polyglot microservice fleets (Python, Java, Node) share a single policy sidecar. For Aegis, external daemon adds 2–10ms network latency per request, introduces a network failure point, and violates the <2ms p99 SLA. Embedded evaluation executes in-memory in <0.2ms. |
| **Go Standard Library ReverseProxy (`httputil.ReverseProxy`)** | Envoy Proxy or Traefik | Use Envoy in massive hyperscale service meshes requiring transparent HTTP/3 or complex L4 connection pools. For Aegis, Go's `httputil.ReverseProxy` with `Rewrite` gives programmatic, end-to-end control of the zero-trust pipeline (mTLS verification, pre-forward fsync, signed assertions) without Envoy's C++ / Lua filter complexity. |
| **`pgx/v5` (`github.com/jackc/pgx/v5`)** | `lib/pq` (`github.com/lib/pq`) | Never for new projects. `lib/pq` is officially in maintenance mode, lacks native connection pooling (`pgxpool`), lacks statement caching, and uses slower text format. `pgx/v5` is the active, high-performance PostgreSQL driver in Go. |
| **`go-redis/v9`** | `gomodule/redigo` | `redigo` requires manual connection management and type assertions. `go-redis/v9` is fully type-safe, supports context-aware cancellation, pipelining, and automatic Sentinel/Cluster failover. |
| **Local Disk WAL Spool (`os.File.Sync`)** | Apache Kafka or RabbitMQ | Use Kafka only in multi-team enterprise environments with dedicated streaming infrastructure (Phase 7 extension). For v1, a local disk WAL with pre-forward `fsync()` guarantees durability (Invariant 10), survives database outages, and has zero external broker dependencies. |
| **Docker Compose + K8s Manifests** | Terraform / Cloud-Specific IaaS | Use Terraform when deploying managed cloud infrastructure (AWS/GCP). For Aegis portfolio and local verification, Docker Compose profiles (`mvp`, `hardened`, `distributed`) enable 100% reproducible local execution (`make up`, `make test`) without cloud accounts or costs. |
| **Deterministic OPA Rego Rules** | ML / Anomaly Detection Authorization Models | Never rely on ML models for binary access control decisions. Anomaly models can only act as non-blocking advisory risk signals (Phase 7); they must never turn a policy deny into an allow or introduce non-deterministic authorization delays. |
## What NOT to Use
| Avoid | Why | Use Instead |
|-------|-----|-------------|
| **External OPA HTTP Daemon** | Adds 2–10ms network roundtrip; creates an external dependency on the request hot path; crashes or network timeouts cause fail-open risk or widespread gateway 503s. | **Embedded OPA Go Engine** (`github.com/open-policy-agent/opa/v1/rego`) with precompiled queries. |
| **Heavy Service Mesh (Istio / Linkerd)** | Introduces sidecar injection complexity, Envoy proxy overhead, and obscures the security perimeter. Spec explicitly dictates gateway-to-backend enforcement. | **Gateway reverse proxy with mTLS and signed context assertions** (`X-Aegis-Assertion`). |
| **`httputil.ReverseProxy.Director`** | Deprecated in Go 1.20+. Does not decouple inbound from outbound requests; fails to sanitize hop-by-hop headers; causes path desynchronization between `Path` and `RawPath`. | Modern **`httputil.ReverseProxy.Rewrite`** hook with `*httputil.ProxyRequest`. |
| **`path.Clean()` on Raw Ingress Paths** | Attempting to "repair" paths with dot segments (`..`), encoded slashes (`%2F`), or double slashes (`//`) causes impedance mismatch with backend routers, enabling path-traversal bypasses. | **Strict Zero-Repair Rejection**: Return `HTTP 400 Bad Request` immediately on path anomalies; forward exact validated path bytes. |
| **`github.com/lib/pq`** | Effectively deprecated; lacks native connection pool (`pgxpool`), prepared statement caching, and high-performance binary protocol support. | **`github.com/jackc/pgx/v5/pgxpool`**. |
| **Kafka, RabbitMQ, or NATS in v1** | Excessive operational footprint for local demos; introduces ZooKeeper/KRaft broker management before proving core access gateway primitives. | **Local append-only WAL disk spool** with background batch worker to PostgreSQL. |
| **Raw Bearer Token Forwarding to Backends** | Leaks external user tokens into internal service mesh; allows compromised backend services to replay tokens or impersonate users across other services. | **Signed Backend Assertions** (`iss: aegis-gateway`, audience-bound, 15s expiry) minted by gateway after authorization. |
| **Client-Supplied Forwarding Headers** | Blindly trusting `X-Forwarded-For`, `X-Aegis-User`, or `X-Client-Cert-SAN` enables trivial identity spoofing and bypasses. | **Strip all external forwarding and `X-Aegis-*` headers**; reconstruct forwarding context strictly from authenticated TLS connection. |
| **Custom Cryptographic Primitives** | Writing custom JWT parsing, RSA verification, or password hashing creates severe cryptographic vulnerabilities (e.g., timing attacks, padding oracles). | **Maintained standard libraries**: `crypto/ed25519`, `crypto/tls`, `golang-jwt/jwt/v5`. |
| **Synchronous Database Reads on Request Hot Path** | Querying PostgreSQL for route matching or policy checks introduces 10–50ms latency, connection pool starvation, and makes the database a hard single point of failure. | **Immutable in-memory snapshots** updated via streaming gRPC and swapped with `sync/atomic.Pointer`. |
## Stack Patterns by Variant
### Variant 1: Milestone MVP (Phase 1 — Secure Vertical Slice)
- **Profile:** Single gateway replica, demo issuer, three mock backend services.
- **Components:**
- **Exit Gate:** Proves developer allow/deny matrix, strict path traversal rejection (400), and header sanitization.
### Variant 2: Hardened Local Deployment (Phases 2–4 — Complete Security Demonstration)
- **Profile:** Mutual TLS, bypass prevention, Control Plane, PostgreSQL, Redis, React Dashboard.
- **Components:**
- **Exit Gate:** Proves workload authentication, enforced bypass prevention, dependency outage fail-closed semantics, and dashboard policy simulation.
### Variant 3: Distributed Resilience & Performance (Phase 5)
- **Profile:** Multi-replica gateway grid with load balancing and failure testing.
- **Components:**
- **Exit Gate:** Demonstrates sub-5-second snapshot convergence, <20ms added p99 gateway latency under 1,000 RPS, and zero bypass during node failure.
### Variant 4: Production Target Architecture (Phase 6)
- **Profile:** Kubernetes isolation, external OIDC, and high-availability infrastructure.
- **Components:**
## Version Compatibility Matrix
| Component A | Component B / Environment | Compatibility & Integration Notes |
|-------------|---------------------------|-----------------------------------|
| **Go 1.25.x / 1.24.x** | **`open-policy-agent/opa/v1/rego` (v1.21.1)** | Compatible. OPA v1.x requires Go 1.23+ and defaults to Rego v1 syntax (`allow if { ... }`). |
| **Go 1.25.x / 1.24.x** | **`google.golang.org/grpc` (v1.83.2)** | Compatible. Supports HTTP/2 multiplexing and context cancellation. |
| **`protoc-gen-go-grpc` (v1.6.2)** | **`google.golang.org/grpc` (v1.83.2)** | Compatible. Requires `grpc-go` >= v1.64.0 to resolve generated `StaticMethod` symbols. |
| **`pgx/v5` (v5.9.2)** | **PostgreSQL 16.x / 17.x** | Full support for SCRAM-SHA-256 authentication, range partitioning, JSONB, and batch COPY operations. |
| **`goose/v3` (v3.27.3)** | **`pgx/v5/stdlib`** | Native support for running SQL migrations against PostgreSQL using the `pgx` driver. |
| **`go-redis/v9` (v9.22.0)** | **Redis 7.2+ / 7.4+** | Full RESP3 protocol support, Lua script caching (`EVALSHA`), and Sentinel/Cluster support. |
| **`redis_rate/v10` (v10.0.1)** | **`go-redis/v9` (v9.22.0)** | Specifically designed for `go-redis/v9` using an atomic single-roundtrip GCRA Lua script. |
| **`golang-jwt/jwt/v5` (v5.3.1)** | **Ed25519 & ECDSA P-256** | Supports standard Go `crypto/ed25519` and `crypto/ecdsa` keys out of the box with zero external CGO bindings. |
| **Vite 8.3.2** | **React 19.3.0 & TypeScript 5.7+** | High-speed ESM-based dev server and Rollup production build; compatible with React 19 JSX runtime. |
| **Tailwind CSS 4.3.3** | **Vite 8.3.2** | Uses `@tailwindcss/vite` plugin for zero-config CSS compilation without PostCSS boilerplate. |
## Sources
- **`/open-policy-agent/opa`** (Context7) — Verified embedded Rego SDK v1 usage: `rego.PrepareForEval(ctx)`, `rego.EvalInput()`, and Rego v1 declarative syntax (`allow if { ... }`).
- **`/jackc/pgx`** (Context7) — Verified `pgx/v5` connection pool configuration (`pgxpool.New()`) and high-throughput PostgreSQL integration.
- **`/redis/go-redis` & `/go-redis/redis_rate`** (Context7) — Verified `go-redis/v9` client and GCRA token bucket rate limiting via atomic Lua script execution.
- **`/golang-jwt/jwt`** (Context7) — Verified `golang-jwt/jwt/v5` parser options (`jwt.WithValidMethods`, `jwt.WithIssuer`, `jwt.WithAudience`) and RFC 8725 compliance.
- **Official GitHub Releases (2026)** — Verified current production tags for OPA (`v1.21.1`), gRPC (`v1.83.2`), pgx (`v5.9.2`), go-redis (`v9.22.0`), Vite (`8.3.2`), React (`19.3.0`), Tailwind (`4.3.3`), and Goose (`v3.27.3`).
- **NIST SP 800-207 & RFC 8725** — Zero Trust Architecture and JWT Best Current Practices guiding default-deny, fail-closed, and assertion signing standards.
<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->
## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->
## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->
## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->
## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:
- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->



<!-- GSD:profile-start -->
## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
