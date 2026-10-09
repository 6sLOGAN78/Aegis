---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
verified: 2026-10-09T11:00:00Z
status: human_needed
score: 6/6 success criteria verified (SC1-SC5 by code and hermetic tests re-run here; SC6 and the live parts of SC1-SC3 are recorded-not-reproduced evidence)
overrides_applied: 0
re_verification: false
gaps: []
deferred: []
human_verification:
  - test: "Accept (or reproduce) the recorded live MVP run in 08-11-SUMMARY.md as evidence for SC6, AUD-03 and AUD-04"
    expected: "On a rebuilt MVP stack with AEGIS_SPOOL_SEGMENT_BYTES=65536 and AEGIS_AUDIT_SUPPRESS_WINDOW=5s, `SMOKE_SUPPRESS_WINDOW_SECS=5 SMOKE_ROTATION_REQUESTS=300 bash scripts/compose-smoke.sh mvp --stop-check` exits 0 with every AUDIT-TYPES, DEDUPE and ROTATION line PASS and no SKIPPED"
    why_human: "The verifier was forbidden to start Docker or run the smoke script. The run is corroborated (local compose-* images were built at 16:03 IST, after the last fix commit 05f1dcf at 15:58 and about at the d400d0b commit at 16:00; no code change under cmd/internal/deployments/scripts/migrations/tests between d400d0b and HEAD; the quoted smoke output is internally consistent with the script text) but not reproduced."
  - test: "Optional, closes the one AUD-04 wording gap: stop the orders backend, send one allowed request through :8080, read the completion row"
    expected: "A `completion` row with decision=allow, http_status=502, error_code=UPSTREAM_UNAVAILABLE (or UPSTREAM_TIMEOUT), duration_ms>0, and its `decision` pair with a different event_id"
    why_human: "Needs a running stack. No completion row with a non-empty error_code was ever observed live; those classes are hermetic only."
  - test: "Optional, closes the 'both listeners' live gap: one denied and one allowed request through the workload mTLS listener :9443"
    expected: "A `denial` row (for example UNAUTHORIZED_NO_CERT or DENIED_WORKLOAD_FORBIDDEN) and a `completion` + `decision` pair appear in audit_events for requests made on :9443"
    why_human: "The smoke script only drives :8080. The :9443 path reaches the pipeline only by construction (same AuditMiddleware + WithSink, enforced by an AST guard), never by a live or request-level test."
  - test: "Decision for the requirements owner: accept AUD-03 as 'satisfied with stated limits' for the MVP profile"
    expected: "Owner confirms that hardened/distributed live runs, a live crash between insert and cursor save, and a live full-queue shutdown drain are not required for the AUD-03 checkbox; otherwise revert AUD-03 only (see Requirements Coverage)"
    why_human: "Scoping judgement, not something a grep can settle."
---

# Phase 8: Close gap B7 (route denials and completion events to the audit spool) Verification Report

**Phase Goal:** Make the gateway write denial records and completion records (backend HTTP status, duration, error code) into the durable disk spool so the existing audit worker delivers them to PostgreSQL with an explicit record type, closing v1.0 audit blocker B7 (`audit.NewLogger(nil)` sent completions and every denial to stdout only). Fail closed when audit is losing data, bound spool use by denial floods, harden the worker against hostile field values, and show the result on the real binary with a dedupe-replay and a multi-segment rotation run.
**Verified:** 2026-10-09
**Status:** human_needed (no gaps; the live run is recorded-not-reproduced and four small human items remain)
**Re-verification:** No, initial verification

## Evidence classes used in this report

- **Reproduced here:** `go build ./...`, `go vet ./...`, `go test -race -count=1 $(go list ./... | grep -v /tests/integration)` under `TMPDIR=/dev/shm/aegis-gotmp` (26 packages ok, exit 0, including `benchmarks` first time, so no timing flake), a targeted re-run of 38 named regression tests (all PASS, 0 FAIL, 0 SKIP), gofmt on files changed since 94cbd2f (clean), `bash -n` on the smoke script, `docker compose config -q` for mvp, hardened and distributed (all exit 0), the D-09 hash comparison, source reading. `/dev/shm/aegis-gotmp` removed afterwards.
- **Recorded, not reproduced:** the live MVP run in 08-11-SUMMARY.md. Corroboration only: `compose-*` images exist locally, built 16:03 IST on 2026-10-09 (after fix commit 05f1dcf at 15:58 and d400d0b at 16:00); `docker ps -a | grep -c '^compose-'` = 0 and `docker volume ls | grep -c '^compose_'` = 0 (stack torn down as claimed); `git diff --stat d400d0b HEAD -- cmd internal deployments scripts migrations tests` is empty.

## Goal Achievement: success criteria (ROADMAP contract)

