# Feature Research

**Domain:** Distributed Zero-Trust Access Gateway & Policy Control Plane (Cloud-Native Identity-Aware Reverse Proxy)  
**Researched:** 2026-10-06  
**Confidence:** HIGH  

---

## Feature Landscape

### Table Stakes (Users Expect These)

Features operators and security architects assume exist in any zero-trust application access proxy. Missing any of these renders the gateway incomplete or fundamentally insecure.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| **Default-Deny Authorization** | Zero-trust core invariant (NIST SP 800-207). If no policy explicitly permits an action, or evaluation fails, the gateway must deny (HTTP 403/503). | LOW | Explicit deny rules must supersede any allow rule. Never allow implicit pass-through. |
| **User Authentication (JWT/OIDC)** | User-facing endpoints require strong cryptographic proof of identity rather than network IP or perimeter trust. | MEDIUM | Parse and validate signature, pinned algorithm allowlist (e.g. `RS256`/`ES256`, reject `none` and symmetric confusion), issuer, audience, subject, expiry (`exp`), and not-before (`nbf`). Key fetching via trusted JWKS with bounded caching. |
| **Workload Authentication (mTLS)** | Machine-to-machine (service-to-service) calls cannot rely on ambient network trust or replayable user bearer tokens. | MEDIUM | Dedicated mTLS listener demanding valid client certificates. Parse URI SAN identities (e.g., `spiffe://aegis.local/workload/orders`), verify trust chain, validity, and EKU. Reject bearer tokens on the workload listener to prevent identity confusion. |
| **Embedded OPA/Rego Policy Engine** | Fast, expressive, declarative authorization policy decoupled from application business logic. | MEDIUM | Embed OPA in Go (`github.com/open-policy/opa/rego`). Evaluates in-memory against typed request context in <2ms. Strict prohibition on synchronous network calls from Rego rules during request handling. |
| **Strict Path Normalization & Traversal Defense** | Path traversal, encoded slash variations, and dot-segments allow attackers to bypass path-based routing and policy checks. | MEDIUM | Canonicalize and validate path before policy evaluation. Reject rather than attempt to "fix" malformed encodings, `%2f`, `%5c`, `..`, null bytes, and double slashes. Policy and upstream forwarder must operate on the exact same validated path. |
| **Header Sanitization & Hop-by-Hop Stripping** | Attackers forge headers (`X-Forwarded-For`, `X-User-Id`, `X-Aegis-*`) to spoof caller identities or authorization states. | LOW | Strip hop-by-hop headers and all untrusted gateway/forwarding headers (`X-Aegis-*`, `Forwarded`, `X-Forwarded-*`). Gateway reconstructs authoritative forwarding metadata solely from verified TLS connection properties. |
| **Signed Backend Assertions** | Backends must not trust raw incoming headers or receive long-lived user bearer tokens that could be replayed across microservices. | MEDIUM | Gateway mints short-lived (<15s) signed context assertions (JWT issued by `aegis-gateway`) with destination audience, original principal, canonical method/path, and request ID. Backends verify assertions via public key. |
| **Workload Mutual TLS to Backends (Bypass Prevention)** | If microservices can be called directly by skipping the gateway, all policy enforcement is bypassed. | MEDIUM | Gateway connects to private backends over mTLS with dedicated gateway identity certificates. Backends verify gateway client certificate and reject direct callers, even within the same cluster network. |
| **Deterministic Upstream Route Binding** | Prevents SSRF and open proxy vulnerabilities where attackers direct traffic to arbitrary internal services. | LOW | Map incoming requests strictly through an immutable route catalog (service ID, method, path template). Client-supplied host headers, query params, or absolute URLs never dictate upstream selection. |
| **Structured Request & Decision Audit Logging** | Regulatory compliance, incident investigation, and forensic auditing require an immutable record of every access attempt. | MEDIUM | Emit structured JSON events containing unique Request ID, timestamp, authenticated principal, route, evaluated policy version, allow/deny decision, denial reason code, and backend latency. |
| **Global Request Budgets & Deadlines** | Protects gateway and downstream services from hanging sockets, slowloris attacks, and connection pool exhaustion. | LOW | Configurable per-route timeouts: 5s header read, 2s connect, 5s backend response headers, 15s request lifecycle, 16 KiB header limit, and 1 MiB body limit. Downstream context cancellation propagation. |

