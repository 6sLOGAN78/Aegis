---
phase: 11
slug: close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-10-11
---

# Phase 11 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution. Source: 11-RESEARCH.md § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test + testify v1, pgxmock v4.9, miniredis v2.34, `-race` |
| **Config file** | none (plain `go test`; Makefile targets exist) |
| **Quick run command** | `go test ./<touched pkg>/... -race -count=1` |
| **Full suite command** | `go vet ./... && go test ./... -race -count=1` |
| **Live command** | `docker compose -f deployments/compose/docker-compose.<p>.yml up -d --build --wait && scripts/compose-smoke.sh <p>` for p in mvp, hardened, distributed (one at a time, `down -v` between) |
| **Estimated runtime** | ~120 seconds for the full suite; ~5–10 minutes per live profile |

---

## Sampling Rate

- **After every task commit:** Run the quick command for the touched packages.
- **After every plan wave:** Run the full suite command.
- **Before `/gsd:verify-work`:** Full suite green, then three live smoke runs (the disk pre-flight must pass).
- **Max feedback latency:** 120 seconds (hermetic)

---

## Per-Task Verification Map

The planner fills in task IDs. Requirement and decision mapping from research:

| Req / Decision | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---|---|---|---|---|---|
| REV-03 / D-01 | Migration creates principal_quarantines and revoked_tokens; Up/Down | unit | `go test ./internal/storage -run SecurityState -race` | ❌ W0 | ⬜ pending |
| REV-03 / D-02 | Postgres-first ordering; Postgres fail → 503 with no Redis write; Redis fail → 503 with row kept; idempotent retry; unquarantine deletes from both | unit | `go test ./internal/control -run 'Quarantine\|Revoke\|WriteThrough' -race` | ❌ W0 | ⬜ pending |
| REV-03 / D-03, D-04 | Reconstruct restores only unexpired rows with remaining TTL, then sets the marker; re-runs on marker loss; reconcile heals | unit | `go test ./internal/control ./internal/revocation ./tests/dr -run 'Reconstruct\|Marker\|Reconcile' -race` | partial | ⬜ pending |
| REV-03 / D-05 | Marker missing → 503 SECURITY_STATE_MISSING on both listeners and /readyz 503; recovers when the marker returns | unit | `go test ./cmd/gateway ./internal/proxy ./internal/revocation -run SecurityState -race` | ❌ W0 | ⬜ pending |
| D-05 classification | Reason classified in the governor table | unit | `go test ./internal/audit -run TestEveryMainReasonIsClassified -race` | ✅ | ⬜ pending |
| D-22, D-26 | Control plane fails at startup without Postgres; /livez and /readyz unauthenticated | unit | `go test ./cmd/control-plane ./internal/control -run 'Livez\|Readyz\|Postgres' -race` | ❌ W0 | ⬜ pending |
| AUTH-01, CTRL-02 / D-07, D-08, D-25 | Reloader: good swap; bad, empty or partial set rejected with last good kept; `..data` layout; non-dot files; SIGHUP; concurrent verify | unit | `go test ./internal/keyset ./tests/rotation -race` | ❌ W0 | ⬜ pending |
| D-09 | Issuer and control plane load keys from files; invalid path fatal; seed fallback warns | unit | `go test ./cmd/demo-issuer ./cmd/control-plane -race` | partial | ⬜ pending |
| D-10 | No kid; token and snapshot formats unchanged | unit | `go test ./internal/identity ./internal/snapshot -race` | ✅ | ⬜ pending |
| CTRL-03, CTRL-05 / D-13..D-16 | Valid client accepted; plaintext and wrong-CA clients rejected (Unavailable); ID mismatch → PermissionDenied on Ack, Pong and heartbeat with nothing recorded; gateway ID from cert; env mismatch fatal | unit (bufconn + in-memory PKI) | `go test ./internal/control ./internal/snapshot ./internal/identity ./cmd/gateway -run 'MTLS\|Identity\|GatewayID' -race` | ❌ W0 | ⬜ pending |
| D-14, D-18 | No insecure gRPC credentials in production code; chaos and integration tests run over mTLS | static + integration | `! grep -rn 'insecure.NewCredentials' --include='*.go' cmd internal && go test ./tests/chaos ./tests/integration -race` | ✅ (rewrite) | ⬜ pending |
| D-17, D-24 | Generator reuses the root CA and emits the control-plane server cert and per-gateway control-plane client certs with the SPIFFE shape | unit | `go test ./scripts/certificates ./internal/pki -race` | ❌ W0 | ⬜ pending |
| B2 / D-19..D-21, D-23 | Env keys read by the binaries, ports and probes match, PVC spools, sidecar worker, secret refs, no key material, POD_NAME selection | hermetic YAML | `go test ./tests/manifests -race` | ✅ (extend) | ⬜ pending |
| D-06, D-11, D-12, D-14 | Live DR FLUSHALL, key rotation drill, authenticated stream on 3 profiles | lint + live | `go test ./tests/compose -race` + `scripts/compose-smoke.sh <p>` | ✅ (extend) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/keyset` (keyset directory loader and reloader) + tests
- [ ] Shared in-memory test PKI helper for mTLS gRPC tests
- [ ] `internal/storage` security-state repo tests (pgxmock), plus a migration Up/Down check
- [ ] `tests/manifests` cross-check tests that derive env keys, ports and probes from the Go source
- [ ] `tests/compose` extensions (new env keys, keyset/cert mounts, smoke sections)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live smoke on 3 profiles | REV-03, AUTH-01, CTRL-02/03/05 | Needs Docker and enough free disk (pre-flight ≤85%) | Run the live command per profile; every DR, KEYROT and mTLS section prints PASS |
| Kubernetes live bring-up | B2 | Deferred (no kind or kubectl on host) | N/A for this phase; hermetic cross-checks are the bar |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 120s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
