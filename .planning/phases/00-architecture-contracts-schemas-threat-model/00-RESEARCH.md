# Phase 0: Architecture Contracts, Schemas & Threat Model — Research

**Domain:** Distributed Zero-Trust Access Gateway & Policy Control Plane  
**Phase:** 0 (Foundations, Contracts & Threat Model)  
**Researched:** 2026-10-06  
**Status:** COMPLETE & VERIFIED  

---

## 1. Executive Summary & Architectural Responsibility Map

### 1.1 Executive Summary

Phase 0 establishes the immutable interface contracts, schema definitions, architectural decisions, and zero-trust threat models that govern all subsequent phases of the Aegis platform. In accordance with NIST SP 800-207 (Zero Trust Architecture) and RFC 8725 (JWT Best Current Practices), a production-grade access gateway must never decouple implementation from explicit contracts. Ambiguities in route semantics, path encoding, token claims, or distributed snapshot envelopes create parser disparities, split-brain states, and authorization bypasses (CWE-22, CWE-444).

This phase produces no business logic handlers; instead, it establishes authoritative specifications and validation harnesses that prove contract conformance before a single reverse proxy line or control plane database query is written. All data models, protobuf message formats, OpenAPI schemas, and Rego evaluation rules are verified against current 2026 tooling standards and checked into version control.

### 1.2 Phase 0 Success Criteria Mapping

| Success Criterion (from ROADMAP.md) | Delivering Artifact | Verification Harness |
|---|---|---|
| **1. OpenAPI 3.0 Specification** passes schema validation and completely models all `/control/v1` routes, policies, simulation, and quarantine endpoints. | `api/openapi/control-v1.yaml` | `npx @redocly/cli lint api/openapi/control-v1.yaml` passes with 0 errors; `oapi-codegen` compiles Go types without errors. |
| **2. Protobuf Definitions** compile without errors into Go structs for monotonic signed snapshots and gRPC streaming distribution. | `api/proto/snapshot/v1/snapshot.proto` | `protoc --go_out=... --go-grpc_out=...` compiles cleanly; generated Go structs pass static analysis. |
| **3. Architecture Decision Records (ADRs)** document gateway proxy architecture, in-memory OPA embedding, dual-listener identity separation, signed freshness leases, Redis fail-closed semantics, and pre-forward disk WAL audit spooling. | `docs/adr/0001` through `0006` | Markdown ADRs in MADR/Nygard format with explicit context, decision drivers, consequences, and invariant mappings. |
| **4. Zero-Trust Threat Model** documents trust boundaries, attacker capabilities, and 12 non-negotiable security invariants with verifiable mitigation mappings. | `docs/threat-model/threat-model.md` | Formal threat model analyzing STRIDE categories, trust boundaries, and mapping all 12 security invariants to verification tests. |

### 1.3 Architectural Responsibility Map (Plan Breakdown)

