# Architecture Research

**Domain:** Distributed Zero-Trust Access Gateway & Policy Control Plane  
**Researched:** 2026-10-06  
**Confidence:** HIGH (Derived directly from Aegis Specification v1.0, NIST SP 800-207, RFC 8725, and production gateway engineering practices)

---

## Standard Architecture

A zero-trust access gateway decouples policy decision and enforcement (data plane) from policy management, distribution, and lifecycle tracking (control plane). In zero-trust architecture (NIST SP 800-207), the gateway serves as the **Policy Enforcement Point (PEP)**, while the control plane acts as the **Policy Administration Point (PAP)** and configures the in-memory **Policy Decision Point (PDP)**. 

To achieve sub-2ms authorization decisions and strict fail-closed security guarantees, the data plane must never perform synchronous network calls to databases or external policy daemons on the request path. Instead, it relies on locally compiled, cryptographically signed, immutable configuration snapshots updated via streaming gRPC with cryptographic freshness leases.

### System Overview

```
                                      CLIENTS & WORKLOADS
                                               │
               ┌───────────────────────────────┴───────────────────────────────┐
               │ HTTPS (Port 8443)                                             │ mTLS (Port 9443)
               ▼ [User Bearer Token]                                           ▼ [URI SAN Cert: spiffe://...]
┌─────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│ DATA PLANE: AEGIS GATEWAY REPLICAS (PEP / Embedded PDP)                                                     │
│                                                                                                             │
│  1. Ingress Listener ──► 2. Strict Target ──► 3. Route Resolution ──► 4. Authenticate Identity             │
│     (Limits, Req ID)        Validation           (Prefix / Exact)        (JWT / X.509 URI SAN)              │
│                                                                                  │                          │
│  7. Pre-Forward Fsync ◄── 6. Atomic Rate Limit ◄── 5. Revocation Check ◄─────────┘                          │
│     (Local WAL Spool)        & Concurrency            (Redis Bloom/Hash)                                    │
│             │                                                                                               │
│             ▼                                                                                               │
│  8. Policy Evaluation ──► 9. Header Sanitization ──► 10. Signed Assertion ──► 11. Upstream Proxy            │
│     (In-Memory OPA Rego)     (Strip X-Aegis-*, etc)      (Short-lived JWT)        (mTLS Client Context)     │
│                                                                                           │                 │
│                                                       12. Completion Event & Metrics ◄────┘                 │
└──────────────┬───────────────────────────────┬───────────────────────────────┬──────────────────────────────┘
               │                               │                               │
               │ Verified mTLS                 │ Pre-forward WAL               │ gRPC Snapshot Stream
               │ + Signed Assertion            │ Spool Appends                 │ (10s lease / 60s timeout)
               ▼                               ▼                               ▲
┌──────────────────────────────┐ ┌──────────────────────────────┐              │
│ PRIVATE BACKEND SERVICES     │ │ DURABLE AUDIT SPOOL WORKER   │              │
│ (Bypass-Prevented Microserv) │ │ (At-Least-Once Delivery)     │              │
│                              │ │                              │              │
│ ┌──────────────────────────┐ │ │ ┌──────────────────────────┐ │              │
│ │ Orders Service (:8081)   │ │ │ │ Disk WAL Reader / Cursor │ │              │
│ └──────────────────────────┘ │ │ └────────────┬─────────────┘ │              │
│ ┌──────────────────────────┐ │ │              ▼               │              │
│ │ Payments Service (:8082) │ │ │ │ Batch De-duplication     │ │              │
│ └──────────────────────────┘ │ │ └────────────┬─────────────┘ │              │
│ ┌──────────────────────────┐ │ └──────────────┼───────────────┘              │
│ │ Admin Service (:8083)    │ │                │                              │
│ └──────────────────────────┘ │                │ Batch Inserts                │
└──────────────────────────────┘                ▼                              │
                                 ┌──────────────────────────────┐              │
                                 │ POSTGRESQL 16+               │              │
                                 │ (Authoritative State Store)  │              │
                                 │                              │              │
                                 │ • Routes & Policy Drafts     │              │
                                 │ • Signed Snapshots & Outbox  │              │
                                 │ • Gateway Heartbeat / Acks   │              │
                                 │ • Partitioned Audit Events   │              │
                                 └──────────────▲───────────────┘              │
                                                │                              │
                                                │ Read / Write State           │
                                                ▼                              │
                                 ┌──────────────────────────────┐              │
                                 │ CONTROL PLANE (PAP)          │──────────────┘
                                 │ (Policy Admin & Compiler)    │
                                 │                              │
                                 │ • Policy Compiler / Rego Test│
                                 │ • Monotonic Snapshot Signer  │
                                 │ • gRPC Snapshot Streamer     │
                                 │ • Redis Quarantine Dispatch  │
                                 └──────────────▲───────────────┘
                                                │
                                                │ REST / OpenAPI (/control/v1)
                                                │ Session Cookie + CSRF
                                                ▼
                                 ┌──────────────────────────────┐
                                 │ OPERATOR DASHBOARD           │
                                 │ (React / TypeScript Vite)    │
                                 │                              │
                                 │ • Real-time Policy Simulator │
                                 │ • Rollout & Convergence Map  │
                                 │ • Audit Stream & Quarantine  │
                                 └──────────────────────────────┘
```

