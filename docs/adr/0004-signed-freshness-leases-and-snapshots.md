# ADR-0004: Monotonic Signed Snapshots with 10-Second Freshness Leases

## Status
Accepted

## Context and Problem Statement
In a distributed access gateway deployment, multiple gateway data plane replicas evaluate authorization decisions based on configuration state published by the control plane. This state includes route catalog definitions, upstream cluster endpoints, identity role mappings, rate limit policies, and declarative OPA Rego policy modules.

In distributed systems, distributing configuration poses severe consistency and security challenges:
- **Rollback & Replay Attacks**: An attacker or network partition could replay an older, vulnerable configuration snapshot (e.g. one granting access to a revoked credential or decommissioned service).
- **Split-Brain & Silent Stale Operation**: If a gateway replica becomes partitioned from the control plane, allowing it to serve traffic indefinitely on stale policies can lead to critical authorization bypasses (e.g. an operator quarantines a principal, but the partitioned gateway never learns of it and continues allowing requests).
- **Non-Atomic Snapshot Activation**: If route definitions and Rego policies update out-of-sync, requests may evaluate a new policy against an old routing table, resulting in erroneous decisions.

Aegis requires a distribution protocol that guarantees atomic activation, prevents rollback, bounds the operational lifetime of configuration state, and fails closed during prolonged control plane partitions.

## Decision Drivers
1. **Atomic Configuration Activation (Invariant 9)**: Routing, identity catalogs, and Rego policies must activate simultaneously in a single atomic swap.
2. **Rollback Immunity**: Monotonic versioning must prevent older configuration snapshots from ever being reactivated.
3. **Bounded Stale Window & Fail-Closed Partition Semantics**: Gateway replicas must maintain cryptographic proof of state freshness and halt authorization if partitioned beyond a defined safety threshold.
4. **Transport Cryptography**: All control plane to data plane communications must enforce mutual TLS and payload signature verification.
5. **Low Telemetry & Network Overhead**: Multiplexed streaming with heartbeat leases rather than expensive polling.

## Considered Options
* **Option A**: Bidirectional gRPC streaming (`SnapshotDistributionService`) delivering Ed25519-signed snapshots with strictly monotonic integer versions and 10-second signed freshness leases (*Chosen*)
* **Option B**: Replicas polling PostgreSQL directly on a periodic timer (e.g. every 5 seconds)
* **Option C**: Long-polling REST endpoints with indefinite local caching (fail-open on partition)

## Decision Outcome
Chosen option: **Option A — Bidirectional gRPC streaming with Ed25519-signed monotonic snapshots and 10s freshness leases**.

### Rationale and Architectural Implementation
1. **gRPC Snapshot Distribution Service**:
   - The control plane exposes `SnapshotDistributionService.StreamSnapshots` over mutual TLS.
   - Gateway replicas establish a persistent HTTP/2 gRPC stream on startup with randomized exponential reconnect jitter (`base: 1s`, `max: 30s`, `factor: 1.5`, `jitter: 0.2`).
2. **Cryptographic Snapshot Envelope**:
   - Snapshots are distributed in a strongly typed protobuf message:
     - `snapshot_version`: Monotonic integer strictly greater than the preceding version (`version > current_version`).
     - `payload`: Serialized protobuf bytes containing routes, clusters, policies, and role definitions.
     - `payload_sha256`: SHA-256 digest of the raw payload bytes.
     - `signature`: Ed25519 digital signature over the SHA-256 digest, signed by the control plane private signing key.
     - `signing_key_id`: Key identifier verified against pinned trusted public keys in the gateway.
   - Gateways reject any snapshot with `version <= current_version` or an invalid digital signature.
3. **Atomic Activation via `sync/atomic.Pointer`**:
   - The gateway parses and validates the snapshot out-of-band: compiling Rego queries (`rego.PrepareForEval`), verifying route paths, and constructing routing tables.
   - Once compilation and validation succeed, the entire state is atomically activated using `sync/atomic.Pointer[SnapshotState].Store()`. No requests see intermediate or partial state.
   - The gateway emits a `SnapshotAck` message over the gRPC stream acknowledging activation (`ACK_STATUS_ACTIVATED`).
4. **10-Second Freshness Leases & 60-Second Fail-Closed Boundary**:
   - To prevent partitioned replicas from serving stale policy indefinitely, the control plane issues signed `FreshnessLease` messages over the gRPC stream every **10 seconds**.
   - Each lease specifies: `lease_id`, `snapshot_version`, `issued_at`, `valid_until` (15s validity), and `lease_signature`.
   - **Safety Boundary**:
     - Normal operation: Lease renewed every 10 seconds.
     - Transient partition (15s–60s): Gateway continues serving traffic with active snapshot while attempting gRPC reconnect with jitter, logging degraded warnings.
     - Hard partition (>60s without valid lease renewal): The gateway replica **drops its readiness health check** (`/healthz/ready`) and **fails closed**: all requests to protected upstream routes return **HTTP 503 Service Unavailable**. No unauthenticated or stale authorization is permitted.

### Pros and Cons of the Options

#### Option A: gRPC Streaming + Ed25519 Monotonic Leases (Chosen)
* **Good**: Sub-second snapshot convergence (<5 seconds across all grid replicas).
* **Good**: Cryptographic signature prevents configuration tampering even in transit.
* **Good**: Monotonic integer check makes rollback attacks structurally impossible.
* **Good**: 10-second freshness leases with 60-second fail-closed boundary rigorously prevents stale operation during partitions.
* **Bad**: Requires maintaining persistent gRPC connections and implementing reconnect backoff logic with jitter.

#### Option B: Gateway Replicas Polling PostgreSQL Directly
* **Good**: Simpler control plane logic.
* **Bad**: Introduces direct database network connections from data plane gateway replicas, violating least-privilege isolation.
* **Bad**: Gateway grid scaling creates connection pool exhaustion on the central database.
* **Bad**: Incurs polling delay latency and lacks bidirectional acknowledgment telemetry.

#### Option C: Long-Polling REST with Indefinite Local Cache
* **Good**: Standard HTTP/1.1 REST endpoints.
* **Bad**: If partitioned, replicas run indefinitely on stale policies, creating a major security vulnerability where revoked privileges remain active.
* **Bad**: Violates default-deny and fail-closed zero-trust principles.

## Invariant Mapping
This decision directly enforces the following non-negotiable security invariants from `spec.md` §3:
* **Invariant 4 (TLS verification enabled everywhere)**: Control plane to gateway gRPC streaming enforces strict mutual TLS with pinned CA validation (`InsecureSkipVerify: false`).
* **Invariant 9 (Atomic snapshot activation with monotonic versioning)**: Snapshots activate atomically in memory with zero intermediate states; strictly increasing integer versions prevent rollback and replay attacks.