| # | Success criterion | Status | Evidence |
|---|---|---|---|
| 1 | Allowed request: unchanged `decision` row plus separate `completion` row (own event_id, same request_id, status, duration_ms>0, any error code); denied request: one `denial` row (decision=deny, reason, status, duration) | VERIFIED (error-code-on-completion hermetic only) | Code: `logger.go` `buildAuditEvent` mints a fresh `uuid.NewString()` EventID, sets `EventTypeCompletion` / `EventTypeDenial`; pre-forward event is `ToCompletionEvent` with `EventTypeDecision`, status 0. Worker `worker.go:113-119` stores the explicit type, falling back only for untyped/unknown. Hermetic: `TestE2EAllowedProducesDecisionAndCompletion`, `TestE2EDenialSingleRow`, `TestE2EBackendFailureKeepsAllow`, `TestAuditWorker_EventType` PASS. Live (recorded): 686 rows = 341 completion + 341 decision + 4 denial; pair `bf5a1e60` completion allow 200 7.761 ms / decision allow 0 0 ms with distinct event ids; denial 403 FORBIDDEN 0.938 ms. |
| 2 | Identified-principal rejections recorded once per window per (principal, route, reason), then counted in a `suppressed_count` summary; outage reasons keyed by reason only; full map -> per-reason overflow bucket; unauthenticated 400/401 rate-capped and counted; pre-middleware rejections metrics only | VERIFIED (live only for the identified suppression + summary and one anonymous 401) | `governor.go`: `reasonTable` classes; `Admit` routes `classOutage` to `govKeyOutage(reason)` (reason only), non-anonymous to `govKeyIdentified` (principal, route, reason), anonymous to token buckets; `admitSuppress` falls to `g.overflow[label]` when `len(entries) >= MaxKeys`, never records unconditionally; `Pipeline.RecordDenial` counts `Suppress` and drops `Drop` with `aegis_audit_records_dropped_total{reason=unauth_cap}`. D-16: `server.go` / `limiter.go` diffs add only `rejections.record(...)` calls. Hermetic PASS: `TestGovernorSuppression`, `TestGovernorOutageByReason`, `TestGovernorOverflowBucket`, `TestGovernorUnauthCap`, `TestE2ESuppressionAndSummary`, `TestE2EOutageKeyedByReason`, `TestE2EUnauthCap`, `TestEveryMainReasonIsClassified`, `TestEveryPolicyReasonIsClassified`. Live (recorded): DENY2 produced no row, summary row suppressed_count=1 carrying DENY1's request id, anonymous 401 denial row. Outage/overflow/unauth-cap behaviour is hermetic only. |
| 3 | Denials fsynced (group flush) before the response; completions queued off the request path; pre-forward allow keeps synchronous fsync; queued records flushed on graceful shutdown | VERIFIED (shutdown drain with a non-empty queue hermetic only) | `logger.go` `commitHeader` runs `onFirstHeader` -> `sink.RecordDenial` -> `Committer.SubmitDenial` (blocks until fsync, 2 s waiter bound) before `ResponseWriter.WriteHeader` on the `WriteHeader`, `Write` and `Flush` paths; `EnqueueCompletion` is a non-blocking select on a bounded channel; `AppendPreForward` body hash identical to 94cbd2f; `main.go:976-980` `auditPipeline.Shutdown` after `dualServer.Shutdown` and before `diskSpool.Close()`. Tests PASS: `TestDenialRecordedBeforeResponse`, `TestDenialRecordedBeforeImplicitHeader`, `TestE2EDenialDurableBeforeBody`, `TestCompletionNonBlocking`, `TestE2EAllowedNotDelayedByStuckDisk`, `TestGroupCommit*`, `TestPipelineShutdown`, `TestE2EShutdownFlushesQueuedRecords`, `TestWiringShutdownOrder`. Live (recorded): gateway stop exit 0, 30 of 30 completion rows (sequential, already-finished traffic, so not a full-queue-at-SIGTERM proof). |
| 4 | Between 90% and 95% only denial/completion written; past hard limit dropped and counted; completion loss trips the gate (503, `/readyz` unready); recovers without inbound traffic | VERIFIED (hermetic only, as the ROADMAP itself states) | `spool.go`: `WriteFrames` gated by `checkHardLimit` (default 0.95, validated > 0.90 and <= 0.99), never by `CheckSaturation`; `CheckSaturation` returns true while `writeFault` or `needsRotate`; `main.go:934` passes `diskSpool.IsSaturated` to the probe handler so `/readyz` follows. `committer.go`: `EnqueueCompletion` queue-full -> `setFault`; `ErrHardLimit` with a completion in the batch -> `setFault(hard_limit)`; write error with retained completions -> `setFault(write_error)` plus a backoff retry loop that clears on success; the run-loop ticker calls `recoveryProbe()` (queue under half, hard limit not exceeded, monotonic `faultGen`) with no request needed. PASS: `TestHardLimitBand`, `TestWriteFaultGate`, `TestCommitterFaultGenerationRace`, `TestE2E*` degraded-disk scenarios. Not exercised live (stated). |
| 5 | Field values normalized at construction and again in the worker | VERIFIED | `event.go` `Normalize()` (NUL strip, invalid UTF-8 drop, rune-safe clip to column widths, roles capped 64 x 128 B, UUID canonicalization, 2048 B path) called in `ToCompletionEvent`, the middleware hook and deferred path, `Committer.frame`, `Governor.newEntry`, and `worker.go:92` per event; worker sends `canonicalUUID` text and replaces unparseable ids. PASS: `TestNormalize_*`, `TestNormalize_PrincipalRoles`, `TestNormalize_IDs`, `TestAuditWorker_NormalizesPoison`, `TestAuditWorker_NormalizesRolesAndLenientUUIDs`, `TestAuditWorker_BoundsRoles`, `TestE2EPoisonNormalized`. |
| 6 | On the rebuilt MVP stack: typed rows by request id, suppression summary row, unchanged row count after deleting `wal.cursor` and restarting the worker, every row delivered across >= 2 rotations; crash window unit-level; hardened/distributed not live | VERIFIED as recorded-not-reproduced; assertions read and judged non-vacuous (see Smoke script review) | 08-11-SUMMARY.md verbatim output: all AUDIT-TYPES lines PASS, `DEDUPE ... count unchanged at 25`, `ROTATION 300 requests delivered across >=2 segment rotations (seq 1 -> 5)`, `GRACE ... exited 0`, `0 hard failure(s)`. Not reproduced by this verifier (constraint). |

