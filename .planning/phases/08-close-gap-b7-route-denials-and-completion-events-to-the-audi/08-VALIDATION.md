---
phase: 8
slug: close-gap-b7-route-denials-and-completion-events-to-the-audi
status: complete
nyquist_compliant: true
wave_0_complete: true
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

Environment notes: the repo has 17 unrelated unformatted Go files, so the gofmt gate is scoped to Go files changed in this phase: `{ git diff --name-only --diff-filter=d 94cbd2f -- '*.go'; git ls-files --others --exclude-standard -- '*.go'; } | sort -u | xargs -r gofmt -l` must print nothing (94cbd2f is the phase base commit). The live run requires the host disk headroom to be inside the spool gate's own ratio (statfs blocks minus bfree, at most 85.0 percent and at least 10 GB available; this replaced the earlier flat 80 percent rule in commit 7896cac, and `df` Use% reads a few points higher than the gate ratio); a live failure caused by host-disk saturation is an environment failure, not a Phase 8 defect. If "audit spool saturated" appears because the host disk is over 90% full, run with `mkdir -p /dev/shm/aegis-gotmp && TMPDIR=/dev/shm/aegis-gotmp go test ...` and remove the directory afterwards. New tests must inject the quota/statfs rather than depend on real disk usage. `benchmarks/TestPolicyEngine_LatencyBudget` is timing-sensitive; run it in isolation if it fails under load.

---

## Sampling Rate

- **After every task commit:** Run the quick run command
- **After every plan wave:** Run the full suite command, plus `go test -count=1 ./tests/compose/...` whenever compose files or env keys change
- **Before `/gsd:verify-work`:** Full suite green with `-race`, then a live `compose-smoke.sh mvp` (and `distributed` if budget allows) with the new assertions passing
- **Max feedback latency:** 30 seconds for unit checks

---

## Per-Task Verification Map

