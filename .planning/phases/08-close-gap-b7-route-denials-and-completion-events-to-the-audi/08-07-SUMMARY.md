---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 07
subsystem: audit
tags: [audit, pipeline, sweeper, shutdown-ordering, end-to-end, worker-delivery]
requires: ["08-01", "08-02", "08-04", "08-05", "08-06"]
provides:
  - "audit.Pipeline implementing audit.Sink: governor + committer + sweeper + ordered shutdown"
  - "audit.PipelineConfig, audit.NewPipeline(spool, rec, cfg) (contract for plan 08 wiring)"
  - "Hermetic end-to-end evidence: real AuditMiddleware -> Pipeline -> DiskSpool -> AuditWorker (pgxmock)"
affects: [08-08]
tech-stack:
  added: []
  patterns: ["injected clock constructor so sweeper tests never race on the clock field", "recovery probe hook used to hold the fail-closed window open deterministically"]
key-files:
  created:
    - internal/audit/pipeline.go
    - internal/audit/pipeline_test.go
    - internal/audit/pipeline_e2e_test.go
    - internal/audit/pipeline_e2e_delivery_test.go
  modified: []
key-decisions:
  - "Pipeline.RecordDenial checks a closed flag first so a suppressed denial after Shutdown is counted dropped{denial, closed} instead of vanishing into a flushed window"
  - "newPipelineClock(spool, rec, cfg, now) is the injected-clock constructor; NewPipeline calls it with time.Now. This avoids a data race on p.now between the sweeper goroutine and tests"
  - "Sweeper interval is max(window/4, 1s) computed from the configured window with the governor's 60s default"
requirements-completed: []
duration: ~40min
completed: 2026-10-09
---

# Phase 8 Plan 07: Audit Pipeline and end-to-end proof Summary

`audit.Pipeline` joins the governor (what to write), the committer (how to write it durably) and the middleware Sink seam. It records, suppresses or drops each denial per the governor, forwards completions ungoverned to the committer's non-blocking queue, turns closed suppression windows into summary rows via a background sweeper, and on Shutdown flushes every open window into the committer before the committer stops intake. Three test files then drive the real middleware, pipeline, spool and an AuditWorker over pgxmock. AUD-03 and AUD-04 are intentionally not marked complete here (plan 08-12 decides from live evidence).

## Tasks and commits

| Task | Commit | Files |
|------|--------|-------|
| 1. Pipeline: governor + committer + sweeper + ordered shutdown | 7bfb708 | internal/audit/pipeline.go, internal/audit/pipeline_test.go |
| 2. Shared e2e harness and request-path scenarios | e125f1d | internal/audit/pipeline_e2e_test.go |
| 3. Delivery, stuck-disk and shutdown scenarios | af8fed4 | internal/audit/pipeline_e2e_delivery_test.go |

## Names defined for plan 08 (package audit)

- `type PipelineConfig struct { Governor GovernorConfig; Committer CommitterConfig }`
- `func NewPipeline(spool *DiskSpool, rec Recorder, cfg PipelineConfig) *Pipeline` (starts the sweeper; nil Recorder becomes a no-op)
- `(*Pipeline).RecordDenial(ev *CompletionEvent)`, `EnqueueCompletion(ev *CompletionEvent)`, `QueueDepth() int`, `Shutdown(ctx) error` (idempotent, second call returns nil); `var _ Sink = (*Pipeline)(nil)`
- Unexported: `newPipelineClock`, `p.now`, `p.sweepOnce(now)`, `p.sweepDone`
- Drop reason string `"unauth_cap"` is emitted for governor Drop; `"closed"` after Shutdown.
- Test helpers (do not redeclare): `e2eClock`/`newE2EClock`, `pipelineT0`, `pipelineFast`, `pipelineStart`, `pipelineDenial`, `pipelineShutdown`, `e2eOptions`, `e2eEnv`, `newE2E`, `e2eRequest`, `e2eClientIP`, `e2eProblem`, `e2eAllowHandler`, `e2eDenyHandler`, `e2eByRequest`, `e2eOfType`, `e2eSplit`, `e2eOrderWriter`, `e2eEffectiveType`.

Plan 08 must call `NewPipeline` with the `*telemetry.Metrics` as Recorder, pass the pipeline to `audit.WithSink`, wire `WrapUpstreamErrorHandler` onto the reverse proxy error handler (still not done by plans 05/07), and call `Pipeline.Shutdown` BEFORE the spool is closed.

## What the tests prove

