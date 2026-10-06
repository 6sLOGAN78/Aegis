# Requirements: Aegis — Distributed Zero-Trust Access Gateway

**Defined:** 2026-10-06
**Core Value:** Default-deny, fail-closed authorization and workload authentication where invalid credentials, stale security state, or dependency failures never produce implicit authorization.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Core Gateway & Reverse Proxy (GW)

- [x] **GW-01**: Gateway enforces HTTP listener request limits (16 KiB headers, 1 MiB body) and assigns cryptographically random UUID request IDs
- [x] **GW-02**: Strict host and path validation rejects traversal (`..`), encoded slashes (`%2f`), NUL bytes, and duplicate slashes without auto-repair (400 Bad Request)
- [x] **GW-03**: Deterministic upstream route resolution binds incoming HTTP method and canonical path template to configured private backend services
- [x] **GW-04**: Ingress header sanitization strips untrusted `X-Aegis-*`, `Forwarded`, `X-Forwarded-*`, and RFC 7230 hop-by-hop headers
- [ ] **GW-05**: Gateway reverse proxy forwards validated requests over verified mutual TLS to private backends using modern Go `Rewrite` hooks

### Authentication & Identity (AUTH)

- [x] **AUTH-01**: User authentication validates short-lived signed JWT bearer tokens against pinned issuer, audience, and algorithm allowlist (rejecting `none` and symmetric keys)
- [x] **AUTH-02**: Dedicated mTLS listener (`:8443`) terminates workload connections and extracts authenticated SPIFFE URI SAN identities (`spiffe://aegis.local/workload/...`)
- [x] **AUTH-03**: Workload mTLS listener rejects user bearer credentials to prevent ambiguous principal selection
- [x] **AUTH-04**: Local development demo JWT issuer with seeded credentials (`developer`, `finance`, `application-admin`) and login throttling
- [ ] **AUTH-05**: Gateway mints and signs short-lived (<=15s) backend assertion JWTs (`X-Aegis-Assertion`) bound to target service, method, canonical path, and request ID

### Policy Engine & Authorization (POL)

- [x] **POL-01**: Embedded OPA/Rego policy engine evaluates typed request context in-memory with sub-2ms latency with zero network calls
- [x] **POL-02**: Strict default-deny authorization where unmapped routes or evaluation errors fail closed (explicit deny supersedes allow)
- [x] **POL-03**: Core RBAC and workload authorization matrix enforces developer, finance, admin, and workload role permissions
- [x] **POL-04**: Policies cannot grant access when authentication, revocation, freshness, audit admission, or transport requirements fail

### Control Plane & Snapshot Distribution (CTRL)

- [ ] **CTRL-01**: Control plane manages route definitions and policy drafts with OpenAPI schema validation and Rego unit test verification
- [ ] **CTRL-02**: Monotonic signed configuration snapshots (Ed25519) containing routes, policies, and identity mappings
- [ ] **CTRL-03**: Streaming gRPC snapshot distribution pushes active configuration to gateway replicas with atomic in-memory swap
- [ ] **CTRL-04**: Signed 10-second freshness leases streamed to replicas; gateways fail closed (503) after 60 seconds without valid lease renewal
- [ ] **CTRL-05**: Gateway replicas report activation acknowledgments and convergence status back to the control plane
- [ ] **CTRL-06**: Policy rollback republishes previous content under a strictly higher monotonic version number

### Rate Limiting, Revocation & Quarantine (REV)

- [ ] **REV-01**: Redis-backed atomic token bucket rate limiting by principal and route (default 100 rps, burst 200)
- [x] **REV-02**: Global ingress connection and request concurrency bounds protect unauthenticated traffic before identity resolution
- [ ] **REV-03**: Ephemeral Redis token `jti` revocation and principal quarantine takes effect across all replicas within 5 seconds
- [ ] **REV-04**: Revocation check timeout (200ms) fails closed (503) on Redis unavailability with no permissive fallback

### Audit & Durable Event Persistence (AUD)

- [ ] **AUD-01**: Gateway appends pre-forward authorization decision records to a local append-only disk WAL with `fsync` before forwarding permitted requests
- [ ] **AUD-02**: Spool saturation safety gate stops admitting permitted requests at 90% disk capacity (503 response)
- [ ] **AUD-03**: Asynchronous audit worker delivers spooled records to PostgreSQL with at-least-once batching and deduplication
- [x] **AUD-04**: Completion audit events record backend HTTP status, request duration, and error codes

### Bypass Prevention & Backend Protection (BYP)

- [x] **BYP-01**: Demo microservices (`orders`, `payments`, `admin`) publish no external ports and run on private networks
- [ ] **BYP-02**: Backend middleware enforces mTLS and validates gateway identity and signed `X-Aegis-Assertion` JWT
- [ ] **BYP-03**: Direct calls from external clients or peer workloads to protected endpoints fail immediately (401/403)