---

### Differentiators (Competitive Advantage)

Features that elevate Aegis from a simple reverse-proxy toy to a resilient, distributed, production-grade zero-trust access gateway.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| **Signed Config Snapshots with Freshness Leases** | Guarantees atomic, tamper-proof policy distribution across distributed replicas with verifiable liveness. | HIGH | Control plane signs immutable snapshot envelopes (routes, Rego modules, data, version) with asymmetric keys. Distributed gateways stream updates via gRPC. Gateways require a signed freshness lease renewed every 10s; if control plane is down >60s, gateway fails closed (503) to prevent running on stale security state indefinitely. |
| **Redis Atomic Rate Limiting & Sub-5s Principal Quarantine** | Fast-path distributed abuse prevention and instant incident response across all gateway instances. | HIGH | Token-bucket rate limiting backed by Redis atomic scripts. Dedicated principal quarantine and JTI revocation registry with sub-5s cluster-wide convergence. Fail-closed semantics: if Redis is unavailable, protected requests return 503 rather than bypassing limits or quarantine. |
| **Durable Local Audit Spool with Async Delivery Worker** | Zero audit log loss during downstream database outages without incurring synchronous DB latency on the hot path. | HIGH | Permitted requests are durably written and `fsync`'d to an append-only local disk spool before forwarding. An asynchronous worker delivers batched events to PostgreSQL with at-least-once semantics and deduplication. If spool reaches 90% capacity, gateway stops admitting new requests (503 fail-closed) to preserve audit compliance. |
| **Operator Dashboard with Policy Simulator & Convergence View** | Eliminates blind policy deployments and operational guesswork; provides verifiable cluster synchronization state. | HIGH | React/TypeScript UI that allows operators to run what-if simulations against candidate Rego policies using historical or synthetic request contexts before publishing. Real-time fleet health view displays each replica's active snapshot version, lease age, and convergence lag. |
| **Lock-Free In-Memory Hot Path Execution** | Eliminates database bottlenecks and network hops during authorization, achieving sub-2ms policy evaluation at p99. | MEDIUM | Hot path reads strictly from immutable in-memory snapshot references (atomic pointer swap). Zero database or remote daemon queries on the authorization path. |
| **Targeted Backend Context Binding** | Minimizes token blast radius by replacing broad user credentials with scoped, single-destination backend assertions. | MEDIUM | Assertions bind strictly to `aud: <destination-service-id>` and canonical path/method. Even if a backend is compromised, it cannot replay the assertion against another microservice. |

---

### Anti-Features (Commonly Requested, Often Problematic)