**Score:** 6/6 criteria verified; none FAILED, none lacking an implementation.

## Point-by-point answers to the requested checks

### 1. B7 itself: both listeners, pipeline not stdout-only, explicit type

- `main.go:107` still reads `audit.NewLogger(nil)`, but that logger is now **only** the stdout JSON log (`LogCompletion`). Durable records go through a separate sink: `main.go:134` builds `audit.NewPipeline(diskSpool, metrics, ...)` and `main.go:928` and `main.go:938` pass `audit.WithSink(auditPipeline)` to `AuditMiddleware` for the user and the workload handler. `Sink` is an interface, so `NewLogger(nil)` can no longer silently mean "durable record dropped": with a sink, the deferred function calls `RecordDenial` (deny) or `EnqueueCompletion` (allow) in addition to the stdout line. `TestWiringAuditMiddlewareHasSink` (AST) fails the build if either call site loses the third argument.
- Denial path: `onFirstHeader` -> `Pipeline.RecordDenial` -> `Governor.Admit` -> `Committer.SubmitDenial` -> one `DiskSpool.WriteFrames` (single write + single fsync) -> frame format identical to `AppendPreForward` (`frameEvent`). Completion path: `EnqueueCompletion` -> bounded channel -> same writer. The worker reads the frames unchanged and stores `event_type` as given.
- **Not live on the workload listener.** The smoke script (grep for 9443/mtls/workload: no match) only drives :8080. The :9443 listener reaches the pipeline by construction (same middleware function, same sink, workload handler sets `SetDecision` on every deny, lines 629-884) and the AST guard, but no request-level test and no live run exercises it. Recorded as a WARNING and an optional human item, not a gap: the code path is identical.
- Real-binary reach on :8080 is shown live (recorded) by `aegis_audit_records_written_total{kind=completion}`=11 and `{kind=denial}`=3 on the gateway's own metrics endpoint plus rows in Postgres.

### 2. Requirement verdicts (AUD-04 and AUD-03)

See the Requirements Coverage table. Short form: both checkboxes are **earned for the MVP profile**; AUD-04 with one named wording gap (upstream-failure error codes never seen live), AUD-03 as "satisfied with stated limits". I do not recommend reverting either; the orchestrator should keep the paper trail explicit about the limits (the existing 08-12-SUMMARY and 08-VALIDATION text already does).

### 3. Locked decisions D-01..D-18

