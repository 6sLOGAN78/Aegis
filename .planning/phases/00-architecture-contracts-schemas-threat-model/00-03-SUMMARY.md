---
phase: 00-architecture-contracts-schemas-threat-model
plan: 03
type: policy
status: completed
tasks_completed: 2
tasks_total: 2
commits:
  - 6f76981: feat(00-03): define Rego input/output schemas and seed routes/roles catalogs
  - ccc71b8: feat(00-03): implement Rego v1 authorization policy and test suite
files_created:
  - policies/schemas/input.schema.json
  - policies/schemas/output.schema.json
  - policies/data/routes.json
  - policies/data/seed_roles.json
  - policies/rego/authz.rego
  - policies/tests/authz_test.rego
---

# Plan 00-03 Summary: Rego Typed Schemas, Route Definitions & Seed Permission Matrix

## Deliverables Summary

Plan 00-03 established the declarative policy authorization engine, typed contract schemas, seed route catalogs, and comprehensive OPA test suite for Aegis:

### 1. Typed Input & Output JSON Schemas
- **`policies/schemas/input.schema.json`** (Draft 2020-12):
  - Strictly constrains the in-memory OPA request context to typed objects:
    - `principal`: non-empty `id`, `kind` (`user`, `workload`, `anonymous`), and string array of `roles`.
    - `resource`: target `service` (`orders`, `payments`, `admin`) and non-empty `route`.
    - `request`: HTTP `method` (standard verbs) and canonical URL `path` matching `^/api/.*$`.
    - `context`: `risk_score` (0-100) and `risk_state` (`available`, `degraded`, `unavailable`).
    - `snapshot_version`: integer >= 1.
  - Enforces `additionalProperties: false` across all levels, eliminating untyped or unvetted parameter tampering.
- **`policies/schemas/output.schema.json`** (Draft 2020-12):
  - Enforces structured decision output schema: `{ allow: boolean, reason_code: string, snapshot_version: integer }`.

### 2. Seed Route Catalogs & Role Definitions
- **`policies/data/routes.json`**:
  - Defines seed routes matching Protobuf `RouteDefinition`:
    - `orders.list` (GET `/api/orders` -> `https://orders:8081`, SPIFFE `spiffe://aegis.local/service/orders`)
    - `payments.get` (GET `/api/payments` -> `https://payments:8082`, SPIFFE `spiffe://aegis.local/service/payments`)
    - `payments.create` (POST `/api/payments` -> `https://payments:8082`, SPIFFE `spiffe://aegis.local/service/payments`)
    - `admin.users.list` (GET `/api/admin/users` -> `https://admin:8083`, SPIFFE `spiffe://aegis.local/service/admin`)
    - `admin.users.manage` (POST `/api/admin/users` -> `https://admin:8083`, SPIFFE `spiffe://aegis.local/service/admin`)
- **`policies/data/seed_roles.json`**:
  - Implements seed RBAC permissions for `developer`, `finance`, and `application-admin`.

### 3. Declarative Rego v1 Authorization Policy (`policies/rego/authz.rego`)
- Implements Rego v1 (`import rego.v1`) with package `aegis.authz`.
- Enforces strict default deny: `default allow := false` and `default reason_code := "DENIED_DEFAULT"`.
- Implements the 5 core allow rules:
  1. `developer` user GET `orders` -> `ALLOWED_DEVELOPER_ORDERS`
  2. `developer` user GET `payments` -> `ALLOWED_DEVELOPER_PAYMENTS`
  3. `finance` user GET/POST `payments` -> `ALLOWED_FINANCE_PAYMENTS`
  4. `application-admin` user accessing `admin` -> `ALLOWED_ADMIN_USERS`
  5. `spiffe://aegis.local/workload/orders` workload POST `payments` -> `ALLOWED_WORKLOAD_ORDERS_PAYMENTS`
- Implements explicit denial reason codes:
  - Developer accessing admin -> `DENIED_DEVELOPER_ADMIN_FORBIDDEN`
  - Workload orders accessing admin -> `DENIED_WORKLOAD_ADMIN_FORBIDDEN`
  - Missing or empty principal ID -> `DENIED_INVALID_PRINCIPAL`
- Emits structured `decision` document with microsecond evaluation latency.

### 4. Comprehensive OPA Unit Test Suite (`policies/tests/authz_test.rego`)
- 8 automated unit tests verifying both positive and negative authorization flows, matching role and workload criteria, and asserting exact reason codes.
- 100% test pass rate (`PASS: 8/8`, 0 failures) executing in ~0.3ms per evaluation.

## Verification Evidence
- Schema JSON validation: Python JSON parser verified all 4 JSON files (Exit 0)
- Policy syntax check: `opa check policies/rego/` (Exit 0)
- Policy test execution: `opa test policies/rego policies/tests -v` (8/8 tests PASS)

## Self-Check: PASSED
- [x] Input and output JSON schemas enforce strict types and `additionalProperties: false`
- [x] `routes.json` and `seed_roles.json` declare canonical seed data
- [x] `authz.rego` implements Rego v1 default-deny authorization with discrete reason codes
- [x] `authz_test.rego` passes 8/8 unit tests
- [x] All tasks committed atomically to git
