# ADR-0002: Embedded In-Memory OPA Engine on Request Hot Path

## Status
Accepted

## Context and Problem Statement
Aegis requires fine-grained, declarative authorization policies to govern user and workload access across protected microservice routes (`/api/orders`, `/api/payments`, `/api/admin/*`). Policy decisions must account for principal identity, assigned roles, SPIFFE workload IDs, HTTP request methods, target paths, query parameters, and configuration context.

Many production zero-trust systems deploy Open Policy Agent (OPA) as a standalone network daemon sidecar or centralized service. However, issuing a synchronous HTTP/REST or gRPC call to an external OPA daemon on every request adds 2–10ms of network overhead, increases tail latency, consumes network socket descriptors, and creates a critical external dependency on the request hot path. If the external OPA daemon stalls or crashes, gateways face an unacceptable trade-off between failing open (security breach) or failing closed (widespread outage).

Aegis requires declarative, auditable Rego policy evaluation that executes locally within microseconds, maintains zero network dependencies on the hot path, and adheres to a strict <2ms p99 policy evaluation budget.

## Decision Drivers
1. **Low Latency (<2ms p99 Budget)**: In-memory evaluation must execute in sub-millisecond time (<0.2ms typical, <2ms p99) to keep total gateway latency overhead below 20ms.
2. **Zero Synchronous Network Dependencies**: Hot-path authorization must never depend on external OPA daemons, databases, or remote RPCs.
3. **Atomic State Consistency**: Routing table definitions, role catalogs, and compiled Rego policies must share an identical snapshot version and swap atomically.
4. **Declarative Expressiveness & Standard Tooling**: Standard Rego v1 syntax compatible with upstream OPA linters, formatters, and unit test runners (`opa test`).
5. **Deterministic Fail-Closed Behavior**: Any policy syntax error, evaluation error, or missing rule must evaluate to an explicit `allow: false` decision (Invariant 1).

## Considered Options
* **Option A**: Embedded OPA Go SDK (`github.com/open-policy-agent/opa/v1/rego`) with precompiled queries (`rego.PrepareForEval`) and atomic snapshot activation
* **Option B**: Standalone external OPA HTTP daemon (`http://opa:8181/v1/data`) sidecar per gateway container
* **Option C**: Hand-rolled custom Go RBAC evaluation engine using Go structs and map lookups

## Decision Outcome
Chosen option: **Option A — Embedded OPA Go SDK with precompiled queries**.

### Rationale and Architectural Implementation
1. **In-Memory Precompiled Evaluation**: Aegis imports `github.com/open-policy-agent/opa/v1/rego` directly into the Go gateway process. Upon snapshot loading or configuration update, the gateway compiles the Rego module into a prepared query:
   ```go
   pq, err := rego.New(
       rego.Query("data.aegis.authz.decision"),
       rego.Module("authz.rego", snapshot.RegoPolicy),
       rego.Store(inMemoryStore),
   ).PrepareForEval(ctx)
   ```
   Executing `pq.Eval(ctx, rego.EvalInput(input))` executes purely in memory via AST interpretation, completing in ~0.15ms with zero heap allocation churn on the critical path.
2. **Lock-Free Atomic Snapshot Swapping**: The compiled OPA query, route catalog, and identity definitions are bundled into an immutable `SnapshotState` struct referenced by a `sync/atomic.Pointer[SnapshotState]`. Live traffic evaluates against the pointer without mutex lock contention. New snapshots are validated and precompiled out-of-band before an atomic pointer swap.
3. **Strict Typed Schemas**: Ingress request attributes (principal, request, context, resource) are transformed into a strictly typed JSON input document adhering to `policies/schemas/input.schema.json`.
4. **Discrete Reason Codes**: The Rego policy yields a structured decision `{ "allow": bool, "reason_code": string, "snapshot_version": int }`, guaranteeing full explainability for audits.

### Pros and Cons of the Options

#### Option A: Embedded OPA Go SDK (Chosen)
* **Good**: Sub-millisecond evaluation (<0.2ms), easily satisfying the <2ms SLA.
* **Good**: Zero network sockets, zero inter-process communication overhead, and zero sidecar failure modes.
* **Good**: Atomic synchronization between route tables and policy modules under a single versioned snapshot.
* **Good**: Uses standard Rego v1 tooling (`opa check`, `opa test`) for local policy validation and CI verification.
* **Bad**: Increases the Go gateway binary size by ~20MB due to embedded OPA compiler dependencies.

#### Option B: External OPA HTTP Sidecar Daemon
* **Good**: Decouples policy engine binary lifecycle from the gateway binary.
* **Bad**: Introduces 2–10ms network round-trip overhead per request, violating the latency budget.
* **Bad**: Requires TCP connection pooling and socket management on the hot path; sidecar crashes cause immediate gateway 503 errors.
* **Bad**: Risk of race conditions or version skew between gateway route state and external OPA policy state during updates.

#### Option C: Hand-Rolled Custom Go RBAC Engine
* **Good**: Extremely small binary footprint and microsecond evaluation.
* **Bad**: Lacks declarative policy expressiveness; complex role-based, attribute-based, or SPIFFE identity rules require hardcoded Go logic.
* **Bad**: Cannot use standard policy authoring tools, visual simulators, or decoupled operator updates.
* **Bad**: High risk of subtle authorization logic flaws and edge-case bugs compared to proven OPA semantics.

## Invariant Mapping
This decision directly enforces the following non-negotiable security invariants from `spec.md` §3:
* **Invariant 1 (Default deny across all routes)**: Embedded policy evaluates with `default allow := false` and `default reason_code := "DENIED_DEFAULT"`. Unmapped routes or policy evaluation errors fail closed.
* **Invariant 7 (Identical validated path/method & snapshot version)**: Both the routing dispatcher and the embedded OPA evaluator evaluate against the identical canonical path bytes, HTTP verb, and immutable snapshot version pointer.
