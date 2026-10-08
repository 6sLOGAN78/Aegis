---
phase: 8
slug: close-gap-b7-route-denials-and-completion-events-to-the-audi
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-10-09
---

# Phase 8 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` + testify v1.12.1; pgxmock v4.9.0; miniredis v2.34.0; `-race` supported |
| **Config file** | none (go.mod; Makefile targets `test-quick`, `test-failure`, `compose-test`, `compose-smoke`) |
| **Quick run command** | `go test -race -count=1 ./internal/audit/ ./internal/telemetry/ ./internal/config/ ./internal/proxy/` |
| **Full suite command** | `go test -race -count=1 $(go list ./... \| grep -v /tests/integration)` — never `./tests/integration/...`, it starts Docker |
| **Live (real binary)** | `bash scripts/compose-smoke.sh mvp` against a stack brought up with `docker compose ... up -d --build --wait` |
| **Estimated runtime** | ~2-4 seconds quick; full suite under a minute; live check several minutes (image builds) |

Environment notes: if "audit spool saturated" appears because the host disk is over 90% full, run with `mkdir -p /dev/shm/aegis-gotmp && TMPDIR=/dev/shm/aegis-gotmp go test ...` and remove the directory afterwards. New tests must inject the quota/statfs rather than depend on real disk usage. `benchmarks/TestPolicyEngine_LatencyBudget` is timing-sensitive; run it in isolation if it fails under load.

---

## Sampling Rate

- **After every task commit:** Run the quick run command
- **After every plan wave:** Run the full suite command, plus `go test -count=1 ./tests/compose/...` whenever compose files or env keys change
- **Before `/gsd:verify-work`:** Full suite green with `-race`, then a live `compose-smoke.sh mvp` (and `distributed` if budget allows) with the new assertions passing
- **Max feedback latency:** 30 seconds for unit checks

---

## Per-Task Verification Map

Task IDs, plan and wave are filled in once plans exist.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| TBD | TBD | TBD | AUD-04 | — | Completion event written to spool with backend status, duration > 0, error code; own `event_id`, same `request_id` as decision row (D-01) | unit (middleware + fake sink) | `go test -race -count=1 ./internal/audit/ -run TestAuditMiddlewareSink` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-04 | — | Upstream failures set `error_code` (UPSTREAM_UNAVAILABLE/TIMEOUT, CLIENT_CANCELED, REQUEST_BODY_TOO_LARGE) and keep decision=allow | unit | `... -run TestWrapUpstreamErrorHandler` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-04 | — | Backend 5xx on allowed request stored `allow`, not `deny` | unit | `... -run TestCompletionKeepsAllowOnBackendError` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-03 / D-03 | — | Worker stores explicit type; untyped falls back; unknown type falls back | unit (pgxmock arg #2) | `... -run TestAuditWorker_EventType` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-03 | — | Poison inputs (NUL, 40-char method, 4 KiB path, 200-char principal) normalised; batch succeeds | unit | `... -run TestNormalize` and `TestAuditWorker_NormalizesPoison` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-03 | — | Group flush: N concurrent denials -> all frames valid, one fsync per group (count via injected sync hook), no interleaving | unit/stress | `go test -race -count=1 ./internal/audit/ -run TestGroupCommit` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-03 | — | Rotation during batch: tiny `MaxSegmentBytes`; every frame readable across all segments; worker consumes all | unit | `... -run TestGroupCommitRotation` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-07 | — | Denial record is durable before response headers are sent (sink observes `ResponseRecorder` not yet written) | unit | `... -run TestDenialRecordedBeforeResponse` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-08 | — | Completion never delays response: blocking sink -> handler returns within bound; queue-full drops and sets fault | unit | `... -run TestCompletionNonBlocking` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-09 | — | `AppendPreForward` unchanged: existing spool/worker tests still green | regression | `go test -race -count=1 ./internal/audit/ ./tests/failure/ ./tests/chaos/ ./tests/dr/` | ✅ | ⬜ pending |
| TBD | TBD | TBD | D-10 | — | Shutdown flushes queued denials/completions/summaries before `Close`; late enqueue returns `ErrClosed`, no hang/panic | unit | `... -run TestPipelineShutdown` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-11 | — | Between 90% and 95% (injected statfs/quota): `AppendPreForward` -> `ErrSpoolSaturated`; denial/completion still written; at >= 95% dropped+counted | unit | `... -run TestHardLimitBand` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-12 | — | Queue-full / hard-limit / write-error sets fault -> `CheckSaturation()` true -> `AppendPreForward` saturated; recovers without traffic | unit | `... -run TestWriteFaultGate` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-05 | — | Unauthenticated cap: rate/burst per reason, fake clock, over-cap `Drop` + counter | unit | `... -run TestGovernorUnauthCap` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-06 / D-13 | — | Suppression: first recorded, repeats counted, summary at window close with correct `suppressed_count`; map bound + overflow; fake clock `Sweep(now)` | unit | `... -run TestGovernorSuppression` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-04 | — | Classification table: every reason literal in `cmd/gateway/main.go` (go/parser scan) is classified | unit (drift guard) | `... -run TestEveryMainReasonIsClassified` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | DIST-02 hygiene | — | New metrics exposed, closed label values, no forbidden label keys | unit | `go test -race -count=1 ./internal/telemetry/` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | Config | — | New env keys parse, defaults, invalid -> error | unit | `go test -race -count=1 ./internal/config/` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-03/AUD-04 live | — | On the real binary: allowed request -> `decision`(status 0) + `completion`(status 200, duration_ms > 0) rows sharing `request_id`; denied request -> one `denial` row (403, deny, duration_ms > 0); unauthenticated -> `denial` 401 anonymous | live smoke | `bash scripts/compose-smoke.sh mvp` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-03 live | — | Replay/dedupe: delete worker `wal.cursor`, restart worker, `count(*)` unchanged | live smoke | same script, new section | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | AUD-03 live | — | Rotation: gateway with small `AEGIS_SPOOL_SEGMENT_BYTES`, enough requests for >= 2 segments, all rows present, count equals expected | live smoke / compose profile | same script | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-14 / D-15 | — | Outage 503s/500s and all identical identified denials are recorded once per window, then counted | unit | `go test -race -count=1 ./internal/audit/ -run TestGovernorSuppression` | ❌ W0 | ⬜ pending |
| TBD | TBD | TBD | D-16 | — | Pre-middleware rejections (concurrency 429, 431, workload bearer 401) increment a low-cardinality counter | unit | `go test -race -count=1 ./internal/telemetry/ ./internal/proxy/` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/audit/governor_test.go` — unauthenticated cap, suppression, bounds, fake clock
- [ ] `internal/audit/committer_test.go` / `spool_group_test.go` — group commit, rotation, hard-limit band, write fault, shutdown, `-race` stress (needs injectable statfs and a sync-count hook)
- [ ] `internal/audit/logger_test.go` additions — sink ordering, non-blocking completion, upstream error classes, decision-flip fix
- [ ] `internal/audit/worker_test.go` additions and `event_test.go` — typed events, normalization
- [ ] `internal/audit/main_reasons_test.go` — drift guard over `cmd/gateway/main.go`
- [ ] `internal/telemetry/metrics_test.go` and `internal/config/` test additions
- [ ] `scripts/compose-smoke.sh` — new audit-type, dedupe-replay and rotation sections
- [ ] No framework install needed

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Crash between insert and cursor save delivers at-least-once against real Postgres | AUD-03 | Needs a precisely timed worker kill; covered at unit level by `TestAuditWorker_RestartRecovery` only | Not planned live; record as residual evidence gap |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
