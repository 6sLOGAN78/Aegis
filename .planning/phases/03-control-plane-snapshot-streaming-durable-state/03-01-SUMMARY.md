---
phase: 03-control-plane-snapshot-streaming-durable-state
plan: 01
subsystem: storage
tags: [postgresql, pgx, goose, migrations, control-plane, snapshot, repositories, validator, rego]

requires:
  - phase: 02-workload-identity-enforced-bypass-prevention
    provides: "Workload authentication, mTLS, and defense-in-depth architecture"
provides:
  - "PostgreSQL 16 relational schemas for control plane metadata and daily range-partitioned audit events"
  - "Goose embedded database migrations runner with embed.FS"
  - "pgxpool connection pool management with bounded connections and health ping"
  - "High-performance repositories for services, routes, snapshots, and gateway acknowledgments"
  - "Control plane route definition schema validator and in-memory Rego unit test evaluator"
affects:
  - "03-02 Control plane gRPC snapshot streamer and freshness lease daemon"
  - "03-03 Redis rate limiting and token revocation store"
  - "03-04 Pre-forward WAL disk spool and asynchronous PostgreSQL audit worker"

tech-stack:
  added:
    - "github.com/jackc/pgx/v5 v5.10.0 (pgxpool & stdlib)"
    - "github.com/pressly/goose/v3 v3.28.0"
    - "github.com/pashagolub/pgxmock/v4 v4.9.0"
    - "github.com/redis/go-redis/v9 v9.22.0"
    - "github.com/go-redis/redis_rate/v10 v10.0.1"
    - "github.com/alicebob/miniredis/v2 v2.34.0"
  patterns:
    - "Goose embedded SQL migrations via root package migrations.FS avoiding invalid relative path patterns"
    - "Decoupled DBPool interface satisfied by *pgxpool.Pool and pgxmock.PgxPoolIface"
    - "Parameterized SQL binary protocol bindings preventing SQL injection"
    - "In-memory AST compilation and evaluation of Rego policy rules and test assertions"

key-files:
  created:
    - migrations/migrations.go
    - migrations/000001_create_control_plane_tables.sql
    - migrations/000002_create_partitioned_audit_tables.sql
    - internal/storage/db.go
    - internal/storage/db_test.go
    - internal/storage/migration.go
    - internal/storage/route_repo.go
    - internal/storage/snapshot_repo.go
    - internal/storage/storage_test.go
    - internal/control/validator.go
    - internal/control/validator_test.go
    - .planning/phases/03-control-plane-snapshot-streaming-durable-state/03-01-SUMMARY.md
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "Isolated control plane storage to PostgreSQL 16; gateways never connect directly to relational database (CTRL-01, Invariant 11)"
  - "Employed embed.FS in dedicated migrations package to cleanly avoid illegal relative embed paths"
  - "Defined DBPool interface matching pgxpool and pgxmock to enable 100% deterministic, zero-external-dependency repository unit tests"
  - "Configured OPA Rego v1 parser options by default in validator to enforce strict declarative syntax (allow if { ... })"

patterns-established:
  - "Pattern 1: Embedded Goose database migrations via migrations.FS"
  - "Pattern 2: Parameterized binary-protocol PostgreSQL repositories using DBPool abstraction"
  - "Pattern 3: In-memory AST precompilation and unit test runner for Rego policies before draft publication"

requirements-completed:
  - CTRL-01

duration: 15min
completed: 2026-10-06
---

# Plan 03-01: PostgreSQL Relational Schemas, Goose Migrations, and Control Plane Snapshot Repository Summary

**PostgreSQL relational schemas, Goose migrations with embed.FS, connection pooling, route & snapshot repositories, and OPA/Rego draft validator**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-10-06T18:08:00Z
- **Completed:** 2026-10-06T18:23:00Z
- **Tasks:** 3 completed
- **Files created/modified:** 13

## Accomplishments

- **Go Dependencies & PostgreSQL Goose Migrations (`internal/storage/db.go`, `internal/storage/migration.go`, `migrations/`)**:
  - Added core Phase 3 Go dependencies: `pgx/v5`, `goose/v3`, `go-redis/v9`, `redis_rate/v10`, `miniredis/v2`, and `pgxmock/v4`.
  - Created embedded Goose migrations (`migrations/migrations.go`) exporting `migrations.FS`.
  - Implemented migration `000001_create_control_plane_tables.sql` establishing authoritative relational tables for `services`, `routes`, `policy_drafts`, `snapshots`, and `gateway_acks`.
  - Implemented migration `000002_create_partitioned_audit_tables.sql` establishing daily range-partitioned `audit_events` table with default partition and secondary indexes on `request_id`, `(principal_id, timestamp)`, and `(route_id, timestamp)`.
  - Implemented connection pool builder `NewPool` with bounded connections (`MaxConns: 25`, `MinConns: 5`, `MaxConnLifetime: 1h`, `MaxConnIdleTime: 30m`) and health checks.
  - Implemented `RunMigrations` and `RunMigrationsWithPool` executing embedded migrations against `database/sql` and `*pgxpool.Pool`.