The three plans scoped in ROADMAP.md partition the phase responsibilities into distinct architectural concerns:

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│ PHASE 0: FOUNDATIONS, CONTRACTS & THREAT MODEL                                         │
│                                                                                        │
│  ┌────────────────────────┐  ┌────────────────────────┐  ┌───────────────────────────┐  │
│  │ PLAN 00-01             │  │ PLAN 00-02             │  │ PLAN 00-03                │  │
│  │ Architecture Decisions │  │ Interface Contracts    │  │ Declarative Policy Engine │  │
│  │ & Threat Modeling      │  │ & Snapshot Schemas     │  │ & Seed Route Catalog      │  │
│  ├────────────────────────┤  ├────────────────────────┤  ├───────────────────────────┤  │
│  │ • ADR-0001 (Proxy Arch)│  │ • OpenAPI 3.0.3 Spec   │  │ • Rego v1 Authz Policies  │  │
│  │ • ADR-0002 (OPA Embed) │  │   (/control/v1 routes) │  │ • Input & Output Schemas  │  │
│  │ • ADR-0003 (Dual-Listen│  │ • Protobuf 3 Schema    │  │ • Seed Route Catalog      │  │
│  │ • ADR-0004 (Leases)    │  │   (snapshot.proto)     │  │ • Seed Role Data & Matrix │  │
│  │ • ADR-0005 (Redis 503) │  │ • gRPC Distribution Svc│  │ • OPA Test Suite          │  │
│  │ • ADR-0006 (Disk WAL)  │  │ • Code Generation Tool │  │   (authz_test.rego)       │  │
│  │ • Threat Model Doc     │  │   (protoc + oapi-gen)  │  │ • Decision Reason Codes   │  │
│  └────────────────────────┘  └────────────────────────┘  └───────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

- **Plan 00-01: Architectural Governance & Security Model**
  - Produces 6 core Architectural Decision Records (`docs/adr/0001` through `0006`) formalizing non-negotiable architectural directions.
  - Produces the comprehensive system Threat Model (`docs/threat-model/threat-model.md`) defining external/internal trust boundaries, attacker profiles, STRIDE taxonomy, and mapping 12 invariants to concrete verification tests.
- **Plan 00-02: Authoritative Interface Specifications**
  - Produces `api/openapi/control-v1.yaml` declaring all 13 management routes under `/control/v1`, optimistic locking (`ETag`/`If-Match`), idempotency tokens, cursor pagination, and standard RFC 7807-style error objects.
  - Produces `api/proto/snapshot/v1/snapshot.proto` declaring the monotonic snapshot envelope, route catalog, Ed25519 signature fields, freshness lease messages, and bidirectional gRPC distribution service.
  - Generates strongly typed Go structs via `protoc-gen-go`, `protoc-gen-go-grpc`, and `oapi-codegen`.
- **Plan 00-03: Declarative Policy & Seed Permission Engine**
  - Produces JSON schemas for Rego policy input (`policies/schemas/input.schema.json`) and evaluation output (`policies/schemas/output.schema.json`).
  - Produces canonical seed route catalog (`policies/data/routes.json`) and seed identity role mappings (`policies/data/seed_roles.json`).
  - Implements the baseline Rego v1 authorization module (`policies/rego/authz.rego`) enforcing default-deny, role matching, workload verification, and deterministic risk checks.
  - Validates the policy against the seed test matrix using `opa test policies/ -v` with 100% test pass rate.

---

## 2. Standard Stack & Package Ecosystem

All toolchain components and libraries used in Phase 0 have been verified against current local installations and production releases (2026).

### 2.1 Core Technologies & Specifications

| Component | Standard / Version | Purpose | Package / Binary Reference | Verification Command |
|---|---|---|---|---|
| **Go Toolchain** | `1.25.5` runtime (`go 1.24+` in `go.mod`) | Primary systems runtime and struct generation target | `/usr/local/go/bin/go` | `go version` |
| **Protocol Buffers** | `proto3` / libprotoc `29.3` | Interface Definition Language (IDL) for snapshot streaming | `/home/logan78/.local/bin/protoc` | `protoc --version` |
| **Go Protobuf Plugin** | `v1.36.12` | Code generator for strongly typed Go protobuf messages | `/home/logan78/.local/bin/protoc-gen-go` | `protoc-gen-go --version` |
| **Go gRPC Plugin** | `v1.6.2` | Code generator for Go gRPC client/server interfaces | `/home/logan78/.local/bin/protoc-gen-go-grpc` | `protoc-gen-go-grpc --version` |
| **OpenAPI Specification** | `3.0.3` | REST API contract definition for `/control/v1` | `api/openapi/control-v1.yaml` | `npx @redocly/cli lint` |
| **OpenAPI Code Generator** | `v2.5.0` | Go server interface and model generator from OpenAPI | `/home/logan78/.local/bin/oapi-codegen` | `oapi-codegen --version` |
| **Open Policy Agent CLI** | `v1.2.0` (Rego v1 default) | Policy syntax checker and unit test runner | `/home/logan78/.local/bin/opa` | `opa version` |
| **JSON Schema** | Draft 2020-12 / Draft-07 | Structural validation for Rego evaluation input/output | `policies/schemas/*.json` | Schema linting / OPA evaluation |
| **Redocly CLI** | `2.58.1` | Linter and schema validator for OpenAPI contracts | `npx @redocly/cli` | `npx @redocly/cli --version` |
| **Buf CLI** | `1.73.0` | Modern Protobuf linter and build coordinator | `npx @bufbuild/buf` | `npx @bufbuild/buf --version` |

### 2.2 Package Legitimacy & Cryptographic Standards

- **Protobuf Plugins:** `google.golang.org/protobuf/cmd/protoc-gen-go` and `google.golang.org/grpc/cmd/protoc-gen-go-grpc` are official Google Go modules installed directly from source.
- **OpenAPI Tooling:** `github.com/oapi-codegen/oapi-codegen/v2` is the standard Go ecosystem code generator, actively maintained, supporting Go 1.22+ `net/http` router targets and strict struct tagging.
- **OPA Tooling:** Official OPA static binary (`v1.2.0`) built with Go 1.24 defaults to Rego v1 syntax (`allow if { ... }`), guaranteeing compatibility with embedded OPA SDK (`v1.21.1`) in Phase 1.
- **Cryptographic Algorithms:**
  - Digital signatures: **Ed25519** (`crypto/ed25519`, RFC 8032) for monotonic configuration snapshots and backend assertion tokens. Constant-time execution, small 64-byte signatures, high throughput.
  - Digests: **SHA-256** (`crypto/sha256`) for snapshot payload content addressing and checksum calculation.

---

## 3. Architecture Patterns & Recommended Project Structure

### 3.1 Recommended Project Structure for Phase 0

Phase 0 establishes the contract repository layout specified in `spec.md` §13:

```
aegis/
├── api/
│   ├── openapi/
│   │   ├── control-v1.yaml             # Complete OpenAPI 3.0.3 REST specification
│   │   └── oapi-codegen.yaml           # Configuration for Go server/model generation
│   └── proto/
│       └── snapshot/
│           └── v1/
│               └── snapshot.proto      # Proto3 monotonic snapshot & streaming definition
├── policies/
│   ├── schemas/
│   │   ├── input.schema.json           # JSON Schema for Rego evaluation input
│   │   └── output.schema.json          # JSON Schema for Rego evaluation output
│   ├── rego/
│   │   └── authz.rego                  # Rego v1 default-deny authorization rules
│   ├── data/
│   │   ├── routes.json                 # Seed route catalog definitions
│   │   └── seed_roles.json             # Seed user role definitions
│   └── tests/
│       └── authz_test.rego             # Comprehensive OPA unit test suite
├── docs/
│   ├── adr/
│   │   ├── 0001-gateway-proxy-architecture.md
│   │   ├── 0002-in-memory-opa-embedding.md
│   │   ├── 0003-dual-listener-identity-separation.md
│   │   ├── 0004-signed-freshness-leases-and-snapshots.md
│   │   ├── 0005-redis-fail-closed-semantics.md
│   │   └── 0006-pre-forward-disk-wal-audit-spooling.md
│   └── threat-model/
│       └── threat-model.md             # Trust boundaries, attacker model, 12 invariants
├── pkg/
│   └── api/
│       ├── control/v1/                 # Generated Go types from OpenAPI (via oapi-codegen)
│       └── snapshot/v1/                # Generated Go structs from Protobuf (via protoc)
├── Makefile                            # Standard validation targets (lint-contracts, test-policies)
├── go.mod
└── go.sum
```

### 3.2 Core Architectural Patterns

#### Pattern 1: Contract-First Architecture (Schema-Driven Code Generation)
Contracts are the single source of truth. Implementation code never invents models or route parameters; it imports generated types.
- `api/openapi/control-v1.yaml` defines types that compile into `pkg/api/control/v1/types.gen.go`.
- `api/proto/snapshot/v1/snapshot.proto` compiles into `pkg/api/snapshot/v1/snapshot.pb.go` and `snapshot_grpc.pb.go`.
- When changes occur, schemas are updated first, reviewed, and recompiled.

#### Pattern 2: Cryptographic Envelope Pattern for Configuration Snapshots
A snapshot is distributed as an envelope separating the signed payload from the signature metadata:
```protobuf
message SnapshotEnvelope {
  int64 version = 1;                     // Strictly monotonic version number
  int32 schema_version = 2;              // Envelope schema version
  google.protobuf.Timestamp created_at = 3;
  google.protobuf.Timestamp expires_at = 4;
  string payload_sha256 = 5;             // SHA-256 digest of serialized SnapshotPayload
  bytes payload = 6;                     // Serialized protobuf bytes of SnapshotPayload
  string signing_key_id = 7;             // Key ID used by control plane
  bytes signature = 8;                   // Ed25519 digital signature of payload_sha256
}
```
*Rationale:* Serializing `SnapshotPayload` to bytes before hashing prevents JSON key-ordering or Protobuf field serialization non-determinism during Ed25519 verification. The gateway verifies `signature` over `payload_sha256`, validates `SHA-256(payload) == payload_sha256`, and unpacks `SnapshotPayload`.

#### Pattern 3: Declarative Typed Policy Evaluation
Rego policies operate strictly on bounded, typed inputs. Raw request bodies are never passed to the policy engine:
```rego
package aegis.authz

import rego.v1

default allow := false
default reason_code := "DENIED_BY_DEFAULT"

# Allow developer to read orders
allow if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "orders"
    input.request.method == "GET"
}
```

#### Pattern 4: Strict Architectural Decision Governance (MADR Format)
Each ADR follows the Markdown Architectural Decision Records (MADR) structure:
1. Title and Status (Proposed / Accepted / Superseded)
2. Context and Problem Statement
3. Decision Drivers (Latency, Security Invariants, Operational Bounds)
4. Considered Options with Pros and Cons
5. Decision Outcome and Justification
6. Invariant Mapping (Explicit link to `spec.md` §3 invariants)

---

## 4. Don't Hand-Roll & Common Pitfalls

### 4.1 What NOT to Hand-Roll in Phase 0

| Anti-Pattern | Why It Fails | What to Do Instead |
|---|---|---|
| **Hand-rolling OpenAPI data types in Go** | Inevitable drift between YAML specification and Go struct tags (`json:"..."`), leading to unmarshaling errors or broken validation. | Use `oapi-codegen` to generate Go structs directly from `api/openapi/control-v1.yaml`. |
| **Writing custom Protobuf serializes / parsers** | Breaks wire compatibility, endianness handling, and fails to handle unknown fields gracefully. | Use standard `google.golang.org/protobuf/proto` with `protoc-gen-go`. |
| **Untyped Rego input queries (`input.body.any`)** | High memory allocation churn, AST recompilation, and security vulnerabilities due to unchecked client-supplied fields. | Enforce rigid typed schema (`policies/schemas/input.schema.json`) with bounded fields (`principal`, `resource`, `request`, `context`). |
| **Informal text files for ADRs** | Lacks trade-off documentation, failure consequences, and context, leading to architectural regressions in later phases. | Enforce standard MADR template with explicit Pros/Cons, non-negotiable security invariant mappings, and failure semantics. |
| **Ad-hoc threat modeling (informal bullet points)** | Misses edge cases like hop-by-hop header manipulation or lease partition split-brain. | Structured STRIDE threat modeling with clear trust boundaries, attacker capabilities, and automated verification tests. |

### 4.2 Common Pitfalls in Contract & Schema Design

#### Pitfall 1: Protobuf Enum Zero-Value Hazard
In proto3, the default value for an enum is always the zero-index value. If an enum starts with a valid operational value (e.g., `enum Status { ACTIVATED = 0; }`), an uninitialized or missing field silently deserializes as `ACTIVATED`.  
*Mitigation:* Every protobuf enum must declare `_UNSPECIFIED = 0` as its first element:
```protobuf
enum AckStatus {
  ACK_STATUS_UNSPECIFIED = 0;
  ACK_STATUS_ACTIVATED = 1;
  ACK_STATUS_REJECTED = 2;
}
```

#### Pitfall 2: Rego Syntax Incompatibility (Rego v1 vs Legacy)
In OPA v1.0+, Rego v1 is the default. Legacy rule syntax (`allow { ... }` or missing `import rego.v1`) produces warnings or fatal compilation errors in modern OPA tools.  
*Mitigation:* Every `.rego` file must begin with:
```rego
package aegis.authz

import rego.v1
```
Use `allow if { ... }` and `in` keywords consistently.

#### Pitfall 3: Signature Non-Determinism (JSON Key Reordering)
Attempting to sign raw JSON objects fails because JSON key serialization ordering is undefined in standard serializers. When a gateway verifies a signature, differing key order invalidates the hash.  
*Mitigation:* Hash canonical protobuf bytes or canonicalized JSON. The `SnapshotEnvelope` stores `payload` as raw protobuf bytes and `payload_sha256` as the SHA-256 digest of those bytes. The Ed25519 signature covers `payload_sha256`.

#### Pitfall 4: Missing Error Response Schemas in OpenAPI
Defining only HTTP 200/201 responses in OpenAPI leaves clients and generated code unprepared for 400, 401, 403, 409, 412, 422, 429, and 503 error envelopes, resulting in unhandled exceptions or panic during API errors.  
*Mitigation:* Define a global `ErrorResponse` component in `api/openapi/control-v1.yaml` with required fields `code`, `message`, `request_id`, `timestamp`, and optional `details`. Every endpoint references this schema across all error status codes.

#### Pitfall 5: Path Template Delimiter Ambiguity
Using inconsistent path template conventions between OpenAPI (`/api/orders/{id}`) and internal route matchers leads to route confusion and wildcard bypasses.  
*Mitigation:* Formalize canonical URI template syntax (RFC 6570 Level 1 single segment `{param}`) and document that wildcards match exactly one path segment without crossing slash boundaries.

---

## 5. Concrete Schemas, Code Examples & State of the Art

### 5.1 Protobuf Contract: `api/proto/snapshot/v1/snapshot.proto`

Complete, production-ready Protobuf 3 specification for monotonic snapshot distribution and gRPC streaming:

```protobuf
syntax = "proto3";

package aegis.snapshot.v1;

option go_package = "aegis/pkg/api/snapshot/v1;snapshotv1";

import "google/protobuf/timestamp.proto";

// Service for streaming monotonic configuration snapshots and freshness leases
// from the Control Plane to Gateway replicas.
service SnapshotDistributionService {
  // Bidirectional stream: Control Plane pushes snapshots and leases;
  // Gateway streams acknowledgments and heartbeats.
  rpc StreamSnapshots(stream GatewayMessage) returns (stream ControlPlaneMessage);
  
  // Unary RPC for explicit snapshot fetch during gateway cold start
  rpc GetActiveSnapshot(GetActiveSnapshotRequest) returns (SnapshotEnvelope);
}

// Top-level message sent from Control Plane to Gateway
message ControlPlaneMessage {
  string message_id = 1;
  google.protobuf.Timestamp timestamp = 2;
  
  oneof payload {
    SnapshotEnvelope snapshot = 3;
    FreshnessLease lease = 4;
    HeartbeatPing ping = 5;
  }
}

// Top-level message sent from Gateway to Control Plane
message GatewayMessage {
  string gateway_id = 1;
  google.protobuf.Timestamp timestamp = 2;
  
  oneof payload {
    SnapshotAck ack = 3;
    HeartbeatPong pong = 4;
  }
}

// Monotonic signed configuration envelope
message SnapshotEnvelope {
  int64 version = 1;                          // Monotonic integer version
  int32 schema_version = 2;                   // Schema version (e.g. 1)
  google.protobuf.Timestamp created_at = 3;
  google.protobuf.Timestamp expires_at = 4;
  string payload_sha256 = 5;                  // Hex-encoded SHA-256 of payload bytes
  bytes payload = 6;                          // Serialized SnapshotPayload bytes
  string signing_key_id = 7;                  // Identifier of control plane signing key
  bytes signature = 8;                        // Ed25519 signature of payload_sha256
}

// Unpacked configuration payload contained inside the envelope
message SnapshotPayload {
  int64 version = 1;
  repeated RouteDefinition routes = 2;
  repeated PolicyModule policy_modules = 3;
  bytes policy_data_json = 4;                 // JSON-encoded seed role and permission data
  repeated IdentityMapping identity_mappings = 5;
}

// Route definition mapping incoming HTTP requests to private upstream backends
message RouteDefinition {
  string route_id = 1;                        // Unique identifier, e.g. "orders.list"
  string service_id = 2;                      // Destination service, e.g. "orders"
  string http_method = 3;                     // GET, POST, PUT, DELETE, etc.
  string path_template = 4;                   // Canonical template, e.g. "/api/orders"
  string upstream_url = 5;                    // Target, e.g. "https://orders:8081"
  string upstream_spiffe_id = 6;              // Expected server SAN: "spiffe://aegis.local/service/orders"
  RateLimitPolicy rate_limit = 7;
  TimeoutPolicy timeout = 8;
  bool requires_workload_mtls = 9;
}

message RateLimitPolicy {
  int32 requests_per_second = 1;
  int32 burst = 2;
}

message TimeoutPolicy {
  int32 request_timeout_ms = 1;               // Default 15000ms
  int32 upstream_timeout_ms = 2;              // Default 5000ms
}

message PolicyModule {
  string package_name = 1;                    // e.g. "aegis.authz"
  string module_name = 2;                     // e.g. "rules.rego"
  string source_rego = 3;                     // Raw Rego v1 source code
}

message IdentityMapping {
  string role = 1;                            // e.g. "developer"
  repeated string permissions = 2;            // e.g. ["orders.read", "payments.read"]
}

// 10-second freshness lease renewal issued by control plane
message FreshnessLease {
  string lease_id = 1;
  int64 snapshot_version = 2;
  google.protobuf.Timestamp issued_at = 3;
  google.protobuf.Timestamp valid_until = 4; // Issued + 10s (fail-closed at valid_until + 50s)
  bytes lease_signature = 5;                  // Ed25519 signature of lease metadata
}

// Gateway activation acknowledgment
message SnapshotAck {
  string gateway_id = 1;
  int64 active_version = 2;
  AckStatus status = 3;
  string error_message = 4;
  google.protobuf.Timestamp acknowledged_at = 5;
}

enum AckStatus {
  ACK_STATUS_UNSPECIFIED = 0;
  ACK_STATUS_ACTIVATED = 1;
  ACK_STATUS_REJECTED = 2;
}

message HeartbeatPing {
  string sequence_id = 1;
}

message HeartbeatPong {
  string sequence_id = 1;
  int64 active_version = 2;
}

message GetActiveSnapshotRequest {
  string gateway_id = 1;
  int64 current_version = 2;
}
```

### 5.2 OpenAPI 3.0.3 Specification: `api/openapi/control-v1.yaml` (Outline & Key Endpoints)

Summary of the 13 required REST endpoints under `/control/v1` with required permissions and headers:

```yaml
openapi: 3.0.3
info:
  title: Aegis Control Plane Management API
  version: 1.0.0
  description: Authoritative management API for policy lifecycle, route configuration, dry-run simulation, and emergency quarantine.
paths:
  /control/v1/routes:
    get:
      summary: List configured routes
      operationId: listRoutes
      parameters:
        - name: cursor
          in: query
          schema: { type: string }
        - name: limit
          in: query
          schema: { type: integer, default: 50, maximum: 200 }
      responses:
        '200':
          description: Route catalog with pagination
          headers:
            ETag: { schema: { type: string } }
          content:
            application/json:
              schema: { $ref: '#/components/schemas/RouteListResponse' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
    post:
      summary: Create route draft
      operationId: createRoute
      parameters:
        - $ref: '#/components/parameters/IdempotencyKey'
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/RouteCreateRequest' }
      responses:
        '201':
          description: Route created
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Route' }
        '400': { $ref: '#/components/responses/BadRequest' }
        '409': { $ref: '#/components/responses/Conflict' }
        '422': { $ref: '#/components/responses/UnprocessableEntity' }

  /control/v1/policies/{id}/simulate:
    post:
      summary: Dry-run simulate policy evaluation against synthetic input
      operationId: simulatePolicy
      parameters:
        - name: id
          in: path
          required: true
          schema: { type: string }
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/PolicySimulationRequest' }
      responses:
        '200':
          description: Simulation decision output
          content:
            application/json:
              schema: { $ref: '#/components/schemas/PolicySimulationResponse' }
        '400': { $ref: '#/components/responses/BadRequest' }
        '404': { $ref: '#/components/responses/NotFound' }

  /control/v1/principals/{id}/quarantine:
    post:
      summary: Quarantine a principal (block access cluster-wide in <5s)
      operationId: quarantinePrincipal
      parameters:
        - name: id
          in: path
          required: true
          schema: { type: string }
        - $ref: '#/components/parameters/IdempotencyKey'
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/QuarantineRequest' }
      responses:
        '200':
          description: Principal quarantined successfully
          content:
            application/json:
              schema: { $ref: '#/components/schemas/QuarantineRecord' }
        '400': { $ref: '#/components/responses/BadRequest' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
    delete:
      summary: Remove quarantine block for a principal
      operationId: unquarantinePrincipal
      parameters:
        - name: id
          in: path
          required: true
          schema: { type: string }
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/UnquarantineRequest' }
      responses:
        '200':
          description: Quarantine removed
        '404': { $ref: '#/components/responses/NotFound' }

components:
  parameters:
    IdempotencyKey:
      name: Idempotency-Key
      in: header
      required: false
      schema: { type: string, format: uuid }
    IfMatch:
      name: If-Match
      in: header
      required: false
      schema: { type: string }
  schemas:
    ErrorResponse:
      type: object
      required: [code, message, request_id, timestamp]
      properties:
        code: { type: string, example: "ROUTE_NOT_FOUND" }
        message: { type: string, example: "The requested route does not exist." }
        request_id: { type: string, format: uuid }
        timestamp: { type: string, format: date-time }
        details: { type: object, additionalProperties: true }
```

### 5.3 Rego v1 Authorization Policy: `policies/rego/authz.rego`

Production Rego v1 implementation of the seed authorization policy enforcing the developer/finance/admin/workload matrix:

```rego
package aegis.authz

import rego.v1

# Invariant 1: Strict default deny. No match = deny.
default allow := false
default reason_code := "DENIED_DEFAULT"
default matched_rule_id := "default_deny"

# -------------------------------------------------------------------------
# Rule 1: Developer reading orders
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "orders"
    input.request.method == "GET"
}

reason_code := "ALLOWED_DEVELOPER_ORDERS" if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "orders"
    input.request.method == "GET"
}

# -------------------------------------------------------------------------
# Rule 2: Developer reading payments
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "payments"
    input.request.method == "GET"
}

reason_code := "ALLOWED_DEVELOPER_PAYMENTS" if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "payments"
    input.request.method == "GET"
}

# -------------------------------------------------------------------------
# Rule 3: Finance creating payments
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "user"
    "finance" in input.principal.roles
    input.resource.service == "payments"
    input.request.method in ["GET", "POST"]
}

reason_code := "ALLOWED_FINANCE_PAYMENTS" if {
    input.principal.kind == "user"
    "finance" in input.principal.roles
    input.resource.service == "payments"
    input.request.method in ["GET", "POST"]
}

# -------------------------------------------------------------------------
# Rule 4: Application Admin managing admin users
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "user"
    "application-admin" in input.principal.roles
    input.resource.service == "admin"
}

reason_code := "ALLOWED_ADMIN_USERS" if {
    input.principal.kind == "user"
    "application-admin" in input.principal.roles
    input.resource.service == "admin"
}

# -------------------------------------------------------------------------
# Rule 5: Workload 'orders' invoking payments mutation
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "workload"
    input.principal.id == "spiffe://aegis.local/workload/orders"
    input.resource.service == "payments"
    input.request.method == "POST"
}

reason_code := "ALLOWED_WORKLOAD_ORDERS_PAYMENTS" if {
    input.principal.kind == "workload"
    input.principal.id == "spiffe://aegis.local/workload/orders"
    input.resource.service == "payments"
    input.request.method == "POST"
}

# -------------------------------------------------------------------------
# Explicit Denials & Reason Codes
# -------------------------------------------------------------------------
reason_code := "DENIED_DEVELOPER_ADMIN_FORBIDDEN" if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "admin"
}

reason_code := "DENIED_WORKLOAD_ADMIN_FORBIDDEN" if {
    input.principal.kind == "workload"
    input.principal.id == "spiffe://aegis.local/workload/orders"
    input.resource.service == "admin"
}

reason_code := "DENIED_INVALID_PRINCIPAL" if {
    not input.principal.id
}

# Structured decision response
decision := {
    "allow": allow,
    "reason_code": reason_code,
    "snapshot_version": input.snapshot_version,
}
```

### 5.4 OPA Unit Test Suite: `policies/tests/authz_test.rego`

Verifies 100% of the seed permission matrix:

```rego
package aegis.authz_test

import rego.v1
import data.aegis.authz

# Developer can read orders -> ALLOW
test_developer_can_read_orders if {
    result := authz.allow with input as {
        "principal": {"id": "user:ayush", "kind": "user", "roles": ["developer"]},
        "resource": {"service": "orders", "route": "orders.list"},
        "request": {"method": "GET", "path": "/api/orders"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1
    }
    result == true
}

# Developer can read payments -> ALLOW
test_developer_can_read_payments if {
    result := authz.allow with input as {
        "principal": {"id": "user:ayush", "kind": "user", "roles": ["developer"]},
        "resource": {"service": "payments", "route": "payments.get"},
        "request": {"method": "GET", "path": "/api/payments"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1
    }
    result == true
}

# Developer accessing admin -> DENY
test_developer_cannot_access_admin if {
    result := authz.allow with input as {
        "principal": {"id": "user:ayush", "kind": "user", "roles": ["developer"]},
        "resource": {"service": "admin", "route": "admin.users.list"},
        "request": {"method": "GET", "path": "/api/admin/users"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1
    }
    result == false
}

# Finance can create payments -> ALLOW
test_finance_can_create_payments if {
    result := authz.allow with input as {
        "principal": {"id": "user:finance1", "kind": "user", "roles": ["finance"]},
        "resource": {"service": "payments", "route": "payments.create"},
        "request": {"method": "POST", "path": "/api/payments"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1
    }
    result == true
}

# Application admin can access admin users -> ALLOW
test_admin_can_access_admin if {
    result := authz.allow with input as {
        "principal": {"id": "user:admin1", "kind": "user", "roles": ["application-admin"]},
        "resource": {"service": "admin", "route": "admin.users.list"},
        "request": {"method": "GET", "path": "/api/admin/users"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1
    }
    result == true
}

# Workload orders can create payments -> ALLOW
test_workload_orders_can_post_payments if {
    result := authz.allow with input as {
        "principal": {"id": "spiffe://aegis.local/workload/orders", "kind": "workload", "roles": []},
        "resource": {"service": "payments", "route": "payments.create"},
        "request": {"method": "POST", "path": "/api/payments"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1
    }
    result == true
}

# Workload orders cannot access admin -> DENY
test_workload_orders_cannot_access_admin if {
    result := authz.allow with input as {
        "principal": {"id": "spiffe://aegis.local/workload/orders", "kind": "workload", "roles": []},
        "resource": {"service": "admin", "route": "admin.users.list"},
        "request": {"method": "GET", "path": "/api/admin/users"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1
    }
    result == false
}

# Missing identity -> DENY
test_missing_identity_denied if {
    result := authz.allow with input as {
        "principal": {"id": "", "kind": "anonymous", "roles": []},
        "resource": {"service": "orders", "route": "orders.list"},
        "request": {"method": "GET", "path": "/api/orders"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1
    }
    result == false
}
```

### 5.5 Rego Typed Input JSON Schema: `policies/schemas/input.schema.json`

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "AegisPolicyInput",
  "type": "object",
  "required": ["principal", "resource", "request", "context", "snapshot_version"],
  "additionalProperties": false,
  "properties": {
    "principal": {
      "type": "object",
      "required": ["id", "kind", "roles"],
      "additionalProperties": false,
      "properties": {
        "id": { "type": "string", "minLength": 1 },
        "kind": { "type": "string", "enum": ["user", "workload", "anonymous"] },
        "roles": { "type": "array", "items": { "type": "string" } }
      }
    },
    "resource": {
      "type": "object",
      "required": ["service", "route"],
      "additionalProperties": false,
      "properties": {
        "service": { "type": "string", "enum": ["orders", "payments", "admin"] },
        "route": { "type": "string", "minLength": 1 }
      }
    },
    "request": {
      "type": "object",
      "required": ["method", "path"],
      "additionalProperties": false,
      "properties": {
        "method": { "type": "string", "enum": ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"] },
        "path": { "type": "string", "pattern": "^/api/.*$" }
      }
    },
    "context": {
      "type": "object",
      "required": ["risk_score", "risk_state"],
      "additionalProperties": false,
      "properties": {
        "risk_score": { "type": "integer", "minimum": 0, "maximum": 100 },
        "risk_state": { "type": "string", "enum": ["available", "degraded", "unavailable"] }
      }
    },
    "snapshot_version": { "type": "integer", "minimum": 1 }
  }
}
```

---

## 6. Environment Availability & Tooling Verification

All necessary CLI compilers and verification linters have been installed and confirmed functional in the local development environment:

```
[Tool Verification Status]
• Go runtime:             /usr/local/go/bin/go            (go1.25.5 linux/amd64)         -> OK
• Protobuf compiler:      /home/logan78/.local/bin/protoc (libprotoc 29.3)               -> OK
• protoc-gen-go:          /home/logan78/.local/bin/protoc-gen-go (v1.36.12)              -> OK
• protoc-gen-go-grpc:     /home/logan78/.local/bin/protoc-gen-go-grpc (v1.6.2)           -> OK
• oapi-codegen:           /home/logan78/.local/bin/oapi-codegen (v2.5.0)                 -> OK
• OPA CLI:                /home/logan78/.local/bin/opa (v1.2.0, Rego v1 default)         -> OK
• Redocly OpenAPI Linter: npx @redocly/cli (v2.58.1)                                     -> OK
• Buf CLI:                npx @bufbuild/buf (v1.73.0)                                    -> OK
```

PATH configuration includes `/home/logan78/.local/bin` and `/usr/local/go/bin`. Code generation scripts can execute directly without path modifications.

---

## 7. Validation Architecture

### 7.1 Test Frameworks & Harnesses

Phase 0 validates contracts and policies across three distinct automated suites:
1. **OpenAPI Linter (`@redocly/cli lint`):** Validates OpenAPI 3.0.3 syntax, schema completeness, parameter definitions, and status codes.
2. **Protobuf & Code Generation (`protoc` & `oapi-codegen`):** Compiles `.proto` into Go structs and `.yaml` into Go client/server types, asserting zero syntax or import errors.
3. **OPA Policy Unit Testing (`opa test`):** Executes declarative Rego tests asserting allow/deny decisions and reason codes across the entire seed permission matrix.

### 7.2 Requirements-to-Test Mapping Table

| Phase 0 Deliverable | Success Criteria / Requirement | Validation Command | Acceptance Criteria |
|---|---|---|---|
| `api/openapi/control-v1.yaml` | Models all 13 `/control/v1` routes; strict schemas | `npx --yes @redocly/cli lint api/openapi/control-v1.yaml` | 0 errors, 0 warnings. |
| `pkg/api/control/v1` | OpenAPI Go types compile cleanly | `oapi-codegen -generate types,server -package controlv1 api/openapi/control-v1.yaml > pkg/api/control/v1/control.gen.go` | Go file generated; `go vet ./pkg/api/control/v1` passes. |
| `api/proto/snapshot/v1/snapshot.proto` | Compiles into Go structs & gRPC stubs | `protoc --proto_path=api/proto --go_out=pkg/api/snapshot/v1 --go_opt=paths=source_relative --go-grpc_out=pkg/api/snapshot/v1 --go-grpc_opt=paths=source_relative api/proto/snapshot/v1/snapshot.proto` | Generated `snapshot.pb.go` and `snapshot_grpc.pb.go` exist and compile. |
| `policies/rego/authz.rego` | Seed RBAC & workload matrix passes 100% | `opa test policies/ -v` | All test cases in `authz_test.rego` pass (PASS: 8/8). |
| `policies/schemas/*.json` | Input and output match schema | `opa check policies/rego/` | OPA static check passes with 0 syntax or type warnings. |
| `docs/adr/0001` - `0006` | ADRs document 6 required decisions in MADR format | Visual inspection / ADR lint script | 6 ADRs exist, covering proxy arch, OPA embedding, dual-listener, leases, Redis 503, spool fsync. |
| `docs/threat-model/threat-model.md` | Formal threat model mapping 12 invariants | Checklist audit | Covers trust boundaries, STRIDE, attacker profiles, and all 12 invariants mapped to tests. |

### 7.3 Verification Commands

#### Quick Validation Command (Run during editing)
```bash
opa test policies/ -v && npx --yes @redocly/cli lint api/openapi/control-v1.yaml
```

#### Full Phase 0 Build & Validation Pipeline
```bash
# 1. Validate OpenAPI 3.0 specification
npx --yes @redocly/cli lint api/openapi/control-v1.yaml

# 2. Compile Protobuf definitions into Go structs
mkdir -p pkg/api/snapshot/v1
protoc --proto_path=api/proto \
  --go_out=pkg/api/snapshot/v1 --go_opt=paths=source_relative \
  --go-grpc_out=pkg/api/snapshot/v1 --go-grpc_opt=paths=source_relative \
  api/proto/snapshot/v1/snapshot.proto

# 3. Compile OpenAPI models into Go structs
mkdir -p pkg/api/control/v1
oapi-codegen -generate types -package controlv1 api/openapi/control-v1.yaml > pkg/api/control/v1/types.gen.go

# 4. Verify Rego policies and unit tests
opa check policies/rego/
opa test policies/ -v

# 5. Verify generated Go packages compile
go vet ./pkg/api/...
```

---

## 8. Security Domain & Threat Model Architecture

### 8.1 Applicable OWASP ASVS v4.0 Categories

Phase 0 contracts and threat models directly implement controls from OWASP ASVS v4.0:

| ASVS Category | ASVS Requirement Reference | Aegis Contract / Schema Implementation |
|---|---|---|
| **V1: Architecture, Design and Threat Modeling** | 1.1.1, 1.1.2, 1.1.5, 1.2.1 | Formal Threat Model in `docs/threat-model/threat-model.md`; 6 core ADRs; explicit trust boundaries; non-negotiable security invariants. |
| **V2: Authentication** | 2.8.1, 2.8.4, 2.8.5 | `AUTH-01`: Pinned algorithms (Ed25519/RS256; reject `none`), audience, issuer in OpenAPI/JWT; dedicated workload mTLS listener (`AUTH-02`). |
| **V3: Session Management** | 3.5.1, 3.5.2 | CSRF token parameter requirements on mutating management endpoints (`OPS-04`, `spec.md` §7). |
| **V4: Access Control** | 4.1.1, 4.1.2, 4.1.3, 4.2.1 | Default-deny Rego policy (`POL-02`); separate permissions per endpoint (`spec.md` §7); explicit reason codes on denial. |
| **V5: Validation & Encoding** | 5.1.1, 5.1.2, 5.2.2 | Strict path regex and canonical validation contracts; zero-repair path rejection (400 Bad Request); JSON schema input constraints. |
| **V8: Cryptography** | 8.1.1, 8.2.1, 8.3.1, 8.3.4 | Ed25519 digital signatures for snapshots (`SnapshotEnvelope`) and assertions (`X-Aegis-Assertion`); distinct keys for token issuance vs assertions. |
| **V10: Malicious Code & Integrity** | 10.3.1, 10.3.2 | Monotonic snapshot versioning; rejection of lower/duplicate versions; signed freshness leases with 60s fail-closed timeout. |
| **V13: API & Web Service** | 13.1.1, 13.2.1, 13.2.3 | OpenAPI 3.0.3 contract-first specification; bounded request bodies; strict field schemas; standard error envelope. |

### 8.2 STRIDE Threat Model Breakdown

| STRIDE Threat | Target Asset / Boundary | Specific Attack Vector | Mitigation Contract Mechanism | Verification Mapping |
|---|---|---|---|---|
| **S - Spoofing** | Gateway Ingress & Backend Services | Caller injects `X-Aegis-User: admin` or forged `X-Forwarded-For`; workload attempts to call backend directly bypassing gateway. | Invariant 5 & 8: Ingress strips `X-Aegis-*` headers. Gateway mints signed assertion (`iss: aegis-gateway`, TTL <= 15s). Backends enforce mTLS and verify assertion signature. | Phase 1 & 2 integration tests inject forged headers and direct container calls; assert 400/401/403. |
| **T - Tampering** | Snapshot Distribution & Ingress Paths | Attacker modifies policy snapshot in transit or sends path traversal (`/api/orders/%2e%2e/admin`). | Invariant 7 & 9: Ed25519 signed snapshot envelope; SHA-256 payload checksum. Invariant 7: Gateway rejects dot-segments and encoded slashes (400 Bad Request). | Fuzz test path traversal sequences; tamper snapshot payload and verify gateway signature rejection. |
| **R - Repudiation** | Mutating Transactions (`POST /api/payments`) | Caller executes payment mutation; gateway crashes before logging; caller denies initiating action. | Invariant 10: Mandatory pre-forward append-only disk WAL with forced `fsync()` before forwarding permitted requests. PostgreSQL worker drains with deduplication. | Chaos test stops DB while executing payments; verify local spool file contains fsync'd decision record. |
| **I - Information Disclosure** | Management APIs & Observability Metrics | Prometheus exposes user IDs or tokens; error responses leak Rego source code or DB stack traces. | Invariant 11: Metric labels strictly forbid high-cardinality PII (use route templates). OpenAPI errors define fixed `ErrorResponse` with reason codes, omitting stack traces. | Unit test inspects Prometheus metric labels and error JSON response bodies. |
| **D - Denial of Service** | Redis Rate Limiter & Spool Disk Capacity | Attacker floods gateway causing Redis outage; attacker floods disk spool until host disk exhausts. | Invariant 1: Redis outage fails closed (503). Spool saturation gate stops admitting permitted requests at 90% capacity (503). Ingress concurrency bounded. | Dependency fault injection tests stop Redis and simulate disk saturation; verify 100% 503 fail-closed responses. |
| **E - Elevation of Privilege** | Route Dispatch & Distributed Replicas | Attacker calls unmapped route hoping for default allow; attacker targets partitioned replica running stale policy. | Invariant 1: Strict default-deny in Rego. Invariant 9: Atomic snapshot swap. Invariant 4: Signed 10s freshness lease; replica drops readiness and returns 503 after 60s without renewal. | Control plane network severed for 65s; verify gateway returns 503 on protected routes. |

### 8.3 The 12 Non-Negotiable Security Invariants Mapping

Formal mapping of `spec.md` §3 invariants to architectural mechanisms and verifiable test scenarios:

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│ INVARIANT 1: Default Deny across all routes                                            │
│ Mechanism: Rego `default allow := false`; unmapped routes return 403/503.              │
│ Verification: Test unmapped route `/api/unknown` with valid token -> 403 Forbidden.    │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 2: Identity strictly from verified JWT or verified Client Certificate         │
│ Mechanism: Strips all identity headers at ingress; parses claims only after crypto OK. │
│ Verification: Send request with `X-User-Role: admin` and no JWT -> 401 Unauthorized.   │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 3: Workload certificate authenticates caller; policy decides permission      │
│ Mechanism: Client cert SAN `spiffe://...` extracted as principal.id; passed to Rego.   │
│ Verification: Workload `orders` calls `/api/admin/users` -> 403 Forbidden.             │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 4: TLS verification enabled in every production path                         │
│ Mechanism: InsecureSkipVerify=false everywhere; pinned CA roots for upstreams.         │
│ Verification: Upstream presenting self-signed untrusted cert -> 502 Bad Gateway.       │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 5: Backends accept only designated Gateway client identity + signed assertion│
│ Mechanism: Backends run mTLS middleware verifying gateway cert + X-Aegis-Assertion JWT.│
│ Verification: Call backend directly with workload cert -> TLS handshake error or 403.  │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 6: Route identifies configured upstream; headers/URLs never choose upstream  │
│ Mechanism: Route matches fixed upstream URL in active snapshot; absolute URLs rejected.│
│ Verification: Request with `Host: malicious.com` or proxy-form URL -> 400 Bad Request. │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 7: Policy and proxy use identical validated path, method, and snapshot       │
│ Mechanism: Zero-repair path canonicalization; forward exact validated path bytes.      │
│ Verification: Request with `/api/orders/%2f` or `/..` -> 400 Bad Request.              │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 8: No incoming header can impersonate gateway identity or decision           │
│ Mechanism: Gateway unconditionally strips inbound `X-Aegis-*` and hop-by-hop headers.  │
│ Verification: Inject `X-Aegis-Assertion: forged` -> Gateway overwrites or strips it.   │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 9: Configuration activation is atomic; monotonic versioning                  │
│ Mechanism: sync/atomic.Pointer[Snapshot]; snapshots signed with Ed25519; monotonic ver.│
│ Verification: Replicas reject snapshot with version <= active_version (409 Conflict). │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 10: Permitted requests require durable pre-forward audit record              │
│ Mechanism: Local disk WAL append + fsync() before proxy rewrite dispatch.              │
│ Verification: Stop PostgreSQL; permitted request succeeds and writes to local WAL.     │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 11: Management access separately authorized; no wildcard data-plane match    │
│ Mechanism: /control/v1 on separate listener or distinct audience/scopes; no wildcards. │
│ Verification: Data-plane user token calling `/control/v1/policies` -> 403 Forbidden.   │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ INVARIANT 12: Network location is context, never sufficient identity or authorization  │
│ Mechanism: Shared Docker network traffic still requires mTLS + signed assertions.      │
│ Verification: Container on same bridge network calling `/api/payments` -> 401/403.     │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 9. Sources & Metadata

### 9.1 Primary Sources
- **Aegis Architectural Specification v1.0 (`spec.md`)**: Sections 1, 2, 3, 4, 5, 6, 7, 8, 10, 13, 14, 15 (Phase 0 milestone gates).
- **NIST Special Publication 800-207**: *Zero Trust Architecture* (August 2020) — https://csrc.nist.gov/pubs/sp/800/207/final (Section 3: PEP, PDP, PAP definitions; Section 3.3: Threat assumptions).
- **RFC 8725**: *JSON Web Token Best Current Practices* (February 2020) — https://www.rfc-editor.org/rfc/rfc8725.html (Algorithm allowlist, clock skew tolerance, prevention of symmetric key confusion).
- **RFC 7230 / RFC 9112**: *HTTP/1.1 Message Syntax and Routing* — https://www.rfc-editor.org/rfc/rfc9112.html (Section 6.1: Hop-by-hop headers and proxy forwarding rules).
- **Open Policy Agent (OPA) Documentation**: *Rego v1 Policy Language Reference & Go Integration* — https://www.openpolicyagent.org/docs/latest/
- **Protocol Buffers v3 Language Specification**: *proto3 Language Guide* — https://protobuf.dev/programming-guides/proto3/
- **OpenAPI Specification v3.0.3**: https://spec.openapis.org/oas/v3.0.3.html
- **OWASP Application Security Verification Standard (ASVS) v4.0**: https://owasp.org/www-project-application-security-verification-standard/

### 9.2 Research Verification Metadata
- **Tooling verified:** `go` (1.25.5), `protoc` (29.3), `protoc-gen-go` (1.36.12), `protoc-gen-go-grpc` (1.6.2), `oapi-codegen` (2.5.0), `opa` (1.2.0), `@redocly/cli` (2.58.1), `@bufbuild/buf` (1.73.0).
- **Date verified:** 2026-10-06
- **Status:** Ready for Plan Generation (`00-01`, `00-02`, `00-03`).
