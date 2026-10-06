# Project Research Summary

**Project:** Aegis — Distributed Zero-Trust Access Gateway  
**Domain:** Distributed Systems / Zero-Trust Access Proxy & Policy Control Plane  
**Researched:** 2026-10-06  
**Confidence:** HIGH  

---

## Executive Summary

Aegis is an independently designed, cloud-native distributed zero-trust access gateway and policy control plane implemented in Go. In modern enterprise zero-trust architectures conforming to NIST SP 800-207, industry leaders decouple the policy decision and enforcement tier (data plane PEP/PDP) from the administration and lifecycle tier (control plane PAP). To achieve sub-2ms authorization latencies and uninterrupted throughput without single points of failure, production gateways strictly avoid external network hops or synchronous database lookups during request evaluation. Instead, they evaluate identity and declarative policies (OPA/Rego) entirely in-memory using local immutable configuration snapshots, enforce mutual TLS with cryptographic identity parsing (SPIFFE URI SANs), and secure backend microservices with short-lived signed assertions rather than passing untrusted external headers or raw user bearer tokens.

The recommended architectural approach builds Aegis as an independent Go reverse proxy utilizing standard library `httputil.ReverseProxy` (with modern Go 1.20+ `Rewrite` hooks) paired with an embedded in-memory OPA engine (`open-policy-agent/opa/v1/rego`). The gateway data plane remains completely decoupled from synchronous database operations, receiving monotonic signed configuration snapshots and 10-second freshness leases from a standalone control plane over gRPC. Authorization decisions evaluate against in-memory snapshots atomically swapped via `sync/atomic.Pointer`. Machine-to-machine traffic terminates on a dedicated mTLS listener, and upstream dispatch employs client-certificate verification and 15-second signed JWT assertions (`iss: aegis-gateway`). Abuse prevention and emergency quarantine leverage Redis atomic token buckets and JTI registries with sub-5-second convergence, while audit compliance is guaranteed via a local append-only Write-Ahead Log (WAL) with pre-forward `fsync` drained asynchronously into partitioned PostgreSQL.

The primary risks in building a distributed zero-trust gateway are (1) fail-open authorization bypass during dependency outages, (2) path traversal and parser desynchronization between proxy and backend routers, (3) silent audit record loss or hot-path latency degradation, and (4) split-brain stale policy enforcement across distributed replicas. Aegis mitigates these by enforcing strict default-deny and fail-closed semantics across all failure modes (e.g., returning HTTP 503 if Redis is unreachable, if the control-plane lease exceeds 60s, or if the disk spool reaches 90% capacity); adopting zero-repair path rejection (immediate HTTP 400 on dot-segments or encoded slashes); mandating pre-forward WAL disk `fsync` before backend dispatch; and using signed monotonic snapshots with bounded 10-second freshness leases and load-balancer convergence tracking.

---

## Key Findings

### Recommended Stack

The recommended stack relies on Go 1.25+ as the unified systems runtime across the gateway, control plane, audit worker, and demo microservices. By combining Go standard library networking (`net/http`, `httputil.ReverseProxy`) with embedded OPA evaluation and lock-free atomic pointer swaps, the architecture eliminates third-party proxy runtime bloat (such as Envoy or sidecar meshes) and delivers predictable sub-millisecond p99 authorization overhead.

Shared state and persistence are sharply decoupled by operational role: PostgreSQL 16+ via `pgx/v5` serves as the authoritative transactional repository for route definitions, policy version drafts, signed snapshot history, and partitioned audit event storage; Redis 7+ via `go-redis/v9` is dedicated exclusively to shared atomic rate-limiting counters and sub-5-second emergency quarantine/revocation registries. The operator management console is implemented in React 19, Vite, and TypeScript with TanStack Query and Monaco editor for live Rego policy authoring and dry-run simulation.

