---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
reviewed: 2026-10-09T00:00:00Z
depth: standard
files_reviewed: 15
files_reviewed_list:
  - internal/audit/committer.go
  - internal/audit/pipeline.go
  - internal/audit/governor.go
  - internal/audit/logger.go
  - internal/audit/spool.go
  - internal/audit/event.go
  - internal/audit/worker.go
  - cmd/gateway/main.go
  - internal/config/config.go
  - internal/telemetry/metrics.go
  - internal/proxy/limiter.go
  - internal/proxy/server.go
  - migrations/000003_add_audit_suppressed_count.sql
  - scripts/compose-smoke.sh
  - deployments/compose/docker-compose.mvp.yml
findings:
  critical: 1
  warning: 7
  info: 6
  total: 14
status: issues_found
---

# Phase 08: Code Review Report

**Reviewed:** 2026-10-09
**Depth:** standard
**Files Reviewed:** 15 (plus tests read for reliability)
**Status:** issues_found

## Summary

The pipeline is well structured. I traced the lock graph (governor mutex, fault mutex and spool mutex are never nested), the shutdown and double-close paths, and the fault generation counter. I found no deadlock, no data race and no unbounded blocking on the request path.

- `go test -race` on `internal/audit`, `config`, `telemetry`, `proxy` and `cmd/gateway` passes, and `internal/audit` passed 8 runs in a row.
- The body of `AppendPreForward` is byte-identical to 94cbd2f (D-09 holds).
- Fault recovery does not depend on traffic, and a stale probe cannot clear a newer fault.
- The suppression map is bounded.
- Metric labels come from closed sets.
- Migration 000003 is additive and idempotent.
- The compose-smoke.sh SQL type-checks by inspection.

The one serious defect is in the new `WriteFrames` torn-write handling. It trusts a size counter that the unchanged `AppendPreForward` lets drift, so a later failure can truncate acknowledged frames. The remaining findings are real but narrower:

- a failed first denial still opens a suppression window;
- suppression summaries are dropped without retry;
- the D-07 ordering hook has holes;
- the D-18 normalization has gaps;
- config ranges allow the unauthenticated cap to be switched off.

## Critical Issues

### CR-01: WriteFrames truncates to a tracked size that AppendPreForward lets drift, which can cut acknowledged frames

**File:** `internal/audit/spool.go:416` (drift source: `spool.go:330-337`, unchanged AppendPreForward)
**Issue:** On a write or fsync error, `WriteFrames` calls `Truncate(s.currentSize)`. `currentSize` is only advanced after a fully successful write and sync. `AppendPreForward` returns early on a failed `Write` (possibly partial, such as ENOSPC mid-frame) or a failed `Sync` (the frame is in the file) without updating `currentSize`. After either failure the real file size is `currentSize + k` and the counter is wrong by k.

Failure scenario:
1. A pre-forward append hits a partial ENOSPC write of k bytes. This is likely at 90-95% disk, the exact region this phase targets.
2. A later `WriteFrames` batch succeeds. The file is now `N + k + B` bytes but `currentSize` is `N + B`.
3. A subsequent `WriteFrames` batch fails, or its fsync fails. `Truncate(N + B)` removes the last k bytes of the previous durably acknowledged batch.
4. The last frame of that batch now has a bad CRC or length. The worker resyncs past it, so an acknowledged denial or completion record is lost, and a completion lost this way is never re-counted as a fault.

This also corrupts the torn-write guarantee ("no appending after partial bytes"). No test covers AppendPreForward failing followed by WriteFrames failing.

**Fix:** Do not trust the counter in the error path. D-09 forbids touching `AppendPreForward`, so fix `WriteFrames`: take the true base offset from the file before writing, and re-sync `currentSize` from it.
```go
base := s.currentSize
if fi, serr := s.activeFile.Stat(); serr == nil {
    base = fi.Size() // authoritative; also repairs drift left by AppendPreForward
    s.currentSize = base
}
// ... write + sync ...
if werr != nil {
    if terr := s.activeFile.Truncate(base); terr != nil { /* rotate as today */ }
}
```
Add a test that injects a failed `AppendPreForward` write and then a failed `WriteFrames`, and asserts that earlier frames still decode.