---

### Component Responsibilities

| Component | Responsibility | Typical Implementation | Must Not Do |
|-----------|----------------|------------------------|-------------|
| **Gateway Replicas (`cmd/gateway`)** | Ingress TLS/mTLS termination, identity extraction, path canonicalization, atomic snapshot resolution, embedded OPA evaluation, Redis revocation/rate limiting, pre-forward disk spool fsync, header scrubbing, short-lived assertion signing, upstream mTLS proxying, completion auditing. | Go 1.23+ HTTP server, `net/http`, `httputil.ReverseProxy`, embedded OPA Go SDK (`github.com/open-policy-agent/opa/rego`), `crypto/tls`. | Accept client-specified upstreams; perform synchronous DB queries; authorize requests with expired snapshot leases (>60s); fallback to fail-open on Redis outage. |
| **Control Plane (`cmd/control-plane`)** | Policy drafting, OpenAPI validation, Rego unit testing & simulation, cryptographic snapshot packaging and signing, gRPC distribution with 10s freshness leases, gateway replica acknowledgment tracking, instant quarantine dispatch to Redis. | Go service, `google.golang.org/grpc`, `crypto/ed25519` or `crypto/ecdsa`, PGX pool for PostgreSQL. | Sit on the synchronous data path; allow publishing untested or unsigned snapshots; perform unauthenticated management operations. |
| **Durable Audit Spool Worker (`cmd/audit-worker`)** | Tails gateway append-only local WAL files, batches security decision and completion events, deduplicates by `(partition_date, event_uuid)`, commits to PostgreSQL with exponential backoff, advances persistent file checkpoints. | Go background daemon or embedded gateway worker reading local POSIX file descriptors, batching with configurable flush interval/size (e.g. 500 events / 200ms). | Silently drop audit events on DB outage; mark uncommitted batches as acknowledged; execute table updates/deletions on audit tables. |
| **Backend Demo Services (`services/{orders, payments, admin}`)** | Core application functionality (orders listing, payment processing, user management). Enforce bypass prevention via mTLS middleware verifying gateway identity and short-lived backend assertions. | Go microservices running internal HTTP listeners; custom mTLS middleware checking SPIFFE URI SAN and `X-Aegis-Assertion` JWT signature, audience, and path. | Bind to public or host interfaces; accept raw caller bearer tokens directly; trust plain identity headers without signed gateway context. |
| **PostgreSQL 16+** | Authoritative relational persistence for route catalogs, policy drafts, version history, signed snapshots, outbox events, gateway replica acks, and daily partitioned immutable audit logs. | PostgreSQL instance with daily range partitioning on `audit_events(event_timestamp)`, foreign keys, JSONB validation, separate least-privilege DB users. | Serve hot-path authorization queries directly to gateway replicas; allow audit writer user to modify or delete historical records. |
| **Redis 7+** | High-performance shared in-memory state for token bucket rate limiting (per-principal and per-route) and emergency credential revocations (`jti` blocklist) / principal quarantine records (<5s SLA). | Redis standalone or HA cluster; atomic Lua scripts for token bucket counters; Redis Strings/Hashes with explicit TTLs. | Serve as authoritative store for policy or route configurations; allow permissive bypass when down. |
| **Operator Dashboard (`web/dashboard`)** | Policy authoring and dry-run simulation UI, snapshot release management, one-click rollback, replica convergence visualization, audit log filter/inspector, emergency quarantine control. | React 18+, TypeScript, Vite, Tailwind CSS, TanStack Query, Lucide icons. Communicates exclusively with `/control/v1` via session cookies + CSRF tokens. | Expose private signing keys or raw credentials; display unverified or fabricated metrics; trigger state changes without CSRF validation. |
| **Dev PKI / Demo Issuer (`scripts/certificates`, `cmd/demo-issuer`)** | Generates development root/intermediate CAs, workload certificates, gateway certificates, and issues signed short-lived user JWTs for local development. | Go CLI tools / shell scripts using standard `crypto/x509`, `crypto/rsa`, or `crypto/ed25519`. | Run in production profiles; commit private keys to version control; allow clients to self-assign arbitrary roles. |

---

## Recommended Project Structure

Following Go standards and clean architectural decoupling (matching `spec.md` §13):