**Core technologies:**
- **Go 1.25+ (`go1.25.5` runtime, `go 1.24+` in `go.mod`):** Primary runtime for Gateway, Control Plane, Audit Worker, and demo backends — systems language with low GC latency, lock-free atomics, and high networking throughput.
- **Go Reverse Proxy (`net/http` & `httputil.ReverseProxy` with `Rewrite`):** Edge HTTP/mTLS termination, strict validation, header scrubbing, and upstream proxying — eliminates third-party runtime overhead while preventing path desynchronization and header leaks.
- **Embedded OPA Engine (`open-policy-agent/opa/v1/rego` v1.21.1):** In-memory declarative policy evaluation — evaluates precompiled Rego queries in <0.2ms locally on the hot path without remote network latency or external daemon failure.
- **gRPC & Protobuf (`grpc-go` v1.83.2, Protobuf v1.36.12):** Control plane to gateway snapshot streaming, lease heartbeats, and ack tracking — multiplexed HTTP/2 bidirectional distribution with low CPU overhead.
- **PostgreSQL 16+ & `pgx/v5` (`pgxpool` v5.9.2):** Authoritative persistence for control plane metadata, routes, policy versions, and batch audit ingestion — pure Go driver with statement caching and COPY/batch insertion into partitioned tables.
- **Redis 7+ & `go-redis/v9` (v9.22.0) / `redis_rate/v10`:** Shared atomic token bucket rate limiting, JTI revocation, and <5s principal quarantine — high-speed atomic Lua scripts with context cancellation and strict fail-closed enforcement.
- **JWT Cryptography (`golang-jwt/jwt/v5` v5.3.1 & `crypto/ed25519`):** Ingress token validation and short-lived backend assertion signing — RFC 8725 compliant, algorithm pinned, prevents algorithm confusion (`alg: none`).
- **Observability (`client_golang` v1.24.1 & `opentelemetry-go` v1.47.0):** Prometheus telemetry and W3C distributed tracing — strictly bounded metric cardinality and non-blocking sampling.
- **Operator Dashboard (React 19.3.0, Vite 8.3.2, TypeScript 5.7+):** Single-page management console with Monaco Rego editor, dry-run simulator, convergence telemetry, and TanStack Query state caching.

---

### Expected Features