## Warnings

### WR-01: A failed first denial still opens the suppression window, so the denial is masked for the whole window

**File:** `internal/audit/pipeline.go:105-111`, `internal/audit/governor.go:236-250`
**Issue:** `Admit` creates the window entry and returns `Record` before the write is attempted. `RecordDenial` ignores the `SubmitDenial` result (`_ = p.com.SubmitDenial(ev)`). If the write fails (queue full, `ErrHardLimit`, timeout, write error or closed), the key is already "recorded". Every identical denial for the next 60s is counted as suppressed and no row exists. When the window closes, the summary row carries the first event's `request_id`, which was never written.

Failure scenario: during a transient disk stall or at the hard limit, the first 403 for (principal, route, reason) is lost. The principal's following 403s are suppressed and counted, so the audit trail has a summary pointing at a missing row. The same applies to unauthenticated bucket tokens, which are consumed even when the write fails.

**Fix:** Let the governor learn the outcome.
```go
case Record:
    if err := p.com.SubmitDenial(ev); err != nil {
        p.gov.Forget(ev)   // delete the entry (or set count=0 and windowStart=zero) so the next occurrence is Recorded
    }
```
Alternatively, have `Admit` return a commit callback.

### WR-02: Suppression summaries are the only record of suppressed counts, but are dropped on a full queue and never retried

**File:** `internal/audit/pipeline.go:88-92`, `internal/audit/committer.go:221-249`, `internal/config/config.go` (queue size minimum 64)
**Issue:** `sweepOnce` and the shutdown `Flush` call `EnqueueSummary`, which is non-blocking and drops the summary when `completionCh` is full. The governor has already deleted the window, so the count is lost permanently. A sweep or flush can emit up to `MaxKeys` (default 4096) plus overflow and stash summaries in one burst. `AEGIS_AUDIT_COMPLETION_QUEUE_SIZE` is allowed down to 64, and under load the queue is shared with completions. The loss is metered only as `denial/queue_full` and does not set the fault.

**Fix:** Route summaries through the denial queue (bounded, with a blocking send that has a timeout). Alternatively, make the governor keep the stashed window until `EnqueueSummary` reports success. Also require `QueueSize >= MaxKeys` at config validation, or give summaries their own channel.

### WR-03: D-07 "denial recorded before any response byte" has holes in StatusCaptureResponseWriter

**File:** `internal/audit/logger.go:320-332` (`Write` and `Flush`), `logger.go:459-477` (deferred path)
**Issue:** The `onFirstHeader` hook only runs from `WriteHeader`.
- `Write` with no prior `WriteHeader` sets `wroteHeader = true` and forwards the bytes with an implicit 200, never calling the hook. The comment says "Write ensures WriteHeader(http.StatusOK) is called" but it does not.
- `Flush()` before a header sends an implicit 200 the same way.
- A handler that sets `SetDecision("deny", ...)` and then only `Write`s therefore emits response bytes before the durable record. The event is built with `StatusCode` 200, so the row also reports the wrong status.
- No current gateway handler does this (all use `WriteProblemDetails`, which calls `WriteHeader`), so this is a latent hole in the stated ordering guarantee.

**Fix:**
```go
func (rw *StatusCaptureResponseWriter) Write(b []byte) (int, error) {
    if !rw.wroteHeader { rw.WriteHeader(http.StatusOK) }
    return rw.ResponseWriter.Write(b)
}
func (rw *StatusCaptureResponseWriter) Flush() {
    if !rw.wroteHeader { rw.WriteHeader(http.StatusOK) }
    ...
}
```

### WR-04: D-18 normalization gaps: roles JSONB, unbounded request_id, and lenient uuid.Parse can still wedge the worker

