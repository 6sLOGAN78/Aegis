# Project Retrospective

*A living document updated after each milestone. Lessons feed forward into future planning.*

## Milestone: v1.0 — Aegis Distributed Zero-Trust Access Gateway

**Shipped:** 2026-10-07  
**Phases:** 7 | **Plans:** 25 | **Tasks:** 55  

### What Was Built
- **Edge Reverse Proxy & Path Security**: Zero-repair wire path validation rejecting traversal, encoded slashes, and NUL bytes with HTTP 400; unauthenticated concurrency throttling; listener byte bounding; and Go `Rewrite` hook header sanitization.
- **Embedded OPA Engine**: Precompiled Rego v1 query evaluator executing typed request contexts in-memory (~40µs latency vs <2ms budget) with zero network calls and strict default-deny semantics.
- **Workload Identity & Enforced Bypass Prevention**: Pure Go development PKI, dedicated mTLS listener (:9443) with SPIFFE URI SAN extraction, short-lived signed assertions (`X-Aegis-Assertion`), backend middleware, and zero exposed host ports.
- **Control Plane & Streaming Freshness Leases**: PostgreSQL persistence with Goose migrations, monotonic Ed25519 snapshots, streaming gRPC distribution with atomic pointer swap, 10s freshness leases with 60s fail-closed boundary, and monotonic rollback engine.
- **Atomic Rate Limiting & Ephemeral Quarantine**: Redis GCRA token-bucket rate limiter, sub-5-second JTI token revocation and principal quarantine, strict 200ms context deadlines, and fail-closed HTTP 503 outage enforcement.
- **Pre-Forward Durable Audit WAL Spool**: Local append-only disk WAL spool with synchronous pre-forward `fsync`, 90% capacity saturation gate (HTTP 503), persistent cursor checkpoints, and asynchronous batch worker flushing to PostgreSQL with idempotent deduplication.
- **SOC Operator Experience & Telemetry**: Embedded React 19/Vite 8 console with Monaco Rego editor, dry-run simulation, real-time replica convergence tracking, emergency quarantine UI, and non-PII Prometheus telemetry.
- **Distributed Resilience & Benchmarks**: 3-replica gateway cluster deployed behind HAProxy with active `/readyz` health polling, 30s graceful connection drain, mathematical full-jitter reconnect backoff, chaos fault injection suite, and k6 benchmarks (<20ms p99 gateway added latency at 1,000 req/s).
- **Production Hardening & SRE Runbooks**: Kubernetes PSS Restricted reference manifests, zero-downtime multi-key rotation engine (JWT, Ed25519, mTLS CA), automated disaster recovery drills (Redis wipeout, PostgreSQL PITR, WAL crash replay), and 7 standardized operational SRE runbooks.

### What Worked
- **In-Memory OPA Hot Path**: Precompiling Rego queries and atomically swapping query instances eliminated external daemon hops, keeping p99 evaluation latency well under 100µs.
- **Go 1.20+ ReverseProxy `Rewrite`**: Using `Rewrite` instead of deprecated `Director` cleanly separated inbound `r.In` from outbound `r.Out`, preventing hop-by-hop header leaks and path divergence.
- **Pure Go In-Memory PKI**: Writing an in-memory PKI generator using `crypto/ecdsa` and `crypto/x509` allowed rapid, deterministic test execution without invoking external CLI binaries (`cfssl` or `openssl`).
- **Fail-Closed by Design**: Bounding all external dependencies (Redis, Control Plane leases, WAL disk space) with strict timeouts and fail-closed HTTP 503 responses guaranteed zero fail-open leaks under chaos.
- **Multi-Key Dynamic Keyset Rotation**: Wrapping trusted keys with `sync.RWMutex` enabled seamless 3-phase rotation without dropping a single in-flight request.

### What Was Inefficient
- **Initial Schema Scoping**: OpenAPI contracts and Protobuf definitions took extra iteration early on to ensure exact alignment across client models and server stubs.
- **Monotonic Version Reconciliation on PITR**: Required careful design to reconcile restored database versions with running gateway fleet versions to avoid monotonic version violations.

### Patterns Established
- **Strict Zero-Repair Path Validation**: Never repair or clean raw URLs with `path.Clean()`; reject anomalies immediately with HTTP 400.
- **Dual-Listener Identity Separation**: Physically isolate user ingress (:8080) from workload ingress (:9443), failing closed if user bearer credentials appear on the workload port.
- **Pre-Forward Durable WAL Spooling**: Synchronously `fsync` audit records to local disk prior to dispatching upstream requests.
- **Standardized 6-Part SRE Runbooks**: Consistent operational runbooks with alert metadata, triage decision trees, CLI commands, verification tests, and post-incident templates.

### Key Lessons
1. **Zero-Trust is about explicit boundaries**: Never trust headers from the wire; extract identities exclusively from cryptographically verified TLS connections or signed tokens.
2. **Fail-Closed requires explicit bounds**: Every dependency check must have a strict deadline (e.g. 200ms for Redis) and default to 503 on timeout.
3. **Decouple control plane from hot path**: Gateway data plane must function autonomously for at least 60s during control plane outages, evaluating policy locally from memory.

---

## Cross-Milestone Trends

### Process Evolution

| Milestone | Sessions | Phases | Key Change |
|---|---|---|---|
| v1.0 | 7 | 7 | Established zero-trust architecture, contract-first design, in-memory OPA, and complete automated verification suites |

### Cumulative Quality

| Milestone | Tests | Coverage | Zero-Dep Additions |
|---|---|---|---|
| v1.0 | 28 test suites | 100% Nyquist compliant across all packages | Pure Go PKI, in-memory OPA, local disk WAL spool |

### Top Lessons (Verified Across Milestones)

1. Default-deny and fail-closed failure semantics prevent silent authorization bypasses during operational degradation.
2. In-memory data structures swapped via `sync/atomic.Pointer` provide ultra-low latency (<0.1ms) policy enforcement without synchronization bottlenecks.