| D | Decision | Verdict | Code / test evidence |
|---|---|---|---|
| D-01 | Two linked rows; completion has its own event_id | HONORED | `buildAuditEvent` new UUID per event; `ToCompletionEvent` separate UUID; live pair `0bd480e4` / `48a8d5a7`, same request id; `TestE2EAllowedProducesDecisionAndCompletion` |
| D-02 | Denied request -> one row with deny, reason, status, duration | HONORED | `buildAuditEvent` sets Decision/ReasonCode/HTTPStatus/DurationMS; live denial 403 0.938 ms; `TestE2EDenialSingleRow` |
| D-03 | Explicit type set by gateway, stored as given, fallback for untyped | HONORED | `EventType*` constants set in `ToCompletionEvent` / `buildAuditEvent`; `worker.go:113-119`; `TestAuditWorker_EventType` |
| D-04 | Identified rejections recorded durably (revoked, quarantined, no route, 429, 403, fail-closed 503) | HONORED | `reasonTable` classIdentified entries; drift guards `TestEveryMainReasonIsClassified` / `TestEveryPolicyReasonIsClassified` PASS |
| D-05 | Unauthenticated 400/401 capped per gateway, dropped and counted | HONORED | `admitBucket` per reason; `Drop` -> `RecordAuditDropped(denial, unauth_cap)`; config bounds on rate 0.001-1000 and burst 1-10000 (WR-05); `TestGovernorUnauthCap`, `TestE2EUnauthCap` |
| D-06 | Rate-limit 429 suppressed per principal and route per window with a count row | HONORED | `RATE_LIMIT_EXCEEDED` classIdentified; `Sweep` / `Flush` emit summaries; `TestE2ESuppressionAndSummary`; summary seen live |
| D-07 | Denial appended (fsync, group flush) before the response | HONORED on all three paths | `commitHeader` on `WriteHeader`, `Write` and `Flush`; `TestDenialRecordedBeforeResponse`, `TestDenialRecordedBeforeImplicitHeader` (Write, Flush, 1xx-then-Write subtests), `TestE2EDenialDurableBeforeBody`. Residual: `Hijack`/panic paths bypass (IN-01) |
| D-08 | Completion off request path, bounded queue, batched fsync | HONORED | `enqueueAsync` non-blocking select; `TestCompletionNonBlocking`, `TestE2EAllowedNotDelayedByStuckDisk` |
| D-09 | Pre-forward allow keeps synchronous fsync; not weakened | HONORED | I ran the comparison myself: `awk` extraction of `func (s *DiskSpool) AppendPreForward(` from `git show 94cbd2f:internal/audit/spool.go` and from the working tree, both `sha256 8bd6714ed39699f787957a339bc9c5f08e09c580dd91d11296fb2e8888f3211f`. `git diff 94cbd2f -- cmd/gateway/main.go` contains no `AppendPreForward` line; call sites are still at `main.go:556` and `:866`. Read the body: write, `Sync()`, error on either. Context: helpers it calls changed on purpose (`CheckSaturation` now also true under `writeFault`/`needsRotate`; `rotateSegment` opens the next segment first). Both only make it refuse more, never skip an fsync. `TestWiringAppendPreForwardUntouched` PASS |
| D-10 | Graceful shutdown flushes queued records before spool close | HONORED | `main.go:963-996` order drain -> `auditPipeline.Shutdown` (5 s) -> `diskSpool.Close`; `Pipeline.Shutdown` flushes governor windows then `Committer.Shutdown` -> `finalDrain`; `TestWiringShutdownOrder`, `TestPipelineShutdown`, `TestE2EShutdownFlushesQueuedRecords` |
| D-11 | 90% gate stops allowed traffic; denial/completion write to a hard limit | HONORED | `WriteFrames` uses `checkHardLimit` only; `TestHardLimitBand` |
| D-12 | Completion loss trips admission; recovery without traffic | HONORED | `setFault` -> `CheckSaturation` true -> unchanged `AppendPreForward` returns `ErrSpoolSaturated` (503) and `/readyz` unready; recovery by ticker `recoveryProbe`, by the retry loop (`write_error`), and by `RepairSegment` for `needsRotate`; monotonic `faultGen` (`TestCommitterFaultGenerationRace`). Not live |
| D-13 | Saturation 503s recorded like rate-limit denials in the headroom | HONORED | `AUDIT_SPOOL_SATURATED` is classIdentified (decision set at `main.go:559` / `:869`); writes go through `WriteFrames` (hard limit) |
| D-14 | Outage reasons keyed by reason only (first per reason per window) | HONORED | `POLICY_LEASE_EXPIRED`, `UNINITIALIZED`, `DEPENDENCY_OUTAGE_REDIS`, `AUDIT_SPOOL_WRITE_ERROR` classOutage; `Admit` checks outage before principal; `govKeyOutage` uses the reason only; `TestGovernorOutageByReason`, `TestE2EOutageKeyedByReason` |
| D-15 | All identical identified denials once per window, then counted; no allowance knob | HONORED | `govKeyIdentified` = principal + route + reason; overflow bucket per reason instead of per-request recording; `grep` of `config.go` shows no allowance / burst-per-key setting for identified denials (only the unauthenticated rate/burst and window/max-keys) |
| D-16 | Pre-middleware rejections: metrics counter only, no reordering | HONORED | `limiter.go` and `server.go` diffs add only `rejections.record("concurrency" / "header_too_large" / "ambiguous_credentials")`; `DualServer.SetRejectionRecorder(metrics)` at `main.go:943`; `TestWiringRejectionRecorder`, `TestConcurrencyLimiter_RejectionRecorder`; closed label set |
| D-17 | Live dedupe replay + multi-segment rotation | HONORED (recorded live, MVP only) | `scripts/compose-smoke.sh` DEDUPE and ROTATION sections; `SpoolSegmentBytes` config (>= 4096) wired through `main.go:111` and the compose knob `AEGIS_SPOOL_SEGMENT_BYTES` |
| D-18 | Normalization at construction and in the worker | HONORED | See SC5; WR-04 extended it to roles JSONB, ids and the header request id; middleware replaces non-canonical inbound `X-Request-ID` |

### 4. Review fixes (CR-01, WR-01..WR-07) and Info findings

