---
phase: 00-architecture-contracts-schemas-threat-model
plan: 02
type: schema
status: completed
tasks_completed: 3
tasks_total: 3
commits:
  - b3d6cc9: feat(00-02): author OpenAPI 3.0.3 management spec and oapi-codegen config
  - 70ea3e2: feat(00-02): author Protobuf 3 snapshot distribution schema
  - 56587d1: feat(00-02): implement code generation pipeline and generated Go packages
files_created:
  - api/openapi/control-v1.yaml
  - api/openapi/oapi-codegen.yaml
  - api/proto/snapshot/v1/snapshot.proto
  - scripts/generate.sh
  - go.mod
  - go.sum
  - pkg/api/control/v1/types.gen.go
  - pkg/api/snapshot/v1/snapshot.pb.go
  - pkg/api/snapshot/v1/snapshot_grpc.pb.go
---

# Plan 00-02 Summary: OpenAPI Management Specification & Protobuf Snapshot Schemas

## Deliverables Summary

Plan 00-02 established authoritative interface contracts and strongly typed Go code generation for the Aegis Control Plane and Data Plane:

### 1. OpenAPI 3.0.3 Management API Specification (`api/openapi/control-v1.yaml`)
- Completely specifies all 13 required `/control/v1` routes:
  - `/control/v1/routes` (`GET`, `POST`): route catalog and creation with pagination and ETag headers.
  - `/control/v1/policies` (`POST`): policy draft creation.
  - `/control/v1/policies/{id}/versions` (`GET`): historical audit and approval logging.
  - `/control/v1/policies/{id}/validate` (`POST`): syntax and unit test pre-flight verification.
  - `/control/v1/policies/{id}/simulate` (`POST`): dry-run in-memory evaluation with microsecond execution timings.
  - `/control/v1/policies/{id}/publish` (`POST`): snapshot publication with optimistic concurrency (`If-Match`).
  - `/control/v1/policies/{id}/rollback` (`POST`): historical reactivation via monotonic version increments.
  - `/control/v1/gateways` (`GET`): replica grid connectivity and freshness lease tracking.
  - `/control/v1/principals/{id}/quarantine` (`POST`, `DELETE`): emergency cluster-wide revocation in Redis.
  - `/control/v1/audit-events` (`GET`, `GET /{id}`): query and inspection of decision and completion audit events.
- Strictly adheres to OpenAPI 3.0.3 and passes Redocly linter (`@redocly/cli lint`) with 0 errors and 0 warnings.
- Prepares Go code generator targeting package `controlv1` via `api/openapi/oapi-codegen.yaml`.

### 2. Protobuf 3 Monotonic Snapshot & Freshness Lease Contract (`api/proto/snapshot/v1/snapshot.proto`)
- Declares gRPC service `SnapshotDistributionService` with bidirectional stream `StreamSnapshots` and unary `GetActiveSnapshot`.
- Defines top-level multiplexed streaming messages: `ControlPlaneMessage` (snapshot, lease, ping) and `GatewayMessage` (ack, pong).
- Specifies cryptographic `SnapshotEnvelope` with Ed25519 digital signature over SHA-256 payload digest and monotonic integer versioning.
- Models strongly typed routing rules, timeout and rate limiting policies, Rego policy modules, and identity role mappings.
- Implements `FreshnessLease` (10s renewal) and `SnapshotAck` with `AckStatus` enum containing explicit `ACK_STATUS_UNSPECIFIED = 0`.
- Compiles cleanly with `protoc`.

### 3. Automated Code Generation Pipeline & Go Stubs (`scripts/generate.sh`)
- Initializes root Go module `aegis` (`go 1.24+`) with pinned dependencies: `google.golang.org/protobuf v1.36.12` and `google.golang.org/grpc v1.83.2`.
- Implements `scripts/generate.sh` coordinating `protoc` and `oapi-codegen`.
- Generates Go packages:
  - `pkg/api/control/v1/types.gen.go` (OpenAPI models)
  - `pkg/api/snapshot/v1/snapshot.pb.go` (Protobuf types and serialisation)
  - `pkg/api/snapshot/v1/snapshot_grpc.pb.go` (gRPC client and server stubs)
- Static verification via `go vet ./pkg/api/...` passes with 0 warnings.

## Verification Evidence
- Redocly lint: `npx --yes @redocly/cli lint api/openapi/control-v1.yaml` (0 errors, 0 warnings)
- Protobuf syntax check: `protoc --proto_path=api/proto -I ~/.local/include --descriptor_set_out=/dev/null api/proto/snapshot/v1/snapshot.proto` (Exit 0)
- Code generation & Go vet: `./scripts/generate.sh && go vet ./pkg/api/...` (Exit 0)

## Self-Check: PASSED
- [x] `api/openapi/control-v1.yaml` models all 13 `/control/v1` routes and passes Redocly linting
- [x] `api/proto/snapshot/v1/snapshot.proto` compiles with protoc
- [x] `scripts/generate.sh` generates Go stubs and `go vet ./pkg/api/...` passes with zero warnings
- [x] All tasks committed atomically to git
