# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed
- Operator console redesigned around transit signage. A black sign band holds the sections, the ground is white, and colour appears only for abnormal state, always with a word and a shape (ISA-101 HMI guidance).
- New **Overview** landing page: data freshness, a status board, the live request path drawn as a line diagram, and the latest denials.
- Each service is marked with a coloured route bullet. Gateway status is shown as Good service, Delayed or Suspended, and heartbeat age as a bar measured against the 60-second lease.
- The page now scrolls past the policy editor. The editor only takes the mouse wheel after you click into it.
- `DESIGN.md` and `PRODUCT.md` replace the borrowed third-party style reference.
- The README now includes console screenshots and the policy rule and route tables.

## [1.0.1] — 2026-11-18

### Fixed
- Kubernetes: Postgres reads its credentials from the `aegis-db-credentials` Secret. The audit-worker sidecar now prunes delivered WAL and outlives the gateway drain.
- Runbooks now name only metrics and objects that exist, and tests enforce this.

### Added
- README, contributing guide, security policy, license, CI workflow, and GitHub issue/PR templates.

## [1.0.0] — 2026-10-12

First feature-complete milestone, built in twelve phases.

### Phase 11 — key rotation, DR reconstruction, Kubernetes manifests, authenticated gRPC (2026-10-12)
- Gateways hot-reload JWT and snapshot trusted-key directories (poll + `SIGHUP`); expand/switch/prune rotation with no gateway restart.
- Quarantines and `jti` revocations are durable in Postgres (`principal_quarantines`, `revoked_tokens`), written Postgres-first, restored to Redis before readiness, and reconciled every 30 s; gateways fail closed with `SECURITY_STATE_MISSING` while state is missing.
- Control-plane gRPC requires mTLS; the gateway id is bound to the client certificate (`/gw/<id>`) on every RPC.
- Kubernetes manifests aligned with the binaries: PVC-backed audit spool with an audit-worker sidecar, per-pod client certificates, Secret/ConfigMap bootstrap script.
- Compose smoke drill gained DR, key-rotation, and mTLS sections.

### Phase 10 — dashboard and control-plane flows (2026-10-11)
- Operator console flows wired end to end against `/control/v1`.

### Phase 9 — `jti` revocation and SPIFFE quarantine keys (2026-10-10)
- Token revocation by `jti` and canonical SPIFFE principal quarantine.

### Phases 7–8 — compose alignment and audit coverage (2026-10-09)
- Compose profiles aligned with current binaries; denials and completion events routed to the audit spool.

### Phases 0–6 — foundation (2026-10-06 → 2026-10-07)
- Architecture contracts, threat model, and ADRs.
- MVP vertical slice: reverse proxy, embedded OPA, JWT authentication, bypass prevention.
- Workload identity (SPIFFE mTLS) and signed gateway→backend assertions.
- Control plane with signed snapshot streaming, Redis revocation/rate limiting, and the pre-forward WAL audit spool.
- Operator dashboard and telemetry.
- 3-replica distributed profile, chaos and benchmark evidence.
- Production hardening manifests and SRE runbooks.
