# ADR-0006: Pre-Forward Append-Only Disk WAL Spool with Asynchronous Database Worker

## Status
Accepted

## Context and Problem Statement
In high-assurance zero-trust architectures, non-repudiation is a fundamental requirement: every permitted request dispatched to a protected backend service must leave an immutable, durable audit record. If an attacker issues a state-mutating transaction (e.g. `POST /api/payments`) and the gateway or server abruptly crashes before writing the audit log, the payment executes on the backend without an audit trail, enabling repudiation attacks and compliance failures.

Conversely, writing audit records synchronously to a central relational database (PostgreSQL) on the request hot path introduces significant latency penalties (10–50ms per request), exhausts database connection pools during ingress traffic spikes, and causes widespread gateway outages whenever the central database undergoes maintenance or network hiccups.

Aegis requires an audit architecture that guarantees strict durability before effect (Invariant 10), decouples hot-path proxy latency from database performance, survives database outages without losing audit events, and halts admission safely before disk exhaustion.

## Decision Drivers
1. **Pre-Forward Durability (Invariant 10)**: Every permitted request must be durably persisted to non-volatile storage with `fsync()` before the gateway issues the upstream network dispatch to the backend service.
2. **Database Decoupling**: Gateway request latencies must remain independent of PostgreSQL write latency and connection availability.
3. **Database Outage Survival**: The gateway must continue serving traffic during temporary database downtime by buffering durable events locally.
4. **Bounded Disk Management & Saturation Safeguards**: The local spool must be bounded, rotating segments automatically; if local disk reaches 90% capacity, the gateway must halt new request admissions (fail closed with HTTP 503) rather than drop audit events silently or crash the host operating system.
5. **Zero External Broker Footprint in v1**: Avoid heavy distributed streaming infrastructure (Kafka/RabbitMQ) for local deployments while providing clean upgrade paths.

## Considered Options
* **Option A**: Local append-only disk Write-Ahead Log (WAL) with `fsync()` before forwarding, drained by an asynchronous background worker delivering batches to PostgreSQL (*Chosen*)
* **Option B**: Synchronous `INSERT` into PostgreSQL inside the HTTP request handler before forwarding
* **Option C**: In-memory Go buffered channel (`chan AuditEvent`) drained by a background database worker
* **Option D**: External distributed message broker (Apache Kafka or RabbitMQ) on the request path

## Decision Outcome
Chosen option: **Option A — Local append-only disk WAL with pre-forward `fsync()` and asynchronous PostgreSQL batch delivery worker**.

### Rationale and Architectural Implementation
1. **Pre-Forward WAL Append and `fsync()`**:
   - When the embedded OPA engine yields an `allow: true` decision, the gateway serializes an `AuditEvent` struct into binary protocol or JSONL with a CRC32 checksum.
   - The gateway appends the record to the active WAL segment file on local disk and immediately executes `os.File.Sync()` (`fsync` syscall).
   - **Crucial Invariant**: The proxy request is dispatched to the upstream backend **only after** `fsync()` returns successfully. If disk write or `fsync` fails, the upstream request is never dispatched, and the client receives **HTTP 500 / 503**.
2. **Segment File Management & Rotation**:
   - WAL files are stored in a dedicated spool directory (e.g. `/var/log/aegis/wal/`).
   - Segments rotate when reaching a size threshold (e.g. 64MB) or after a time window (e.g. 5 minutes): `wal-<timestamp>-<seq>.log`.
   - File naming and monotonic offsets prevent data corruption across gateway restarts.
3. **Asynchronous Batch Database Delivery Worker**:
   - A background worker (`AuditWorker`) tails completed and active WAL segments.
   - Events are buffered into batches (e.g. 500 events or 500ms max latency) and written to PostgreSQL using high-throughput `pgx.Batch` or `COPY aegis_audit_events FROM ...`.
   - The worker records its persisted offset watermark in a durable cursor file (`wal.cursor`). Once a segment is completely acknowledged in PostgreSQL, the segment file is unlinked or archived.
   - The delivery model is **at-least-once**: unique event UUIDs (`event_id`) and database primary keys prevent duplicate rows upon replay after crashes.
4. **90% Spool Saturation Safety Gate**:
   - The gateway actively monitors free disk space on the spool volume.
   - If disk usage reaches **90% capacity** (due to a prolonged database outage or worker stall):
     - The gateway activates the spool saturation circuit breaker.
     - New request admissions are immediately rejected with **HTTP 503 Service Unavailable** (`detail: "Audit spool saturated; halting admission"`).
     - Under no circumstances does the gateway drop audit records or proceed with unlogged requests.

### Pros and Cons of the Options

#### Option A: Local Disk WAL + `fsync()` + Async Worker (Chosen)
* **Good**: Guarantees pre-forward durability (Invariant 10) with zero loss across process crashes or database outages.
* **Good**: Disk sequential append with `fsync()` executes in 0.5–1.5ms on modern NVMe/SSD, keeping proxy overhead minimal.
* **Good**: Completely isolates gateway traffic from database stalls or downtime.
* **Good**: 90% saturation gate provides deterministic, fail-closed protection against silent data loss.
* **Bad**: Requires local storage volume management and disk monitoring logic.

#### Option B: Synchronous PostgreSQL Insertion on Request Path
* **Good**: Immediate global consistency in PostgreSQL without asynchronous workers.
* **Bad**: Incurs 10–50ms latency overhead on every request.
* **Bad**: If PostgreSQL is down or connection pools saturate, every request through the gateway fails, making the database a hard single point of failure.

#### Option C: In-Memory Channel Directly to Database Worker
* **Good**: Extremely low latency (microsecond memory enqueue).
* **Bad**: Severe vulnerability to non-repudiation: if the gateway process is killed or powers down abruptly, all uncommitted audit events in the in-memory buffer are lost forever, violating Invariant 10.

#### Option D: External Message Broker (Kafka / RabbitMQ)
* **Good**: Highly scalable in massive enterprise multi-cluster environments.
* **Bad**: Massive operational overhead (ZooKeeper/KRaft, broker clusters) before baseline access gateway primitives are proven.
* **Bad**: Synchronous ack to an external broker still adds network hops to the critical path.

## Invariant Mapping
This decision directly enforces the following non-negotiable security invariants from `spec.md` §3:
* **Invariant 10 (Permitted requests require pre-forward durable audit record)**: No request reaches an upstream backend before its audit event is safely persisted to local disk with forced `fsync()`.
* **Invariant 11 (Management access separately authorized & audited)**: All management queries and audit access operate under dedicated authorization and separate role boundaries.