- Every regression test named in 08-REVIEW-FIX.md exists (grep for each `func TestX(` returned a file) and passes in my re-run: `TestWriteFramesDriftNeverCutsAcknowledgedFrames`, `TestWriteFramesSyncFailureKeepsPossiblyIngestedBytes`, `TestWriteFramesRotationFailureAfterDurableBatch`, `TestWriteFramesUnrotatableTornTailRefusesAppendUntilRepaired`, `TestCommitterTickRepairsUnrotatableSegment`, `TestPipelineFailedFirstDenialDoesNotMaskRepeats`, `TestPipelineFailedUnauthDenialRefundsToken`, `TestGovernorForget*`, `TestGovernorUnwrittenWindowSummaryDoesNotPointAtMissingRow`, `TestPipelineSummaryCarriedWhenQueueFull`, `TestPipelineSummaryCarryBoundedAndCounted`, `TestPipelineShutdownWaitsForQueueRoomForSummaries`, `TestDenialRecordedBeforeImplicitHeader`, `TestNormalize_PrincipalRoles`, `TestNormalize_IDs`, `TestAuditWorker_NormalizesRolesAndLenientUUIDs`, `TestAuditWorker_BoundsRoles`, `TestAuditMiddlewareRequestIDHeader`. The config bound (WR-05) is in `config.go` (`maxAudit*`, `minAuditUnauthRate`) and covered by `internal/config` tests (package ok).
- Did a fix break a locked decision? I read the fixed code. `WriteFrames` rotation-before-write and rotate-after-failure keep D-11 (hard-limit gated, not 90% gated) and D-12 (`needsRotate` makes the unchanged `AppendPreForward` refuse; repaired from the committer tick). `Governor.Forget` undoes a failed first denial without breaking D-15 (success path unchanged; a timed-out denial that is later written can lead to a duplicate row, which errs toward recording). The summary carry (`sumMu`, `maxCarry` = 4 x MaxKeys, oldest dropped and counted) keeps D-10 (shutdown waits for queue room until ctx). D-09 hash identical after all fixes. No locked decision is broken.
- One property I could not prove from the fix reports: the fix reports mark CR-01 / WR-01 "requires human verification" for the logic. The tests pass and I read the logic; I found no defect, but concurrency-sensitive code with no live fault run remains judgement-based.
- Info findings vs success criteria: none undermines a criterion.
  - IN-01 (panic/hijack paths audit a fabricated 200): inaccurate row for aborted requests or 101 upgrades; no SC is about those paths; WARNING-level tech debt.
  - IN-02 (work before the unauth cap) and IN-03 (double count on `ErrDenialTimeout`): efficiency and metrics accuracy only; SC2's bound on spool use still holds because the cap precedes the write.
  - IN-04 (unbounded `AEGIS_DRAIN_TIMEOUT`, stuck fsync can hold `spool.Close()`): pre-existing shape; compose uses 30 s + 5 s inside a 45 s grace.
  - IN-05 (weak smoke assertions): see next section; less weak in combination than stated.
  - IN-06 (metrics recorder trusts callers for closed labels): all in-repo callers use `ReasonLabel` / constants; negative-label tests pass.

### 5. Smoke script review: can the assertions pass vacuously?

- **AUDIT-TYPES allowed pair:** waits for exactly 2 rows for the request id, then compares the full sorted tuple string `completion|allow|200|1` / `decision|allow|0|0` and `count(distinct event_id)=2`. Not vacuous.
- **Denial row:** exact tuple `denial|deny|403|1|1|FORBIDDEN`, filtered on `suppressed_count is null`. Not vacuous.
- **Suppressed repeat:** `sleep 3` then 0 rows for DENY2's id. Alone this could pass if delivery were slow, but it is paired with the summary assertion `suppressed_count=1` on DENY1's request id; if DENY2 had been recorded as its own row the summary would have count 0 and not exist. The pair is a sound proof of suppression (it is also the case that the first denial row had already been delivered, so the worker was current). The "also assert the suppression counter" part of IN-05 is therefore not needed for the verdict.
- **DEDUPE:** requires a non-empty cursor segment and offset > 0 before, quiesces the count over 3 samples, `rm -f` the cursor in the worker container, `docker compose restart`, waits for the cursor to reappear (the cursor is rewritten only by `SaveOffset`, which runs only after `br.Exec()` / `br.Close()` succeeded, so a reappearing cursor means a batch was re-sent to Postgres), sleeps 5 s, and compares counts. It is not vacuous. On the restart-versus-in-process question: `cursor.go` `LoadOffset` reads `wal.cursor` from disk on every `ProcessAvailable` tick (no in-memory cache), so deleting the file forces a replay from offset 0 at the next 200 ms tick whether or not the process restarts. The replay itself (25 frames re-sent, `ON CONFLICT (event_date, event_id) DO NOTHING` absorbing them on real Postgres) is what was proven; the restart is incidental. That limit narrows the claim "worker-restart replay" but not the dedupe claim.
- **ROTATION:** 300 requests all 200 with a request id (the script fails if any 200 lacks an id, and prints non-200 count, which was 0), then `count(distinct request_id)` of completion rows for exactly those ids must equal 300, segment sequence taken from the worker's cursor must advance by >= 2 (1 -> 5), and `/readyz` must be 200. Non-vacuous. IN-05's "does not fail on non-200" is real for the script text but did not matter in this run (0 non-200).
- Gaps in the script that matter only for strength: it drives only :8080 (no workload listener), and no upstream-failure request.
- `TestSmokeScript*` are substring lints (hermetic); they prove the script contains the assertions, not that they pass.

### 6. Honesty of the paper trail

