---
phase: 01-mvp-secure-vertical-slice
plan: 02
subsystem: policy
tags: [go, opa, rego, rbac, embedded-evaluator, in-memory, security-matrix]

requires:
  - phase: 01-mvp-secure-vertical-slice
    provides: Gateway reverse proxy, zero-repair path validator, and security test harness (01-01)
provides:
  - Embedded in-memory OPA engine wrapper (rego.PrepareForEval) evaluating authorization rules in ~38-47µs (<0.2ms SLA) with zero network calls
  - Strongly typed Go policy contracts (PolicyInput, PrincipalInput, ResourceInput, RequestInput, ContextInput, Decision) mirroring Phase 0 JSON schemas
  - Static snapshot policy loader loading Rego rules and route definitions with filesystem path validation
  - Fail-closed default-deny enforcement returning structured Decision with discrete reason codes (DENIED_DEFAULT, DENIED_INVALID_PRINCIPAL, EVALUATION_ERROR, etc.)
  - Automated RBAC permission matrix security test suite validating developer, finance, admin, workload, and anonymous identities
affects: [01-03-PLAN, 01-04-PLAN, phase-02]

tech-stack:
  added:
    - github.com/open-policy-agent/opa@v1.21.1
  patterns:
    - embedded-opa-engine
    - precompiled-query-evaluator
    - static-snapshot-loader
    - rbac-security-matrix

key-files:
  created:
    - internal/policy/types.go
    - internal/policy/loader.go
    - internal/policy/loader_test.go
    - internal/policy/engine.go
    - internal/policy/engine_test.go
    - tests/security/rbac_matrix_test.go
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "Compile Rego AST once at startup using rego.PrepareForEval(ctx) to satisfy <0.2ms latency budget and eliminate network hops on the hot path (ADR-0002, POL-01)"
  - "Fail closed on all evaluation errors, missing query results, or malformed decisions with explicit reason codes (Invariant 1, POL-02)"
  - "Map policy input and output models 100% to JSON schemas to eliminate serialization impedance mismatches (POL-04)"

patterns-established:
  - "Pattern 3: Embedded In-Memory Precompiled OPA Engine"
  - "Pattern 10: Security Penetration Test Harness for RBAC Matrix"

requirements-completed:
  - POL-01
  - POL-02
  - POL-03
  - POL-04

duration: 10min
completed: 2026-10-06
---

# Phase 01 Plan 02: Embedded OPA Engine & RBAC Matrix Summary

**In-memory precompiled OPA Rego v1 policy evaluator delivering ~40µs decision latency (<0.2ms budget) with zero network calls, typed JSON schema contracts, static snapshot loader, and full RBAC security test matrix.**

## Performance

- **Duration:** 10 min
- **Started:** 2026-10-06T14:11:17Z
- **Completed:** 2026-10-06T14:21:00Z
- **Tasks:** 3
- **Files modified:** 8

## Accomplishments

- Implemented strongly typed policy domain contracts (`internal/policy/types.go`) mapping 100% to `policies/schemas/input.schema.json` and `output.schema.json` with exact JSON tag names and types (`PolicyInput`, `PrincipalInput`, `ResourceInput`, `RequestInput`, `ContextInput`, `Decision`).
- Implemented static snapshot policy loader (`internal/policy/loader.go`) holding versioned policy source and route byte catalogs with path verification and comprehensive error handling.
- Built embedded in-memory OPA evaluator (`internal/policy/engine.go`) using `rego.PrepareForEval(ctx)` targeting `data.aegis.authz.decision` with strict fail-closed defaults (`EVALUATION_ERROR`, `DENIED_EMPTY_RESULT`, `MALFORMED_DECISION`).
- Verified sub-millisecond evaluation performance via `BenchmarkOPAEval` achieving ~38–47µs per evaluation with zero network round-trips (well within the <0.2ms latency SLO and <2ms p99 budget).
- Created negative security test suite (`tests/security/rbac_matrix_test.go`) validating 14 permission permutations across developer, finance, application-admin, workload (`spiffe://aegis.local/workload/orders`), and anonymous identities with discrete reason code assertions.

## Task Commits

Each task was committed atomically:

1. **Task 1: Typed Policy Schema Models and Static Local Snapshot Policy Loader** - `643a358` (feat)
2. **Task 2: Embedded In-Memory OPA Prepared Query Evaluator and Latency Benchmark** - `79f355d` (feat)
3. **Task 3: RBAC Permission Matrix Negative Security Test Suite** - `84e7bca` (feat)

## Files Created/Modified

- `internal/policy/types.go` - Strongly typed Go structs mirroring input/output JSON schemas
- `internal/policy/loader.go` - Static snapshot loader for Rego policies and routing JSON
- `internal/policy/loader_test.go` - Unit tests for snapshot loader verifying valid and invalid path scenarios
- `internal/policy/engine.go` - Embedded OPA engine wrapping precompiled query evaluator
- `internal/policy/engine_test.go` - PDP unit tests for default deny, fail-closed semantics, and latency benchmark
- `tests/security/rbac_matrix_test.go` - Table-driven security verification of full RBAC permission matrix
- `go.mod` - Cleaned dependency graph with OPA dependencies
- `go.sum` - Checksums for OPA dependencies

## Decisions Made

- Precompiled the Rego authorization query once at startup using `rego.PrepareForEval(ctx)` rather than compiling per request or querying a remote OPA daemon, cutting evaluation time to ~40µs (ADR-0002).
- Enforced fail-closed behavior across all evaluation failure modes: syntax errors fail startup, while runtime evaluation failures, empty results, or malformed AST outputs yield `Allow: false` and explicit reason codes.
- Structured input models to strictly adhere to `policies/schemas/input.schema.json` so in-memory evaluation mirrors external policy simulations identically.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Ran go mod tidy for indirect OPA subpackages**
- **Found during:** Task 2 (`internal/policy/engine.go` compilation)
- **Issue:** `rego` imports required indirect dependencies in `go.sum` that were not yet downloaded.
- **Fix:** Ran `go mod tidy` to download and record all required dependencies.
- **Files modified:** `go.mod`, `go.sum`
- **Verification:** `go test -v -race ./internal/policy/` passed.
- **Committed in:** `79f355d` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (dependency synchronization)
**Impact on plan:** Standard Go module dependency resolution. No scope creep.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Policy Decision Point (PDP) is fully verified with microsecond in-memory evaluation.
- Ready for Plan 01-03: Deterministic Upstream Router and JWT Authentication.

---
*Phase: 01-mvp-secure-vertical-slice*
*Completed: 2026-10-06*