Measured on HEAD 69f9daf (code identical to the live-run build d400d0b for cmd, internal, deployments, scripts, migrations) on 2026-10-09. Statuses come from the runs recorded in 08-12-SUMMARY.md and, for live rows, from 08-11-SUMMARY.md. Task IDs are `NN-TT` (plan, task).

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 05-01, 07-02 | 05, 07 | 2, 3 | AUD-04 | — | Completion event written to spool with backend status, duration > 0, error code; own `event_id`, same `request_id` as decision row (D-01) | unit (middleware + fake sink) and request-path e2e | `go test -race -count=1 ./internal/audit/ -run 'TestAuditMiddlewareSink|TestE2EAllowedProducesDecisionAndCompletion'` | ✅ | ✅ green |
| 05-02 | 05 | 2 | AUD-04 | — | Upstream failures set `error_code` (UPSTREAM_UNAVAILABLE/TIMEOUT, CLIENT_CANCELED, REQUEST_BODY_TOO_LARGE) and keep decision=allow | unit | `... -run TestWrapUpstreamErrorHandler` | ✅ | ✅ green |
| 05-01, 07-02 | 05, 07 | 2, 3 | AUD-04 | — | Backend 5xx on allowed request stored `allow`, not `deny` | unit and e2e | `... -run 'TestCompletionKeepsAllowOnBackendError|TestE2EBackendFailureKeepsAllow'` | ✅ | ✅ green |
| 01-02 | 01 | 1 | AUD-03 / D-03 | — | Worker stores explicit type; untyped falls back; unknown type falls back | unit (pgxmock arg #2) | `... -run TestAuditWorker_EventType` | ✅ | ✅ green |
| 01-01, 01-02 | 01 | 1 | AUD-03 | — | Poison inputs (NUL, 40-char method, 4 KiB path, 200-char principal) normalised; batch succeeds | unit | `... -run 'TestNormalize|TestAuditWorker_NormalizesPoison|TestE2EPoisonNormalized'` (no test is named exactly `TestNormalize`; the `TestNormalize_*` family is the coverage) | ✅ | ✅ green |
| 02-02, 06-01 | 02, 06 | 1, 2 | AUD-03 | — | Group flush: N concurrent denials -> all frames valid, one fsync per group (count via injected sync hook), no interleaving | unit/stress | `go test -race -count=1 ./internal/audit/ -run TestGroupCommit` | ✅ | ✅ green |
| 02-02 | 02 | 1 | AUD-03 | — | Rotation during batch: tiny `MaxSegmentBytes`; every frame readable across all segments; worker consumes all | unit | `... -run TestGroupCommitRotation` | ✅ | ✅ green |
| 05-01, 07-02 | 05, 07 | 2, 3 | D-07 | — | Denial record is durable before response headers are sent (sink observes `ResponseRecorder` not yet written) | unit and e2e | `... -run 'TestDenialRecordedBeforeResponse|TestE2EDenialDurableBeforeBody'` | ✅ | ✅ green |
| 05-01, 06-02, 07-03 | 05, 06, 07 | 2, 3 | D-08 | — | Completion never delays response: blocking sink -> handler returns within bound; queue-full drops and sets fault | unit and e2e | `... -run 'TestCompletionNonBlocking|TestE2EAllowedNotDelayedByStuckDisk'` | ✅ | ✅ green |
| 02-01, 02-02, 08-01, 10-01 | 02, 08, 10 | 1, 4, 5 | D-09 | — | `AppendPreForward` unchanged: existing spool/worker tests still green; function body sha256 equals 94cbd2f (8bd6714e...3211f); the two `diskSpool.AppendPreForward(` call sites in cmd/gateway/main.go unchanged (0 diff lines) | regression | `go test -race -count=1 ./internal/audit/ ./tests/failure/ ./tests/chaos/ ./tests/dr/` plus `TestWiringAppendPreForwardUntouched` | ✅ | ✅ green |
| 06-01, 07-01, 07-03, 08-03, 11-02 | 06, 07, 08, 11 | 2, 3, 4, 6 | D-10 | — | Shutdown flushes queued denials/completions/summaries before `Close`; late enqueue returns `ErrClosed`, no hang/panic. Live: gateway stop exit 0, 30 of 30 completion rows present (sequential traffic, so not a full-queue-at-SIGTERM proof) | unit, wiring guard, live stop check | `... -run 'TestPipelineShutdown|TestE2EShutdownFlushesQueuedRecords'` and `go test ./cmd/gateway/ -run TestWiringShutdownOrder` | ✅ | ✅ green |
| 02-01, 06-02, 07-03 | 02, 06, 07 | 1, 2, 3 | D-11 | — | Between 90% and 95% (injected statfs/quota): `AppendPreForward` -> `ErrSpoolSaturated`; denial/completion still written; at >= 95% dropped+counted | unit | `... -run TestHardLimitBand` | ✅ | ✅ green |
| 02-01, 06-02, 07-03 | 02, 06, 07 | 1, 2, 3 | D-12 | — | Queue-full / hard-limit / write-error sets fault -> `CheckSaturation()` true -> `AppendPreForward` saturated; recovers without traffic | unit | `... -run TestWriteFaultGate` | ✅ | ✅ green |
| 04-02 | 04 | 2 | D-05 | — | Unauthenticated cap: rate/burst per reason, fake clock, over-cap `Drop` + counter | unit | `... -run 'TestGovernorUnauthCap|TestE2EUnauthCap'` | ✅ | ✅ green |
| 04-01 | 04 | 2 | D-06 / D-13 | — | Suppression: first recorded, repeats counted, summary at window close with correct `suppressed_count`; map bound + overflow; fake clock `Sweep(now)` | unit | `... -run 'TestGovernorSuppression|TestE2ESuppressionAndSummary'` | ✅ | ✅ green |
| 04-02 | 04 | 2 | D-04 | — | Classification table: every reason literal in `cmd/gateway/main.go` (go/parser scan) and every reason in `internal/policy/engine.go` and `policies/rego/authz.rego` is classified | unit (drift guard) | `... -run 'TestEveryMainReasonIsClassified|TestEveryPolicyReasonIsClassified'` | ✅ | ✅ green |
| 03-02 | 03 | 1 | DIST-02 hygiene | — | New metrics exposed, closed label values, no forbidden label keys | unit | `go test -race -count=1 ./internal/telemetry/` | ✅ | ✅ green |
| 03-01 | 03 | 1 | Config | — | New env keys parse, defaults, invalid -> error | unit | `go test -race -count=1 ./internal/config/` | ✅ | ✅ green |
| 09-01, 11-01 | 09, 11 | 4, 6 | AUD-03/AUD-04 live | — | On the real binary: allowed request -> `decision`(status 0) + `completion`(status 200, duration_ms > 0) rows sharing `request_id`; denied request -> one `denial` row (403, deny, duration_ms > 0); unauthenticated -> `denial` 401 anonymous. 08-11-SUMMARY.md records all AUDIT-TYPES PASS lines (no SKIPPED) | live smoke | `bash scripts/compose-smoke.sh mvp` (run at HEAD d400d0b, exit 0) | ✅ | ✅ green |
| 09-02, 11-01 | 09, 11 | 4, 6 | AUD-03 live | — | Replay/dedupe: delete worker `wal.cursor`, restart worker, `count(*)` unchanged (PASS, count 25). Limit: does not distinguish a restart-driven replay from the worker's in-process cursor reload | live smoke | same script, DEDUPE section | ✅ | ✅ green |
| 09-02, 11-01 | 09, 11 | 4, 6 | AUD-03 live | — | Rotation: gateway with small `AEGIS_SPOOL_SEGMENT_BYTES`, enough requests for >= 2 segments, all rows present, count equals expected (PASS, 300 of 300, seq 1 -> 5, not SKIPPED) | live smoke / compose profile | same script, ROTATION section | ✅ | ✅ green |
| 04-01 | 04 | 2 | D-14 / D-15 | — | Outage 503s/500s are recorded once per reason per window regardless of principal (then counted); all identical identified denials once per window; a full suppression map routes new keys to a per-reason overflow bucket (no per-request fsync) | unit | `go test -race -count=1 ./internal/audit/ -run 'TestGovernorSuppression|TestGovernorOutageByReason|TestGovernorOverflowBucket|TestGovernorStashBounded'` | ✅ | ✅ green |
| 06-02 | 06 | 2 | D-12 | — | A recovery probe never clears a fault raised after the probe started (monotonic fault generation) | unit | `go test -race -count=1 ./internal/audit/ -run TestCommitterFaultGenerationRace` | ✅ | ✅ green |
| 03-02, 03-03 | 03 | 1 | D-16 | — | Pre-middleware rejections (concurrency 429, 431, workload bearer 401) increment a low-cardinality counter | unit and wiring guard | `go test -race -count=1 ./internal/telemetry/ ./internal/proxy/` and `go test ./cmd/gateway/ -run TestWiringRejectionRecorder` | ✅ | ✅ green |
| 11-02 | 11 | 6 | D-10 live | — | Graceful stop: gateway exit code 0 on stop (smoke GRACE PASS) and 30 of 30 completion rows after a stop/restart; hermetic drain proof is TestPipelineShutdown / TestE2EShutdownFlushesQueuedRecords | live smoke `--stop-check` | `bash scripts/compose-smoke.sh mvp --stop-check` | ✅ | ✅ green |
| 08-02, 08-03, 09-03 | 08, 09 | 4 | Compose / wiring lint | — | Compose knobs default safe, per-spool worker, stop grace period, smoke script syntax and evidence strings, gateway wiring guards | unit lint | `go test -count=1 ./tests/compose/ ./cmd/gateway/` plus `docker compose -f <mvp|hardened|distributed> config -q` and `bash -n scripts/compose-smoke.sh` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] `internal/audit/governor_test.go` — unauthenticated cap, suppression, bounds, fake clock
- [x] `internal/audit/committer_test.go` / `spool_group_test.go` — group commit, rotation, hard-limit band, write fault (monotonic fault generation), shutdown, `-race` stress (needs injectable statfs and a sync-count hook); `committer_test.go` owns the shared helpers `newTestSpool`, `newTestSpoolControlled`, `readAllEvents`, `fakeRecorder`
- [x] `internal/audit/pipeline_test.go`, `pipeline_e2e_test.go`, `pipeline_e2e_delivery_test.go` — pipeline, request-path end-to-end, worker delivery / degraded-disk / shutdown end-to-end (reuse the shared helpers)
- [x] `internal/audit/logger_test.go` additions — sink ordering, non-blocking completion, upstream error classes, decision-flip fix
- [x] `internal/audit/worker_test.go` additions (new tests write frames with a local `writeWorkerTestFrames` helper, not the statfs-gated spool), `tests/dr/dr_test.go` 20-argument helper, and `event_test.go` — typed events, normalization
- [x] `internal/audit/main_reasons_test.go` — drift guards over `cmd/gateway/main.go` and over `internal/policy/engine.go` / `policies/rego/authz.rego`
- [x] `internal/telemetry/metrics_test.go` and `internal/config/` test additions
- [x] `scripts/compose-smoke.sh` — new audit-type, dedupe-replay and rotation sections
- [x] No framework install needed