- 08-11-SUMMARY.md and 08-12-SUMMARY.md state the limits plainly (profiles not run live, crash window unit-only, shutdown drain hermetic, DEDUPE restart-vs-reload undistinguished, fault paths not live). No claim of jti, SPIFFE encoding, B8, B5/B6, B2 closure found in any 08-* file (grep for "closes B8 / B5 / B2 / jti closed" returned nothing). ROADMAP.md Phase 8 text says AUD-03/AUD-04 "ticked only for what the live evidence recorded in plan 08-11 proves".
- 08-VALIDATION.md sets `nyquist_compliant: true` and `wave_0_complete: true`; the file itself explains this means "every row has an automated command with a recorded passing result" and lists a "Residual evidence gaps" section. I find this acceptable and not an over-claim, with one correction: its "Approval" line says "the phase verifier remains to confirm", which this report now does.
- `.planning/REQUIREMENTS.md` (untracked file) has AUD-03 and AUD-04 ticked at lines 53-54 and the traceability rows 136-137 changed to `Complete`. Earned for MVP scope (below). The traceability table does not carry the stated limits; the limits live in the 08-12-SUMMARY and 08-VALIDATION text.
- Minor scope note: `web/dashboard/src/api/types.ts`, `api/openapi/control-v1.yaml` and `pkg/api/control/v1/types.gen.go` each gained the `denial` enum value (plan 08-01, "denial in the event_type contract"). That is a contract widening, not a change to how the dashboard displays rows; consistent with the plan, noted for completeness.
- `STATE.md` shows "Phase complete - ready for verification"; no over-claim.

## Required Artifacts

| Artifact | Expected | Status | Details |
|---|---|---|---|
| `internal/audit/pipeline.go` | Sink: governor + committer + sweeper + ordered shutdown | VERIFIED | 201 lines, `var _ Sink = (*Pipeline)(nil)`, used at `main.go:134` |
| `internal/audit/committer.go` | Group-commit writer, bounded queues, fault handling | VERIFIED | 651 lines, substantive, wired through Pipeline |
| `internal/audit/governor.go` | Suppression, buckets, overflow, Forget | VERIFIED | 465 lines, closed reason table with drift guards |
| `internal/audit/spool.go` `WriteFrames`, hard limit, fault flag | Batch write path, `AppendPreForward` body unchanged | VERIFIED | hash identical to 94cbd2f |
| `internal/audit/logger.go` | Sink seam, denial before first byte, error-code wrapper | VERIFIED | `WrapUpstreamErrorHandler` assigned on both proxies (`main.go:617`, `:921`; AST-guarded) |
| `internal/audit/worker.go` | 20-arg insert, explicit type, normalization | VERIFIED | `suppressed_count` bound as NULL when 0 |
| `internal/audit/event.go` | Type constants, `Normalize()` | VERIFIED | |
| `internal/config/config.go` | Bounded audit keys with safe defaults | VERIFIED | defaults 2 ms / 25 ms / 8192 / 0.95 / 10 / 50 / 60 s / 4096 / 16 MiB |
| `internal/telemetry/metrics.go` | written / dropped / suppressed / overflow / depth / degraded / flush / rejected | VERIFIED | closed labels, `InitAuditSuppressedLabels` |
| `internal/proxy/{limiter,server}.go` | Rejection counter only | VERIFIED | |
| `migrations/000003_add_audit_suppressed_count.sql` | Additive nullable column | VERIFIED | live: column count 1 (recorded) |
| `scripts/compose-smoke.sh` | AUDIT-TYPES, DEDUPE, ROTATION | VERIFIED | `bash -n` ok; read in full |
| `deployments/compose/docker-compose.mvp.yml` | Segment-size and window knobs with safe defaults | VERIFIED | `:-16777216` and `:-60s` defaults; `config -q` ok for all three profiles |

## Key Link Verification

| From | To | Via | Status |
|---|---|---|---|
| `main.go` user handler | pipeline | `audit.AuditMiddleware(auditLogger, 0, audit.WithSink(auditPipeline))` (`:928`) | WIRED |
| `main.go` workload handler | pipeline | same (`:938`) | WIRED (code/AST only, not exercised) |
| middleware hook | spool | `RecordDenial` -> `SubmitDenial` -> `WriteFrames` | WIRED |
| middleware deferred | spool | `EnqueueCompletion` -> `completionCh` -> `WriteFrames` | WIRED |
| fault flag | admission + `/readyz` | `setFault` -> `CheckSaturation` -> unchanged `AppendPreForward` and `diskSpool.IsSaturated` at `:934` | WIRED |
| both reverse proxies | audit error codes | `rp.ErrorHandler = audit.WrapUpstreamErrorHandler(...)` (`:617`, `:921`) | WIRED |
| `DualServer` | metrics | `SetRejectionRecorder(metrics)` (`:943`) | WIRED |
| shutdown | spool close | drain -> `auditPipeline.Shutdown` -> `diskSpool.Close` | WIRED (AST-guarded order) |
| worker | Postgres | `SendBatch`, cursor saved only after `br.Close()` succeeds | WIRED |

## Data-Flow Trace (Level 4)