Features that seem desirable or appear in enterprise marketing buzzwords, but introduce severe security liabilities, unnecessary operational complexity, or scope creep.

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| **General-Purpose Identity Provider (Built-in User Management)** | "Everything in one box: user registration, password resets, MFA, user database." | Massive attack surface, high cryptographic risk, password storage liabilities, and huge maintenance overhead. Violates single responsibility principle. | Focus strictly on access proxying. Use standard OIDC/PKCE federated with external identity providers (Keycloak, Okta, Auth0). In MVP, use a strictly isolated, mockable demo-issuer clearly labeled for development only. |
| **Enterprise Appliance Clone / Turnkey Zscaler Substitute** | "Make it do VPN, SD-WAN, CASB, endpoint DLP, and mobile device agents." | Massive architectural bloat, loss of cloud-native composability, unmaintainable monolith, violates educational and portfolio clarity. | Focus purely on Layer 7 HTTP/gRPC zero-trust access gateway and workload microsegmentation. |
| **Transparent TLS Interception (Forward Proxy MITM)** | "Inspect all outbound employee web traffic and decrypt arbitrary HTTPS." | Breaks end-to-end security, requires installing custom root CA certificates on all client machines, breaks client certificate pinning, creates massive compliance liability for intercepted personal data. | Reverse proxying for internal and published services. For outbound calls, use an optional constrained forward egress proxy with domain/IP allowlists without TLS decryption. |
| **Arbitrary TCP & Database Wire Protocol Proxying** | "Proxy direct PostgreSQL, MySQL, and Redis client connections through the gateway." | Stateful, long-lived binary protocols cannot be cleanly modeled with request-response OPA Rego policies; requires parsing dialect-specific SQL wire formats; introduces extreme connection-pooling complexity. | Gateways protect HTTP/gRPC API microservices that front the databases. Databases remain isolated in private subnets with service-specific credentials and Kubernetes NetworkPolicies. |
| **Full Service Mesh Sidecar Injection (Envoy/Istio Clone)** | "Run sidecars beside every container for universal microservice interception." | Massive operational overhead, 100MB+ memory footprint per pod, debugging nightmares, and intrusive container mutation webhooks. Overkill for bounded service topologies. | Run Aegis as an explicit, distributed ingress and internal gateway tier. Clean perimeter boundaries with mTLS and signed context without sidecar management complexity. |
| **Kafka / Distributed Streaming Queue in Core Pipeline** | "Enterprise message queue for streaming audit events." | Heavy JVM/ZooKeeper/KRaft footprint, complex offset management, and operational drag before core gateway logic is validated. | Append-only local disk spool with lightweight Go batch worker delivering to partitioned PostgreSQL. Kafka can be evaluated in later phases if event volume justifies it. |
| **Inline ML/AI Anomaly Detection in Authorization Path** | "AI model dynamically blocks anomalous or suspicious requests in real time." | Non-deterministic, unpredictable tail latencies, high false-positive rate blocking legitimate business traffic, completely unexplainable to audit/compliance teams. | Strict deterministic, versioned Rego policy rules. Telemetry can feed offline anomaly detection models that generate *advisory* signals or trigger quarantine via administrative APIs, but never inline non-deterministic authorization. |
| **Terraform / Multi-Cloud IaC Before Baseline Proven** | "Automate multi-cloud AWS/Azure/GCP provisioning on day one." | Diverts focus away from core distributed security engineering toward cloud provider API quirks, VPC peering, and billable cloud resources. | Standard Docker Compose for 100% reproducible local development, followed by clean Kubernetes manifests for production-target deployments. |

---

## Feature Dependencies

```
[Local/OIDC User Auth] ──┐
                         ├──> [Request Context Builder] ──> [Embedded OPA Policy Engine]
[Workload mTLS Listener] ─┘               │                               │
                                          │                               ▼
[Strict Path & Header Sanitizer] ─────────┘                     [Allow/Deny Decision]
                                                                          │
  ┌───────────────────────────────────────────────────────────────────────┤
  │ (If Allow)                                                            │ (If Deny)
  ▼                                                                       ▼
[Redis Atomic Rate Limiter] ──> [Durable Local Audit Spool]     [Decision Audit Event]
  │                                       │                               │
  ▼                                       ▼                               ▼
[Signed Backend Assertion] ───> [Upstream mTLS Proxy]           [403 / 429 Response]
  │
  ▼
[Backend Bypass Middleware]

──────────────────────────────────────────────────────────────────────────────────────────
Control Plane & Operational Dependencies:

[PostgreSQL Config Store]
    └──requires──> [Control Plane Management REST API]
                       └──requires──> [Signed Snapshot Generator]
                                          └──requires──> [gRPC Snapshot Distribution]
                                                             └──requires──> [Gateway Memory Snapshot]
                                                                                └──enforces──> [10s Freshness Lease / 60s Timeout]

[Operator Dashboard UI] ──requires──> [Control Plane Management REST API]
                            ├──enhances──> [Policy Simulator]
                            └──enhances──> [Replica Convergence View]

[Administrative Quarantine API] ──requires──> [Redis Revocation Registry]
                                                  └──enforces──> [Sub-5s Replica Convergence]
```

### Dependency Notes

