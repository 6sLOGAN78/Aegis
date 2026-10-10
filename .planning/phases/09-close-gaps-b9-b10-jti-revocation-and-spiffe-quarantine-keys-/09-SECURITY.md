---
phase: 9
slug: close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys-
status: verified
threats_open: 0
asvs_level: 1
created: 2026-10-11
---

# Phase 9 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| client → demo-issuer `/login` | Untrusted credentials exchanged for a signed JWT | username/password (secret), JWT with jti |
| client → gateway :8080 | Bearer token validated, jti checked against revocation | JWT (secret), jti |
| workload → gateway :9443 (mTLS) | SPIFFE id from client cert checked against quarantine | X.509 SVID, SPIFFE id |
| operator → control plane `/control/v1` | Session cookie + CSRF; writes revocation/quarantine keys | principal ids, jti, reasons |
| gateway / control plane → Redis | Shared revocation and quarantine state | `revocation:jti:*`, `quarantine:principal:*` |
| audit spool → worker → Postgres | WAL frames inserted into `audit_events` | audit metadata incl. jti (identifier, not secret) |
| smoke script → live stack | Test harness issuing tokens and control calls | demo credentials, tokens (never printed) |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-09-01 | Elevation | demo-issuer HandleLogin | mitigate | `cmd/demo-issuer/main.go:131-142` username+password only, DisallowUnknownFields, 400 on legacy body | closed |
| T-09-02 | Information disclosure | password compare | mitigate | `main.go:62-65` sha256 + ConstantTimeCompare; :145-153 dummy compare, identical 401 | closed |
| T-09-03 | Information disclosure | startup logging | mitigate | `main.go:55` warns with user/env name only | closed |
| T-09-04 | DoS / brute force | `/login` | accept | per-IP throttle kept (`main.go:88`, :123) — see AR-01 | closed |
| T-09-05 | Information disclosure | default demo passwords | accept | warning on default (`main.go:54`), env override in all compose files — see AR-02 | closed |
| T-09-06 | Tampering | audit worker insert | mitigate | `internal/audit/worker.go:83-87,159` bound `$21`; clipped to 128 (`event.go:93`); VARCHAR(128) | closed |
| T-09-07 | Tampering | WAL frame compatibility | mitigate | `event.go:67` omitempty; plain `json.Unmarshal` (`spool.go:619`) | closed |
| T-09-08 | Information disclosure | jti in audit rows | accept | identifier only, no token column — see AR-03 | closed |
| T-09-09 | Tampering | generated types | mitigate | oapi-codegen v2.5.0 regeneration diff clean vs `pkg/api/control/v1/types.gen.go` | closed |
| T-09-10 | Information disclosure | jti in audit API | accept | `internal/control/api.go:141-143` RequireRole(sec-ops, auditor) in session/CSRF group — see AR-04 | closed |
| T-09-11 | Tampering | SQL in audit_repo | mitigate | `internal/storage/audit_repo.go:93,235` column added to static SELECTs only | closed |
| T-09-12 | Tampering | canonicalPrincipalID | mitigate | `quarantine_handler.go:31-71` single PathUnescape, rejects `%`/ctrl/whitespace/>128; strict ParseSPIFFEID | closed |
| T-09-13 | DoS | quarantine key mismatch | mitigate | `internal/identity/spiffe.go:70-99` shared canonicalSPIFFE; gateway keys on ExtractSPIFFEID (`cmd/gateway/main.go:709,727`) | closed |
| T-09-14 | Elevation | quarantine routes | accept | `api.go:135-138` RequireRole(sec-ops) under SessionMiddleware — see AR-05 | closed |
| T-09-15 | Tampering | path vs body id mismatch | mitigate | `quarantine_handler.go:118-121` 400 on differing canonical ids | closed |
| T-09-16 | Elevation | jti-less token bypass | mitigate | `cmd/gateway/main.go:46-62,398` 401 TOKEN_MISSING_JTI before revStore/CheckRevocation | closed |
| T-09-17 | Repudiation | audit of the deny | mitigate | `internal/audit/governor.go:43,62` classIdentified; SetPrincipal/SetJTI precede guard | closed |
| T-09-18 | Tampering | validator regressions | mitigate | guard in handler; `cmd/gateway/missing_jti_test.go` AST ordering + behaviour tests pass | closed |
| T-09-19 | Information disclosure | docs/tests with demo passwords | accept | demo defaults only — see AR-06 | closed |
| T-09-20 | Spoofing | runbook auth guidance | mitigate | `docs/runbooks/quarantine-and-revocation.md:37-39,49-64,86-89,128-132` cookie + CSRF | closed |
| T-09-21 | Tampering | psql_q interpolation | mitigate | `scripts/compose-smoke.sh:275-277` valid_rid guard on every interpolated id; `jq --arg` bodies | closed |
| T-09-22 | Information disclosure | smoke output | mitigate | `tests/compose/smoke_script_test.go:76-85,111-120` no-echo lint tests pass; no `set -x` | closed |
| T-09-23 | DoS | leftover quarantine after failed run | mitigate | cleanup trap (`compose-smoke.sh:346`) + 5-minute `expires_at` on both smoke quarantine bodies (eab7b89) | closed |
| T-09-24 | Tampering | disk exhaustion masking readiness | mitigate | `compose-smoke.sh:75-93` statfs pre-flight exits 3 above 0.85 | closed |
| T-09-25 | Information disclosure | smoke logs | mitigate | same controls as T-09-22; recorded evidence is timings/PASS lines only | closed |
| T-09-26 | DoS | leftover quarantine/revocation state | mitigate | `down -v` per profile (09-08-SUMMARY, 09-HUMAN-UAT); trap `compose-smoke.sh:339-352`; non-persistent Redis | closed |
| T-09-27 | Tampering | weakening checks to pass | mitigate | 5s bound unchanged (`compose-smoke.sh:218,232-238`); d752bc4/4cec512 loosen no assertion | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-01 | T-09-04 | Demo-only issuer; per-IP throttle retained, per-username lockout deferred (D-04) | Phase 9 plan / user | 2026-10-11 |
| AR-02 | T-09-05 | Demo default passwords overridable by env; startup warning when defaults used | Phase 9 plan / user | 2026-10-11 |
| AR-03 | T-09-08 | jti is a token identifier, not a bearer secret; the token is never stored | Phase 9 plan / user | 2026-10-11 |
| AR-04 | T-09-10 | Audit read API already behind operator session + RBAC; jti not a credential | Phase 9 plan / user | 2026-10-11 |
| AR-05 | T-09-14 | Quarantine routes keep existing RequireRole(sec-ops) + session/CSRF | Phase 9 plan / user | 2026-10-11 |
| AR-06 | T-09-19 | Docs/tests contain only demo defaults already present in compose | Phase 9 plan / user | 2026-10-11 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-10-11 | 27 | 26 | 1 (T-09-23, low) | gsd-security-auditor |
| 2026-10-11 | 27 | 27 | 0 | orchestrator — T-09-23 fixed in eab7b89; `go test ./tests/compose` ok; live mvp smoke exit 0, WORKLOAD QUARANTINE PASS |

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-10-11
