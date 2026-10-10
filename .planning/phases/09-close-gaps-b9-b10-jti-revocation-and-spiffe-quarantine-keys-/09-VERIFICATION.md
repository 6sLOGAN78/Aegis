---
phase: 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
verified: 2026-10-11T00:00:00Z
status: human_needed
score: 5/5 must-haves verified (code level); live evidence predates review fixes
overrides_applied: 0
human_verification:
  - test: "Re-run `bash scripts/compose-smoke.sh` on mvp, hardened and distributed against images rebuilt from current HEAD (after 4cec512)"
    expected: "All Phase 9 sections PASS on every profile; per-replica deny times <= 5s. Host disk statfs ratio must be under 0.85 (free space or prune build cache first)."
    why_human: "The live smoke ran on images from HEAD 7671781, BEFORE review fixes CR-01 (control-plane principal id classification), WR-01 (gateway SPIFFE canonicalisation), WR-02 (TOKEN_REVOKED governor suppression key) and WR-06 (smoke poller). Needs Docker, which this verification may not start."
  - test: "Decide how to handle the distributed AUDIT-TYPES suppression failures (2 hard failures)"
    expected: "Either fix the harness to send DENY1/DENY2 to a single replica, or accept as a pre-existing Phase 8 harness issue so distributed exits 0."
    why_human: "Not a Phase 9 regression (governor suppression is per gateway process; HAProxy is round-robin), but it keeps `compose-smoke.sh distributed` at exit 1, so plan 09-08 truth D-14 (zero FAIL on distributed) is not literally met."
  - test: "Run tests/integration (TestMVPEndToEnd, TestBackendBypassPrevention) with Docker"
    expected: "Pass with username/password login helpers"
    why_human: "Needs Docker; only compile-checked."
---

# Phase 9: Close gaps B9/B10 (JTI revocation, SPIFFE quarantine keys) Verification Report

**Phase Goal:** Demo issuer requires seeded username/password and issues a jti; JTI revocation and workload (SPIFFE) quarantine are denied on every gateway replica within 5s, proven live on mvp, hardened and distributed.
**Status:** human_needed
**Re-verification:** No, initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Demo issuer authenticates seeded username/password (constant-time compare) and issues a jti | VERIFIED (code) | `cmd/demo-issuer/main.go` uses `subtle.ConstantTimeCompare`; smoke `dev_login` reads `.jti` from the response |
| 2 | Gateway fails closed on a validated token with no jti, before revocation check (TOKEN_MISSING_JTI) | VERIFIED | `cmd/gateway/main.go:39-51` `missingJTI`/`rejectMissingJTI`; class registered in `internal/audit/governor.go:62` |
| 3 | Audit events carry jti (CompletionEvent.JTI, omitempty, clipped to 128, persisted by worker) | VERIFIED | `internal/audit/event.go:65-93`, `logger.go:142 SetJTI`, `worker.go:127` |
| 4 | Control plane quarantine/unquarantine path handlers PathUnescape the SPIFFE id so the Redis key matches the gateway's; body-form route exists | VERIFIED | `internal/control/quarantine_handler.go:32`; spec documents `/control/v1/quarantine` body form (control-v1.yaml:329-351) |
| 5 | JTI revoke and SPIFFE quarantine denied on every replica within 5s, live on three profiles | PARTIAL / needs human | 09-08 live run: mvp, hardened exit 0; distributed all Phase 9 sections PASS (<=0.55s per replica) but exit 1 from two Phase 8 AUDIT-TYPES assertions. Evidence predates review fixes. |

**Score:** 4 verified in code, truth 5 depends on live re-run.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build | `go build ./...` | ok | PASS |
| Vet | `go vet ./...` | ok | PASS |
| Hermetic tests | `go test $(go list ./... \| grep -v /tests/integration)` | no failures (only "no test files" lines) | PASS |

Live smoke / Docker not run by this verifier (instructed not to start Docker).

### Requirements Coverage

| Requirement | Source Plans | Status | Evidence |
|-------------|-------------|--------|----------|
| REV-03 | 09-01,02,03,04,05,07,08 | SATISFIED in code; live re-confirmation pending | jti issuance, missing-jti deny, audit jti, SPIFFE key fix, smoke sections |
| AUTH-04 | 09-01,06,07,08 | SATISFIED (seeded credential login) | demo-issuer login; integration helpers updated (compile-checked only). Login throttling unchanged from Phase 1. |
| OPS-01 | 09-03,04,06,07,08 | PARTIAL by design | Quarantine/revocation routes documented and working; remaining OPS-01 spec drift out of scope |

All three IDs appear in plan frontmatter and in REQUIREMENTS.md (traceability rows at lines 118, 132, 141). No orphaned requirements. REQUIREMENTS.md checkboxes remain unchecked (`[ ]`) and the file is untracked; the orchestrator should update them at phase close.

### Anti-Patterns Found

None: no TBD/FIXME/XXX markers found in `cmd`, `internal`, or `scripts/compose-smoke.sh`.

### Gaps Summary

No code-level gaps. Status is human_needed because (1) the live proof predates four review fixes touching the exact paths being proven, (2) distributed smoke exits 1 due to a pre-existing harness assumption, and (3) integration tests were only compile-checked.

---

_Verified: 2026-10-11_
_Verifier: Claude (gsd-verifier)_