---

Every listed file was verified present with `test -f` on 2026-10-09 (`governor_test.go`, `committer_test.go`, `spool_group_test.go`, `pipeline_test.go`, `pipeline_e2e_test.go`, `pipeline_e2e_delivery_test.go`, `logger_test.go`, `worker_test.go`, `event_test.go`, `main_reasons_test.go`, `internal/telemetry/metrics_test.go`, `internal/config/config_test.go`, `tests/dr/dr_test.go`, `scripts/compose-smoke.sh`).

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Crash between insert and cursor save delivers at-least-once against real Postgres | AUD-03 | Needs a precisely timed worker kill; covered at unit level by `TestAuditWorker_RestartRecovery` only | Not planned live; record as residual evidence gap |

---

## Residual evidence gaps

- Crash between the Postgres insert and the cursor save is unit-level evidence only (`TestAuditWorker_RestartRecovery`); it was not exercised against real Postgres.
- Only the MVP profile was run live. The hardened and distributed profiles were not run live (they are covered by `docker compose config -q` and the `tests/compose` lint only).
- The 502 upstream-error and the hard-limit (90 to 95 percent and above) behaviours are unit-level only, as are the review-fix fault paths (disk-error recovery, summary carry, failed-first-denial); none was exercised live.
- Shutdown drain with a non-empty queue is proven hermetically (`TestPipelineShutdown`, `TestE2EShutdownFlushesQueuedRecords`); the live stop check used sequential traffic that had already finished.
- The live DEDUPE check does not distinguish a restart-driven replay from the worker's in-process cursor reload.
- The six Info findings of 08-REVIEW.md (IN-01..IN-06) were not fixed and have no regression tests.

**nyquist_compliant note:** set to true because every row has an automated command with a recorded passing result (hermetic rows on 2026-10-09 at HEAD 69f9daf, live rows from 08-11-SUMMARY.md). That statement is about command coverage, not about the residual gaps above, which are listed rather than hidden.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** executor sign-off from measured results (plan 08-12); the phase verifier remains to confirm
