---
phase: 9
slug: close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-10-10
---

# Phase 9 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution. Source: `09-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` + testify, pgxmock v4, miniredis v2 (`-race` supported) |
| **Config file** | none (go.mod; Makefile `test-quick`, `compose-test`, `compose-smoke`) |
| **Quick run command** | `go test -race -count=1 ./internal/audit/ ./internal/identity/ ./internal/control/ ./internal/storage/ ./cmd/demo-issuer/ ./cmd/gateway/ ./tests/compose/` |
| **Full suite command** | `go test -race -count=1 $(go list ./... \| grep -v /tests/integration)` (never `./tests/integration` from plain go test; it starts Docker) |
| **Live command** | `bash scripts/compose-smoke.sh {mvp\|hardened\|distributed}` |
| **Estimated runtime** | quick ~30 s; full ~3 min; live ~5 min per profile |

---

## Sampling Rate

- **After every task commit:** Run the quick command for the touched packages
- **After every plan wave:** Run the full suite command plus `go test -count=1 ./tests/compose/`
- **Before `/gsd:verify-work`:** Full suite green, then live smoke on mvp, hardened and distributed (operator frees disk below the 0.85 statfs ratio first)
- **Max feedback latency:** 30 seconds (hermetic)

---

## Per-Task Verification Map

Task IDs are filled in by the planner/executor. Rows below map each requirement/decision to its automated proof.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| TBD | TBD | TBD | AUTH-04 (D-01, D-02, D-04) | — | Seeded username+password required; wrong/missing → 401; old role body → 400; throttle still 429 | unit | `go test -count=1 ./cmd/demo-issuer/` | update `main_test.go` | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-05, D-06) | — | Every token carries a UUID jti; login response returns it; unique per login | unit | `go test -count=1 ./cmd/demo-issuer/ -run Jti` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-09) | — | Token without jti denied 401 `TOKEN_MISSING_JTI`; reason classified | unit + AST | `go test -count=1 ./cmd/gateway/ ./internal/audit/ -run 'MissingJTI\|TestEveryMainReasonIsClassified'` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-07) | — | `CompletionEvent.JTI` normalized; old frames without it decode | unit | `go test -count=1 ./internal/audit/ -run 'Normalize\|JTI'` | extend `event_test.go`, `spool_test.go` | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-07) | — | Worker inserts 21 columns, jti last, NULL when empty | unit (pgxmock) | `go test -count=1 ./internal/audit/ -run Worker` | update `worker_test.go` | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-07) | — | Migration 000004 embedded with Up/Down | unit | `go test -count=1 ./internal/storage/ -run Migrations` | update `db_test.go` | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-07) | — | Audit read API returns jti | unit (pgxmock) | `go test -count=1 ./internal/storage/ -run AuditRepo` | update `audit_repo_test.go` | ⬜ pending |
| TBD | TBD | TBD | REV-03, OPS-01 (D-10, D-12) | — | Encoded SPIFFE id decoded, validated, stored under the canonical key; bad trust domain, `%zz`, `a%2541` → 400 | unit (miniredis) | `go test -count=1 ./internal/control/ -run Quarantine` | extend `api_test.go` | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-12) | — | `ParseSPIFFEID` accepts only valid aegis.local ids | unit | `go test -count=1 ./internal/identity/ -run SPIFFE` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | OPS-01 (D-11) | — | `POST /quarantine {principal_id}` → 200, same Redis key as path form | unit (miniredis) | `go test -count=1 ./internal/control/ -run Quarantine` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | OPS-01 (D-13) | — | Spec regenerates to committed `types.gen.go` | check | `oapi-codegen -generate types -package controlv1 api/openapi/control-v1.yaml \| diff - pkg/api/control/v1/types.gen.go` | script | ⬜ pending |
| TBD | TBD | TBD | AUTH-04 (D-02, D-03) | — | Compose sets issuer password vars in all profiles | unit | `go test -count=1 ./tests/compose/ -run Issuer` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-14, D-17, D-18) | — | Smoke lint: syntax, new sections, disk pre-flight, no credential echo | unit | `go test -count=1 ./tests/compose/` | extend `smoke_script_test.go` | ⬜ pending |
| TBD | TBD | TBD | REV-03 (D-15, D-16) | — | Every replica denies revoked jti, quarantined workload (:9443, encoded path) and body-form quarantine within 5 s; audit row has jti | live | `bash scripts/compose-smoke.sh mvp` / `hardened` / `distributed` | extend script | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `missingJTI` helper + gateway wiring test (D-09)
- [ ] `ParseSPIFFEID` + tests (D-12)
- [ ] Control handler tests for encoded and body forms (D-10, D-11)
- [ ] Compose test for issuer env vars; smoke lint additions (D-02, D-18)
- [ ] gofmt gate scoped to changed files (repo has pre-existing unformatted files)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live smoke on three profiles | REV-03 | Needs Docker and disk below the 0.85 statfs ratio (host at ~0.878 on 2026-10-10) | Free disk, `docker compose -f deployments/compose/docker-compose.<profile>.yml up -d --build --wait`, then `bash scripts/compose-smoke.sh <profile>`; all new sections PASS |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
