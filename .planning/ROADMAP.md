# Roadmap: Aegis — Distributed Zero-Trust Access Gateway

## Overview

Aegis is an independently designed, cloud-native distributed zero-trust access gateway and control plane in Go. This roadmap guides implementation from foundational architectural contracts and threat models through a secure MVP vertical slice, workload mTLS with bypass prevention, dynamic control plane snapshot streaming with durable audit persistence, operator management dashboard and telemetry, distributed multi-replica resilience and benchmarks, to final production hardening and operational runbooks.

The journey enforces strict default-deny authorization, zero-repair path normalization, pre-forward durable audit logging, and fail-closed security invariants at every milestone.

## Phases

**Phase Numbering:**
- Integer phases (0, 1, 2, 3, 4, 5, 6): Planned milestone work
- Decimal phases (e.g. 2.1): Urgent insertions (marked with INSERTED)

Phases execute in numeric order: 0 → 1 → 2 → 3 → 4 → 5 → 6.

- [x] **Phase 0: Architecture Contracts, Schemas & Threat Model** - Authoritative ADRs, OpenAPI/Protobuf contracts, Rego input schemas, and zero-trust threat models (completed 2026-10-06)
- [ ] **Phase 1: MVP Secure Vertical Slice** - Go gateway reverse proxy, local demo JWT issuer, embedded OPA engine, 3 private microservices, and Docker Compose MVP
- [ ] **Phase 2: Workload Identity & Enforced Bypass Prevention** - Dedicated workload mTLS listener (:9443), SPIFFE URI SANs, backend mTLS, signed assertion JWTs, and direct bypass elimination
- [ ] **Phase 3: Control Plane, Snapshot Streaming & Durable State** - PostgreSQL persistence, signed Ed25519 snapshots, gRPC streaming with 10s freshness leases, Redis rate limits/revocation, and local disk WAL spool with async worker
- [ ] **Phase 4: Operator Experience & Telemetry** - React/TypeScript dashboard, Monaco Rego editor, dry-run policy simulator, replica convergence tracking, /control/v1 REST APIs, and Prometheus metrics
- [ ] **Phase 5: Distributed Resilience, Chaos & Benchmark Evidence** - 3 gateway replicas behind load balancer, 30s graceful drain, gRPC reconnect jitter, chaos fault injection tests, and reproducible k6 benchmarks
- [ ] **Phase 6: Production Hardening & Operational Runbooks** - Kubernetes reference manifests, credential rotation drills, disaster recovery drills, security audit review, and SLO validation runbooks

---

## Phase Details

### Phase 0: Architecture Contracts, Schemas & Threat Model
**Goal:** Establish authoritative interface contracts, Protobuf/OpenAPI schemas, typed Rego input definitions, and zero-trust threat models before implementation.  
**Mode:** mvp  
**Depends on:** Nothing (foundational phase)  
**Requirements:** None (establishes schemas, ADRs, and threat models for all v1 requirements GW-01 through DIST-03)  
**Success Criteria** (what must be TRUE):
  1. OpenAPI 3.0 specification (`api/openapi/control-v1.yaml`) passes schema validation and completely models all `/control/v1` routes, policies, simulation, and quarantine endpoints.
  2. Protobuf definitions (`api/proto/snapshot/v1/snapshot.proto`) compile without errors into Go structs for monotonic signed snapshots and gRPC streaming distribution.
  3. Architecture Decision Records (ADRs) document gateway proxy architecture, in-memory OPA embedding, dual-listener identity separation, signed freshness leases, Redis fail-closed semantics, and pre-forward disk WAL audit spooling.
  4. Threat model documents trust boundaries, attacker capabilities, and 12 non-negotiable security invariants with verifiable mitigation mappings.

Plans:
- [x] 00-01: Architecture Decision Records (ADRs) and zero-trust threat model documentation
- [x] 00-02: OpenAPI 3.0 management specification and Protobuf snapshot distribution schemas
- [x] 00-03: Rego typed request input schemas, route definitions, and seed permission matrix

---

### Phase 1: MVP Secure Vertical Slice
**Goal:** Deliver a fully functional, self-contained single-replica Go gateway enforcing strict path validation, header scrubbing, user JWT authentication, embedded OPA policy evaluation, and completion auditing against three private backend services in Docker Compose.  
**Mode:** mvp  
**Depends on:** Phase 0  
**Requirements:** GW-01, GW-02, GW-03, GW-04, AUTH-01, AUTH-04, POL-01, POL-02, POL-03, POL-04, REV-02, AUD-04, BYP-01  
**Success Criteria** (what must be TRUE):
  1. User with `developer` role token successfully retrieves `/api/orders` (HTTP 200) and `/api/payments` (HTTP 200), but is rejected from `/api/admin/users` with HTTP 403 Forbidden.
  2. Incoming requests with path traversal (`/..`), encoded separators (`%2f`, `%5c`), NUL bytes (`%00`), or duplicate slashes (`//`) are rejected immediately with HTTP 400 Bad Request without path repair.
  3. Inbound client-supplied `X-Aegis-*` and hop-by-hop headers are stripped; unauthenticated ingress concurrency is bounded before identity resolution.
  4. Embedded OPA engine evaluates typed request context in-memory in under 2ms using a precompiled local snapshot without making external network calls.
  5. All three backend services (`orders`, `payments`, `admin`) run in Docker Compose with zero published host ports, and every proxy response emits a structured completion audit event.