**File:** `internal/audit/event.go:55-71`, `internal/audit/worker.go:93-100`, `internal/audit/logger.go:422`
**Issue:** Any single value that Postgres rejects fails the whole `SendBatch`. The cursor then never advances and the worker retries the same batch forever. Three paths are not covered by `Normalize`:
1. `PrincipalRoles` is not normalized. `json.Marshal` renders a NUL in a role as `\u0000`, and Postgres JSONB rejects it ("unsupported Unicode escape sequence"). Role count and length are also unbounded, so the frame size is unbounded. Roles come from signed JWT claims, so this needs a lenient or compromised issuer, but the D-18 goal is that no field can wedge the worker.
2. `RequestID` and `EventID` are validated with `uuid.Parse`, which accepts forms Postgres does not, namely `urn:uuid:<uuid>`. Postgres accepts only canonical, braced and bare-hex forms. A request id of that shape passes the check and then aborts the batch.
3. `AuditMiddleware` trusts the inbound `X-Request-ID` header, with no format or length check. Today `proxy/server.go` ingress overwrites it with a fresh UUID before the audit middleware runs, so this is not reachable from the network now. It is one refactor away, and a request id of up to ~1 MB would be framed verbatim into the WAL.

**Fix:**
- Normalize or cap roles in `Normalize` (strip NUL, cap count and element length).
- In the worker, canonicalize with `if u, err := uuid.Parse(x); err == nil { x = u.String() } else { x = uuid.NewString() }`.
- In `AuditMiddleware`, accept an inbound id only when `uuid.Parse` succeeds and `len == 36`, otherwise generate one.
- Longer term, make `ProcessBatch` quarantine a poison event instead of failing forever.

### WR-05: Config ranges allow the unauthenticated denial cap and the suppression map to be effectively disabled or unbounded

**File:** `internal/config/config.go` (`loadAuditConfig`: `AEGIS_AUDIT_UNAUTH_RATE`, `AEGIS_AUDIT_UNAUTH_BURST`, `AEGIS_AUDIT_SUPPRESS_MAX_KEYS`, `AEGIS_AUDIT_COMPLETION_QUEUE_SIZE`)
**Issue:** The task asked for ranges that prevent unsafe settings.
- `UNAUTH_RATE` only checks `r > 0`. `strconv.ParseFloat` accepts `Inf` and `1e308`. With `Inf`, `admitBucket` refills to burst on every call, so the D-05 cap vanishes and every 401/400 pays an fsync.
- `UNAUTH_BURST` goes up to `MaxInt32`, which has the same effect.
- `SUPPRESS_MAX_KEYS` goes up to `MaxInt32`. Each entry holds a full event copy (path up to 2 KiB plus roles), so memory is effectively unbounded and the "bounded map" property is configurable away.
- `COMPLETION_QUEUE_SIZE` goes up to 1,000,000 frames, which at ~1 KiB each is about 1 GB of memory.

**Fix:** Add finite upper bounds and reject non-finite values, for example `math.IsInf(r, 0) || r > 1000`, burst ≤ 10_000, max keys ≤ 1_000_000 (and consider the memory cost), queue ≤ 100_000.

### WR-06: Truncating after a failed fsync can race the worker and misalign its cursor

**File:** `internal/audit/spool.go:414-421`, `internal/audit/worker.go:269-330`
**Issue:** The worker tails the active segment concurrently. If the write succeeds and `Sync` fails, the whole batch is already readable. The worker can ingest it and save a cursor past it. `WriteFrames` then truncates to the old size, and the committer retries only the non-denial frames. The denial frames are dropped from the retry, so the rewritten bytes are shorter than what the worker already consumed. The cursor now sits beyond EOF or inside a later frame. The worker sees a bad magic, resyncs, and can skip a real frame whose magic fell inside the 12 header bytes it consumed. This is a narrow window (needs an fsync error and a worker tick inside it), but it silently loses ingestion of acknowledged records.

**Fix:** After a failed sync, do not truncate away frames that may already have been read. Keep the bytes and rotate to a fresh segment (frames are self-describing, and duplicates are absorbed by `ON CONFLICT`). That makes the retry append to a new file and removes the offset hazard.

### WR-07: Failed rotation leaves a closed active file and misreports a durable batch as failed

**File:** `internal/audit/spool.go:426-430`, `spool.go:434-452` (`rotateSegment`)
**Issue:** `rotateSegment` closes the active file before opening the next one. If `OpenFile` fails (EMFILE, ENOSPC), `s.activeFile` is a closed handle.
- `WriteFrames` returns `failed to rotate segment after batch` even though the batch was written and fsynced. Waiting denial callers are told it failed. Retained completions are retried and written twice. The duplicates are absorbed by `ON CONFLICT`, but the metrics and the denial result are wrong.
- `AppendPreForward` has no recovery. It fails with a write error on every allowed request until some `WriteFrames` error path rotates again. Allowed traffic stays refused (fail-closed), but recovery depends on denial traffic arriving.