| Artifact | Data | Source | Real data | Status |
|---|---|---|---|---|
| `audit_events.event_type/http_status/duration_ms/error_code/suppressed_count` | CompletionEvent fields | `buildAuditEvent` from live `AuditContext` and measured `time.Since(start)` | Yes (recorded live rows with non-zero durations and typed rows) | FLOWING |
| `aegis_audit_*` counters | committer / governor callbacks | real `*telemetry.Metrics` passed at `main.go:134` | Yes (recorded live: completion 11, denial 3) | FLOWING |

## Behavioral Spot-Checks and Probe Execution

| Behavior | Command | Result | Status |
|---|---|---|---|
| Build | `go build ./...` | exit 0 | PASS |
| Vet | `go vet ./...` | exit 0 | PASS |
| Hermetic suite | `TMPDIR=/dev/shm/aegis-gotmp go test -race -count=1 $(go list ./... \| grep -v /tests/integration)` | 26 packages ok, EXIT=0, `benchmarks` ok (3.477 s) | PASS |
| 38 named regression / decision tests | `go test -race -count=1 -v ./internal/audit/ -run '<names>'` | 38 `--- PASS`, no FAIL/SKIP | PASS |
| D-09 hash | extract `AppendPreForward` body at 94cbd2f and at HEAD, `sha256sum` | identical `8bd6714e...3211f` | PASS |
| gofmt on phase-changed Go files | `gofmt -l` over files changed since 94cbd2f | empty | PASS |
| Smoke script syntax; compose configs | `bash -n`; `docker compose -f ... config -q` x3 | ok; ok/ok/ok | PASS |
| Live smoke / integration | not run (constraint) | recorded only | SKIPPED (human item) |

Probe execution: the phase declares no `scripts/*/tests/probe-*.sh`; none exist. Not applicable.

## Requirements Coverage

Plan frontmatter: every plan 08-01..08-12 declares AUD-03 and AUD-04 (08-12 explicitly leaves `requirements-completed: []`). REQUIREMENTS.md contains both IDs and maps no other requirement ID to Phase 8 (the traceability table lists AUD-03 under Phase 3 and AUD-04 under Phase 1, both reopened as gap closure). No orphaned requirement found.

| Requirement | Wording | Verifier verdict | Checkbox earned? |
|---|---|---|---|
| AUD-04 | Completion audit events record backend HTTP status, request duration, and error codes | **SATISFIED for the MVP profile, with one named limit.** Status and duration: observed live (completion 200, 7.761 ms; denial 403, 0.938 ms) and in real Postgres. Error codes: observed live only on **denial** rows (`FORBIDDEN`, `UNAUTHORIZED`, `PRINCIPAL_QUARANTINED`), which proves the `error_code` column, the event field and the worker mapping work end to end on the real binary. The **upstream-failure** classes on completion rows (`UPSTREAM_UNAVAILABLE`, `UPSTREAM_TIMEOUT`, `CLIENT_CANCELED`, `REQUEST_BODY_TOO_LARGE`) were never observed live: they are proven only by `TestWrapUpstreamErrorHandler`, `TestE2EBackendFailureKeepsAllow` (reads spool frames, not Postgres) and the AST guard that both proxies install the wrapper. Every live completion row had an empty `error_code`. I judge that sufficient because the only live-unproven link is the wrapper installation, which is a one-line assignment guarded at source level and uses the same `AuditContext` -> event -> worker path the live denial codes exercised; but it is a real, nameable gap and the optional human check (stop a backend, one request) would close it cheaply. | **Yes** (keep `[x]`) |
| AUD-03 | Asynchronous audit worker delivers spooled records to PostgreSQL with at-least-once batching and deduplication | **SATISFIED WITH STATED LIMITS (MVP profile).** Live, recorded: delivery of four row kinds on real Postgres (686 rows, completion count equals decision count, nothing lost), the 20-argument insert and migration 000003 on a real database, dedupe by full replay from offset 0 absorbed by the real `(event_date, event_id)` constraint, 300 of 300 completions delivered across seq 1 -> 5. This answers the audit's "SQL/migrations only ever run against pgxmock" evidence for the MVP path, and B3 (one worker per spool, cursors advance) was closed live in Phase 7, B7 here. Code reading confirms the at-least-once ordering (cursor saved only after batch commit succeeded; `ProcessAvailable` resumes from the persisted cursor). Not live: crash between insert and cursor save (`TestAuditWorker_RestartRecovery`, unit-level); hardened and distributed profiles with Phase 8 code (three gateways, three spools, three workers: Phase 7 proved the wiring live on the pre-Phase-8 binary, the Phase 8 gateway code is identical across profiles and the compose diff for distributed is comments only); shutdown drain under load (hermetic); DEDUPE does not separate restart from in-process reload (immaterial for the dedupe claim, see section 5). These are limits on strength of evidence, not failures. "Satisfied with stated limits" is the accurate label; plain "SATISFIED" would overstate it, "needs human verification" is not warranted for the checkbox because nothing observed contradicts it. | **Yes, conditionally**: keep `[x]`, but the limits must stay visible. If the requirements owner insists on a live distributed run or a live crash test, **revert AUD-03 only** (set line 53 to `[ ]` and row 136 to `Gap closure`). I would not revert it. |

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|---|---|---|---|---|
| (all phase-changed non-test Go, SQL, YAML, shell files) | | `TBD` / `FIXME` / `XXX` / `TODO` / `HACK` grep | none found | no debt markers |
| `internal/audit/logger.go` | 475-493 | deferred path builds an event from `captureWriter.StatusCode` (default 200) on panic or after `Hijack` (IN-01) | WARNING | a panicking or hijacked/upgraded request is audited as an allowed completion with status 200 |
| `internal/audit/committer.go` | 205-207 | `SubmitDenial` timeout counts a drop but the frame may still be written (IN-03) | WARNING | metrics double count; also lets the governor's `Forget` undo a window for a row that later lands (errs toward recording) |
| `internal/audit/pipeline.go` / `logger.go` | | expensive work (UUID, Normalize, stdout log) before the unauthenticated cap (IN-02) | INFO | CPU under a flood of unauthenticated requests, spool use still bounded |
| `cmd/gateway/main.go` | 947-951 | `AEGIS_DRAIN_TIMEOUT` unbounded relative to the compose grace (IN-04) | INFO | pre-existing; stuck fsync can reach SIGKILL |
| `scripts/compose-smoke.sh` | rotation block | does not fail on non-200 (IN-05) | INFO | 0 non-200 in the recorded run |
| `internal/telemetry/metrics.go` | | recorder trusts callers for label set (IN-06) | INFO | all callers use closed constants |
| `cmd/gateway/wiring_test.go` | | AST-level guard only; `main()` is not executed by any test | WARNING | the real wiring of both listeners is proven only by source inspection plus the live :8080 run |

