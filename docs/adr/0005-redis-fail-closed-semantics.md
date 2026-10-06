# ADR-0005: Redis Atomic Token Bucket Rate Limiting and Fail-Closed Revocation Semantics

## Status
Accepted

## Context and Problem Statement
In zero-trust architecture, authenticating a valid cryptographic signature on an ingress JWT or mTLS certificate is necessary but insufficient. Operational incidents require the ability to:
1. **Instantly Revoke Tokens**: Invalidate individual JWT tokens by token ID (`jti`) before expiration when a user logs out or credentials are leaked.
2. **Quarantine Principals**: Immediately block compromised users or rogue workloads across the entire gateway grid within seconds (<5s convergence requirement).
3. **Atomic Rate Limiting**: Enforce shared distributed rate limits (token bucket / GCRA) per principal and per route to mitigate brute-force attacks and resource exhaustion.

Because gateway data planes are distributed across multiple stateless replicas, revocation and rate-limit counters require a shared, low-latency state store. Traditional architectures often treat the cache as an advisory layer: if the cache fails, requests are allowed through (fail-open). However, in a zero-trust system, failing open during a cache outage permits quarantined malicious actors to access sensitive microservices, directly violating default-deny security principles.

Aegis requires high-throughput shared state with strict latency bounds (200ms context deadlines) and non-negotiable fail-closed semantics.

## Decision Drivers
1. **Sub-5-Second Quarantine Convergence**: Principal quarantine and JTI revocation updates must propagate to all gateway replicas and block requests in <5 seconds.
2. **Fail-Closed Zero-Trust Semantics (Invariant 1)**: If the shared revocation store is unavailable or uncontactable, the gateway must never produce implicit authorization.
3. **Strict Latency Budget & Deadline Bounds**: Redis lookups must complete within milliseconds; dependency calls must be bounded by a rigid 200ms timeout context to protect gateway request budgets.
4. **Atomic Rate Limiting**: Counter increments and token bucket refills must execute atomically without race conditions across concurrent replicas.

## Considered Options
* **Option A**: Redis cluster/instance utilizing atomic Lua scripts (GCRA / `redis_rate`) and SET/EXPIRE keys with a strict 200ms timeout context and fail-closed **HTTP 503 Service Unavailable** on error or timeout (*Chosen*)
* **Option B**: Fail-open Redis integration: if Redis is unreachable, log a warning and proceed with authorization
* **Option C**: Synchronous PostgreSQL database queries for token revocation and rate limits on every ingress request
* **Option D**: Purely local in-memory replica caches synchronized via periodic broadcast gossip

## Decision Outcome
Chosen option: **Option A — Redis with 200ms timeout and fail-closed HTTP 503 semantics**.

### Rationale and Architectural Implementation
1. **Redis Key Schema & Atomic Operations**:
   - **Revoked JTIs**: Keyed as `revocation:jti:<jti_uuid>` with TTL set to the token's remaining validity duration (`exp - now + clock_skew`). Gateway checks `EXISTS revocation:jti:<jti>` via pipeline.
   - **Quarantined Principals**: Keyed as `quarantine:principal:<principal_id>` containing metadata (reason, quarantined_at, created_by). Lookups execute via `EXISTS quarantine:principal:<id>`.
   - **Rate Limiting**: Uses the Generic Cell Rate Algorithm (GCRA) via `go-redis/redis_rate/v10` executing a single-roundtrip atomic Lua script (`EVALSHA`) to increment tokens and calculate `Retry-After` headers.
2. **Strict 200ms Dependency Deadline**:
   - Every Redis call on the gateway request hot path is executed with a dedicated Go context with timeout:
     ```go
     ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
     defer cancel()
     ```
   - If Redis responds within 200ms, the check proceeds. If the call exceeds 200ms, context deadline exceeded is triggered immediately.
3. **Fail-Closed Semantics (No Permissive Fallback)**:
   - If Redis returns a connection error, cluster error, or context deadline exceeded:
     - The gateway **aborts request processing immediately**.
     - An audit log event is recorded with reason `DEPENDENCY_OUTAGE_REDIS`.
     - The client receives **HTTP 503 Service Unavailable** with RFC 7807 problem details:
       ```json
       {
         "type": "https://aegis.local/errors/dependency-unavailable",
         "title": "Service Unavailable",
         "status": 503,
         "detail": "Authorization dependency check unavailable; failing closed."
       }
       ```
   - Under no circumstances does the gateway fall back to an allow decision when revocation status cannot be verified.

### Pros and Cons of the Options

#### Option A: Redis with 200ms Deadline and Fail-Closed 503 (Chosen)
* **Good**: Guarantees zero compromise during security incidents: revoked tokens and quarantined principals can never sneak through during cache failures.
* **Good**: Redis in-memory lookups typically complete in 0.2–0.5ms, well within the gateway's latency budget.
* **Good**: Atomic Lua GCRA scripts prevent race conditions and lock contention.
* **Good**: Rigid 200ms deadline prevents slow Redis connections from exhausting gateway worker pools.
* **Bad**: Redis becomes a hard availability dependency for serving protected traffic. High availability (Redis Sentinel or Cluster) is required in production.

#### Option B: Fail-Open Redis Fallback
* **Good**: Ingress requests continue succeeding even during Redis maintenance or network drops.
* **Bad**: Catastrophic security flaw: an attacker possessing a revoked credential could deliberately DDoS the Redis instance to trigger fail-open mode and achieve unauthorized access.
* **Bad**: Explicitly violates `spec.md` §3 Invariant 1 and NIST SP 800-207.

#### Option C: Synchronous PostgreSQL Queries on Request Path
* **Good**: Durable persistence and immediate consistency without a separate cache component.
* **Bad**: Incurs 10–50ms database query latency per request, violating the <20ms p99 gateway SLA.
* **Bad**: Saturates database connection pools under ingress surges and makes the primary database a hard single point of failure on the hot path.

#### Option D: In-Memory Gossip Across Replicas
* **Good**: Zero external infrastructure dependency.
* **Bad**: Eventual consistency gossip cannot guarantee sub-5-second quarantine convergence across large replica grids.
* **Bad**: Complex split-brain failure modes and high memory usage per replica.

## Invariant Mapping
This decision directly enforces the following non-negotiable security invariants from `spec.md` §3:
* **Invariant 1 (Default deny and fail closed)**: Invalid credentials, missing revocation state, or dependency failures never produce implicit authorization. Outages fail closed with HTTP 503.