- **PostgreSQL Repositories for Services, Routes, and Snapshots (`internal/storage/`)**:
  - Implemented `RouteRepo` supporting `UpsertService`, `GetService`, `UpsertRoute`, `GetRoute`, `ListRoutes`, and `DeleteRoute`.
  - Implemented `SnapshotRepo` supporting `SaveSnapshot`, `GetLatestSnapshot`, `GetSnapshotByVersion`, `RecordGatewayAck`, and `ListGatewayAcks`.
  - Standardized sentinel error `ErrNotFound` across all queries returning zero rows.
  - Guaranteed zero SQL injection risks through binary parameterized queries (`$1, $2, ...`).
  - Implemented complete unit test suite in `internal/storage/storage_test.go` using `pgxmock/v4`, verifying positive CRUD paths, not-found handling, and SQL injection resistance under the Go race detector (`go test -v -race ./internal/storage/...`).

- **Control Plane Route Schema and Rego Unit Test Validator (`internal/control/`)**:
  - Implemented `Validator` in `internal/control/validator.go` with method `ValidateRoute` enforcing OpenAPI constraints:
    - Non-empty alphanumeric route ID (`^[a-zA-Z0-9._-]+$`).
    - Non-empty service ID.
    - Valid HTTP method allowlist (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `HEAD`, `OPTIONS`).
    - Path traversal rejection (disallowing `..`, duplicate `//`, and encoded `%2f` / `%2F`).
    - Valid absolute URI for upstream URL with scheme (`http`/`https`) and host.
    - Valid SPIFFE URI format (`spiffe://{trust-domain}/{path}`).
    - Positive rate limit and timeout constraints.
  - Implemented `ValidatePolicyDraft` compiling raw Rego policy drafts in-memory via OPA Rego SDK with default Rego v1 syntax.
  - Implemented `RunRegoTests` compiling both policy and unit test modules, discovering all `test_` rules, evaluating them, and returning structured `TestSummary` with pass/fail counts and failure reasons.
  - Implemented comprehensive unit tests in `internal/control/validator_test.go` verifying positive and negative validation behaviors with 100% pass rate under race detection (`go test -v -race ./internal/control/...`).

## Task Commits

Each task was committed atomically:

1. **Task 1: Go Dependencies and PostgreSQL Goose Migrations** - `ac9e748` (feat)
2. **Task 2: PostgreSQL Repositories for Services, Routes, and Snapshots** - `07203c8` (feat)
3. **Task 3: Control Plane Route Schema and Rego Unit Test Validator** - `b141203` (feat)

## Files Created/Modified

- `go.mod` - Registered Phase 3 dependencies (`pgx/v5`, `goose/v3`, `go-redis/v9`, `redis_rate/v10`, `miniredis/v2`, `pgxmock/v4`)
- `go.sum` - Checksums for newly added modules
- `migrations/migrations.go` - Embedded filesystem containing migration scripts
- `migrations/000001_create_control_plane_tables.sql` - Goose migration for control plane tables
- `migrations/000002_create_partitioned_audit_tables.sql` - Goose migration for range-partitioned audit events
- `internal/storage/db.go` - Connection pool configuration and `DBPool` interface
- `internal/storage/db_test.go` - Unit tests for pool configuration and embedded migration discovery
- `internal/storage/migration.go` - Goose runner executing embedded migrations
- `internal/storage/route_repo.go` - Repository for services and route catalog
- `internal/storage/snapshot_repo.go` - Repository for signed snapshots and gateway acknowledgments
- `internal/storage/storage_test.go` - pgxmock unit tests for route and snapshot repositories
- `internal/control/validator.go` - Route definition and Rego policy/unit-test validator
- `internal/control/validator_test.go` - Comprehensive validator unit tests

## Decisions Made

- Embedded migration SQL files into a standalone `migrations` package so Go's `//go:embed` can reference files in the current package directory without illegal `..` traversals.
- Defined a `DBPool` interface in `internal/storage/db.go` that mirrors `pgxpool.Pool` and is fulfilled by `pgxmock.PgxPoolIface`, decoupling repository logic from a live PostgreSQL instance for fast, reliable unit testing.
- Configured OPA's AST compiler options with `ast.RegoV1` by default across `ValidatePolicyDraft` and `RunRegoTests` to support modern Rego syntax without requiring explicit imports in each draft.

## Deviations from Plan

- None. All tasks executed as planned.

## Issues Encountered

- `pgx/v5@v5.11.0` introduced a new method `TypeMap()` to the `pgx.Rows` interface, causing a build failure in `pgxmock/v4`. Pinned `pgx/v5` to `v5.10.0` and upgraded `pgxmock/v4` to `v4.9.0`, resolving the interface mismatch.

## Next Phase Readiness

- Plan 03-01 delivers the foundational database storage schemas and validation logic required for Plan 03-02 (gRPC Snapshot Distribution Service, Ed25519 signer, and 10s freshness lease ticker).

---
*Phase: 03-control-plane-snapshot-streaming-durable-state*
*Plan: 01*
*Completed: 2026-10-06*