## Human Verification Required

### 1. Recorded live run

**Test:** Treat 08-11-SUMMARY.md as evidence or reproduce it: `docker compose -f deployments/compose/docker-compose.mvp.yml down -v --remove-orphans`, then `AEGIS_SPOOL_SEGMENT_BYTES=65536 AEGIS_AUDIT_SUPPRESS_WINDOW=5s docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build --wait`, then `SMOKE_SUPPRESS_WINDOW_SECS=5 SMOKE_ROTATION_REQUESTS=300 bash scripts/compose-smoke.sh mvp --stop-check`.
**Expected:** exit 0, `0 hard failure(s)`, no `SKIPPED` lines.
**Why human:** verifier constraint (no Docker mutation); same disposition as Phase 7's recorded-not-reproduced live evidence.

### 2. Upstream-failure error code on a completion row (optional)

**Test:** with the MVP stack up, stop the `orders` service, send one authorized `GET /api/orders` on :8080, query `audit_events` for its request id.
**Expected:** `completion|allow|502|error_code=UPSTREAM_UNAVAILABLE` and a separate `decision` row.
**Why human:** needs a running stack; closes the only AUD-04 wording gap.

### 3. Workload listener :9443 (optional)

**Test:** make one denied and one allowed request with a workload client certificate on :9443 and query by request id.
**Expected:** typed `denial` / `completion` / `decision` rows.
**Why human:** needs a running mTLS stack and client cert; the smoke script never touches :9443.

### 4. AUD-03 scoping decision

**Test:** requirements owner decides whether "satisfied with stated limits (MVP profile)" is acceptable.
**Expected:** accept, or require live distributed / crash-window evidence and revert AUD-03.
**Why human:** scoping judgement.

## Gaps Summary

No blocking gaps. Every success criterion has an implementation that I read and, where hermetic, re-ran; all locked decisions D-01..D-18 are honored in code, including the D-09 hash identity. The status is `human_needed` rather than `passed` only because (a) the live run that SC6 and the AUD-03/AUD-04 verdicts lean on is recorded-not-reproduced, (b) two small live checks (upstream-failure error code on a completion row; the :9443 listener) were never done and are cheap, and (c) the AUD-03 "with stated limits" label needs the requirements owner's acceptance.

## Tech debt carried forward (not gaps)

- IN-01 panic/hijack/101 paths audit a fabricated 200; IN-02 pre-cap work; IN-03 timeout double count; IN-04 unbounded drain vs grace; IN-05 smoke ROTATION non-200 leniency and substring lint tests; IN-06 recorder label trust. None has a regression test.
- `main()` is an inline closure: wiring is proven by AST guards, not by running the pipeline; extracting it is explicitly out of scope.
- Hardened and distributed profiles not run live with Phase 8 code; a live crash between insert and cursor save; live hard-limit band; live fault-injection of the review-fix paths; live full-queue shutdown drain.
- Out of scope and untouched (not claimed): `jti` in the demo issuer, percent-encoded SPIFFE quarantine keys, B8, B5/B6, B2, multi-directory audit worker, dashboard display of new rows.
- Process: `.planning/REQUIREMENTS.md` is an untracked file carrying the two ticks; the orchestrator decides how to commit it. `docker` images `compose-*` (about 2 GB) remain on the host by design.

---

_Verified: 2026-10-09_
_Verifier: Claude (gsd-verifier)_