- **Embedded OPA Policy Engine requires Request Context Builder:** Rego cannot evaluate arbitrary raw byte streams; it requires structured, typed JSON input containing validated principal identity, resource, canonical path, and method.
- **Request Context Builder requires Auth + Sanitizer:** Context creation depends on prior cryptographic verification of either the user JWT (OIDC) or workload certificate (mTLS), as well as canonical path extraction and header sanitization.
- **Signed Backend Assertion requires Upstream mTLS Proxy & Auth:** The assertion is minted only after an allow decision, encoding verified identity into an assertion token bound to the specific target service.
- **Backend Bypass Middleware requires Gateway Client Cert + Signed Assertion:** Backends must verify both the transport-layer gateway certificate and the application-layer signed context assertion before processing requests.
- **Gateway Memory Snapshot requires gRPC Snapshot Distribution & Freshness Lease:** Gateways load immutable in-memory configurations delivered over gRPC. A gateway will fail closed if its 60-second freshness lease expires without a renewed signature from the control plane.
- **Durable Audit Spool precedes Upstream Proxy:** Pre-forward audit durability requires the gateway to write and fsync the decision record before the HTTP request is dispatched to the backend.
- **Redis Revocation Registry conflicts with Open Degraded Fallback:** In production mode, if Redis fails, the system must fail closed (503). Permissive fallback is strictly prohibited by security invariants.

---

## MVP Definition

### Launch With (v1 / Phase 0 & Phase 1)

The minimum viable product provides a complete, runnable vertical slice demonstrating zero-trust policy enforcement, request sanitization, and structured auditing without distributed state complexity.

- [ ] **Go HTTP Reverse Proxy Gateway** — Core proxy built with `net/http` and `httputil.ReverseProxy` with request ID tracking and strict timeout budgets. *(Essential: foundation of all request routing)*
- [ ] **Embedded OPA/Rego Engine with Local Snapshot** — Gateway loads a static, pre-compiled Rego policy and route configuration from disk into memory, evaluating requests in <2ms. *(Essential: proves zero-trust policy model)*
- [ ] **Local Demo JWT Issuer** — Standalone service issuing signed short-lived tokens with seeded roles (`developer`, `finance`, `application-admin`). *(Essential: allows exercising role-based access without external cloud dependencies)*
- [ ] **Strict Path Normalization & Header Sanitization** — Rejects traversal attacks (`..`), encoded separators (`%2f`), and strips external forwarding/identity headers. *(Essential: guarantees data-plane integrity and prevents bypasses)*
- [ ] **Default-Deny Policy Execution** — Developer role can access `/api/orders` but is blocked from `/api/admin/users`. *(Essential: primary functional success criterion)*
- [ ] **Structured Audit Event Logging** — Structured JSON logging to stdout/file capturing request IDs, principal, decision, and reasons. *(Essential: compliance and operational observability)*
- [ ] **Three Private Demo Services** — `orders`, `payments`, and `admin` mock services running in Docker Compose with unexposed ports. *(Essential: targets to prove reverse proxy behavior)*
- [ ] **Automated Negative Test Suite** — Tests proving rejection of missing tokens, expired tokens, forged roles, header tampering, and traversal paths. *(Essential: validates security boundaries)*

### Add After Validation (v1.x / Phases 2–5)

Features to introduce progressively once the core proxy and policy vertical slice are validated.

- [ ] **Workload mTLS Listener & Backend Assertions (Phase 2)** — Dedicated mTLS port for machine identities (`spiffe://aegis.local/...`), backend gateway verification, and short-lived signed assertions (`aegis-gateway`). *(Trigger: core user proxy validated; need service-to-service zero trust)*
- [ ] **Control Plane & gRPC Snapshot Distribution (Phase 3)** — PostgreSQL configuration store, monotonic signed snapshot generation, gRPC streaming, and 10s lease renewals. *(Trigger: need centralized policy management across multiple gateways)*
- [ ] **Redis Rate Limiting & Sub-5s Quarantine (Phase 3)** — Atomic token bucket limits and sub-5s principal quarantine / token revocation with 503 fail-closed semantics. *(Trigger: need dynamic abuse prevention and emergency response)*
- [ ] **Durable Local Audit Spool & Postgres Worker (Phase 3)** — Disk spooling with `fsync` before forwarding and async delivery to PostgreSQL. *(Trigger: need audit compliance during database failure)*
- [ ] **Operator Dashboard & Policy Simulator (Phase 4)** — React/TypeScript UI for route management, live Rego policy simulation, version rollback, and audit log inspection. *(Trigger: control plane APIs complete and operational visibility required)*
- [ ] **Distributed Deployment & Convergence Testing (Phase 5)** — 3 gateway replicas behind an LB, graceful drain, rolling restarts, and fault injection benchmarks. *(Trigger: multi-replica architecture ready for resilience validation)*