**Fix:** Open the new segment first, then swap and close the old one. Treat a rotation failure after a good batch as success for the batch, with a logged or metered warning.

## Info

### IN-01: Panic and hijack paths record a fabricated HTTP status

**File:** `internal/audit/logger.go:459-477`
**Issue:** The deferred recorder runs on panic. With no header written, `StatusCode` is still the default 200. An aborted request with an allow decision is therefore audited as a 200 completion. A 101 upgrade (hijack via `Unwrap`) is likewise recorded as 200.
**Fix:** Track whether a header was written, and record the status as 0 or 500 when `recover()` shows a panic. Re-panic after recording.

### IN-02: Expensive work happens before the unauthenticated cap

**File:** `internal/audit/logger.go:447-455`, `logger.go:462`; `pipeline.go:105`
**Issue:** For every denial, the code builds the full event (`uuid.NewString()` reads crypto/rand), runs `Normalize` over up to 2 KiB of path, and writes a JSON line to stdout (`LogCompletion`) before `Admit` can drop it. `buildAuditEvent` also runs for every allowed response in the header hook and is discarded. This is small per request and the stdout line is pre-existing, but a flood of 401s pays the full cost before the cap.
**Fix:** Run a cheap pre-check (the bucket decision on reason and kind) before building the event, or build the allowed-path event lazily.

### IN-03: dropTimeout and written can double-count one denial

**File:** `internal/audit/committer.go:205-207`
**Issue:** `SubmitDenial` returns `ErrDenialTimeout` and counts the denial as dropped (timeout), but the frame stays queued and is later written and counted as written.
**Fix:** Document it, or count timeouts under a separate "late" metric.

### IN-04: Shutdown budget and spool.Close can still hang

**File:** `cmd/gateway/main.go:947-990`
**Issue:** 30s drain + 5s audit + 5s metrics fits the 45s `stop_grace_period` only with the default drain timeout. `AEGIS_DRAIN_TIMEOUT` is still unvalidated and unbounded. If `auditPipeline.Shutdown` times out because the committer is stuck in a hung fsync, `diskSpool.Close()` then blocks on the spool mutex that the stuck `WriteFrames` holds. The process is killed at the grace period (137).
**Fix:** Cap `AEGIS_DRAIN_TIMEOUT` to the compose grace budget, and have `Close()` not wait forever (for example `TryLock` with a timeout).

### IN-05: compose-smoke.sh assertions that can pass weakly

**File:** `scripts/compose-smoke.sh` (ROTATION block, and the suppressed-repeat check)
**Issue:** The SQL itself reads correctly against migrations 000002 and 000003, with no type, quoting or column-name errors that I could find.
- ROTATION counts non-200 responses (`rot_non200`, such as 429s or 503s) but does not fail on them, so a rate-limited run can PASS having delivered far fewer rows than claimed.
- The suppressed-repeat check passes on "0 rows for the repeat id" after a 3s sleep. That equally holds if the denial was lost entirely, and only the later summary check disambiguates.
- `TestSmokeScript*` in `tests/compose/smoke_script_test.go` are substring greps, not behavior tests. That is acceptable as a lint, but it should not be read as proof of the SQL.
**Fix:** Fail ROTATION when `rot_non200 > 0`. In the repeat check, also assert the denial counter for suppression, via `aegis_audit_records_suppressed_total`.

### IN-06: Metrics recorder trusts callers for the closed label set

**File:** `internal/telemetry/metrics.go` (`RecordAuditSuppressed`)
**Issue:** The method uses the supplied string as a label value without checking it. Today the only caller passes `ReasonLabel(...)`, so labels are closed. `RecordAuditDropped` and `RecordAuditWritten` have the same shape. A future caller could introduce cardinality.
**Fix:** Validate against the pre-initialized set inside the recorder and fold unknown values to `OTHER`.

---

_Reviewed: 2026-10-09_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