```
aegis/
├── cmd/
│   ├── gateway/                  # Gateway binary entrypoint (PEP / Embedded PDP)
│   │   └── main.go
│   ├── control-plane/            # Control plane binary (PAP & gRPC streamer)
│   │   └── main.go
│   ├── audit-worker/             # Spool delivery worker daemon
│   │   └── main.go
│   └── demo-issuer/              # Development-only JWT issuer (OIDC mock)
│       └── main.go
├── internal/
│   ├── identity/                 # JWT verification, JWKS caching, X.509 / SPIFFE URI parsing
│   │   ├── jwt.go
│   │   ├── jwks.go
│   │   ├── spiffe.go
│   │   └── assertion.go          # Short-lived gateway-to-backend assertion signer & verifier
│   ├── policy/                   # OPA/Rego compilation, evaluator interface, input schemas
│   │   ├── engine.go
│   │   ├── compiler.go
│   │   └── types.go
│   ├── snapshot/                 # Snapshot structs, signing, monotonic versioning, validation
│   │   ├── snapshot.go
│   │   ├── signer.go
│   │   └── verifier.go
│   ├── proxy/                    # Reverse proxy engine, path canonicalizer, header scrubber
│   │   ├── proxy.go
│   │   ├── router.go
│   │   └── sanitizer.go
│   ├── ratelimit/                # Redis-backed atomic token bucket implementation
│   │   ├── limiter.go
│   │   └── script.go             # Redis Lua scripts
│   ├── revocation/               # Redis revocation and quarantine store
│   │   └── store.go
│   ├── audit/                    # Durable disk spool WAL, event serializers, batcher
│   │   ├── event.go
│   │   ├── spool.go              # Append-only WAL with fsync
│   │   └── postgres_writer.go    # Batch PostgreSQL inserter
│   ├── config/                   # Strongly typed environment and file configuration
│   │   └── config.go
│   └── telemetry/                # Prometheus metrics, OpenTelemetry tracers, structured logging
│       ├── metrics.go
│       └── tracing.go
├── services/                     # Private backend demo services
│   ├── middleware/               # Reusable backend mTLS & assertion verification middleware
│   │   └── auth_middleware.go
│   ├── orders/                   # Orders service (:8081)
│   │   └── main.go
│   ├── payments/                 # Payments service (:8082)
│   │   └── main.go
│   └── admin/                    # Admin service (:8083)
│       └── main.go
├── api/
│   ├── openapi/                  # OpenAPI 3.0 specs for /control/v1 management APIs
│   │   └── control-v1.yaml
│   └── proto/                    # Protobuf definitions for gRPC snapshot distribution
│       └── snapshot/v1/
│           └── snapshot.proto
├── policies/                     # Seed Rego policies, input data, and OPA unit tests
│   ├── rego/
│   │   ├── authz.rego
│   │   └── rules.rego
│   ├── data/
│   │   └── seed_roles.json
│   └── tests/
│       └── authz_test.rego
├── migrations/                   # Forward-compatible PostgreSQL schema migrations
│   ├── 000001_init_schema.up.sql
│   └── 000001_init_schema.down.sql
├── web/
│   └── dashboard/                # React / TypeScript operator dashboard (Vite)
│       ├── src/
│       ├── package.json
│       └── vite.config.ts
├── deployments/
│   ├── compose/                  # Docker Compose profiles (mvp, hardened, distributed)
│   │   ├── docker-compose.mvp.yml
│   │   ├── docker-compose.hardened.yml
│   │   └── docker-compose.distributed.yml
│   ├── kubernetes/               # Production target manifests (NetworkPolicies, Deployments)
│   │   ├── gateway-deployment.yaml
│   │   └── network-policy.yaml
│   └── observability/            # Prometheus scrape configs, Grafana dashboards
├── scripts/
│   ├── bootstrap/                # Development environment bootstrap scripts
│   ├── certificates/             # Dev CA and mTLS certificate generator
│   ├── demo/                     # End-to-end security demo verification scripts
│   └── benchmark/                # k6 load test scripts and baseline benchmarks
├── tests/
│   ├── integration/              # Component integration tests
│   ├── security/                 # Penetration & bypass test suite (negative tests)
│   └── failure/                  # Dependency fault injection tests (Redis/PG/Lease outage)
├── docs/
│   ├── adr/                      # Architectural Decision Records
│   ├── threat-model/             # Threat model analysis and trust boundaries
│   └── runbooks/                 # Operational runbooks (rotation, recovery, incidents)
├── spec.md                       # Canonical architecture specification
├── progress.md                   # Milestone and phase tracking
├── Makefile                      # Standard lifecycle commands (make up, make test, etc.)
├── go.mod
└── go.sum
```

### Structure Rationale

- **`cmd/` vs `internal/`**: In Go standard practice, `cmd/` contains thin binary main packages responsible only for dependency injection, flag parsing, and graceful shutdown orchestration. All domain logic resides in `internal/` to strictly prohibit external unversioned imports.
- **`services/middleware/`**: Backend microservices (`orders`, `payments`, `admin`) are strictly isolated from gateway core code, but share a single, hardened HTTP middleware that enforces gateway mTLS certificate validation and decrypts/verifies `X-Aegis-Assertion` tokens.
- **`api/proto/` and `api/openapi/`**: Single source of truth for all network contracts. Contracts are declared before implementation. Protobuf code (`.pb.go`) and OpenAPI models are code-generated, preventing manual drift.
- **`policies/`**: Policies are managed as first-class code artifacts. OPA Rego policies include their own unit tests (`authz_test.rego`) executed during CI and control plane compilation before snapshot generation.
- **`deployments/compose/`**: Multi-profile orchestration (`mvp`, `hardened`, `distributed`) enables running progressive milestones without altering core application configurations.

---

## Architectural Patterns

### Pattern 1: Immutable In-Memory Snapshot with Atomic Pointer Swapping

**What:** The gateway maintains its entire active routing table, compiled OPA Rego policy decision engine, identity configuration, and lease status in a single immutable Go struct (`Snapshot`). When the control plane streams a new valid snapshot via gRPC, the gateway compiles and validates it, then atomically updates an active pointer using `sync/atomic.Pointer[Snapshot]`.  
**When to use:** On high-throughput authorization hot paths where requests must read policy and routes without acquiring mutex locks, and where route definitions and policies must never be mismatched across versions.  
**Trade-offs:** Consumes memory proportional to policy size (negligible for Rego bundles, typically a few megabytes). Guarantees zero lock contention and instantaneous, atomic policy activation.