### Future Consideration (v2+ / Phases 6 & 7)

Features deferred until multi-replica distributed operation is proven and benchmarked.

- [ ] **External OIDC Provider with PKCE BFF (Phase 6)** — Integration with Keycloak/Auth0 via same-origin backend-for-frontend and session cookies. *(Why defer: demo issuer is sufficient for local development and integration tests)*
- [ ] **Kubernetes Production Hardening (Phase 6)** — NetworkPolicies, topology spread, persistent volume spool mounts, and non-root security contexts. *(Why defer: Docker Compose provides faster developer feedback loop)*
- [ ] **SPIRE Workload Attestation (Phase 6)** — Automated workload certificate issuance and rotation via SPIFFE/SPIRE agents. *(Why defer: scripted dev CA certificates suffice for portfolio demonstration)*
- [ ] **Deterministic Risk Scoring Engine (Phase 7)** — Rule-based risk context calculation evaluated in Rego policies. *(Why defer: advanced feature built on top of stable baseline policy)*
- [ ] **Constrained Egress Proxy (Phase 7)** — Egress gateway enforcing strict destination host allowlists. *(Why defer: distinct threat model separate from ingress access)*
- [ ] **Immutable Audit S3 Object Archival (Phase 7)** — Long-term tamper-resistant cold storage for compliance. *(Why defer: PostgreSQL partitioned tables fulfill operational needs)*

---

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| **Default-Deny Reverse Proxy Gateway** | HIGH | MEDIUM | P1 |
| **Strict Path Normalization & Header Sanitization** | HIGH | MEDIUM | P1 |
| **Embedded OPA/Rego In-Memory Evaluation** | HIGH | MEDIUM | P1 |
| **Local Demo JWT Issuer & Token Validation** | HIGH | LOW | P1 |
| **Structured Audit Logging (JSON/stdout)** | HIGH | LOW | P1 |
| **Compose Multi-Service Test Environment** | HIGH | LOW | P1 |
| **Negative Security Test Suite** | HIGH | MEDIUM | P1 |
| **Workload mTLS Listener & Identity Parsing** | HIGH | MEDIUM | P2 |
| **Signed Backend Context Assertions** | HIGH | MEDIUM | P2 |
| **Backend Bypass Middleware & Mutual TLS** | HIGH | MEDIUM | P2 |
| **PostgreSQL Schema & Migrations** | HIGH | MEDIUM | P2 |
| **Control Plane & gRPC Snapshot Sync** | HIGH | HIGH | P2 |
| **Freshness Leases & 60s Fail-Closed Timeout** | HIGH | MEDIUM | P2 |
| **Redis Rate Limiting & Sub-5s Quarantine** | HIGH | MEDIUM | P2 |
| **Durable Disk Spool & Async Audit Worker** | HIGH | HIGH | P2 |
| **Operator Dashboard & Simulator (React)** | HIGH | HIGH | P2 |
| **3-Replica Convergence & Fault Injection** | HIGH | HIGH | P2 |
| **External OIDC / PKCE BFF** | MEDIUM | HIGH | P3 |
| **Kubernetes Helm / Manifests & NetworkPolicy** | MEDIUM | MEDIUM | P3 |
| **SPIRE Workload Attestation** | MEDIUM | HIGH | P3 |
| **Deterministic Risk Engine** | LOW | MEDIUM | P3 |
| **Constrained Egress Proxy** | LOW | HIGH | P3 |
| **S3 Audit Export** | LOW | MEDIUM | P3 |

