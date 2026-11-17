# Roadmap: Aegis — Distributed Zero-Trust Access Gateway

## Milestones

- ✅ **v1.0 Aegis Distributed Zero-Trust Access Gateway** — Phases 0–13 (shipped 2026-11-18) — [full roadmap](milestones/v1.0-ROADMAP.md), [requirements](milestones/v1.0-REQUIREMENTS.md), [audit](milestones/v1.0-MILESTONE-AUDIT.md)
- 📋 **v2** — not yet defined (`/gsd:new-milestone`)

## Phases

<details>
<summary>✅ v1.0 Aegis Distributed Zero-Trust Access Gateway (Phases 0–13) — SHIPPED 2026-11-18</summary>

- [x] **Phase 0: Architecture Contracts, Schemas & Threat Model** - Authoritative ADRs, OpenAPI/Protobuf contracts, Rego input schemas, and zero-trust threat models (completed 2026-10-06)
- [x] **Phase 1: MVP Secure Vertical Slice** - Go gateway reverse proxy, local demo JWT issuer, embedded OPA engine, 3 private microservices, and Docker Compose MVP (completed 2026-10-06)
- [x] **Phase 2: Workload Identity & Enforced Bypass Prevention** - Dedicated workload mTLS listener (:9443), SPIFFE URI SANs, backend mTLS, signed assertion JWTs, and direct bypass elimination (completed 2026-10-06)
- [x] **Phase 3: Control Plane, Snapshot Streaming & Durable State** - PostgreSQL persistence, signed Ed25519 snapshots, gRPC streaming with 10s freshness leases, Redis rate limits/revocation, and local disk WAL spool with async worker
- [x] **Phase 4: Operator Experience & Telemetry** - React/TypeScript dashboard, Monaco Rego editor, dry-run policy simulator, replica convergence tracking, /control/v1 REST APIs, and Prometheus metrics
- [x] **Phase 5: Distributed Resilience, Chaos & Benchmark Evidence** - 3 gateway replicas behind load balancer, 30s graceful drain, gRPC reconnect jitter, chaos fault injection tests, and reproducible k6 benchmarks
- [x] **Phase 6: Production Hardening & Operational Runbooks** - Kubernetes reference manifests, credential rotation drills, disaster recovery drills, security audit review, and SLO validation runbooks
- [x] **Phase 7: Close gaps B1, B3, B4 (compose alignment)** - v1.0 gap closure: all three compose profiles run the current binaries; partial contribution to REV-03 and AUD-03, which remain unsatisfied (completed 2026-10-09)
- [x] **Phase 8: Close gap B7 (denials and completions to the audit spool)** - v1.0 gap closure: gateway writes denial and completion records to the durable spool; AUD-04 and AUD-03 closed on MVP-profile live evidence, with human verification items open (completed 2026-10-09)
- [x] **Phase 9: Close gaps B9, B10 (jti revocation and SPIFFE quarantine keys)** - v1.0 gap closure: issuer emits jti so JTI revocation matches; workload quarantine keys decoded and POST /quarantine fixed (REV-03, AUTH-04, OPS-01) (completed 2026-10-10)
- [x] **Phase 10: Close gaps B5, B6 (dashboard and control-plane flows)** - v1.0 gap closure (completed 2026-10-11)
- [x] **Phase 11: Close gaps B8, B2, CR-02 (key rotation, DR reconstruct, K8s manifests, gRPC mTLS)** - v1.0 gap closure (completed 2026-10-12)
- [x] **Phase 12: Close gaps N1, DIST-01, DIST-02** - K8s netpol for the audit sidecar, two-phase drain, metric callers and latency histogram (completed 2026-11-17)
- [x] **Phase 13: Close gap N2** - K8s Postgres credentials from the shared Secret, audit sidecar drain and pruning, runbooks name real objects (completed 2026-11-18)

</details>

## Progress

| Milestone | Phases | Plans | Status | Completed |
|-----------|--------|-------|--------|-----------|
| v1.0 | 0–13 (14) | 85/85 | Shipped | 2026-11-18 |