**Example:**
```go
package snapshot

import (
	"sync/atomic"
	"time"
	"github.com/open-policy-agent/opa/rego"
)

type Snapshot struct {
	Version     int64
	ExpiresAt   time.Time
	LeaseValid  time.Time
	Routes      map[string]RouteDefinition
	Evaluator   rego.PreparedEvalQuery
	Checksum    string
}

type Manager struct {
	active atomic.Pointer[Snapshot]
}

func (m *Manager) Active() *Snapshot {
	return m.active.Load()
}

func (m *Manager) Activate(next *Snapshot) {
	// Atomic pointer swap: zero locks, lock-free concurrent reads
	m.active.Store(next)
}
```

---

### Pattern 2: Fail-Closed Freshness Lease & Dependency Isolation

**What:** The gateway enforces a strict "fail-closed" security posture. It relies on the control plane for a signed cryptographic lease renewal every 10 seconds. If 60 seconds elapse without a valid lease renewal, the gateway drops readiness and immediately returns `HTTP 503 Service Unavailable` for protected routes. Similarly, if Redis (revocation/quarantine store) is unreachable, the gateway fails closed with 503 instead of falling back to permissive allow.  
**When to use:** Zero-trust networks where stale policies or unverified revocations could allow compromised credentials or terminated workloads to access critical backends.  
**Trade-offs:** Sacrifices partial availability during prolonged control-plane partitions (>60s) or Redis outages in favor of absolute security consistency.

**Example:**
```go
func (g *Gateway) handleAuthorization(w http.ResponseWriter, r *http.Request) {
	snap := g.snapshots.Active()
	
	// Enforce 60-second lease freshness invariant
	if snap == nil || time.Since(snap.LeaseValid) > 60*time.Second {
		g.telemetry.RecordSecurityFailure("lease_expired")
		http.Error(w, "Security lease expired; gateway unready", http.StatusServiceUnavailable)
		return
	}

	// Fail-closed on revocation check timeout or outage
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
	defer cancel()
	
	revoked, err := g.revocationStore.IsRevoked(ctx, tokenJTI, principalID)
	if err != nil {
		g.telemetry.RecordSecurityFailure("revocation_dependency_unavailable")
		http.Error(w, "Security state verification unavailable", http.StatusServiceUnavailable)
		return
	}
	if revoked {
		http.Error(w, "Principal or token revoked", http.StatusForbidden)
		return
	}
}
```

---

### Pattern 3: Pre-Forward Durable Audit Spool (Local WAL Fsync)

**What:** Prior to dispatching any permitted request to a backend service, the gateway writes the structured authorization event to a local append-only Write-Ahead Log (WAL) on persistent disk and forces an `fsync()`. Only upon successful synchronization is the request forwarded to the upstream proxy. An asynchronous worker tails this WAL and streams batches to PostgreSQL.  
**When to use:** Mandatory compliance and non-repudiation environments where an authorized state-mutating request must never execute upstream without a guaranteed durable audit record, even if the database is temporarily partitioned or the gateway crashes.  
**Trade-offs:** Adds disk I/O latency to the request hot path (typically 0.5–2ms on NVMe SSDs). Protects PostgreSQL from synchronous request spikes and guarantees at-least-once audit delivery. If disk spool capacity reaches 90%, the gateway rejects incoming requests (fail-closed).

**Example:**
```go
func (s *DiskSpool) AppendPreForward(event *AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isSpoolSaturated(0.90) {
		return ErrSpoolSaturated // Gateway returns HTTP 503
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if _, err := s.activeFile.Write(data); err != nil {
		return err
	}

	// Mandatory fsync before upstream network dispatch
	return s.activeFile.Sync()
}
```

---

### Pattern 4: Gateway-to-Backend Cryptographic Context Assertion

**What:** Gateways strip all untrusted external headers (`X-Aegis-*`, `Forwarded`, `Authorization`) from incoming requests. After successful authorization, the gateway mints a short-lived (<15s) cryptographically signed JWT assertion (`iss: aegis-gateway`, `aud: <backend-service-id>`, `sub: <principal>`, `path: <canonical-path>`, `exp: now+15s`). The backend service verifies this assertion via mTLS middleware, guaranteeing caller provenance without forwarding user bearer tokens into private networks.  
**When to use:** Microservice backends protected by a perimeter gateway to prevent header spoofing and lateral movement between services.  
**Trade-offs:** Requires distributing gateway public keys to backend middleware and adds minor CPU overhead for JWT generation/validation. Completely neutralizes internal token forgery and perimeter bypass attacks.

**Example:**
```go
func (g *Gateway) generateBackendAssertion(principal string, targetService, canonicalPath, reqID string) (string, error) {
	claims := jwt.MapClaims{
		"iss":        "aegis-gateway",
		"aud":        targetService,
		"sub":        principal,
		"req_id":     reqID,
		"path":       canonicalPath,
		"exp":        time.Now().Add(15 * time.Second).Unix(),
		"nbf":        time.Now().Add(-5 * time.Second).Unix(),
		"policy_ver": g.snapshots.Active().Version,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEd25519, claims)
	return token.SignedString(g.assertionPrivateKey)
}
```