Plans:
- [ ] 01-01: Go gateway reverse proxy with strict path rejection, request limits, and header sanitization
- [ ] 01-02: Embedded OPA engine, precompiled query evaluator, and static local snapshot loader
- [ ] 01-03: Mock JWT issuer, private demo microservices (orders, payments, admin), and Docker Compose mvp profile
- [ ] 01-04: Structured completion audit events and automated negative security test suite

---

### Phase 2: Workload Identity & Enforced Bypass Prevention
**Goal:** Implement mutual TLS workload authentication on a dedicated listener, enforce gateway client identity verification and short-lived signed assertions on backends, and eliminate direct microservice bypass.  
**Mode:** mvp  
**Depends on:** Phase 1  
**Requirements:** GW-05, AUTH-02, AUTH-03, AUTH-05, BYP-02, BYP-03  
**Success Criteria** (what must be TRUE):
  1. Workload presenting a verified client certificate with SPIFFE URI SAN `spiffe://aegis.local/workload/orders` on port `:9443` successfully invokes `/api/payments` via the gateway; requests presenting user bearer tokens on this listener are rejected with HTTP 401/403.
  2. Gateway mints a short-lived (<=15s) Ed25519-signed JWT assertion (`X-Aegis-Assertion`, issuer `aegis-gateway`) bound to target service, canonical path, and request ID on every forwarded request over mTLS.
  3. Backend middleware validates the gateway mTLS client certificate and verifies assertion claims (signature, audience, method, path); direct HTTP/HTTPS calls from outside or peer containers without gateway certificate and assertion fail immediately (HTTP 401/403 or TLS failure).
  4. Automated security tests prove that an authenticated workload (`orders`) cannot access administrative endpoints (`/api/admin/users`), confirming workload role policy enforcement.

Plans:
- [ ] 02-01: Local PKI generation scripts and dedicated workload mTLS listener (:9443) with SPIFFE URI SAN parsing
- [ ] 02-02: Short-lived signed backend assertion JWT generator (aegis-gateway) and gateway client mTLS transport
- [ ] 02-03: Reusable backend authentication middleware and Docker network isolation (zero published host ports)
- [ ] 02-04: End-to-end workload authentication and bypass prevention validation tests

---

### Phase 3: Control Plane, Snapshot Streaming & Durable State
**Goal:** Establish centralized policy lifecycle management in PostgreSQL, monotonic signed snapshot distribution over gRPC with 10s freshness leases, Redis atomic rate limiting and <5s quarantine, and durable local disk WAL spool with asynchronous batch delivery to PostgreSQL.  
**Mode:** mvp  
**Depends on:** Phase 2  
**Requirements:** CTRL-01, CTRL-02, CTRL-03, CTRL-04, CTRL-05, CTRL-06, REV-01, REV-03, REV-04, AUD-01, AUD-02, AUD-03  
**Success Criteria** (what must be TRUE):
  1. Control plane validates route/policy drafts with OpenAPI schema validation and Rego tests, increments monotonic version, signs snapshots with Ed25519, and pushes to gateway replicas via gRPC with atomic in-memory pointer swap (`sync/atomic.Pointer`).
  2. Gateway enforces 10-second signed freshness leases; if control plane stream is severed for >60 seconds, gateway drops readiness and fails closed returning HTTP 503 on protected routes.
  3. Administrative principal quarantine or token JTI revocation written to Redis blocks caller access across all gateway replicas within 5 seconds; Redis dependency outage or timeout (>200ms) fails closed with HTTP 503 (no permissive fallback).
  4. Permitted requests append authorization records to local append-only disk WAL with `fsync` before forwarding; async worker delivers batches to partitioned PostgreSQL with deduplication; spool reaching 90% disk capacity halts permitted admissions with HTTP 503.
  5. Policy rollback republishes previous configuration under a strictly higher monotonic integer version, and gateway acknowledges active version.

Plans:
- [ ] 03-01: PostgreSQL relational schemas, goose migrations, and control plane snapshot repository
- [ ] 03-02: Monotonic Ed25519 snapshot signer, gRPC snapshot streaming, 10s freshness lease, and 60s fail-closed timeout
- [ ] 03-03: Redis atomic token-bucket rate limiting and <5s JTI revocation / principal quarantine with fail-closed semantics
- [ ] 03-04: Local append-only disk WAL spool with pre-forward fsync, 90% saturation gate, and async audit worker
- [ ] 03-05: Fault injection and restart tests for dependency failure modes

---