Detailed feature analysis in [FEATURES.md](file:///home/logan78/Desktop/Aegis/.planning/research/FEATURES.md) categorizes capabilities into non-negotiable table stakes, high-value architectural differentiators, and deliberately deferred extensions.

**Must have (table stakes):**
- **Default-Deny Authorization:** Deny by default across all routes; unauthenticated or unmatched requests fail closed (403/503).
- **Strict Path Normalization & Traversal Defense:** Zero-repair rejection (HTTP 400) for encoded slashes, dot-segments (`..`), and double slashes.
- **Header Sanitization & Hop-by-Hop Stripping:** Strip all client `X-Aegis-*` and RFC 7230 hop-by-hop headers; reconstruct forwarding headers strictly from verified TLS context.
- **Dual Ingress Identity Authentication:** JWT/OIDC for users on port 8443; dedicated mTLS listener on port 9443 parsing SPIFFE URI SANs for workloads.
- **Embedded OPA In-Memory Policy Evaluation:** Precompiled Rego rules evaluating typed request contexts in <2ms without network calls.
- **Signed Backend Assertions & Bypass Prevention:** Short-lived (<15s) gateway-issued JWTs verified by private backends over mTLS; backends reject direct calls.
- **Deterministic Upstream Route Binding:** Route matching via fixed snapshot routes; client Host headers or absolute URLs never dictate routing (SSRF prevention).
- **Structured Request & Audit Logging:** Structured JSON logs capturing request IDs, principal, decision, reason code, and latency.

**Should have (competitive / differentiators):**
- **Monotonic Signed Snapshots with Freshness Leases:** Atomic snapshot distribution over gRPC with Ed25519 signatures, 10s lease renewals, and 60s fail-closed timeout.
- **Redis Atomic Rate Limiting & Sub-5s Principal Quarantine:** GCRA token buckets and instant revocation/quarantine convergence with fail-closed semantics on Redis outage.
- **Durable Local Audit Spool (WAL) with Async Delivery Worker:** Append-only disk spool with pre-forward `fsync`; at-least-once batch delivery to PostgreSQL; fail-closed at 90% capacity.
- **Operator Dashboard with Policy Simulator & Convergence Map:** React/TypeScript UI for dry-run Rego simulation, version rollback, and real-time replica convergence monitoring.
- **Lock-Free In-Memory Hot Path Execution:** Hot path evaluates against immutable snapshot references via `sync/atomic.Pointer` with zero lock contention.
- **Scoped Backend Context Binding:** Assertions bind strictly to destination service ID (`aud`) and canonical path/method to prevent token replay across backends.

**Defer (v2+):**
- **External OIDC Provider with PKCE BFF:** Standalone mock demo-issuer provides reproducible role tokens in v1; defer external IdPs (Keycloak/Auth0) to Phase 6.
- **Kubernetes Production Hardening & NetworkPolicies:** Docker Compose profiles (`mvp`, `hardened`, `distributed`) satisfy local development and benchmarking; defer K8s manifests to Phase 6.
- **SPIRE Automated Workload Attestation:** Scripted local CA and SPIFFE URI SAN generation suffice for portfolio proof; defer SPIRE daemon integration to Phase 6.
- **ML Anomaly Detection & Deterministic Risk Scoring:** Non-deterministic ML models violate strict authorization SLAs; defer advisory anomaly signaling to Phase 7.
- **Constrained Egress Proxy & S3 Cold Archival:** Egress filtering and cloud object storage address distinct secondary requirements; defer to Phase 7.

---

### Architecture Approach

As detailed in [ARCHITECTURE.md](file:///home/logan78/Desktop/Aegis/.planning/research/ARCHITECTURE.md), Aegis decouples the data plane Policy Enforcement Point (PEP) and in-memory Policy Decision Point (PDP) from the control plane Policy Administration Point (PAP). The data plane request hot path avoids all synchronous database interactions and remote policy daemons: incoming requests authenticate via JWT or mTLS, validate against strict path rules, match against compiled routes and precompiled Rego queries pinned from an immutable snapshot (`sync/atomic.Pointer`), check Redis for revocation and rate limits, append an authorization event to a local disk WAL with `fsync`, and dispatch to private backends over mTLS with short-lived signed assertions (`aegis-gateway`). A background worker drains spooled audit records into PostgreSQL with deduplication, while the control plane compiles Rego policies, generates cryptographically signed snapshots, and streams updates to replicas with 10-second freshness leases over gRPC.

```
CLIENTS & WORKLOADS
     │ HTTPS (:8443) / mTLS (:9443)
     ▼
[DATA PLANE: AEGIS GATEWAY REPLICAS] ──(Pre-forward WAL fsync)──► [LOCAL DISK WAL SPOOL]
     │                                                                   │
     │ Verified mTLS + Signed Assertion                                  │ Async Batch Delivery
     ▼                                                                   ▼
[PRIVATE BACKEND SERVICES]                                       [AUDIT WORKER DAEMON]
(Orders, Payments, Admin)                                                │
     ▲                                                                   ▼
     │ Verified Gateway Identity                                 [POSTGRESQL 16+]
     │                                                           (Snapshots, Routes, Audit)
     │ gRPC Snapshot Stream (10s lease / 60s timeout)                    │
     └───────────────────────────────────────────────────────────┤ Read/Write State
                                                                 ▼
[OPERATOR DASHBOARD (React)] ◄──(REST /control/v1)──────── [CONTROL PLANE (PAP)]
```

**Major components:**
1. **Gateway Replicas (`cmd/gateway`):** Edge HTTP/mTLS termination, zero-repair path validation, header scrubbing, atomic snapshot resolution, embedded OPA evaluation, Redis revocation/rate limiting, pre-forward disk WAL fsync, short-lived assertion signing, and upstream mTLS proxying.
2. **Control Plane (`cmd/control-plane`):** Route/policy version management, OpenAPI validation, Rego unit testing and dry-run simulation, monotonic Ed25519 snapshot signing, gRPC distribution with 10s freshness leases, replica ack tracking, and Redis quarantine dispatch.
3. **Durable Audit Spool Worker (`cmd/audit-worker`):** Tails append-only gateway WAL files, batches decision records (500 events / 200ms), deduplicates by `(partition_date, event_uuid)`, and commits to PostgreSQL with exponential backoff.
4. **Backend Demo Microservices (`services/{orders, payments, admin}`):** Core mock business logic isolated on private internal networks; enforced bypass prevention via mTLS middleware verifying gateway identity and short-lived signed context assertions.
5. **PostgreSQL 16+ Store:** Authoritative relational storage for route catalogs, policy drafts, version history, signed snapshots, outbox events, gateway replica acknowledgments, and range-partitioned audit logs.
6. **Redis 7+ Cache:** In-memory store for shared token bucket rate limiting (per-principal/per-route) and emergency credential revocations (`jti` blocklist) / principal quarantines with <5-second cluster convergence.
7. **Operator Dashboard (`web/dashboard`):** React/TypeScript UI for policy authoring, Monaco-based Rego dry-run simulation, version rollback, fleet convergence monitoring, and emergency quarantine controls.
8. **Dev PKI & Demo Issuer (`scripts/certificates`, `cmd/demo-issuer`):** Generates development CAs, workload mTLS certificates (`spiffe://aegis.local/...`), and issues short-lived mock JWTs with seeded roles (`developer`, `finance`, `admin`).

---

### Critical Pitfalls

Research in [PITFALLS.md](file:///home/logan78/Desktop/Aegis/.planning/research/PITFALLS.md) identified six high-severity failure modes that undermine distributed access gateways:

1. **Path Normalization and Traversal Bypass (`%2F`, `..`, NUL bytes):** Evaluator/backend path desynchronization allows attackers to bypass path-based access controls.  
   *How to avoid:* Enforce strict zero-repair rejection returning immediate HTTP 400 Bad Request on dot-segments (`..`, `%2e%2e`), encoded slashes (`%2F`, `%5C`), NUL bytes, or double slashes. Forward exact validated path bytes via Go 1.20+ `httputil.ReverseProxy.Rewrite`.
2. **Header Spoofing and Forwarding Leakage (`X-Forwarded-*`, `X-Aegis-*`, Hop-by-Hop):** External attackers forge headers to masquerade as privileged roles or bypass security controls.  
   *How to avoid:* Strip all client `X-Aegis-*`, `Forwarded`, and RFC 7230 hop-by-hop headers at ingress. Mint short-lived (<15s) cryptographically signed backend assertions (`iss: aegis-gateway`) over mTLS, verified by backend middleware.
3. **Synchronous Dependency Failure Causing "Fail-Open" Bypass:** Timeouts or network partitions to Redis or the control plane cause fallback logic to permit unauthorized traffic.  
   *How to avoid:* Enforce absolute fail-closed semantics across all failure modes (Invariant 1). Return HTTP 503 if Redis is unreachable or times out (>200ms), if snapshot leases expire (>60s), or if disk spools saturate (>90%).
4. **Split-Brain or Stale Policy Execution Without Leases and Monotonic Versioning:** Replicas enforce conflicting policies during control plane disconnects or after rollbacks.  
   *How to avoid:* Distribute atomic snapshot envelopes containing monotonic integer versions and Ed25519 signatures. Enforce 10-second freshness lease renewals with 60-second fail-closed timeouts. Roll back exclusively by issuing higher monotonic version numbers.
5. **Workload Identity Header Trust Instead of End-to-End mTLS URI SAN Verification:** Gateway relies on plain headers for machine identity or shares listeners between user tokens and client certs.  
   *How to avoid:* Operate a dedicated workload mTLS listener (port 9443) enforcing client certificates with SPIFFE URI SANs (`spiffe://aegis.local/workload/...`). Reject bearer tokens on the workload port; isolate backends with no published host ports.
6. **Audit Record Loss During Database Outage Without Durable Pre-Forward Spool:** Authorizing state-mutating requests while buffering audit logs in memory risks permanent data loss on crash or DB outage.  
   *How to avoid:* Append decision events to a local append-only disk spool (WAL) and force `fsync` before dispatching requests to upstream backends. Async delivery workers stream batches to PostgreSQL with at-least-once delivery; fail closed (503) if spool hits 90% capacity.

---

## Implications for Roadmap

Based on research findings, the implementation follows a 7-phase structural sequence advancing from contracts and isolated vertical slices to distributed clustering and production hardening:

### Phase 0: Foundations, Contract Specifications & Threat Modeling
**Rationale:** Establishing rigid interface contracts, Protobuf/OpenAPI schemas, Rego input schemas, and Architectural Decision Records (ADRs) before writing application code prevents contract drift, interface regressions, and ambiguous security invariants.  
**Delivers:** OpenAPI specification (`api/openapi/control-v1.yaml`), Protobuf snapshot schemas (`api/proto/snapshot/v1/snapshot.proto`), typed Go/Rego request context models, ADRs for core architectural decisions, and threat model documentation.  
**Addresses:** Foundation for all active requirements in [PROJECT.md](file:///home/logan78/Desktop/Aegis/.planning/PROJECT.md).  
**Avoids:** Unversioned interface drift, ambiguous Rego schemas, and architectural misalignment.

### Phase 1: MVP Vertical Slice (Data Plane & Negative Security Validation)
**Rationale:** Proves the core reverse proxy and zero-trust policy enforcement loop end-to-end with zero distributed state complexity before introducing distributed clustering or database dependencies.  
**Delivers:** Go HTTP reverse proxy with `httputil.ReverseProxy.Rewrite`, embedded OPA engine evaluating static local snapshot in <2ms, local demo JWT issuer, three private backend services (`orders`, `payments`, `admin`), Docker Compose `mvp` profile, and an automated negative security test suite.  
**Addresses:** Must-have table stakes: Default-deny authorization, strict path normalization, header sanitization, user JWT validation, embedded OPA policy evaluation, structured JSON access logging.  
**Avoids:** Pitfall 1 (Path normalization/traversal bypass), Pitfall 2 (Header spoofing), Pitfall 7 (Server timeout leaks / OPA AST compilation churn).

### Phase 2: Workload Identity & Enforced Bypass Prevention
**Rationale:** Workload authentication and network boundary isolation must be proven before building multi-replica control plane infrastructure, ensuring microservices cannot be bypassed on internal networks.  
**Delivers:** Dedicated workload mTLS listener (port 9443) with SPIFFE URI SAN validation (`spiffe://aegis.local/...`), backend gateway-to-service mTLS, short-lived (<15s) signed context assertions (`aegis-gateway`), shared backend authentication middleware, and Docker network isolation (zero published backend host ports).  
**Uses:** Standard Go `crypto/tls`, `crypto/x509`, `golang-jwt/jwt/v5`, and PKI generation scripts (`scripts/certificates`).  
**Implements:** Workload mTLS listener, gateway assertion signer, backend bypass middleware.  
**Avoids:** Pitfall 2 (Ambient internal header trust), Pitfall 5 (Workload identity header spoofing & direct container bypass).

### Phase 3: Control Plane, Snapshot Streaming & Durable State
**Rationale:** Replaces static startup configurations with centralized policy lifecycle management, dynamic gRPC streaming, distributed rate limiting, and non-repudiation audit pipelines.  
**Delivers:** PostgreSQL schema & migrations (routes, policy drafts, partitioned audit events), Control Plane daemon (`cmd/control-plane`) with monotonic Ed25519 snapshot signing, gRPC snapshot distribution with 10s freshness lease renewals / 60s fail-closed timeout, Redis atomic token bucket rate limiting and sub-5s token/principal quarantine, and local disk WAL spool with pre-forward `fsync` and `cmd/audit-worker` at-least-once PostgreSQL batch writer.  
**Uses:** `jackc/pgx/v5`, `pressly/goose/v3`, `grpc-go`, `go-redis/v9`, `redis_rate/v10`, and local WAL POSIX file locking.  
**Implements:** Control Plane PAP, gRPC snapshot streamer, Redis rate limiter/revocation store, durable disk WAL spool, and audit worker.  
**Avoids:** Pitfall 3 (Fail-open on Redis/dependency outage), Pitfall 4 (Stale policy execution / split-brain), Pitfall 6 (Audit log loss on database downtime).

### Phase 4: Operator Experience & Telemetry
**Rationale:** With control plane APIs and durable state complete, operators require visual tools for policy authoring, safe validation, dry-run simulation, fleet convergence monitoring, and observability.  
**Delivers:** Control Plane REST management APIs (`/control/v1`) with role-based access control (RBAC), CSRF tokens, and optimistic locking; React/TypeScript Operator Dashboard (`web/dashboard`) with Vite, Monaco Rego editor, dry-run policy simulator, version rollback, replica convergence view, and quarantine controls; Prometheus metrics (`client_golang`) and Grafana dashboard templates.  
**Uses:** React 19, Vite, Tailwind CSS, TanStack Query, Monaco Editor, and Prometheus `client_golang`.  
**Implements:** Control plane REST API router, operator web console, dry-run simulator, and telemetry endpoints.  
**Avoids:** UX Pitfall 1 (Opaque 403s), UX Pitfall 3 (Stale/simulated state confusion), Security Pitfall (CORS/CSRF on management APIs).

### Phase 5: Distributed Resilience, Chaos & Benchmark Evidence
**Rationale:** Validates multi-replica distributed guarantees, graceful degradation, and performance SLOs under realistic load and injected infrastructure failures.  
**Delivers:** Multi-replica cluster (3 gateway instances behind load balancer), gRPC client reconnect with exponential backoff and jitter, 30-second graceful connection draining, automated fault injection suite (killing gateway nodes, Redis crash, PostgreSQL partition, lease expiration tests), and reproducible k6 benchmark suite comparing baseline raw proxy vs 1 gateway vs 3 gateways (<20ms p99 gateway overhead under 1,000 RPS).  
**Uses:** Docker Compose `distributed` profile, HAProxy/NGINX load balancer, k6 load generator, and chaos fault injection scripts.  
**Implements:** Distributed multi-gateway deployment, load-balancer health probes, convergence tracking, and reproducible performance reports.  
**Avoids:** Pitfall 4 (Replica policy desynchronization), Pitfall 7 (Goroutine, socket, and memory leaks under load), Performance Traps (fsync IOPS bottlenecks, TCP connection exhaustion).

### Phase 6: Production Hardening (Reference Architecture Target)
**Rationale:** Translates verified local multi-service architecture into production-ready deployment assets and enterprise identity integrations.  
**Delivers:** Kubernetes deployment manifests with default-deny `NetworkPolicies`, non-root security contexts, and persistent volume mounts for audit WALs; external OIDC PKCE integration with Backend-For-Frontend (BFF) session cookies; SPIRE integration blueprints for automated workload attestation; HA PostgreSQL and Redis backup/restore runbooks.  
**Addresses:** Deferred enterprise features: External OIDC integration, Kubernetes hardening, SPIRE workload attestation, HA infrastructure runbooks.  
**Avoids:** Enterprise deployment drift, container breakout risks, unauthenticated external identity federation vulnerabilities.

---

### Phase Ordering Rationale

- **Strict Dependency Progression:** The sequence adheres to layer-by-layer dependency constraints. Layer 0 establishes immutable schemas; Layer 1 proves data plane request routing and embedded OPA evaluation; Layer 2 hardens the perimeter with mTLS and bypass prevention; Layer 3 introduces the control plane and distributed state stores (Postgres, Redis, Disk WAL); Layer 4 builds operator visibility on top of stable control APIs; Layer 5 scales to multi-replica distributed validation; and Layer 6 hardens for Kubernetes and production OIDC.
- **Data Plane Independence First:** Building the gateway reverse proxy before the control plane ensures that the gateway's hot-path execution loop is validated as a self-contained unit using local file snapshots. This guarantees that when gRPC streaming is introduced in Phase 3, the runtime interface is already mature.
- **Fail-Closed Security Invariant Verification:** Security invariants (such as zero-repair path rejection, Redis fail-closed behavior, and pre-forward WAL fsync) are introduced directly in the phases responsible for those subsystems and verified through automated negative tests before progressing to dashboard or distributed scaling phases.

---

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 2 (Workload Identity & Bypass Prevention):** Deep dive into local PKI script design, OpenSSL/cfssl SAN formatting for SPIFFE URIs (`spiffe://aegis.local/...`), and Go `tls.Config` client verification hooks.
- **Phase 3 (Durable Audit Spool & WAL Fsync):** Concurrency design for WAL file rotation, group committing / batch fsyncing under concurrent goroutines, and persistent offset checkpointing to avoid disk IOPS throttling.
- **Phase 5 (Distributed Chaos & Convergence):** Scripting container network disconnects and process kills in Docker Compose while asserting exact HTTP status codes and convergence latency under active k6 load.

Phases with standard patterns (skip research-phase):
- **Phase 0 (Contract Specifications):** Standard Protobuf 3 and OpenAPI 3.0 syntax; well-defined Rego input schemas.
- **Phase 1 (MVP Reverse Proxy & Embedded OPA):** Well-documented standard Go `httputil.ReverseProxy.Rewrite` and embedded OPA `rego.PrepareForEval` APIs.
- **Phase 4 (Operator Dashboard):** Standard React 19, Vite, TanStack Query, and Monaco editor integration patterns with REST endpoints.
- **Phase 6 (Production Hardening):** Standard Kubernetes Deployment, Service, and NetworkPolicy manifest specifications.

---

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| **Stack** | HIGH | All core Go libraries, OPA v1.21.1 SDK, gRPC v1.83.2, pgx v5.9.2, Redis v9.22.0, React 19, and Vite 8 verified against current 2026 releases and spec requirements. |
| **Features** | HIGH | Table stakes, differentiators, and anti-features aligned directly with NIST SP 800-207, RFC 8725, and canonical project charter requirements. |
| **Architecture** | HIGH | Decoupled PEP/PAP, atomic pointer swapping, pre-forward fsync WAL, and mTLS assertions verified against spec invariants. |
| **Pitfalls** | HIGH | 12 critical security, performance, and operational pitfalls documented with concrete root causes and actionable mitigations. |

**Overall confidence:** HIGH

---

### Gaps to Address

- **Group Commit Optimization for WAL Fsync:** Naive per-request `fsync` can cap throughput to ~1,000 RPS on standard SSDs; evaluate group commit batching (e.g. 1ms flush tick) during Phase 3 planning.
- **Redis Revocation Re-sync After Outage:** When Redis recovers from a restart, active revocations and quarantine entries must be repopulated from PostgreSQL before gateways mark Redis healthy; address recovery script in Phase 3/5.
- **Docker Compose Host Port Leakage:** Verify that Docker Compose network configurations strictly omit `ports:` mappings for backend services across all environments to guarantee bypass prevention.

---

## Sources

### Primary (HIGH confidence)
- **`/open-policy-agent/opa` (Context7)** — Embedded OPA Go SDK: `rego.PrepareForEval`, `rego.EvalInput`, Rego v1 declarative syntax (`allow if { ... }`).
- **`/jackc/pgx` (Context7)** — `pgx/v5` connection pool configuration (`pgxpool.New()`) and high-throughput batch insertion.
- **`/redis/go-redis` & `/go-redis/redis_rate` (Context7)** — Atomic GCRA rate limiting and Redis Lua scripting (`EvalSha`).
- **`/golang-jwt/jwt` (Context7)** — `golang-jwt/jwt/v5` RFC 8725 validation, Ed25519 signature verification, and method allowlists.
- **Go Standard Library Documentation** — `net/http/httputil.ReverseProxy`, `Rewrite` hook, `sync/atomic.Pointer`.
- **NIST SP 800-207** — *Zero Trust Architecture* standard (PEP, PDP, PAP models).
- **RFC 8725 & RFC 7230/9112** — *JSON Web Token Best Current Practices* and HTTP/1.1 message syntax & hop-by-hop header specifications.

### Secondary (MEDIUM confidence)
- **Envoy Proxy & Pomerium Architecture Documentation** — Real-world reverse proxy patterns, external authorization, and bypass prevention techniques.
- **SPIFFE / SPIRE Specification** — Workload identity standards and X.509 URI SAN formatting conventions.

### Tertiary (LOW confidence)
- None — all core design elements are grounded in official documentation, standards, and canonical project specifications.

---
*Research completed: 2026-10-06*  
*Ready for roadmap: yes*  
