# Aegis — Distributed Zero-Trust Access Gateway

## What This Is

Aegis is an independently designed, cloud-native distributed zero-trust access gateway and control plane in Go. It enforces identity-based authorization, workload authentication via mTLS, request-time OPA/Rego policy evaluation, rate limiting, and durable audit event streaming for private backend microservices without requiring a full service mesh. Built as an SDE internship portfolio showcase and a reference system that can be hardened for controlled production deployments.

## Core Value

Default-deny, fail-closed authorization and workload authentication where invalid credentials, stale security state, or dependency failures never produce implicit authorization.

## Requirements

### Validated

- [x] Reverse proxy gateway in Go with strict path parsing, header sanitization, and request ID tracking — *Validated in Phase 1 (GW-01, GW-02, GW-04)*
- [x] Embedded OPA/Rego policy engine evaluating typed request contexts locally in-memory (<2ms) — *Validated in Phase 1 (POL-01..POL-04, ~57µs eval)*
- [x] User authentication with short-lived JWTs (demo issuer for dev) — *Validated in Phase 1 (AUTH-01, AUTH-04)*
- [x] Enforced bypass prevention: backends have no exposed ports in isolated Docker network — *Validated in Phase 1 (BYP-01)*
- [x] Automated security and negative test suite (path traversal, header spoofing, RBAC, JWT negative tests, race detection) — *Validated in Phase 1 (REV-02, AUD-04, E2E)*
- [x] Workload authentication using dedicated mTLS listener with URI SAN identities (`spiffe://aegis.local/...`) — *Validated in Phase 2 (GW-05, AUTH-02, AUTH-03)*
- [x] Signed short-lived backend assertions (`aegis-gateway` issuer) from gateway to backends over mutual TLS — *Validated in Phase 2 (AUTH-05, GW-05)*
- [x] Enforced bypass prevention via backend mTLS and signed assertion verification — *Validated in Phase 2 (BYP-02, BYP-03)*

### Active

- [ ] Control plane for route/policy versioning, schema validation, policy simulation, and atomic snapshot distribution
- [ ] Monotonic signed configuration snapshots distributed to replicas over gRPC with bounded freshness leases (10s renew, 60s timeout)
- [ ] Redis-backed atomic rate limiting and sub-5-second principal quarantine / token revocation
- [ ] Durable local audit spool with fsync before request forwarding and asynchronous worker delivery to PostgreSQL
- [ ] Operator dashboard (React/TypeScript) and management REST APIs (`/control/v1`) with RBAC and CSRF protection
- [ ] Multi-replica distributed deployment behind a load balancer with convergence tracking and graceful drain

### Out of Scope

- [Enterprise certified security appliance / Zscaler clone] — Educational/portfolio reference system, not an audited turn-key enterprise appliance
- [General-purpose identity provider] — Demo issuer is strictly for development; production delegates to external OIDC
- [Transparent TLS interception & arbitrary database protocol proxying] — Protects HTTP application traffic only; no deep packet inspection or raw TCP DB proxying
- [Full service mesh] — Gateway architecture deliberately chosen to keep enforcement boundaries simple and explicit without sidecar overhead
- [Kafka, Terraform, ML anomaly models in v1] — Optional later extensions; avoid introducing complex infrastructure without proven requirement

## Context

- Target: SDE internship portfolio showcase and production-grade reference architecture demonstrating distributed systems and zero-trust engineering.
- Stack: Go (`net/http`, `httputil.ReverseProxy`, gRPC/Protobuf), embedded OPA/Rego, React/TypeScript (Vite), PostgreSQL 16+, Redis 7+, Prometheus, OpenTelemetry, Docker Compose, Kubernetes.
- Threat model: Default-deny across external boundary and internal network. Attackers can send arbitrary requests, spoof headers, replay stolen tokens until revoked, compromise an individual demo service, or interrupt dependencies. Privileged trust boundaries include gateway hosts, signing keys, and PKI admins.
- Delivery profiles: MVP (vertical slice) → Hardened local (mTLS, bypass prevention, control plane, audit spool) → Distributed (3 replicas, LB, convergence, benchmarks) → Production target (OIDC, Kubernetes, HA).

## Constraints

- **Language & Runtime**: Go for gateway, control plane, audit worker, and demo services; React/TypeScript for operator dashboard.
- **Architecture**: In-memory OPA snapshot evaluation on the gateway hot path; no synchronous PostgreSQL or external OPA network calls during authorization.
- **Security Invariants**: 12 mandatory invariants (default-deny, verified identity only, no upstream URL tampering, atomic snapshot activation, pre-forward durable audit, mTLS to backends with signed assertions).
- **Failure Semantics**: Fail-closed (503) on Redis revocation outage, expired snapshot lease (>60s), or spool saturation (>90%).
- **Verification**: Strict automated test suite with negative tests, race detection, fault injection, and reproducible benchmarks.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Embedded OPA engine over external OPA daemon | Guarantees hot-path in-memory sub-2ms evaluation without network hop or external daemon failure | Validated (~57µs eval) |
| Separate control plane with signed snapshots over direct DB lookups | Gateway hot-path must not depend synchronously on PostgreSQL; immutable memory snapshots ensure atomic updates | — Pending (Phase 3) |
| Dedicated mTLS listener for workloads | Avoids ambiguous principal selection between user bearer tokens and client certificates | Validated in Phase 2 |
| Signed backend assertions over raw bearer forwarding | Backends verify caller context originates from gateway without exposing user tokens downstream | Validated in Phase 2 |
| Durable local disk spool before forwarding | Prevents loss of audit records during database outages while admitting requests within spool capacity | — Pending (Phase 3) |
| Redis for rate limiting and revocation only (not policy truth) | Retains high-speed shared counters while keeping authoritative policy versioning in PostgreSQL snapshots | — Pending (Phase 3) |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-10-06 after Phase 2 completion*