### Operator Dashboard & Management APIs (OPS)

- [ ] **OPS-01**: REST JSON management endpoints (`/control/v1`) for routes, policies, simulation, quarantine, and audit log inspection
- [ ] **OPS-02**: Optimistic concurrency (ETag/If-Match), idempotency keys, and RBAC permissions on management endpoints
- [ ] **OPS-03**: React/TypeScript operator dashboard with audit stream, live Rego policy simulation, and replica convergence tracking
- [ ] **OPS-04**: Operator session security with HttpOnly cookies, CSRF protection, and Content Security Policy

### Resilience, Telemetry & Distributed Operation (DIST)

- [ ] **DIST-01**: Multi-replica deployment behind load balancer with health checking, graceful drain (30s), and reconnect jitter
- [ ] **DIST-02**: Prometheus metrics tracking requests, latency, denials, snapshot age, spool capacity, and lease health without PII labels
- [ ] **DIST-03**: Automated test suite proving negative authentication, header spoofing, path traversal, bypass prevention, and dependency outages

## v2 Requirements

Deferred to future releases. Tracked but not in current v1 roadmap.

### Enterprise Identity & Managed PKI

- **ID-01**: External OIDC provider integration with PKCE backend-for-frontend (BFF)
- **PKI-01**: Automated SPIRE workload identity issuance and dynamic mTLS certificate rotation

### Advanced Isolation & Cloud Target

- **K8S-01**: Production Kubernetes manifests with default-deny NetworkPolicies and topology spread
- **RISK-01**: Deterministic risk scoring engine based on trusted connection telemetry
- **EGRS-01**: Constrained forward egress proxy for approved destination host/port pairs

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| General-purpose Identity Provider | Huge blast radius; Aegis relies on external OIDC + lightweight dev demo issuer |
| Transparent TLS Interception (MITM) | Breaks end-to-end trust and certificate pinning; Aegis is an explicit Layer 7 reverse proxy |
| Arbitrary Database Wire Protocol Proxying | Impedance mismatch with Rego HTTP policies; protect HTTP/gRPC services fronting DBs instead |
| Full Service Mesh Sidecar Injection | Heavy operational overhead (100MB+ RAM/pod); Aegis provides explicit gateway-tier zero-trust boundaries |
| Kafka / Distributed Event Broker in v1 | Operational complexity; durable local disk WAL spool with Go worker fulfills Invariant 10 |
| Inline ML/AI Anomaly Decision in Auth Path | Non-deterministic latency and false positives; zero-trust decisions must be deterministic and verifiable |
| Multi-Cloud Terraform IaC in v1 | Premature abstraction; focus on rock-solid Docker Compose and clean Kubernetes deployment |

## Traceability

Which phases cover which requirements. Populated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| GW-01 | Phase 1 | Complete |
| GW-02 | Phase 1 | Complete |
| GW-03 | Phase 1 | Complete |
| GW-04 | Phase 1 | Complete |
| GW-05 | Phase 2 | Pending |
| AUTH-01 | Phase 1 | Complete |
| AUTH-02 | Phase 2 | Complete |
| AUTH-03 | Phase 2 | Complete |
| AUTH-04 | Phase 1 | Complete |
| AUTH-05 | Phase 2 | Pending |
| POL-01 | Phase 1 | Complete |
| POL-02 | Phase 1 | Complete |
| POL-03 | Phase 1 | Complete |
| POL-04 | Phase 1 | Complete |
| CTRL-01 | Phase 3 | Pending |
| CTRL-02 | Phase 3 | Pending |
| CTRL-03 | Phase 3 | Pending |
| CTRL-04 | Phase 3 | Pending |
| CTRL-05 | Phase 3 | Pending |
| CTRL-06 | Phase 3 | Pending |
| REV-01 | Phase 3 | Pending |
| REV-02 | Phase 1 | Complete |
| REV-03 | Phase 3 | Pending |
| REV-04 | Phase 3 | Pending |
| AUD-01 | Phase 3 | Pending |
| AUD-02 | Phase 3 | Pending |
| AUD-03 | Phase 3 | Pending |
| AUD-04 | Phase 1 | Complete |
| BYP-01 | Phase 1 | Complete |
| BYP-02 | Phase 2 | Pending |
| BYP-03 | Phase 2 | Pending |
| OPS-01 | Phase 4 | Pending |
| OPS-02 | Phase 4 | Pending |
| OPS-03 | Phase 4 | Pending |
| OPS-04 | Phase 4 | Pending |
| DIST-01 | Phase 5 | Pending |
| DIST-02 | Phase 4 | Pending |
| DIST-03 | Phase 5 | Pending |

**Coverage:**
- v1 requirements: 38 total
- Mapped to phases: 38
- Unmapped: 0 ✓

---
*Requirements defined: 2026-10-06*
*Last updated: 2026-10-06 after roadmap creation*