- Pipeline unit tests (7): first identified denial durable on return, repeats suppressed and counted per label, unauth cap drops with `unauth_cap` and writes nothing, suppressor overflow produces exactly one extra row plus a summary of 19 with 20 overflow counts, sweeper writes a summary only after the window closes (first event's request_id, new event_id), Shutdown writes 20 queued completions plus the pending summary and a second call is a no-op, 1000 identical completions pass through ungoverned, nil events are safe.
- End-to-end request path (9 tests): decision plus completion pair with distinct event_ids and shared request_id equal to the X-Request-ID header; backend 502 keeps decision allow with UPSTREAM_UNAVAILABLE; one denial row per denied request; denial already fsynced when the response header is first written; 25 identical denials -> 1 row + summary(24), 25 distinct principals -> 25 rows; 40-principal outage -> 1 row + summary(39); 200 anonymous 401s -> 50 rows and 150 dropped{denial, unauth_cap}; NUL path and 28-char method normalized while the stdout log keeps the raw values; X-Forwarded-For / X-Real-IP ignored.
- Delivery and degradation (3 tests): a real AuditWorker over pgxmock inserts every frame (decision, completion, denial, anonymous denial, summary with suppressed_count) with the expected event_type and suppressed_count args, and the cursor ends at the latest segment's size; with fsync blocked every allowed response stays under 50ms and returns 200, the queue overflow sets the `queue_full` write fault, `AppendPreForward` then returns `ErrSpoolSaturated` (and a pre-forward handler request gets 503) while the disk is working again, and the fault clears without traffic; Shutdown drains 30 queued completions and the summary, and requests after Shutdown get their normal responses with `closed` drops counted.

## Verification

All `go test` runs used `TMPDIR=/dev/shm/aegis-gotmp` (removed afterwards).

- `go test -race -count=1 -v ./internal/audit/ -run TestPipeline`: 7 PASS; request-path tests: 9 top-level PASS; delivery tests: 3 PASS.
- Flake checks of `TestE2E|TestPipeline` with -race: `-count=30` pass; `-count=10 -cpu 1,2,8` pass; `-count=15` while 8 busy-loop processes saturated the CPUs pass. 0 failures observed in about 700 runs of the new tests. `go test -race -count=3 ./internal/audit/` passes.
- `go vet ./internal/audit/` clean; `gofmt -l internal/audit` empty.
- `go test -race -count=1 ./internal/audit/ ./internal/proxy/ ./internal/telemetry/ ./internal/config/ ./tests/failure/ ./tests/security/ ./tests/chaos/ ./tests/dr/`: all ok.
- D-09: sha256 of the `AppendPreForward` body equals the hash from `git show 94cbd2f:internal/audit/spool.go` (8bd6714e...). `git diff --name-only -- cmd` is empty.
- Acceptance greps: `var _ Sink = (*Pipeline)(nil)` present once; in Shutdown `Flush(` precedes `com.Shutdown(`; `time.Now()` appears 0 times in pipeline.go (only the bare `time.Now` default); no `NewDiskSpool(` and no redeclared shared helper in the new test files.

## Cross-plan fixes

None. No end-to-end assertion exposed a defect in the components of plans 01 to 06, so no `fix(08-07)` commit exists and no file outside this plan's own four files was modified.

## Deviations from Plan

1. [Process] Tests were written in the same pass as the implementation rather than captured red first (Task 1). They do assert the specified behaviors; the e2e tests were green on first run against the earlier plans' components.
2. [Design addition] `Pipeline.RecordDenial` checks a `closed` flag set at the start of Shutdown and counts `dropped{denial, closed}`. Without it a repeat denial arriving after the final Flush would be counted as suppressed into a window that is never summarised. Also added `newPipelineClock` (injected clock) so tests do not race with the sweeper goroutine on `p.now`; `p.now` remains the field the plan names.
3. [Test adjustment] The stuck-disk test does not call `AppendPreForward` while fsync is blocked, because `AppendPreForward` takes the spool mutex before consulting saturation and so blocks behind the held fsync (noted by 08-06, D-09 behavior). Instead it asserts `WriteFaulted` and `CheckSaturation` during the block, then holds the committer's recovery just before the fault clears (via the existing `setProbeHook` seam) and asserts `ErrSpoolSaturated` and a 503 from a pre-forward handler there, then releases the hook and asserts the fault clears and `AppendPreForward` succeeds. Same coverage, deterministic.
4. [Test choice] The 150-drop unauth test and others use the shared frozen `e2eClock`, passed through `newPipelineClock`.
5. No existing test was changed or weakened.

## Residual notes for later plans

- 08-06 notes were respected: nothing in the tests assumes the write-error fault exists at the instant a failed denial returns, and no test depends on a timed-out waiter's frame being absent.
- `Pipeline.Shutdown` returns the committer's error (context deadline); if ctx expires the sweeper has already stopped and windows were already flushed.
- A denial racing exactly with the start of Shutdown can be admitted before the closed flag is set and then be suppressed after Flush; its count is lost from summaries but it is counted in `aegis_audit_suppressed_total`. Accepted as a shutdown-boundary edge.

## Known Stubs

None.

## Threat Flags

None. No new network, auth or file-access surface; production change is the single in-process pipeline.

## Self-Check: PASSED

- internal/audit/pipeline.go, pipeline_test.go, pipeline_e2e_test.go, pipeline_e2e_delivery_test.go exist.
- Commits 7bfb708, e125f1d, af8fed4 present on main.