**Priority key:**
- **P1:** Must have for launch (Phase 0 & Phase 1 MVP).
- **P2:** Core architecture completion (Phases 2–5 Hardened Local & Distributed).
- **P3:** Production hardening and future extensions (Phases 6–7).

---

## Competitor Feature Analysis

| Feature | Envoy Proxy / Istio | Pomerium | HashiCorp Boundary | Aegis (Our Approach) |
|---------|---------------------|----------|-------------------|----------------------|
| **Core Architecture** | C++ universal proxy with sidecar mesh or ingress gateway. | Go identity-aware reverse proxy (uses embedded Envoy). | Identity-based access management for TCP/SSH/DB sessions. | Dedicated Go reverse proxy + separate Go control plane; no Envoy dependency, no sidecar overhead. |
| **Policy Engine** | Envoy RBAC filters or remote `ext_authz` gRPC calls to OPA. | Embedded OPA/Rego policy engine compiling routes. | Built-in role and grant string system (not Rego). | Native embedded OPA in Go hot path; sub-2ms evaluation without remote `ext_authz` network hops. |
| **Configuration Sync** | Complex dynamic xDS protocol (gRPC stream from control plane). | Internal data broker synced via Redis/Postgres. | Controller-worker model using database as state store. | Signed immutable snapshots over gRPC with 10s freshness leases and 60s fail-closed timeout. |
| **Workload Identity** | Istio Citadel / SPIFFE mTLS sidecar interception. | Service accounts via header injection or API tokens. | Host and credential brokering with dynamic KMS secrets. | Dedicated workload mTLS listener with URI SAN parsing; backend verification with signed assertions. |
| **Backend Trust Model** | Assumes mesh sidecar mTLS terminates and forwards plaintext locally. | Injects identity headers (e.g. `X-Pomerium-Claim-*`) with optional JWT. | Proxies raw TCP connection after authenticating user. | Issues short-lived (<15s) signed context assertions (`aegis-gateway`) over mTLS; strips untrusted headers. |
| **Audit Durability** | Emits access logs to stdout or external gRPC collector (lossy on partition). | Streams logs to stdout, Datadog, or S3 storage. | Session recordings and event logs stored in database. | Local append-only disk spool with `fsync` before forward; async delivery to Postgres; fail-closed at 90% capacity. |
| **Rate Limiting & Quarantine** | Envoy global rate limit service (requires external gRPC daemon). | Basic rate limiting per route. | Concurrency and session limits per target. | Atomic Redis token bucket + sub-5s principal quarantine and token revocation with fail-closed semantics. |
| **Operator Experience** | Kiali / Jaeger / Grafana / complex Kubernetes CRDs. | Web console for routes, policies, and directory sync. | Desktop client and web UI for target connection brokering. | Focused React dashboard with what-if Rego policy simulation, version rollback, and replica convergence tracking. |

---

## Sources

- NIST Special Publication 800-207: *Zero Trust Architecture* (https://csrc.nist.gov/pubs/sp/800/207/final)
- RFC 8725: *JSON Web Token Best Current Practices* (https://www.rfc-editor.org/rfc/rfc8725.html)
- Open Policy Agent (OPA) Documentation & Go SDK Architecture (https://www.openpolicyagent.org/docs)
- SPIFFE (Secure Production Identity Framework for Everyone) Standard & Architecture (https://spiffe.io/docs)
- Envoy Proxy Architecture & External Authorization Filter (`ext_authz`) (https://www.envoyproxy.io/docs)
- Pomerium Open-Source Identity-Aware Proxy Architecture (https://www.pomerium.com/docs)
- HashiCorp Boundary Architecture (https://developer.hashicorp.com/boundary/docs)
- Aegis Architecture Specification: `spec.md` and Project Charter: `.planning/PROJECT.md`

---
*Feature research for: Distributed Zero-Trust Access Gateway (Aegis)*  
*Researched: 2026-10-06*
