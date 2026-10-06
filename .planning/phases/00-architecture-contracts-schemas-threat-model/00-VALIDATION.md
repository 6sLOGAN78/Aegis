---
phase: 0
slug: architecture-contracts-schemas-threat-model
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-10-06
---

# Phase 0 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | OPA Test CLI (`opa test`) + Redocly CLI (`@redocly/cli lint`) + Protobuf Compiler (`protoc`) + Go Toolchain (`go vet`) |
| **Config file** | `api/openapi/control-v1.yaml`, `api/proto/snapshot/v1/snapshot.proto`, `policies/rego/authz.rego` |
| **Quick run command** | `opa test policies/rego policies/tests -v && npx --yes @redocly/cli lint api/openapi/control-v1.yaml` |
| **Full suite command** | `npx --yes @redocly/cli lint api/openapi/control-v1.yaml && protoc --proto_path=api/proto -I "${HOME}/.local/include" --go_out=pkg/api --go_opt=paths=source_relative --go-grpc_out=pkg/api --go-grpc_opt=paths=source_relative api/proto/snapshot/v1/snapshot.proto && oapi-codegen -generate types -package controlv1 api/openapi/control-v1.yaml > pkg/api/control/v1/types.gen.go && opa check policies/rego/ && opa test policies/rego policies/tests -v && go vet ./pkg/api/...` |
| **Estimated runtime** | ~6 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick run command (`opa test policies/rego policies/tests -v && npx --yes @redocly/cli lint api/openapi/control-v1.yaml`)
- **After every plan wave:** Run full suite command
- **Before `/gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement / Deliverable | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|---------------------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 00-01-01 | 01 | 1 | ADR-0001 through ADR-0006 | T-00-01 | Architectural decisions explicitly bound to security invariants | static | `test -f docs/adr/0001-reverse-proxy-architecture.md && test -f docs/adr/0006-pre-forward-wal-audit-spool.md` | ❌ W0 | ⬜ pending |
| 00-01-02 | 01 | 1 | Threat Model Specification | T-00-02 | STRIDE taxonomy and 12 non-negotiable invariants mapped | audit | `test -f docs/threat-model/threat-model.md && grep -q "Invariant 12" docs/threat-model/threat-model.md` | ❌ W0 | ⬜ pending |
| 00-02-01 | 02 | 1 | OpenAPI 3.0.3 Specification | T-00-03 | Valid schema for 13 `/control/v1` routes with ETag and RBAC scopes | lint | `npx --yes @redocly/cli lint api/openapi/control-v1.yaml` | ❌ W0 | ⬜ pending |
| 00-02-02 | 02 | 1 | Protobuf 3 Snapshot Schema | T-00-04 | Monotonic signed snapshot and 10s freshness lease messages compile | compile | `protoc --proto_path=api/proto -I "${HOME}/.local/include" --go_out=pkg/api --go_opt=paths=source_relative --go-grpc_out=pkg/api --go-grpc_opt=paths=source_relative api/proto/snapshot/v1/snapshot.proto` | ❌ W0 | ⬜ pending |
| 00-02-03 | 02 | 2 | Generated Go Code Artifacts | T-00-05 | Generated Go types and gRPC interfaces compile without warnings | typecheck | `go vet ./pkg/api/...` | ❌ W0 | ⬜ pending |
| 00-03-01 | 03 | 1 | Rego Typed Schemas & Seed Data | T-00-06 | Typed input/output JSON schemas and route catalog | schema | `opa check policies/rego/` | ❌ W0 | ⬜ pending |
| 00-03-02 | 03 | 1 | Rego Authorization Policy | T-00-07 | Default-deny evaluation and explicit rule matching | unit | `opa test policies/rego policies/tests -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] Toolchain availability: `protoc` (v29.3), `protoc-gen-go` (v1.36.12), `protoc-gen-go-grpc` (v1.6.2), `oapi-codegen` (v2.5.0), `opa` (v1.2.0) verified in PATH.
- [ ] Directory structure scaffolding: `api/openapi`, `api/proto/snapshot/v1`, `policies/{rego,schemas,data,tests}`, `docs/{adr,threat-model}`, `pkg/api/{snapshot/v1,control/v1}`.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| None | All | N/A | All Phase 0 contracts, schemas, and policies have automated linting, compilation, or OPA test suites. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 10s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending 2026-10-06
