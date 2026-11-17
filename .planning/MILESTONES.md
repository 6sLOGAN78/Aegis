# Milestones

## v1.0 Aegis Distributed Zero-Trust Access Gateway (Shipped: 2026-11-18, re-completed after gap closure)

**Phases completed:** 14 phases (00 through 13), 85 plans; phases 07–13 and quick task 261117-rvo closed the gaps found by four re-audits
**Requirements:** 38/38 v1 requirements satisfied (re-audit 4, status `tech_debt`; see [milestones/v1.0-MILESTONE-AUDIT.md](milestones/v1.0-MILESTONE-AUDIT.md))
**Timeline:** 2026-10-06 → 2026-11-18 (553 commits from fb9a406)
**Codebase:** ~44,800 LOC Go, ~2,700 LOC TypeScript (dashboard)
**Tag:** `v1.0` still points at the first close (ae368ae, already pushed); this re-completion is not re-tagged
**Known deferred items at close:** 12 (see STATE.md Deferred Items); live Kubernetes UAT not run (D-19)

**Key accomplishments:**

- **Zero-trust edge gateway:** Go reverse proxy with path-traversal rejection, header scrubbing, 16 KiB header and 1 MiB body limits (enforced on both listeners, upstream 101 upgrades refused), JWT user listener on :8080 and SPIFFE mTLS workload listener on :9443, and signed backend assertions; backends are unreachable except through the gateway.
- **Embedded OPA policy engine:** precompiled Rego v1 with ~40µs decisions, default deny, an RBAC and workload matrix, and a dashboard simulator that evaluates exactly what the gateway enforces.
- **Signed control plane with freshness leases:** Postgres-backed routes and policies, Ed25519-signed monotonic snapshots streamed over gRPC with mTLS-bound gateway identity, a 60s fail-closed lease, key rotation without restart, and quarantine/revocation reconstruction on disaster recovery.
- **Durable audit pipeline:** a pre-forward fsynced WAL spool with a 90% saturation gate, denial and completion records, an audit worker delivering to Postgres at least once with deduplication, rotation and pruning, proven live by a SIGKILL test (437/437 decision rows, no duplicates).
- **Distributed operation:** three replicas behind HAProxy with two-phase drain (live: zero errors on :8080 and no 5xx on :9443 under load), jittered reconnect, Prometheus metrics with no PII labels, chaos and benchmark evidence, and Kubernetes manifests with NetworkPolicies, a shared DB Secret and a sidecar drain budget.
- **Operator console and runbooks:** React dashboard (policy studio, simulator, gateways, audit log, quarantine) restyled to the design system, plus runbooks whose metric and object names are checked by tests.

---


## v1.0 first close (Shipped: 2026-10-07 — REOPENED 2026-10-08 for gap closure; superseded by the entry above)

> Re-audit on 2026-10-08 found 24/38 requirements wired end to end and 8 integration blockers. See [milestones/v1.0-MILESTONE-AUDIT-2026-10-08.md](milestones/v1.0-MILESTONE-AUDIT-2026-10-08.md). The audit figures below are from the superseded 2026-10-07 audit.

**Phases completed:** 7 phases (00 through 06), 25 plans, 55 tasks  
**Codebase:** 26,710 LOC Go, 3,050 LOC TypeScript, 264 LOC Rego  
**Git Range:** `fb9a406` → `9813cbf` (155 commits, 330 files modified, +63,540 lines)  
**Timeline:** 2026-10-06 → 2026-10-07  
**Audit:** [milestones/v1.0-MILESTONE-AUDIT-2026-10-07.md](milestones/v1.0-MILESTONE-AUDIT-2026-10-07.md) (superseded)

**Key accomplishments:**

- **Edge Reverse Proxy & Path Security**: Go HTTP reverse proxy featuring strict zero-repair wire-path traversal rejection (HTTP 400), unauthenticated concurrency throttling (HTTP 429), listener limits (16 KiB headers, 1 MiB body), and modern Go `Rewrite` hook ingress header scrubbing.
- **In-Memory OPA Policy Engine**: Precompiled Rego v1 policy evaluator delivering ~40µs decision latency (<2ms budget) with zero network calls, typed JSON schema contracts, atomic snapshot loading, and comprehensive RBAC security test matrix.
- **Workload Identity & Enforced Bypass Prevention**: Pure Go PKI generator, dedicated workload mTLS listener (:9443) with strict SPIFFE URI SAN identity extraction, short-lived signed backend assertions (`X-Aegis-Assertion`), backend mTLS middleware, and zero exposed host ports.
- **Control Plane & Streaming Freshness Leases**: PostgreSQL persistence with Goose migrations, monotonic Ed25519-signed snapshots, streaming gRPC distribution with atomic pointer swap, 10s freshness leases with 60s fail-closed boundary, and monotonic rollback engine.
- **Atomic Rate Limiting & Ephemeral Quarantine**: Redis GCRA token-bucket rate limiter, sub-5-second JTI token revocation and principal quarantine, strict 200ms context deadlines, and fail-closed HTTP 503 outage enforcement.
- **Pre-Forward Durable Audit WAL Spool**: Local append-only disk WAL spool with synchronous pre-forward `fsync`, 90% capacity saturation gate (HTTP 503), persistent cursor checkpoints, and asynchronous batch worker flushing to PostgreSQL with idempotent deduplication.
- **SOC Operator Experience & Telemetry**: Embedded React 19/Vite 8/Tailwind CSS console with Monaco Rego editor, dry-run simulation, real-time replica convergence tracking, emergency quarantine UI, and non-PII Prometheus telemetry.
- **Distributed Resilience & Benchmarks**: 3-replica gateway cluster deployed behind HAProxy with active `/readyz` health polling, 30s graceful connection drain, mathematical full-jitter reconnect backoff, chaos fault injection suite, and k6 benchmarks (<20ms p99 gateway added latency at 1,000 req/s).
- **Production Hardening & SRE Runbooks**: Kubernetes PSS Restricted reference manifests, zero-downtime multi-key rotation engine (JWT, Ed25519, mTLS CA), automated disaster recovery drills (Redis wipeout, PostgreSQL PITR, WAL crash replay), and 7 standardized operational SRE runbooks.

---