---

### Pattern 5: Strict Path Normalization & Zero-Repair Canonicalization

**What:** The gateway rejects any incoming request containing path anomalies (dot segments `/../`, double slashes `//`, backslashes `\`, encoded directory separators `%2F` / `%5C`, NUL bytes, or invalid UTF-8) with `HTTP 400 Bad Request`. It strictly refuses to "clean" or "repair" malicious paths before evaluating routing or policy.  
**When to use:** Any reverse proxy or authorization gateway to permanently eliminate path-traversal bypasses, route confusion, and impedance mismatch between gateway routing and backend routing.  
**Trade-offs:** Rejects malformed clients instead of fixing their URLs. Eliminates an entire class of authorization bypass vulnerabilities (e.g., CVE-2018-18074 style proxy bypasses).

---

## Data Flow

### Request Flow (Data Plane)

The complete end-to-end lifecycle of an HTTP request entering the gateway:

```
[Client / Workload]
        │
        ▼ 1. Ingress Listener
[Port 8443 (TLS/JWT) OR Port 9443 (mTLS/SPIFFE SAN)]
        │
        ▼ 2. Target & Limit Validation
[Limits (16KB headers, 1MB body), Assign X-Request-ID, Strict Path Check]
        │
        ▼ 3. Route Resolution & Snapshot Pinning
[Match (Method, Path) to Route; Pin current Snapshot pointer reference]
        │
        ▼ 4. Authentication & Freshness Check
[Verify JWT / X.509 cert; Assert Snapshot Lease < 60s]
        │
        ▼ 5. Revocation & Quarantine Check
[Redis Check: token JTI blocklist & principal quarantine]
        │ ├── (Revoked / Unreachable) ──► Return 403 / 503 (Fail-Closed)
        │
        ▼ 6. Atomic Rate Limiting
[Redis Token Bucket: per-principal and per-route]
        │ ├── (Exceeded) ───────────────► Return 429 Too Many Requests
        │
        ▼ 7. In-Memory OPA Policy Evaluation
[Execute embedded Rego query using precompiled query and snapshot context]
        │ ├── (Deny / No Match) ────────► Write Spool Event ──► Return 403 Forbidden
        │
        ▼ 8. Pre-Forward Audit Spooling
[Append decision event to local disk WAL & execute fsync()]
        │ ├── (Disk Full / Write Err) ──► Return 503 Service Unavailable
        │
        ▼ 9. Header Sanitization & Assertion Signing
[Strip hop-by-hop & external X-Aegis-*; Mint signed short-lived backend JWT]
        │
        ▼ 10. Upstream Forwarding via Verified mTLS
[httputil.ReverseProxy to private backend service]
        │
        ▼ 11. Upstream Execution
[Backend middleware validates Gateway mTLS + Assertion token, executes handler]
        │
        ▼ 12. Completion Event & Telemetry
[Record response status & latency; Append completion event to Spool; Return response]
```

---

### Control Plane Flow (Policy Lifecycle & Snapshot Distribution)

The lifecycle of policy authoring, testing, signing, streaming, and convergence tracking:

```
[Security Operator / Dashboard]
        │
        ▼ 1. Author Draft
[POST /control/v1/policies or /routes (Stored in PostgreSQL policy_drafts)]
        │
        ▼ 2. Validation & Unit Testing
[POST /control/v1/policies/{id}/validate: Schema check + Rego unit test suite]
        │
        ▼ 3. Dry-Run Simulation
[POST /control/v1/policies/{id}/simulate: Test against synthetic/past traffic context]
        │
        ▼ 4. Approval & Atomic Publication
[POST /control/v1/policies/{id}/publish: Distinct Approver Identity required]
        │
        ▼ 5. Monotonic Snapshot Generation & Signing
[Control Plane bundles routes, Rego AST, data, increments version N+1,
 signs envelope with Ed25519 snapshot key, commits to PostgreSQL snapshots & outbox]
        │
        ▼ 6. gRPC Streaming Distribution
[Control Plane pushes snapshot to connected Gateway Replicas via bidirectional gRPC stream;
 Transmits continuous signed freshness lease heartbeats every 10 seconds]
        │
        ▼ 7. Gateway Validation & Atomic Swap
[Gateway verifies cryptographic signature, checksum, and monotonic version > active;
 Pre-compiles Rego evaluator; Atomically swaps pointer via sync/atomic.Pointer]
        │
        ▼ 8. Acknowledgment & Convergence Telemetry
[Gateway sends gRPC SnapshotAck(gateway_id, active_version, timestamp);
 Control Plane updates gateway_acks table; Dashboard reflects convergence status]
```

---

### Audit Spooling & Persistence Flow

The asynchronous, non-blocking durable audit delivery pipeline:

```
┌────────────────────────────────────────────────────────┐
│ GATEWAY PROCESS                                        │
│                                                        │
│  [Pre-Forward Event]       [Completion Event]          │
│          │                         │                   │
│          ▼                         ▼                   │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Local Append-Only WAL (/var/log/aegis/audit.wal) │  │
│  │ (Direct fsync per decision; 1GB ring-buffer)     │  │
│  └─────────────────────────┬────────────────────────┘  │
└────────────────────────────┼───────────────────────────┘
                             │ Local File Read
                             ▼
┌────────────────────────────────────────────────────────┐
│ AUDIT SPOOL WORKER (Daemon or Background Thread)       │
│                                                        │
│  1. Tail WAL segment files via tracked byte offset    │
│  2. Batch records (max 500 records or 200ms window)    │
│  3. Deduplicate using (partition_date, event_uuid)     │
│  4. Execute PostgreSQL batch INSERT:                   │
│     INSERT INTO audit_events (...)                     │
│     ON CONFLICT (event_date, event_id) DO NOTHING;     │
│  5. Advance local persistent offset checkpoint         │
│  6. Retry on DB error with bounded exponential backoff │
└────────────────────────────┬───────────────────────────┘
                             │ Batch SQL INSERT
                             ▼
┌────────────────────────────────────────────────────────┐
│ POSTGRESQL 16+ (Partitioned Storage)                  │
│                                                        │
│  Table: audit_events (PARTITION BY RANGE (event_date)) │
│  • audit_events_2026_10_06                             │
│  • audit_events_2026_10_07                             │
│  Read-only to audit consumers; 30-day retention job     │
└────────────────────────────────────────────────────────┘
```

---

## Scaling Considerations

| Scale Level | Architectural Adjustments | Key Bottlenecks & Solutions |
|-------------|--------------------------|-----------------------------|
| **Single Replica / Dev (< 500 RPS)** | • Single gateway process + embedded audit worker.<br>• Local SQLite or single-node PostgreSQL.<br>• In-memory or standalone Redis.<br>• Local filesystem spool (1GB). | **Bottleneck:** Local development certificates and static token configurations.<br>**Solution:** Scripted dev PKI and mock JWT issuer with seeded test accounts. |
| **Medium Scale (1,000 – 10,000 RPS)** | • 3 Gateway replicas behind L4/L7 load balancer.<br>• Standalone Audit Worker daemon per gateway node with dedicated spool volume.<br>• Standalone PostgreSQL with connection pooling (PgBouncer).<br>• Standalone Redis with AOF persistence or Redis Sentinel. | **Bottleneck 1: Disk Fsync Latency.** Hot path fsync on standard HDDs/SATA SSDs adds 5–10ms.<br>**Solution:** Mount spool directory on NVMe SSD with `O_DSYNC` or dedicated WAL disk.<br>**Bottleneck 2: Redis Roundtrips.** Network hop for rate limit + revocation.<br>**Solution:** Pipeline Redis commands or execute unified Lua script; keep connection pool sized to max concurrent requests. |
| **High Scale (10,000 – 50,000+ RPS)** | • 5–10 Gateway replicas with Kubernetes HPA (CPU & connection count).<br>• High-Availability PostgreSQL (Primary with streaming physical replicas).<br>• Redis Cluster with read replicas for rate-limit reads.<br>• Daily partition pruning and archival of PostgreSQL audit logs to object storage (S3/GCS). | **Bottleneck 1: Audit Spool Saturation during DB Outage.** If PostgreSQL is unavailable for minutes under high load, 1GB spool fills in seconds.<br>**Solution:** Expand spool volume to 20GB+ NVMe; implement backpressure alerts at 70%; aggregate denial events under DoS conditions.<br>**Bottleneck 2: Control Plane gRPC Fanout.** Reconnecting hundreds of gateways simultaneously.<br>**Solution:** Randomized exponential reconnect jitter (100ms–5s) and cached signed snapshot blobs. |

### Scaling Priorities & Bottlenecks

1. **First Bottleneck: Pre-Forward Spool fsync IOPS**  
   Every authorized request invokes `file.Sync()`. On cloud block storage (e.g., standard AWS EBS gp3), IOPS caps can throttle proxy throughput to ~3,000 RPS.  
   *Remedy:* Use local instance storage (NVMe), tune write buffer sizes, or batch fsyncs across microsecond tick windows without violating the ordering invariant.
2. **Second Bottleneck: Redis Network Latency for Revocation & Limits**  
   If the gateway executes sequential round trips for token revocation, principal quarantine, and rate limiting, latency accumulates by 3–6ms.  
   *Remedy:* Combine checks into a single atomic Redis Lua script invocation per request, executing in <0.5ms.
3. **Third Bottleneck: Database Ingestion Rate for Audit Records**  
   At 10,000 RPS, PostgreSQL receives 10,000 rows/second, leading to write amplification and index bloat.  
   *Remedy:* Audit worker batches 1,000 rows per `COPY` or multi-value `INSERT`, daily partition boundaries with zero index contention on historical partitions, and least-privilege append-only permissions.

---

## Anti-Patterns

### Anti-Pattern 1: Synchronous Database or External OPA Daemon Lookups on the Hot Path

**What people do:** The gateway queries PostgreSQL for route definitions or calls an external OPA daemon over HTTP (`POST http://opa:8181/v1/data/authz`) during request processing.  
**Why it's wrong:** Introduces a remote network dependency with variable latency (10–50ms), creates a single point of failure, and degrades p99 latency. If PostgreSQL or the OPA daemon restarts or experiences connection pool exhaustion, the entire gateway grid halts.  
**Do this instead:** Embed the OPA engine directly in the Go gateway process. Precompile Rego queries and evaluate against an immutable in-memory snapshot. All authorization decisions complete locally in <2ms.

---

### Anti-Pattern 2: Permissive Fail-Open Fallback on Security Dependency Outages

**What people do:** If Redis is unreachable or times out, the gateway logs a warning and proceeds to evaluate policy assuming the token is not revoked.  
**Why it's wrong:** Catastrophic zero-trust violation. An attacker who discovers a Redis outage (or induces one via volumetric flood) can bypass token revocation and principal quarantine.  
**Do this instead:** **Fail closed.** If Redis revocation verification fails or times out (bounded at 100ms), return `HTTP 503 Service Unavailable`. Never allow an unverified credential through.

---

### Anti-Pattern 3: Soft Path Normalization and URL Rewriting

**What people do:** The gateway encounters `//api/orders/../payments` or `%2Fadmin%2Fusers` and calls `path.Clean()` to normalize the string before matching routes and evaluating policy.  
**Why it's wrong:** Different web frameworks, reverse proxies, and backend servers decode and normalize paths differently (impedance mismatch). Attackers exploit subtle differences between the gateway's normalizer and the backend's router to bypass policy (e.g., Spring/Tomcat matrix parameter bypasses, NGINX off-by-slash bypasses).  
**Do this instead:** Reject any request containing path traversal (`..`), double slashes, encoded separators (`%2F`, `%5C`), or NUL bytes immediately with `HTTP 400 Bad Request`. Forward the exact canonical validated string without modification.

---

### Anti-Pattern 4: Ambient Internal Network Trust and Raw Bearer Token Forwarding

**What people do:** The gateway validates the user's bearer token, sets `X-User-Id: alice`, and forwards the raw token and plain headers over an unencrypted internal Docker network to the backend microservice.  
**Why it's wrong:** Any compromised container on the Docker network can spoof `X-User-Id` headers to call neighbor services directly, or sniff user bearer tokens from the wire.  
**Do this instead:** Enforce **bypass prevention**. Microservices must never publish ports to the host; all internal communication requires mutual TLS (mTLS); backends verify gateway client identities and require short-lived (<15s) cryptographically signed `X-Aegis-Assertion` tokens. External user tokens are stripped at the perimeter.

---

### Anti-Pattern 5: Indefinite Snapshot Trust without Freshness Leases

**What people do:** The gateway caches the last received snapshot and uses it forever if the control plane goes offline.  
**Why it's wrong:** If a gateway gets disconnected from the control plane due to network partition or split brain, it continues enforcing obsolete policies, ignoring revocations and route deletions indefinitely.  
**Do this instead:** Implement a **cryptographic freshness lease**. The control plane issues signed lease renewals every 10 seconds. If a gateway receives no valid lease renewal for 60 seconds, it transitions to unready and fails closed (`HTTP 503`) for protected traffic.

---

### Anti-Pattern 6: In-Memory Only Audit Event Buffering

**What people do:** Gateway buffers audit events in a Go channel or memory slice and flushes them to the database in background goroutines.  
**Why it's wrong:** A process crash, kernel OOM kill, or power failure destroys the memory buffer, permanently erasing non-repudiation audit records for requests that already modified backend database state.  
**Do this instead:** Pre-forward durable disk spooling. Append each decision event to a local disk WAL and call `fsync()` before the proxy dispatches the request to the upstream service.

---

## Integration Points

### External Services

| Service | Integration Pattern | Notes & Gotchas |
|---------|---------------------|-----------------|
| **External OIDC Provider (Okta, Keycloak, Auth0)** | In production: Authorization Code Flow with PKCE via Backend-For-Frontend (BFF). Gateway validates JWT access tokens against cached JWKS. | • Reject ID tokens as API access tokens.<br>• Bound JWKS refresh rate; single coalesced refresh on unknown `kid`.<br>• Enforce pinned signing algorithms (`RS256`, `ES256`, `EdDSA`; reject `none`).<br>• Maximum 5-minute token lifetime, 30s clock skew allowance. |
| **Protected Upstream Microservices** | Verified mTLS HTTP/1.1 or HTTP/2 reverse proxy (`httputil.ReverseProxy`). Gateway sends signed assertion header `X-Aegis-Assertion`. | • Backends must verify gateway client certificate identity.<br>• Assertions expire in 15 seconds; backends match assertion audience and method/path.<br>• Non-idempotent requests (POST/PATCH) must not be retried by proxy. |
| **Prometheus / OpenTelemetry Collector** | Pull-based `/metrics` endpoint on private internal listener (port 9090); gRPC trace export to OpenTelemetry Collector. | • Metric labels must NEVER contain high-cardinality values (e.g. user IDs, request IDs, tokens, raw paths).<br>• Gateway exposes route template (`/api/orders/{id}`) as label, not raw request URI. |

---

### Internal Boundaries

| Boundary | Communication Protocol | Notes & Considerations |
|----------|------------------------|------------------------|
| **Control Plane ↔ Gateway Replicas** | Bidirectional gRPC stream with Protobuf (`snapshot.v1.SnapshotDistributionService`) | • Authenticated via TLS.<br>• Streams monotonic signed snapshots and 10s lease renewals.<br>• Gateway acknowledges active version; control plane computes cluster convergence.<br>• Reconnect with exponential backoff and randomized jitter. |
| **Gateway ↔ Redis** | Redis RESP3 protocol over TCP with connection pooling | • Bounded timeout (100ms); fail-closed on timeout.<br>• Atomic Lua script executes token bucket rate limit + JTI revocation + principal quarantine in single trip.<br>• Keys expire with explicit TTL matching token lifetime. |
| **Gateway └──► Local Spool WAL** | Local POSIX file append with mandatory `fsync()` | • Append-only flat file with JSON/Protobuf lines.<br>• Pre-forward write forces `fsync()` before upstream dispatch.<br>• File rotation and size quota (1GB default; alert at 70%, fail-closed at 90%). |
| **Spool Worker ──► PostgreSQL** | Direct TCP via `pgxpool` with multi-row batch inserts | • Worker runs with least-privilege DB role (`INSERT` only on `audit_events`).<br>• Idempotent deduplication on `(event_date, event_id)`.<br>• Checkpoint commit cursor only after PostgreSQL transaction commits. |
| **Dashboard ↔ Control Plane** | HTTPS REST JSON (`/control/v1`) | • Session-based authentication with HttpOnly, Secure, SameSite cookies.<br>• Strict CSRF token header validation on mutating endpoints (POST/PUT/DELETE).<br>• Optimistic concurrency control via `ETag` and `If-Match` headers. |
| **Gateway ──► Upstream Backends** | Mutual TLS (mTLS) with pinned CA root and URI SAN validation | • Gateway presents client certificate (`spiffe://aegis.local/gateway`).<br>• Upstream presents server certificate (`spiffe://aegis.local/service/{name}`).<br>• Upstream middleware rejects any client that is not a verified gateway identity. |

---

## Suggested Build Order (Phase Alignment)

The architecture dictates strict layer-by-layer dependency progression. Components must be implemented in an order that satisfies runtime contracts without scaffolding empty placeholders:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ PHASE 0: Contracts, Schemas & Threat Model                                  │
│ • OpenAPI /control/v1 specs, Protobuf snapshot schemas, Rego input contract │
│ • ADRs: In-memory OPA, Snapshot leases, Redis fail-closed, Spool fsync      │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ PHASE 1: MVP Vertical Slice                                                 │
│ • Single Go gateway reverse proxy with strict path rejection & sanitization │
│ • Embedded OPA engine loading static local snapshot                         │
│ • Demo JWT issuer + 3 demo microservices (orders, payments, admin)          │
│ • Docker Compose; negative auth/path penetration tests                      │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ PHASE 2: Workload Identity & Enforced Bypass Prevention                     │
│ • Dedicated mTLS listener (Port 9443) with X.509 URI SAN extraction         │
│ • Gateway-to-backend verified mTLS with short-lived signed assertions       │
│ • Backend middleware rejecting direct/unauthenticated calls                 │
│ • Docker network isolation (no published backend ports)                     │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ PHASE 3: Control Plane, Snapshot Streaming & Durable State                  │
│ • PostgreSQL migrations (routes, policy drafts/versions, partitioned audit) │
│ • Control plane with Rego compilation, Ed25519 snapshot signing & gRPC push│
│ • 10-second freshness lease renewal & 60-second gateway timeout             │
│ • Redis token-bucket rate limiting, token revocation & quarantine (<5s SLA) │
│ • Local disk WAL spool with fsync & async PostgreSQL audit worker           │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ PHASE 4: Operator Experience                                                │
│ • Management REST APIs (/control/v1) with RBAC, CSRF, and optimistic locks  │
│ • React/TypeScript Dashboard: policy editor, dry-run simulator, audit viewer│
│ • Replica convergence telemetry, live quarantine management                 │
│ • Prometheus metrics & Grafana dashboards                                   │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ PHASE 5: Distributed Resilience & Performance Evidence                     │
│ • 3 Gateway replicas behind load balancer with health checking              │
│ • gRPC reconnect with jitter, graceful drain (30s)                          │
│ • Fault tests: kill gateway, kill Redis, kill PostgreSQL, lease expiry      │
│ • Reproducible k6 benchmarks: raw baseline vs 1 vs 3 gateways               │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ PHASE 6: Production Hardening (Reference Target)                            │
│ • Kubernetes manifests with NetworkPolicies & restricted PodSecurity        │
│ • External OIDC PKCE integration, SPIRE/managed PKI                         │
│ • HA PostgreSQL & Redis configurations, backup/restore drills, runbooks     │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Sources

- **NIST Special Publication 800-207:** *Zero Trust Architecture* (August 2020) — https://csrc.nist.gov/pubs/sp/800/207/final
- **RFC 8725:** *JSON Web Token Best Current Practices* (February 2020) — https://www.rfc-editor.org/rfc/rfc8725.html
- **Open Policy Agent (OPA) Documentation:** *Go API & Precompiled Evaluator* — https://www.openpolicyagent.org/docs/latest/integration/#integrating-with-the-go-api
- **SPIFFE Standards:** *The SPIFFE Trust Grid and Workload API* — https://spiffe.io/docs/latest/spiffe-about/overview/
- **Go Standard Library Documentation:** `net/http/httputil.ReverseProxy` & `sync/atomic.Pointer` — https://pkg.go.dev/net/http/httputil#ReverseProxy
- **Aegis System Specification v1.0:** `/home/logan78/Desktop/Aegis/spec.md` (October 2026)

---
*Architecture research for: Aegis Distributed Zero-Trust Access Gateway*  
*Researched: 2026-10-06*