### Phase 4: Operator Experience & Telemetry
**Goal:** Provide operators with a secure React/TypeScript management console for live Rego simulation, policy drafting, rollback, and convergence tracking, backed by `/control/v1` REST APIs and Prometheus observability.  
**Mode:** mvp  
**Depends on:** Phase 3  
**Requirements:** OPS-01, OPS-02, OPS-03, OPS-04, DIST-02  
**Success Criteria** (what must be TRUE):
  1. Operator dashboard authenticates via HttpOnly session cookies with CSRF token verification and Content Security Policy; mutating actions enforce optimistic concurrency (ETag/If-Match) and idempotency keys.
  2. Monaco-based policy editor allows operators to author Rego drafts, run dry-run simulations against synthetic request contexts, and execute policy rollbacks with visual confirmation.
  3. Replica convergence view visualizes active snapshot versions, lease renewal timestamps, and ack statuses across all connected gateway instances.
  4. Filterable audit stream displays recent authorization decisions, reason codes, latencies, and quarantine actions sourced from PostgreSQL.
  5. Prometheus `/metrics` endpoint on private listener exposes request throughput, latency histograms, denial reason codes, snapshot version, lease age, and spool capacity without high-cardinality PII labels.

Plans:
- [ ] 04-01: Control plane REST management APIs (/control/v1) with RBAC, CSRF protection, and optimistic concurrency
- [ ] 04-02: React/TypeScript operator dashboard with Monaco Rego editor, dry-run simulator, and audit log viewer
- [ ] 04-03: Real-time replica convergence tracking, emergency quarantine UI, and Prometheus telemetry metrics

---

### Phase 5: Distributed Resilience, Chaos & Benchmark Evidence
**Goal:** Validate distributed multi-replica gateway operation (3 instances) behind a load balancer, demonstrating graceful draining, gRPC jitter reconnect, automated chaos fault tolerance, and reproducible k6 performance benchmarks.  
**Mode:** mvp  
**Depends on:** Phase 4  
**Requirements:** DIST-01, DIST-03  
**Success Criteria** (what must be TRUE):
  1. Three gateway replicas operate behind a load balancer with automated health checking, 30-second graceful connection draining on SIGTERM, and randomized exponential gRPC reconnect jitter (100ms–5s).
  2. All gateway replicas converge to newly published policy snapshots within 5 seconds at p99 under active traffic load.
  3. Automated chaos test suite validates system behavior during killed gateway instances, Redis partitions, PostgreSQL outages, and spool saturation, proving 100% adherence to fail-closed failure semantics without unintended fail-open bypass.
  4. Reproducible k6 benchmark suite records and reports baseline backend, 1-gateway, and 3-gateway performance, demonstrating <2ms in-memory OPA evaluation and <20ms p99 added gateway latency at 1,000 req/s.

Plans:
- [ ] 05-01: Multi-replica gateway cluster (3 instances) behind load balancer with health checking and 30s graceful drain
- [ ] 05-02: gRPC reconnect backoff with randomized jitter and fleet snapshot convergence tracking
- [ ] 05-03: Chaos fault injection suite and reproducible k6 performance benchmark suite

---

### Phase 6: Production Hardening & Operational Runbooks
**Goal:** Establish production reference manifests, automated credential and key rotation drills, disaster recovery procedures, and comprehensive operational runbooks validating all release acceptance criteria.  
**Mode:** mvp  
**Depends on:** Phase 5  
**Requirements:** None (grounds operationalization and release gates for GW-01 through DIST-03, and lays foundation for v2 requirements ID-01, PKI-01, K8S-01)  
**Success Criteria** (what must be TRUE):
  1. Kubernetes reference manifests deploy 3 gateway replicas with default-deny `NetworkPolicies`, persistent volume claims for WAL spools, non-root security contexts, and PodDisruptionBudgets.
  2. Documented credential and key rotation drills demonstrate zero-downtime rotation for JWT issuer keys, snapshot signing keys, and mTLS CAs with defined overlap windows.
  3. Disaster recovery runbook verified through isolated restore drill: PostgreSQL point-in-time recovery and Redis quarantine state reconstruction before gateway readiness activation.
  4. Comprehensive operational runbooks cover incident response, quarantine procedures, spool saturation recovery, control-plane partition recovery, and SLO validation checklists.

Plans:
- [ ] 06-01: Kubernetes production reference manifests with default-deny NetworkPolicies and non-root security contexts
- [ ] 06-02: Security audit review, negative fuzzing verification, and credential rotation drill runbooks
- [ ] 06-03: PostgreSQL/Redis disaster recovery drill, backup restore verification, and production SLO validation

---

## Progress

**Execution Order:**
Phases execute in numeric order: 0 → 1 → 2 → 3 → 4 → 5 → 6

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 0. Architecture Contracts, Schemas & Threat Model | 3/3 | Complete   | 2026-10-06 |
| 1. MVP Secure Vertical Slice | 0/4 | Not started | - |
| 2. Workload Identity & Enforced Bypass Prevention | 0/4 | Not started | - |
| 3. Control Plane, Snapshot Streaming & Durable State | 0/5 | Not started | - |
| 4. Operator Experience & Telemetry | 0/3 | Not started | - |
| 5. Distributed Resilience, Chaos & Benchmark Evidence | 0/3 | Not started | - |
| 6. Production Hardening & Operational Runbooks | 0/3 | Not started | - |
