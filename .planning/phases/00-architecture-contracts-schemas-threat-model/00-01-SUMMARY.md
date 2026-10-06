---
phase: 00-architecture-contracts-schemas-threat-model
plan: 01
type: architecture
status: completed
tasks_completed: 2
tasks_total: 2
commits:
  - 8e6379c: docs(00-01): author ADR-0001 through ADR-0006
  - cb9dfe1: docs(00-01): author Zero-Trust Threat Model and invariant verification matrix
files_created:
  - docs/adr/0001-reverse-proxy-architecture.md
  - docs/adr/0002-in-memory-opa-embedding.md
  - docs/adr/0003-dual-listener-identity-separation.md
  - docs/adr/0004-signed-freshness-leases-and-snapshots.md
  - docs/adr/0005-redis-fail-closed-semantics.md
  - docs/adr/0006-pre-forward-wal-audit-spool.md
  - docs/threat-model/threat-model.md
---

# Plan 00-01 Summary: Architecture Decision Records & Zero-Trust Threat Model

## Deliverables Summary

Plan 00-01 established the authoritative architectural governance and security foundation for Aegis by authoring six Architectural Decision Records (ADRs) in standard MADR format and a comprehensive Zero-Trust Threat Model with STRIDE analysis and invariant verification matrix.

### 1. Architectural Decision Records (ADRs 0001–0006)
- **ADR-0001: Go Standard Library Reverse Proxy with Strict Path Validation and Rewrite Hook**
  - Specifies Go 1.20+ `httputil.ReverseProxy` with `Rewrite` hook, cleanly isolating inbound `pr.In` from outbound `pr.Out`.
  - Enforces strict zero-repair path rejection (HTTP 400 Bad Request on dot segments `..`, encoded separators `%2f`/`%5c`, duplicate slashes `//`, and NUL bytes `%00`).
  - Mandates unconditional stripping of client-supplied `X-Aegis-*` and forwarding headers upon ingress arrival.
  - Maps to Invariants 6, 7, 8.
- **ADR-0002: Embedded In-Memory OPA Engine on Request Hot Path**
  - Embeds `github.com/open-policy-agent/opa/v1/rego` into the Go gateway process with precompiled queries (`rego.PrepareForEval`).
  - Achieves <0.2ms typical evaluation latency (well within the <2ms SLA) with zero network calls on the hot path.
  - Utilizes atomic pointer swapping (`sync/atomic.Pointer`) for lock-free snapshot transitions.
  - Maps to Invariants 1, 7.
- **ADR-0003: Physical Dual-Listener Identity Separation & Backend Assertions**
  - Physically separates user ingress (`:8080`/`:8443` for Bearer JWT) from workload ingress (`:9443` with mandatory mTLS client cert and SPIFFE URI SANs).
  - Mints short-lived (<=15s) Ed25519-signed assertion JWTs (`X-Aegis-Assertion`) on gateway dispatch to private backends.
  - Backends verify gateway mTLS identity and assertion claims, eliminating direct network bypass.
  - Maps to Invariants 2, 3, 5, 12.
- **ADR-0004: Monotonic Signed Snapshots with 10-Second Freshness Leases**
  - Configures bidirectional gRPC streaming for snapshot distribution, requiring Ed25519 payload signatures and strictly increasing integer versions (`version > current_version`).
  - Distributes signed freshness leases every 10 seconds; enforces a 60-second fail-closed boundary where gateways drop readiness and return HTTP 503 on protected routes during prolonged partitions.
  - Maps to Invariants 4, 9.
- **ADR-0005: Redis Atomic Token Bucket Rate Limiting and Fail-Closed Revocation Semantics**
  - Employs Redis for atomic token-bucket rate limiting (GCRA via Lua) and instant JTI revocation/principal quarantine (<5s convergence).
  - Enforces a strict 200ms context deadline; fails closed with HTTP 503 Service Unavailable on timeout or Redis outage with zero permissive fallback.
  - Maps to Invariant 1.
- **ADR-0006: Pre-Forward Append-Only Disk WAL Spool with Asynchronous Database Worker**
  - Requires pre-forward disk WAL append with synchronous `os.File.Sync()` (`fsync`) before upstream network dispatch to eliminate non-repudiation vulnerabilities.
  - Decouples gateway request latency from PostgreSQL write latency; an async worker delivers batches to PostgreSQL.
  - Halts admissions with HTTP 503 if the spool volume reaches 90% disk capacity, preventing data loss.
  - Maps to Invariants 10, 11.

### 2. Zero-Trust Threat Model & Invariant Verification Matrix
- Defines system context with Policy Enforcement Point (PEP), Policy Decision Point (PDP), and Policy Administration Point (PAP).
- Formally specifies 7 Trust Boundaries (TB-1 through TB-7) and 4 threat actors (External Unauthenticated, Authenticated Normal User, Compromised Peer Workload, Malicious Network Adversary).
- Includes exhaustive STRIDE threat analysis addressing Spoofing, Tampering, Repudiation, Information Disclosure, Denial of Service, and Elevation of Privilege across all boundaries.
- Establishes the Complete Invariant Verification Matrix mapping all 12 non-negotiable security invariants from `spec.md` §3 to formal definitions, enforcement mechanisms, failure semantics, and automated test commands.

## Verification Evidence
All automated verification commands passed:
- `test -f docs/adr/0001-reverse-proxy-architecture.md ... && grep -q "Invariant" docs/adr/0006-pre-forward-wal-audit-spool.md` (Exit code 0)
- `test -f docs/threat-model/threat-model.md && grep -q "TB-1" ... && grep -q "Invariant 12" ...` (Exit code 0)

## Self-Check: PASSED
- [x] All 6 ADRs exist in `docs/adr/` in standard MADR format with decision drivers, options, and invariant mappings
- [x] Threat model defines 7 trust boundaries, 4 attacker profiles, STRIDE table, and complete 12-invariant verification matrix
- [x] All tasks committed atomically to git
