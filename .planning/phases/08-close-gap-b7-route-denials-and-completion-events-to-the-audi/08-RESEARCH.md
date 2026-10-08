# Phase 8: Close gap B7 — route denials and completion events to the audit spool - Research

**Researched:** 2026-10-09
**Domain:** Go gateway audit write path (durable disk WAL spool, group-commit fsync, bounded queues, suppression/capping), audit worker typing, Postgres ingestion hazards
**Confidence:** HIGH on code facts and Postgres behaviour (read in this session and reproduced locally); MEDIUM on proposed numeric defaults (derived from one local NVMe measurement)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Record shape per request**
- **D-01:** An allowed request produces two linked rows: the existing pre-forward decision row, unchanged, plus a separate completion row with its own `event_id`, linked by `request_id`. The completion must not reuse the decision's `event_id` (the worker's `ON CONFLICT (event_date, event_id) DO NOTHING` would silently drop it).
- **D-02:** A denied request produces one row carrying `decision=deny`, the reason code, the HTTP status and the duration.
- **D-03:** The record type is explicit: the gateway sets it when writing the record (decision, completion, denial) and the worker stores it as given. Records already in a spool without a type fall back to today's status-based guess.

**Which denials are recorded**
- **D-04:** Every rejection of an identified principal is recorded durably: revoked or quarantined principal, no matching route, rate limit (429), policy deny (403), and fail-closed 503s (stale lease, saturation).
- **D-05:** Unauthenticated rejections (400 malformed request, 401 missing or invalid credentials) are recorded only up to a fixed cap per gateway; beyond the cap they are dropped and counted. They always remain in stdout logs and metrics.
- **D-06:** Rate-limit denials are suppressed per principal and route per window: the first 429 in a window is recorded, repeats are counted, and the count is recorded when the window closes.

**Durability of new records**
- **D-07:** A denial record is appended to the spool before the denial response is sent, with fsyncs shared across concurrent denials within a few milliseconds (group flush). Losing the last few milliseconds of denial records in a crash is acceptable.
- **D-08:** A completion record is written off the request path: handed to a bounded in-memory queue drained by a writer that batches fsyncs. It must never delay a response.
- **D-09:** The pre-forward allow record keeps its current guarantee: synchronous fsync before forwarding (AUD-01). This phase must not weaken it.
- **D-10:** On graceful shutdown, queued denial and completion records are written and fsynced within the existing drain window before the spool closes.

**Behavior at spool saturation**
- **D-11:** The 90% gate continues to stop allowed traffic (AUD-02). Denial and completion records may keep writing into reserved headroom above 90%, up to a higher hard limit; past the hard limit they are dropped and counted.
- **D-12:** When a completion record cannot be written (queue full, hard limit reached, or write error), the gateway counts it, logs it, and treats itself as saturated: new allowed requests get 503 until writes succeed again. No traffic is admitted while audit is losing data.
- **D-13:** Saturation 503s are recorded like rate-limit denials (first per principal and route per window, then a count), written into the reserved headroom.

### Claude's Discretion
- Exact numbers: group-flush interval, completion queue size, the hard limit above 90%, the cap rate for unauthenticated rejections, and the suppression window length. Research should propose values and justify them; they should be configurable with safe defaults.
- Names of the type values and of the new metrics.
- How the suppressed-count record is represented (a field on the event versus a dedicated summary record).
- Whether the cap in D-05 and the suppression in D-06/D-13 share one mechanism.

### Deferred Ideas (OUT OF SCOPE)
None raised during discussion. Items already tracked for other closure phases (Phase Boundary, out of scope): `jti` in the demo issuer, percent-encoded SPIFFE quarantine keys, B8 (quarantine reconstruction, key rotation wiring), B5/B6 (dashboard convergence and publish), B2 (Kubernetes), a multi-directory audit worker, extracting the gateway pipeline from `main()` for testability, and changes to how the dashboard displays audit rows.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| AUD-03 | Asynchronous audit worker delivers spooled records to PostgreSQL with at-least-once batching and deduplication | Sections "Worker changes", "Poison-record hazard", "Verification" (live replay/dedupe check, rotation check). See "Requirement closure statement". |
| AUD-04 | Completion audit events record backend HTTP status, request duration, and error codes | Sections "Exit-point inventory", "Seam", "Completion path". Fully closable by this phase. |
</phase_requirements>

## Summary

The gateway's audit logger is `audit.NewLogger(nil)` (stdout JSON). `AuditMiddleware` already builds a full `CompletionEvent` (status, duration, reason, error code) in a `defer` for every request that reaches it, and logs it to stdout only. The disk spool receives exactly one kind of record: the pre-forward allow row from `AppendPreForward`. The cleanest seam is to give `AuditMiddleware` an optional **sink** (backward-compatible variadic option, so `tests/failure`, `tests/security`, `tests/chaos` keep compiling) and hook the first `WriteHeader` of the `StatusCaptureResponseWriter`: that is the one point that runs after the pipeline has set decision/reason/principal on the `AuditContext` and before any denial bytes reach the client, which is exactly D-07. Completions are enqueued from the existing `defer` (D-08). `cmd/gateway/main.go` needs only: construct the pipeline, pass `audit.WithSink(...)` at the two `AuditMiddleware` calls, wrap the reverse proxy `ErrorHandler` (to set an upstream error code), and add the shutdown call. The pre-forward append code path is not touched (D-09).

Three findings the planner must act on that CONTEXT.md did not anticipate: (1) **Denial rows carry attacker-controlled fields, and a single bad value permanently wedges the audit worker.** `ac.CanonicalPath` is initialised from the *decoded* `r.URL.Path` (so `GET /api/%00` yields a NUL byte), and `r.Method` can be any HTTP token (a 28-character method reaches the handler). Postgres rejects NUL in `text` and rejects `http_method VARCHAR(16)` overflow; a batch is one implicit transaction, so the whole batch rolls back, the cursor never advances, the worker retries forever, and the spool fills until the 90% gate halts the gateway. I reproduced both failures against a throwaway Postgres 14. Before this phase only route-matched allow requests were spooled, so this was latent; this phase makes it remotely triggerable by unauthenticated traffic. Normalisation (strip NUL, truncate to column widths) is mandatory at record construction and should also be applied in the worker. (2) **D-04 lists "stale lease" as an identified-principal denial, but the lease check and snapshot-present check run before authentication**, so those 503s have an anonymous principal. They need the suppress-and-count treatment, not "record every one". (3) **Request-pipeline exits that happen before `AuditMiddleware`** (concurrency-limiter 429, 431 header-too-large, workload "ambiguous credentials" 401, Go's own parse/TLS-handshake rejections) are invisible to the audit middleware, to stdout logs *and* to the Prometheus request metrics, which contradicts D-05's "they always remain in stdout logs and metrics" for those exits.

Event-type values need **no migration** (`event_type VARCHAR(32) NOT NULL`, no CHECK or enum; verified against a real Postgres 14 with migration 000002). The suppressed-count representation does need one small additive migration if a queryable count is wanted (`suppressed_count INT`, verified that `ALTER TABLE ... ADD COLUMN` works on the partitioned table). AUD-04 can be fully closed by this phase. AUD-03 can be closed only if the plan also adds real-Postgres evidence for deduplication on replay and multi-segment rotation (see "Requirement closure statement"); without that, "delivery of every record kind" is proven but at-least-once/dedup/rotation remain unit-level evidence only.

**Primary recommendation:** Add `audit.Pipeline` (suppression governor + single group-commit goroutine writing CRC frames into the existing single segment stream under the existing `DiskSpool.mu`), wire it through an optional `AuditMiddleware` sink hooked at first `WriteHeader`, add an explicit `event_type`, sanitise/truncate every new record's strings, and prove the real binary with an extended `scripts/compose-smoke.sh` that asserts a `decision`+`completion` pair and a `denial` row by `request_id`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Deciding record kind per exit (decision / completion / denial) | API / Backend (gateway `AuditMiddleware`) | — | Only the gateway knows decision, reason, principal, status, duration. |
| Durable denial append before response | API / Backend (gateway spool group-commit) | Database / Storage (local disk WAL) | D-07: must complete before bytes reach the client. |
| Completion write off the request path | API / Backend (bounded queue + committer goroutine) | Database / Storage (local disk WAL) | D-08. |
| Suppression window / unauthenticated cap | API / Backend (in-memory, per gateway) | — | Per-gateway by D-05; no Redis (a Redis outage is itself a suppressed class). |
| Saturation headroom and hard limit | Database / Storage (spool dir quota + statfs) | API / Backend (admission gate) | D-11/D-12; the gate is `DiskSpool.CheckSaturation`. |
| Record type persistence, normalisation, dedup | Database / Storage (audit worker -> Postgres) | — | Worker stores type as given; `ON CONFLICT` dedup. |
| Schema for suppressed count | Database / Storage (goose migration, run by control plane) | — | Additive column. |
| Audit API / dashboard display of new types | Control plane API (`audit_repo.go`) | Browser (dashboard) | Pass-through already works; display deferred. |

## Standard Stack

No new external packages. Everything is stdlib plus modules already in `go.mod`.

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go stdlib (`sync`, `sync/atomic`, `time`, `os`, `unicode/utf8`, `hash/crc32`, `net/http`) | go1.26.0 | Group commit, queues, suppression map, token bucket | Project pattern is hand-rolled Go with `sync` primitives; Kafka/brokers out of scope (REQUIREMENTS.md). [VERIFIED: go.mod, `go version`] |
| `github.com/prometheus/client_golang` | v1.24.1 | New audit counters/gauges in `internal/telemetry` | Already used. [VERIFIED: go.mod] |
| `github.com/jackc/pgx/v5` | v5.10.0 | Worker batch insert (unchanged API) | Already used. [VERIFIED: go.mod] |
| `github.com/google/uuid` | v1.6.0 | New event IDs | Already used. [VERIFIED: go.mod] |

### Supporting (tests)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/pashagolub/pgxmock/v4` | v4.9.0 | Worker unit tests | Existing worker tests; `anyAuditArgs()` is 19 args. [VERIFIED: go.mod, worker_test.go] |
| `github.com/stretchr/testify` | v1.12.1 | Assertions | Existing. |
| `github.com/alicebob/miniredis/v2` | v2.34.0 | Redis fakes in failure tests | Existing; not needed for new code. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Hand-rolled token bucket (about 20 lines, injectable clock) | `golang.org/x/time/rate` | Not in go.mod (only `x/sync` indirect). A new dependency for 20 lines is not worth it; injectable clock is needed for deterministic tests anyway. |
| Group-commit goroutine | Reuse `AppendPreForward` per denial | Serialises at about 1,600 fsync/s on this NVMe (measured) under the same mutex as allows, and its 90% gate would reject denials exactly when D-13 needs headroom writes. Rejected. |
| Dedicated summary row + nullable column | Field on the first event | The first event is written before the count is known; the WAL is append-only, so a field on it is impossible without an upsert. Rejected. |

**Installation:** none.

## Package Legitimacy Audit

No external packages are installed by this phase (stdlib and existing `go.mod` modules only). Slopcheck not applicable.

**Packages removed due to slopcheck [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

## Exit-point inventory (deliverable 1)

Line numbers are `cmd/gateway/main.go` unless stated. "Today" = what is written to the spool/Postgres. In every case inside `AuditMiddleware` the only record is the stdout line from `Logger.LogCompletion`; the only spool write anywhere is the pre-forward allow row. [VERIFIED: codebase read]

Pipeline order actually wired (main.go:905-919 plus `internal/proxy/server.go`): `http.Server` -> `ConcurrencyLimiter.Wrap` (limiter.go:21) -> ingress closure (server.go:127/159: overwrites `X-Request-ID` with a fresh UUID, 431 check, workload bearer check, `MaxBytesReader`) -> `CreateProbeHandler` (user listener only; `/livez` `/healthz` `/readyz` `/healthz/ready` handled here and never audited) -> `MetricsMiddleware` -> `AuditMiddleware` -> `gatewayHandler` / `workloadHandler`. Because the ingress closure always overwrites `X-Request-ID` with `uuid.NewString()`, the audit `request_id` is always a valid UUID (no client-supplied poison there).

### A. Exits before `AuditMiddleware` (never audited, never in stdout, never in `aegis_http_requests_total`)

| # | Where | Status | Reason | Principal | Notes |
|---|-------|--------|--------|-----------|-------|
| P1 | proxy/limiter.go:26-36 (both listeners, one shared limiter) | 429 | `concurrency-exceeded` | none | Flood-class. |
| P2 | proxy/server.go:140-150 (user), 185-195 (workload) | 431 | header-too-large | none | Go's own `http.Server.MaxHeaderBytes` (16 KiB) rejects before the handler too. |
| P3 | proxy/server.go:165-175 (workload only) | 401 | ambiguous-credentials (Bearer on mTLS port) | none | Security-relevant authn event, still invisible. |
| P4 | `net/http` internals | 400 / 431 / timeouts / TLS handshake failure (workload `RequireAndVerifyClientCert`) | — | none | Unrecordable by any handler; not fixable here. |
| P5 | probe.go | 200 / 503 | readyz states | none | Intentionally not audited (no change). |
| P6 | body > 1 MiB (`MaxBytesReader`) | **502, not 413**: the error surfaces when `ReverseProxy` copies the body, so `ErrorHandler` fires. Reproduced locally; `errors.As(err, *http.MaxBytesError)` matches; the backend receives a truncated body. | — | identified | After pre-forward row; becomes a *completion* with error code `REQUEST_BODY_TOO_LARGE` (see E1). Changing the wire status is out of scope. |

### B. User listener exits inside `gatewayHandler` (main.go:262-597)

| # | Line | Status | reason_code | error_code | Principal identified? | Class |
|---|------|--------|-------------|------------|----------------------|-------|
| U1 | 267-281 | 503 | POLICY_LEASE_EXPIRED | POLICY_LEASE_EXPIRED | **No (anonymous)** | anonymous-503: suppress+count (see Q1) |
| U2 | 285-299 | 503 | UNINITIALIZED | UNINITIALIZED | **No** | anonymous-503: suppress+count |
| U3 | 306-321 | 400 | BAD_REQUEST_INVALID_PATH | INVALID_PATH | No (`ac.CanonicalPath` is the raw decoded `r.URL.Path`; can contain NUL) | D-05 cap |
| U4 | 325-341 | 401 | UNAUTHORIZED | UNAUTHORIZED | No | D-05 cap |
| U5 | 350-365 | 503 | DEPENDENCY_OUTAGE_REDIS (revocation check) | same | Yes (`claims.Subject`), route not yet set | identified; suppress+count (extension of D-13, see Q2) |
| U6 | 366-380 | 403 | PRINCIPAL_QUARANTINED / TOKEN_REVOKED | same as reason | Yes | D-04 record always |
| U7 | 384-399 | 404 | ROUTE_NOT_FOUND | NOT_FOUND | Yes; no route; `canonical_path` attacker-chosen up to about 16 KiB | D-04 record always (truncate path; see Q3) |
| U8 | 413-428 | 503 | DEPENDENCY_OUTAGE_REDIS (limiter) | same | Yes, route set | suppress+count (Q2) |
| U9 | 429-448 | 429 | RATE_LIMIT_EXCEEDED | RATE_LIMIT_EXCEEDED | Yes, route set | D-06 suppress+count |
| U10 | 473-496 | 403 | policy `ReasonCode` or DENIED_DEFAULT | FORBIDDEN, or POLICY_EVALUATION_ERROR (still 403) | Yes | D-04 record always |
| U11 | 535-550 | 503 | AUDIT_SPOOL_SATURATED | AUDIT_SPOOL_SATURATED | Yes | D-13 suppress+count into headroom |
| U12 | 551-564 | 500 | AUDIT_SPOOL_WRITE_ERROR | INTERNAL_ERROR | Yes | suppress+count (Q2) |
| U13 | 578-592 | 500 | stays the allow reason (decision stays `allow`) | INTERNAL_ASSERTION_MINT_ERROR | Yes | **completion** of an allowed request (decision row already spooled) |
| U14 | 595-596 | backend status (any) | allow reason | none today | Yes | **completion**; upstream failures go through E1 |

### C. Workload listener exits inside `workloadHandler` (main.go:600-900)

Same structure. W1 605-613 lease (anonymous), W2 618-625 uninit (anonymous), W3 632-641 400 invalid path (anonymous), W4 644-652 401 UNAUTHORIZED_NO_CERT (anonymous; practically unreachable because the TLS config requires a verified client cert), W5 653-662 401 UNAUTHORIZED_INVALID_SPIFFE (anonymous: valid cert, not a SPIFFE URI SAN), W6 671-701 revocation outage 503 / quarantined 403 (identified, principal_kind `workload`), W7 705-714 404, W8 728-763 limiter outage 503 / 429, W9 789-805 403 (default reason DENIED_WORKLOAD_FORBIDDEN), W10 844-874 spool saturated 503 / write error 500, W11 877-895 mint error 500 (completion), W12 898-899 forward (completion).

### D. Proxy-side failures (`internal/proxy/proxy.go:84-97`, `ErrorHandler`)

`WriteProblemDetails(502)` for every `RoundTrip`/body-copy error. It does not touch the `AuditContext`, so today there is no error code and, worse, `AuditMiddleware` flips the decision to `deny` for any status >= 400 (logger.go:311-313), so a backend 404/500 or a gateway 502 on an *allowed* request is logged as `decision=deny, reason_code=ALLOWED`. [VERIFIED: logger.go]

| # | Condition | Wire status | Proposed `error_code` |
|---|-----------|-------------|-----------------------|
| E1a | `errors.As(err, *http.MaxBytesError)` | 502 (unchanged) | REQUEST_BODY_TOO_LARGE |
| E1b | `errors.Is(err, context.Canceled)` (client went away) | 502 written to a dead connection | CLIENT_CANCELED (keep captured status; recording a synthetic 499 is the alternative, see Q6) |
| E1c | `errors.Is(err, context.DeadlineExceeded)` or `net.Error.Timeout()` (dial timeout is 5 s; the transport sets no `ResponseHeaderTimeout`, and `route.timeout` is not enforced, a known audit tech-debt item) | 502 | UPSTREAM_TIMEOUT |
| E1d | anything else (connection refused, TLS handshake failure, EOF) | 502 | UPSTREAM_UNAVAILABLE |
| E2 | Backend dies mid-body after headers | 200 captured; `ReverseProxy` panics `http.ErrAbortHandler`; the audit `defer` still runs, `MetricsMiddleware` (no defer) does not | optional: `recover()`, tag UPSTREAM_ABORTED, re-panic |

### E. Cannot be recorded
Go-level parse errors, `ReadHeaderTimeout`, TLS handshake rejections on the workload port (P4). Document as residual.

## Architecture Patterns

### System Architecture Diagram

```
request ──> http.Server ──> ConcurrencyLimiter ──> ingress (UUID X-Request-ID, 431, bearer-on-mTLS 401)   [P1-P3: no audit]
                                                      │
                                              CreateProbeHandler (/livez /readyz)  [no audit]
                                                      │
                                              MetricsMiddleware
                                                      │
                                   AuditMiddleware(logger, ver, WithSink(pipeline))
                                      │  start timer, ac := AuditContext, w := StatusCaptureResponseWriter{onFirstHeader}
                                      ▼
                         gatewayHandler / workloadHandler (sets ac: principal, route, decision, reason, error)
                                      │
            first WriteHeader(code>=200) on capture writer
               │                                   │
      ac.Decision=="deny"                  ac.Decision=="allow"
               │                                   │ (proxy runs, status/err known later)
   Pipeline.RecordDenial(ev)                       │
     Governor.Admit(ev) ──> Record | Suppress | Drop(counted)        defer after handler returns:
        │ Record                                                      Pipeline.EnqueueCompletion(ev)
        ▼                                                                  │ non-blocking, bounded
   Normalize + marshal + frame                                             ▼
   denialCh ──┐                                                       completionCh (cap 8192) ── full ──> drop+count+SET FAULT
              ▼                                                            │
        ┌──────────────── committer goroutine (single) ────────────────────┘
        │ linger: 2 ms if a denial waits, else 25 ms; max 512 frames / 512 KiB
        │ DiskSpool.mu.Lock ─ hardLimit check (>=95%?) ─ ONE Write(all frames) ─ Sync ─ rotate if >= MaxSegmentBytes ─ Unlock
        ▼
   wal-*.log  (same CRC frame, same single segment stream)  <── AppendPreForward (unchanged, sync fsync, 90% gate, takes same mu)
        │
        ▼ tail
   audit-worker (per spool) ── ProcessAvailable ── pgx.Batch INSERT … ON CONFLICT (event_date,event_id) DO NOTHING ──> Postgres audit_events
                                                                      ▲ event_type stored as given (decision|completion|denial), normalised strings

   Governor sweeper (window/4 tick): closed windows with count>0 ──> summary denial event ──> completionCh path (async)
   Fault flag (queue full | hard limit | write error) ──> DiskSpool.CheckSaturation()==true ──> AppendPreForward ErrSpoolSaturated (503) and /readyz 503
   Shutdown: dualServer.Shutdown ──> Pipeline.Shutdown(ctx 5s): stop sweeper, emit all summaries, drain channels, final fsync ──> DiskSpool.Close()
```

### Recommended Project Structure
```
internal/audit/
├── event.go        # + EventType, SuppressedCount, Kind consts, Normalize()
├── spool.go        # + writeBatchLocked, hard limit, fault flag, StatfsFunc test hook; AppendPreForward body UNCHANGED
├── committer.go    # NEW: group-commit goroutine (denialCh, completionCh, linger, shutdown)
├── governor.go     # NEW: suppression map + unauthenticated token buckets (injectable clock)
├── pipeline.go     # NEW: Sink implementation = Governor + committer; metrics Recorder interface
├── logger.go       # + WithSink option, first-WriteHeader hook, decision-flip fix, WrapUpstreamErrorHandler
└── worker.go       # + typed event_type, Normalize before insert, suppressed_count arg
migrations/000003_add_audit_suppressed_count.sql   # additive, nullable
internal/config/config.go                          # + AEGIS_AUDIT_* and AEGIS_SPOOL_* keys
internal/telemetry/metrics.go                      # + audit counters/gauges
```

### Pattern 1: Sink seam on `AuditMiddleware` (D-07/D-08, minimum testable seam)
**What:** `AuditMiddleware(logger *Logger, snapshotVersion int64, opts ...Option)` with `WithSink(Sink)`. Existing call sites (`tests/failure/redis_outage_test.go:338`, `tests/security/workload_identity_test.go:419-420`, chaos helpers) compile unchanged because the new parameter is variadic.
```go
type Sink interface {
    RecordDenial(ev *CompletionEvent)     // returns after fsync (or timeout/drop); never panics
    EnqueueCompletion(ev *CompletionEvent) // non-blocking
}
```
`StatusCaptureResponseWriter` gets an unexported `onFirstHeader func(code int)`. In `WriteHeader`, **before** `rw.ResponseWriter.WriteHeader(code)`, call the hook once for `code >= 200` (ignore 1xx). The hook snapshots `ac` under its mutex; if `ac.Decision == "deny"` it builds the denial event (duration = time since `start`), calls `sink.RecordDenial`, and sets `ac.durableRecorded = true`. The `defer` then: always `logger.LogCompletion` (stdout unchanged); if denial and not yet recorded (handler never wrote a header), record it there; if allow, `sink.EnqueueCompletion`.
**Why this hook:** every denial exit in main.go sets `ac.SetDecision/SetErrorCode` first and then calls `proxy.WriteProblemDetails`, which calls `w.WriteHeader` before writing the body, so decision/reason/principal are final and no response byte has been sent. `Retry-After` is set before and is unaffected. No main.go call site other than the middleware needs to change.
**Minimum testable seam without extracting the pipeline:** the middleware plus a fake `Sink` and an `httptest` handler that sets the `AuditContext` the way main.go does. Add one cheap drift guard: a test that parses `cmd/gateway/main.go` with `go/parser`, collects every string literal passed to `SetDecision`/`SetErrorCode`, and asserts each reason code is classified in the governor's table. This catches a new exit added to main() without classification, without needing `main()` to be callable.

### Pattern 2: One committer goroutine, two inputs, one `Write` per batch (D-07, D-08, D-11)
- `denialCh` (cap 1024) carries `{frame, done chan error}`; `completionCh` (cap 8192) carries `{frame}` with `done == nil`.
- The committer blocks for the first item, then sets a flush deadline: `first_arrival + groupFlush` if the batch contains a denial, else `first_arrival + completionFlush`. If a denial arrives while only completions are pending, shorten the deadline to `now + groupFlush` (otherwise a denial would wait the 25 ms completion linger and violate D-07's "few milliseconds"). Cap a batch at 512 frames or 512 KiB.
- `DiskSpool.writeBatchLocked(frames [][]byte)`: lock `s.mu`; closed check; hard-limit check (`>= 0.95` of quota OR statfs, never the 0.90 gate); concatenate frames and issue a **single** `activeFile.Write` (a reader never sees an interleaved partial frame, and a torn tail is at most the unacknowledged batch); `Sync()` once; add to `currentSize`; rotate after the write (same as `AppendPreForward`) so no frame straddles segments and a batch can exceed `MaxSegmentBytes` by at most one batch (<= 512 KiB, immaterial against a 16 MiB segment).
- On write/fsync error: `Truncate(currentSize)` back to the last known-good size; if truncate fails, rotate to a new segment (leaving the torn tail at the end of the old segment, which the worker already handles as "corrupt, scan to EOF"). This matters because a retry after a partial write would otherwise append whole frames after torn bytes, and the worker's `ReadFramedRecord` consumes `length+1` bytes after the torn header and can skip past the next valid frame's magic (pre-existing weakness of `AppendPreForward` too; not in scope to fix there, D-09).
- Waiters (`RecordDenial`) block on `done` with a 2 s safety timeout and **ignore the request context** (a client disconnect must not skip durability), then proceed to send the denial regardless: a failed or dropped denial append never turns a deny into an allow and is counted (`dropped{kind="denial"}`).
- **Why not give pre-forward the same group commit?** It would improve allow throughput but D-09 says leave it alone. Consequence to document: a flush holds `s.mu` for about one fsync, so under a heavy denial flood allow appends see up to about 1 fsync extra latency (about 0.5-2 ms here) and the mutex duty cycle is bounded by `fsync_time / groupFlush` (about 25-50 percent at a 2 ms interval).

### Pattern 3: Governor (suppression and cap, shared package, two mechanisms, one decision API)
```go
type Disposition int // Record, Suppress, Drop
func (g *Governor) Admit(ev *CompletionEvent, now time.Time) Disposition
func (g *Governor) Sweep(now time.Time) []*CompletionEvent // summary events for closed windows
```
Classification (from `ac.PrincipalKind != "anonymous"` and the normalised reason code):
| Class | Reasons | Mechanism |
|-------|---------|-----------|
| identified, suppressed | RATE_LIMIT_EXCEEDED (D-06), AUDIT_SPOOL_SATURATED (D-13), plus DEPENDENCY_OUTAGE_REDIS and AUDIT_SPOOL_WRITE_ERROR (extension, Q2) | Suppressor keyed `(principal_id, route_id, reason_code)`: first event in the key's window is recorded, repeats increment a counter, at window close a summary is emitted |
| identified, always | everything else (403, 404, revoked/quarantined) | Record (Q3 discusses an optional safety valve) |
| anonymous 503 | POLICY_LEASE_EXPIRED, UNINITIALIZED | Suppressor keyed `("anonymous","",reason)` |
| anonymous 400/401 | BAD_REQUEST_INVALID_PATH, UNAUTHORIZED, UNAUTHORIZED_NO_CERT, UNAUTHORIZED_INVALID_SPIFFE | D-05 token bucket per reason code (closed set; unknown reasons share "OTHER"): rate 10/s, burst 50; beyond that `Drop` + counter |
| completion | decision allow | never governed (queue-bounded) |

- **Bounded memory:** the suppressor map is capped at `maxKeys` (4096 default). Each entry stores a compact copy of the first event (its path is already truncated to 2,048 bytes by `Normalize`) plus `windowStart` and `count`: at most about 4096 x (about 1-3 KB) = about 4-12 MB. On overflow (new key, map full) **record the event unsuppressed** (fail toward auditability) and count `aegis_audit_suppressor_overflow_total`; the hard limit and the identified-principal population bound the damage. Token buckets: at most about 8 keys (closed reason set).
- **Window semantics:** tumbling per key, starting at the key's first event (not a global tick). Sweeper ticks every `window/4` (minimum 1 s) and emits summaries for entries whose window has closed with `count > 0`, then deletes the entry; entries with `count == 0` expire silently. `Sweep(now)` is exported-for-test so tests use a fake clock and never sleep.
- **Representation of the count (recommended):** a **dedicated summary row**, `event_type="denial"`, same principal/route/reason/status as the first event, new `event_id`, `timestamp` = window close, `suppressed_count = N` (repeats only, excluding the recorded first), `duration_ms = 0`, `request_id` = the first event's request id (keeps correlation to the recorded first denial). Requires `suppressed_count INT` (nullable) via migration 000003 and one more insert arg (`anyAuditArgs()` becomes 20). Fallback if the user rejects a schema change: put the count in `error_code` as `SUPPRESSED:<n>` (works with zero migration but is not queryable as a number).
- Summaries and shutdown flush go through the async completion path, not the request path.

### Anti-Patterns to Avoid
- **Calling `AppendPreForward` for denials/completions:** fsync-per-record under the shared mutex, and its 90% check rejects them at exactly the moment D-11/D-13 need headroom writes.
- **Decision flip by HTTP status for durable rows:** logger.go:311 flips `allow` to `deny` for any status >= 400, so a backend 500 would be stored as `deny`. For the durable record, take `ac.Decision` as authoritative when a handler set it explicitly (add an `decisionSet` flag in `SetDecision`) and keep the legacy status guess only for contexts where no handler set a decision (existing middleware tests rely on it). Stdout may keep legacy behaviour or follow; recommend following so stdout and DB agree.
- **Reusing `ac.CanonicalPath`/`r.Method`/`principal_id` verbatim in new rows:** see Pitfall 1.
- **Blocking send on the completion channel:** use `select { case ch <- x: default: drop }`.
- **Closing the data channels on shutdown:** panics late senders; use a `closed` atomic checked before send plus a `quit` channel, and complete every leftover waiter with `ErrClosed` after the committer exits.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| CRC frame format | A second record format | Existing 12-byte header `[magic][crc32][len]` + payload + `\n` built exactly as `AppendPreForward` does (extract a private `frameEvent(*CompletionEvent) ([]byte, error)` used only by new paths) | Worker reads it unchanged; `ReadFramedRecord` is the contract. |
| Rotation, naming, quota | New segment management | Existing `rotateSegment`, `dirUsageBytes`, statfs logic | Worker depends on lexicographic `wal-<nanos>-<seq>.log` order. |
| Per-row error isolation in Postgres | Custom retry logic first | Normalise inputs (strip NUL, truncate to column widths) in both producer and worker | Removes the poison class at the source; see Pitfall 1. |
| Unique IDs | Counter-based IDs | `uuid.NewString()` | `event_id` is `UUID` and part of the dedup primary key. |

**Key insight:** this phase adds writers, not a new storage design. Every new byte goes through the same frame builder, the same `DiskSpool.mu`, the same active file and the same rotation, so the worker, cursor and pruning logic are untouched.

## Spool internals relevant to D-07/D-08/D-11 (deliverable 3)

[VERIFIED: internal/audit/spool.go]
- `AppendPreForward` holds `s.mu` for: `CheckSaturation()` (which does `os.ReadDir` + `Info()` per file via `dirUsageBytes`, plus `statfs`), `json.Marshal`, one `Write`, one `Sync`, rotation. Cost scales with the number of segments; with `AEGIS_PRUNE_ARCHIVED=true` in all three compose files (verified) there are few.
- `CheckSaturation` is `quota-ratio >= 0.90 OR statfs-used-ratio >= 0.90`. `IsSaturated` treats a `CheckSaturation` error as saturated.
- **The admission gate is `AppendPreForward`'s internal check, not `IsSaturated`.** main.go:911 passes `diskSpool.IsSaturated` only to `CreateProbeHandler` for `/readyz`. So D-12 wiring = make `CheckSaturation()` also return `true` while a fault flag is set; that one change trips both `AppendPreForward` (503 `AUDIT_SPOOL_SATURATED`, existing handling at main.go:536-550 and 845-859) and `/readyz` (HAProxy then drains the replica). The new denial/completion paths must call a *separate* `checkHardLimit()` so the fault flag and the 90% gate never block the headroom writes.
- `NewDiskSpool` reopens the latest segment in append mode if smaller than `MaxSegmentBytes` (`TestDiskSpool_ReopenRecovery` asserts this). A crash during a batch write can leave a torn tail; reopen then appends valid frames after it. Optional hardening: on open, scan the last segment and truncate to the last valid frame (keeps the test green). Low priority; record as known pre-existing weakness.
- `DiskSpoolConfig` has no segment-size env today (`spoolCfg` sets only `SpoolDir` and `VolumeQuotaBytes`). A rotation proof on the real binary needs a new `AEGIS_SPOOL_SEGMENT_BYTES` (default 16 MiB).
- Hard limit (D-11): `checkHardLimit()` = quota ratio `>= hard` OR statfs ratio `>= hard`, default `hard = 0.95`. Headroom at the default 1 GiB quota is 5 percent = 51.2 MiB, about 113,000 frames at the measured 475 bytes/frame (462-byte JSON + 13 bytes of framing for a typical event). [VERIFIED: local measurement]
- **Test determinism (host is 88 percent full):** add `DiskSpoolConfig.StatfsFunc func(path string) (used, total uint64, err error)` (nil = real `syscall.Statfs`). New tests inject fixed ratios (0.50, 0.92, 0.96) and set a small `VolumeQuotaBytes`, so none depend on real disk usage. Existing tests are unaffected.

### Concurrency and ordering hazards
| Hazard | Mitigation |
|--------|------------|
| Interleaved partial frames | Single writer under `s.mu`; each batch is one `Write` of concatenated whole frames. |
| Rotation during a batch | Rotate only after the batch is written and synced, still under `s.mu`; frames never straddle segments; no late write can land in a rotated segment, so the worker's "never revisits a non-latest segment" rule stays safe. |
| `Close()` during flush | `Close()` blocks on `s.mu` until the in-flight flush ends. Shutdown must call `Pipeline.Shutdown(ctx)` first (drain channels, final flush), then `DiskSpool.Close()`. Make `Close()` itself call the committer shutdown with a default timeout if one is attached. |
| Late enqueue after shutdown | `closed` atomic checked before send; leftover waiters completed with `ErrClosed` after the committer exits; waiters also have the 2 s timeout so none can hang. |
| Worker reading a segment while it is appended | Worker treats a partial trailing frame as `ErrCorruptedRecord` -> `scanToNextMagic` hits EOF -> break without advancing the offset; the next 200 ms tick retries from the same offset. Safe, and batched single-`Write` appends make a partial view shorter-lived. |
| Denial flood vs allow throughput | Mutex duty cycle bounded by `fsync/groupFlush`; measured serial `write+fsync` p50 0.52 ms, p99 1.72 ms, max 3.5 ms; 64 concurrent serialized appenders reach about 1,600 fsync/s on this NVMe. Allow ceiling today is therefore about 1,600 req/s per gateway on this disk (pre-existing). |

## Worker changes for D-03 (deliverable 4)

[VERIFIED: worker.go, migration 000002, local Postgres 14 run]
- `event_type VARCHAR(32) NOT NULL`, **no CHECK constraint, no enum**; a row with `event_type='denial'` inserted successfully. **No migration is required for the type values.**
- `CompletionEvent` gains `EventType string \`json:"event_type,omitempty"\``. Old records have no key -> empty string -> fallback.
- `ProcessBatch`: `eventType := e.EventType; if eventType not in {decision, completion, denial} { eventType = "decision"; if e.HTTPStatus > 0 { eventType = "completion" } }` (allowlist so garbage or a future type never reaches the column by accident). `ToCompletionEvent` (the pre-forward builder) should set `EventTypeDecision` explicitly; this does not change the pre-forward durability path (D-09).
- `ON CONFLICT (event_date, event_id) DO NOTHING` accepts the new rows (distinct `event_id`s); the completion's `request_id` equals the decision's, and `request_id` is indexed but not unique. [VERIFIED: migration 000002]
- **Suppressed count:** if the dedicated-summary design is accepted, add `migrations/000003_add_audit_suppressed_count.sql` (`-- +goose Up` `ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS suppressed_count INT;` / `-- +goose Down` drop column; `TestEmbeddedMigrations_Integrity` requires both markers) and a 20th insert argument. Verified `ALTER TABLE ... ADD COLUMN` works on the partitioned parent. The control plane runs goose at startup (`cmd/control-plane/main.go:167`); `audit-worker` already `depends_on: control-plane: service_healthy`. If the worker starts against an unmigrated DB, inserts fail and the cursor is held (no loss) until the migration runs.
- **Normalisation in the worker (defence in depth, also protects legacy spools):** strip NUL, replace invalid UTF-8, truncate to column widths: `event_type` 32, `principal_id` 128, `principal_kind` 32, `service_id` 64, `route_id` 64, `http_method` 16, `decision` 16, `reason_code` 64, `client_ip` 64, `error_code` 64; `request_id` must `uuid.Parse` or be replaced.
- Skip nothing else; `ProcessAvailable`, cursor and pruning logic are unchanged.

## Pitfall catalogue

### Pitfall 1 (CRITICAL): attacker-controlled fields poison the worker and wedge all audit delivery
**What goes wrong:** one row that Postgres rejects aborts the whole `pgx.Batch` (implicit transaction), `ProcessBatch` returns the error before `SaveOffset`, and every later tick fails on the same batch.
**Why it happens now:** (a) `AuditMiddleware` sets `ac.CanonicalPath = r.URL.Path` (logger.go:297), the *decoded* path; `GET /api/%00x` yields `"/api/\x00x"`; U3/W3 (400 invalid path) then writes it. Postgres: "the character with code zero cannot be stored" [CITED: postgresql.org/docs/current/datatype-character.html]; reproduced: `invalid byte sequence for encoding "UTF8": 0x00`. (b) `r.Method` is any HTTP token: a 28-character method reached a Go handler in my test; `http_method VARCHAR(16)` -> `value too long for type character varying(16)` and the transaction rolled back to 0 rows (reproduced). (c) Paths up to about 16 KiB (header budget) also bloat records and the spool.
**How to avoid:** `CompletionEvent.Normalize()` applied in the pipeline before framing (strip NUL; `strings.ToValidUTF8`; truncate each string to its column width on a rune boundary; `canonical_path` to 2,048 bytes) AND the same function in the worker before queuing. Test: table-driven NUL / 4 KiB path / 40-char method / 200-char principal through producer and worker.
**Warning signs:** repeating `AuditWorker batch flush warning` with a constant error, `wal.cursor` not advancing, `aegis_spool_utilization_ratio` climbing.

### Pitfall 2: D-04 "stale lease" is not an identified-principal denial
Lease (U1/W1) and snapshot-present (U2/W2) checks run before path validation and authentication. During a lease lapse every request, authenticated or not, becomes an anonymous 503. Treat as suppress+count keyed `("anonymous","",reason)`; otherwise a lapse turns all inbound traffic into fsync'd records.

### Pitfall 3: `AuditMiddleware` flips `allow` to `deny` on status >= 400
Covered above; it would store a backend 500 as a denial of an allowed request, and `reason_code=ALLOWED, decision=deny` is internally inconsistent.

### Pitfall 4: gate deadlock on D-12
If the fault flag closes the gate, no allowed request exists to produce a completion that would clear it. Recovery rules (state machine below) must not depend on completions flowing.

### Pitfall 5: `ReverseProxy` forwards 1xx via `WriteHeader`
`StatusCaptureResponseWriter.WriteHeader` marks `wroteHeader` on the first call, so a backend `103 Early Hints` would suppress the real final header (pre-existing). The new hook must ignore codes < 200; fixing the capture writer to treat 1xx (except 101) as non-final is a one-line improvement worth making while editing that type.

### Pitfall 6: summary events and request ids
`request_id UUID NOT NULL`: summary events must carry a parseable UUID (reuse the first event's). Do not use the empty string; the worker would generate a random one (breaking correlation), and a non-UUID string would be a poison row.

### Pitfall 7: metrics series appear only after first use
Prometheus `CounterVec` children do not exist until used. For the smoke test and dashboards, pre-initialise all `(kind, reason)` combinations with `.Add(0)` at startup so absence means "not scraped", not "zero".

### Pitfall 8: compose lint allowlist
`tests/compose/compose_test.go` `gatewayAllowlist` fails any `AEGIS_*` key set in compose that is not listed. Defaults need no compose change; any key a compose file sets (for example `AEGIS_SPOOL_SEGMENT_BYTES` for a rotation proof) must be added to the allowlist in the same change.

## Fail-closed wiring for D-12 (deliverable 7)

State held in `DiskSpool` (`faultReason atomic.Value`/`atomic.Bool`), exposed to the pipeline via `SetWriteFault(reason)` / `ClearWriteFault()`; `CheckSaturation()` returns `true` when set.

| Trigger (completion only) | Action |
|---------------------------|--------|
| `completionCh` full on enqueue | drop + `dropped{kind=completion,reason=queue_full}` + log (rate-limited) + set fault |
| Batch rejected by hard limit | drop the batch's completions + `dropped{reason=hard_limit}` + set fault |
| Write/fsync error | truncate/rotate (above); keep the batch and retry with backoff 100 ms -> 1 s; set fault; if the queue then fills, enqueue drops as above |
| Pipeline closed | `dropped{reason=closed}`; no fault (shutdown) |

Recovery (checked by the committer on every successful flush and by a 250 ms ticker while faulted): clear the fault when (a) the last flush succeeded or no batch is pending, (b) `completionCh` depth <= 50 percent, (c) hard limit not exceeded. The queue-full and I/O-error cases clear on their own as the retry/drain succeeds; the hard-limit case needs no completions because the ticker evaluates (c) directly. Because the fault flag feeds `/readyz`, HAProxy removes a faulted replica; recovery does not require inbound traffic. Denial drops do **not** set the fault (D-12 names completions; an unauthenticated flood must not be able to flip a gateway to 503).

## Shutdown ordering for D-10 (deliverable 8)

Current: `dualServer.Shutdown(drainCtx 30s)` -> `streamCancel` -> `metricsServer.Shutdown(5s)` -> `rdb.Close` -> `diskSpool.Close`. `http.Server.Shutdown` waits for in-flight handlers, so every completion `defer` has already enqueued when it returns (unless the 30 s drain timed out). Insert immediately after `dualServer.Shutdown`:
```go
auditCtx, auditCancel := context.WithTimeout(context.Background(), 5*time.Second)
if err := auditPipeline.Shutdown(auditCtx); err != nil { log.Printf("audit pipeline shutdown: %v", err) }
auditCancel()
// ... existing metrics/redis ...
if err := diskSpool.Close(); err != nil { ... }   // after Shutdown, as today
```
`Shutdown`: stop sweeper -> emit summaries for every open window with `count>0` -> mark closed -> drain `denialCh` then `completionCh` with one final flush+fsync -> release leftover waiters. Budget: 30 s drain + 5 s audit + 5 s metrics = 40 s against `stop_grace_period: 45s` (compose); `TestGatewayStopGracePeriod` requires only `grace >= drain + 5s`. Recommend updating the compose comment ("drain 30 + audit 5 + metrics 5 + margin") and tightening the lint to `drain + 10s`. A handler still running after a timed-out drain gets `ErrClosed` and the record is dropped and counted (`reason=closed`).

## Proposed defaults and config keys (deliverable 6)

Config style: `internal/config` returns an error for unparsable values (`AEGIS_PORT`, `AEGIS_SPOOL_MAX_BYTES`); follow that, not main.go's silent fallback for `AEGIS_DRAIN_TIMEOUT`. Durations use Go duration strings (`AEGIS_DRAIN_TIMEOUT` precedent; the worker's `_MS` ints cannot express 2 ms cleanly).

| Env key | Default | Validation | Justification |
|---------|---------|-----------|---------------|
| `AEGIS_AUDIT_GROUP_FLUSH_INTERVAL` | `2ms` | 0 < d <= 50ms | D-07 "few ms". Measured fsync p50 0.52 ms / p99 1.7 ms / max 3.5 ms on local NVMe; 2 ms linger plus one fsync keeps denial latency typically <= 4-6 ms and bounds fsync rate to <= 500/s. Slower disks naturally batch more (frames accumulate during the previous fsync); [ASSUMED] cloud network disks at 1-10 ms fsync remain acceptable with this value. |
| `AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL` | `25ms` | 0 < d <= 1s | Completions are not latency-critical; 25 ms caps the extra fsync load at about 40/s (versus one pre-forward fsync per allow). |
| `AEGIS_AUDIT_COMPLETION_QUEUE_SIZE` | `8192` | 64..1,000,000 | 8x `AEGIS_MAX_CONCURRENT` (1000): even if every in-flight request completes during a stall the queue holds seconds of headroom; about 8192 x about 1 KB = about 8 MB worst case. |
| `AEGIS_SPOOL_HARD_LIMIT_RATIO` | `0.95` | 0.90 < r <= 0.99 | 5 percent of a 1 GiB quota = 51.2 MiB = about 113k frames of headroom; allow traffic is already halted at 90 percent so headroom is consumed only by denials, in-flight completions and summaries. |
| `AEGIS_AUDIT_UNAUTH_RATE` | `10` (per second, per reason code) | > 0 | About 4.7 KB/s per reason, worst case about 400 MB/day per reason class if sustained; enough to keep a forensic trail of a brute-force attempt. Per-reason buckets stop a flood of 400s from starving the 401 trail. |
| `AEGIS_AUDIT_UNAUTH_BURST` | `50` | >= 1 | Absorbs a short legitimate burst (for example a fleet with an expired token). |
| `AEGIS_AUDIT_SUPPRESS_WINDOW` | `60s` | 1s..1h | Standard per-minute rollup: 2 rows per key per minute; summary delay at most window + window/4 sweep. |
| `AEGIS_AUDIT_SUPPRESS_MAX_KEYS` | `4096` | >= 16 | Memory bound 4-12 MB. |
| `AEGIS_SPOOL_SEGMENT_BYTES` | `16777216` | >= 4096 | Exposes the existing default; required to prove multi-segment rotation on the real binary (set small in a test profile). |

Not configurable (constants): max batch 512 frames / 512 KiB, denial waiter timeout 2 s, `canonical_path` cap 2,048 bytes, shutdown budget 5 s.

## Metrics (deliverable 9)

Names follow `aegis_<area>_<noun>_total`; labels are closed enums (no principal, path, IP, route; none of the forbidden label keys in `metrics_test.go`: principal_id, client_ip, user_id, ip, path, url, query, bearer, token). `audit` stays decoupled from `telemetry` via a small `Recorder` interface implemented by `*telemetry.Metrics`. Note `metrics` is currently constructed *after* `auditLogger`/`diskSpool` in main(); construct the pipeline after line 128 or inject the recorder afterwards.

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `aegis_audit_records_written_total` | counter | `kind` in {completion, denial} | Frames durably written by the new paths |
| `aegis_audit_records_dropped_total` | counter | `kind` in {completion, denial}, `reason` in {queue_full, hard_limit, write_error, closed, timeout, unauth_cap} | Records lost; `unauth_cap` is D-05 |
| `aegis_audit_records_suppressed_total` | counter | `reason_code` (closed set, unknown -> OTHER) | Repeats counted instead of recorded (D-06/D-13) |
| `aegis_audit_suppressor_overflow_total` | counter | — | Map full; event recorded unsuppressed |
| `aegis_audit_completion_queue_depth` | gauge | — | Channel length (updated by the existing 1 s gauge loop) |
| `aegis_audit_degraded` | gauge | — | 1 while the D-12 fault flag is set |
| `aegis_audit_flush_duration_seconds` | histogram | — | write+fsync per batch; buckets 0.0002..0.1 |

`aegis_spool_bytes_written_total` exists with no production caller (DIST-02 gap); the committer can call `RecordSpoolBytes` cheaply, optional.

## Validation Architecture

> `workflow.nyquist_validation` is `true` in `.planning/config.json`.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` + testify v1.12.1; pgxmock v4.9.0; miniredis v2.34.0; `-race` supported on this host (verified) |
| Config file | none (go.mod, Makefile targets `test-quick`, `test-failure`, `compose-test`, `compose-smoke`) |
| Quick run command | `go test -race -count=1 ./internal/audit/ ./internal/telemetry/ ./internal/config/ ./internal/proxy/` |
| Full suite command | `go test -race -count=1 $(go list ./... \| grep -v /tests/integration)` (never `./tests/integration/...`: it starts Docker) |
| Live (real binary) | `bash scripts/compose-smoke.sh mvp` against a stack the caller brought up (`docker compose ... up -d --build --wait`); never started by research |
| Tmp-dir workaround | if "audit spool saturated" appears: `mkdir -p /dev/shm/aegis-gotmp && TMPDIR=/dev/shm/aegis-gotmp go test ...`, then remove the dir |

Baseline observed this session: `go test -count=1 ./internal/audit/ ./internal/telemetry/ ./internal/proxy/ ./internal/config/` all pass at 88 percent root usage; `go test -race ./internal/audit/` passes.

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| AUD-04 | Completion event written to spool with backend status, duration > 0, error code; own `event_id`, same `request_id` as decision row (D-01) | unit (middleware + fake sink) | `go test -race -count=1 ./internal/audit/ -run TestAuditMiddlewareSink` | Wave 0 (extend logger_test.go) |
| AUD-04 | Upstream failures set `error_code` (UPSTREAM_UNAVAILABLE/TIMEOUT, CLIENT_CANCELED, REQUEST_BODY_TOO_LARGE) and keep decision=allow | unit | `... -run TestWrapUpstreamErrorHandler` | Wave 0 |
| AUD-04 | Backend 5xx on allowed request stored `allow`, not `deny` | unit | `... -run TestCompletionKeepsAllowOnBackendError` | Wave 0 |
| AUD-03 / D-03 | Worker stores explicit type; untyped falls back; unknown type falls back | unit (pgxmock arg #2) | `... -run TestAuditWorker_EventType` | Wave 0 (extend worker_test.go; update `anyAuditArgs` to 20) |
| AUD-03 | Poison inputs (NUL, 40-char method, 4 KiB path, 200-char principal) normalised; batch succeeds | unit | `... -run TestNormalize` and `TestAuditWorker_NormalizesPoison` | Wave 0 |
| AUD-03 | Group flush: N concurrent denials -> all frames valid, one fsync per group (count via injected sync hook), no interleaving | unit/stress | `go test -race -count=1 ./internal/audit/ -run TestGroupCommit` | Wave 0 |
| AUD-03 | Rotation during batch: tiny `MaxSegmentBytes`; every frame readable across all segments; worker consumes all | unit | `... -run TestGroupCommitRotation` | Wave 0 |
| D-07 | Denial record is durable before response headers are sent (sink observes `ResponseRecorder` not yet written) | unit | `... -run TestDenialRecordedBeforeResponse` | Wave 0 |
| D-08 | Completion never delays response: blocking sink -> handler returns within bound; queue-full drops and sets fault | unit | `... -run TestCompletionNonBlocking` | Wave 0 |
| D-09 | `AppendPreForward` unchanged: existing spool/worker tests still green | regression | `go test -race -count=1 ./internal/audit/ ./tests/failure/ ./tests/chaos/ ./tests/dr/` | ✅ |
| D-10 | Shutdown flushes queued denials/completions/summaries before `Close`; late enqueue returns `ErrClosed`, no hang/panic | unit | `... -run TestPipelineShutdown` | Wave 0 |
| D-11 | Between 90% and 95% (injected statfs/quota): `AppendPreForward` -> `ErrSpoolSaturated`; denial/completion still written; at >= 95% dropped+counted | unit | `... -run TestHardLimitBand` | Wave 0 |
| D-12 | Queue-full / hard-limit / write-error sets fault -> `CheckSaturation()` true -> `AppendPreForward` saturated; recovers without traffic | unit | `... -run TestWriteFaultGate` | Wave 0 |
| D-05 | Unauthenticated cap: rate/burst per reason, fake clock, over-cap `Drop` + counter | unit | `... -run TestGovernorUnauthCap` | Wave 0 |
| D-06 / D-13 | Suppression: first recorded, repeats counted, summary at window close with correct `suppressed_count`; map bound + overflow; fake clock `Sweep(now)` | unit | `... -run TestGovernorSuppression` | Wave 0 |
| D-04 | Classification table: every reason literal in `cmd/gateway/main.go` (go/parser scan) is classified | unit (drift guard) | `... -run TestEveryMainReasonIsClassified` | Wave 0 |
| DIST-02 hygiene | New metrics exposed, closed label values, no forbidden label keys | unit | `go test -race -count=1 ./internal/telemetry/` | Wave 0 (extend metrics_test.go) |
| Config | New env keys parse, defaults, invalid -> error | unit | `go test -race -count=1 ./internal/config/` | Wave 0 |
| AUD-03/AUD-04 live | On the real binary: allowed request -> `decision`(status 0) + `completion`(status 200, duration_ms > 0) rows sharing `request_id`; denied request -> one `denial` row (403, deny, duration_ms > 0); unauthenticated -> `denial` 401 anonymous | live smoke | `bash scripts/compose-smoke.sh mvp` | Wave 0 (extend script) |
| AUD-03 live | Replay/dedupe: delete worker `wal.cursor`, restart worker, `count(*)` unchanged | live smoke | same script, new section | Wave 0 |
| AUD-03 live | Rotation: gateway with small `AEGIS_SPOOL_SEGMENT_BYTES`, enough requests for >= 2 segments, all rows present, count equals expected | live smoke / compose profile | same script | Wave 0 (needs new env + allowlist entry) |

### Sampling Rate
- **Per task commit:** the quick run command above (about 2-4 s).
- **Per wave merge:** full suite command (excludes `tests/integration`); plus `go test -count=1 ./tests/compose/...` whenever compose or env keys change.
- **Phase gate:** full suite green with `-race`, then one live `compose-smoke.sh mvp` (and `distributed` if budget allows) with the new assertions passing, before `/gsd:verify-work`. Note `benchmarks/TestPolicyEngine_LatencyBudget` is timing-sensitive; run it in isolation if it flakes under load.

### Wave 0 Gaps
- [ ] `internal/audit/governor_test.go` (D-05, D-06, D-13, bounds, fake clock)
- [ ] `internal/audit/committer_test.go` / `spool_group_test.go` (group commit, rotation, hard-limit band, fault, shutdown, `-race` stress; needs `StatfsFunc` and a sync-count hook)
- [ ] `internal/audit/logger_test.go` additions (sink ordering, non-blocking, exit-point table, upstream error classes, decision-flip fix)
- [ ] `internal/audit/worker_test.go` additions (typed events, normalisation, 20 args) and `event_test.go` for `Normalize`
- [ ] `internal/audit/main_reasons_test.go` drift guard (go/parser over `cmd/gateway/main.go`)
- [ ] `internal/telemetry/metrics_test.go` and `internal/config/` test additions
- [ ] `scripts/compose-smoke.sh` new `AUDIT-TYPES`, `DEDUPE` (and optional `ROTATION`) sections
- [ ] Framework install: none

## Verification on the real binary (deliverable 10)

Extend `scripts/compose-smoke.sh` (already counts `audit_events` via `dc exec -T postgres psql -U aegis -d aegis -tAc`):
1. New helper `gw_req_id TOKEN PATH` using `curl -s -D - -o /dev/null`, parsing the `X-Request-ID` response header (the ingress sets it and the middleware echoes it).
2. Allowed: `GET /api/orders` with a developer token (expect 200). Poll up to 30 s (worker tick is 200 ms) for `select event_type, decision, http_status, duration_ms>0, error_code from audit_events where request_id='<id>' order by event_type`; expect exactly `completion|allow|200|t|` and `decision|allow|0|...`.
3. Denied: `GET /api/admin/users` (already used for B1, expect 403): expect exactly one row `denial|deny|403|t|` with a non-empty `reason_code`.
4. Unauthenticated: `GET /api/orders` with a bad bearer: expect `denial|deny|401` with `principal_kind='anonymous'` (within the default cap of 50).
5. Dedupe (AUD-03): record `count(*)`; `dc exec -T audit-worker rm /var/log/aegis/wal/wal.cursor`; `dc restart audit-worker`; wait about 5 s; assert count unchanged (the worker re-reads the surviving segments and `ON CONFLICT DO NOTHING` absorbs every row against real Postgres). `cat` already works in that container (the script reads the cursor with it), so `rm` is expected to be available; confirm during execution [ASSUMED].
6. Not asserted live: 429 suppression (window default 60 s), 502 (would require stopping a backend), hard-limit behaviour; cover those with unit tests using fake clocks and injected statfs.
7. Keep the existing "AUDIT ... (proves drain wiring per spool, not AUD-03)" wording honest; update the header comment, which currently says the script "does NOT establish REV-03 or AUD-03", to say what it now does establish.

**Audit API / dashboard (deliverable 10):** `internal/storage/audit_repo.go` needs no code change: it scans `event_type` into a string and casts, selects `http_status`/`duration_ms` (decision rows store `0`, not NULL), and does not select `error_code`. No runtime response validation exists in the control plane (grep for `openapi3filter`/`OapiRequestValidator` found nothing). Contract hygiene, recommended in scope because it is three one-line edits and avoids a spec lie: add `denial` to the `event_type` enum in `api/openapi/control-v1.yaml:869`, the constants in `pkg/api/control/v1/types.gen.go:22-26`, and the union in `web/dashboard/src/api/types.ts:2`. `AuditStream.tsx` never switches on `event_type`, so rendering is unaffected. Showing two rows per allowed request, exposing `error_code`/`suppressed_count`, and filtering by type are dashboard-closure work and stay deferred (CONTEXT: "changes to how the dashboard displays audit rows" are out of scope).

## Requirement closure statement

- **AUD-04** (completion events record backend status, duration and error codes): **can be fully satisfied by this phase**, proven live by the completion-row assertion on the real binary plus unit coverage of upstream error classes.
- **AUD-03** (worker delivers with at-least-once batching and deduplication): after this phase every record kind reaches Postgres (the delivery half). The remaining evidence the milestone audit called out, **at-least-once and deduplication against real Postgres, and multi-segment rotation under load**, closes only if the plan includes the live dedupe-replay check (cheap, step 5 above) and a live rotation check (needs `AEGIS_SPOOL_SEGMENT_BYTES` plus an allowlist entry and a few thousand requests at a small segment size). If the plan omits them, AUD-03 should stay "delivery proven; dedup/rotation unit-evidence only" and the planner should say so rather than tick the checkbox. Crash-mid-batch at-least-once against real Postgres (kill the worker between insert and cursor save) is not covered by either live check and remains unit-level (`TestAuditWorker_RestartRecovery`).

## Open Questions (RESOLVED)

All questions below were resolved on 2026-10-09; user decisions are recorded in 08-CONTEXT.md as D-14 to D-18.

1. **D-04 says stale-lease 503s are identified-principal denials; the code says otherwise.**
   - What we know: lease/uninitialised checks run before authentication (U1/U2/W1/W2), principal is `anonymous`.
   - What's unclear: whether the user intends "record every stale-lease 503".
   - Recommendation: treat as anonymous-503 with suppress-and-count (one row per window per reason). **RESOLVED (user, D-14): suppress and count.**
2. **Extend suppression beyond rate-limit and saturation to the other fail-closed 503/500s (Redis outage, spool write error).**
   - What we know: D-13 names saturation only; a Redis outage turns every request into a denial at request rate.
   - Recommendation: include DEPENDENCY_OUTAGE_REDIS and AUDIT_SPOOL_WRITE_ERROR in the suppressed class (config is a one-line set). **RESOLVED (user, D-14): yes, Redis-outage 503s and spool-write-error 500s are suppressed and counted.**
3. **Uncapped identified denials (404 on arbitrary paths, revoked/quarantined replays) can fill the spool toward the 90% gate (DoS of allow traffic by any valid token holder).**
   - What we know: 403 policy denials are already bounded by the per-principal/route rate limit (it runs before policy); 404 and revoked exits run before the limiter. Path truncation to 2 KiB bounds bytes per record; D-04 says record all.
   - Recommendation: ship D-04 literally, truncate paths, expose `dropped`/`written` metrics, and add an optional `AEGIS_AUDIT_IDENTIFIED_DENIAL_ALLOWANCE` (records per key per window before suppress+count; default 0 = unlimited). **RESOLVED (user, D-15): window suppression applies to all identical identified denials (same principal, route, reason): first per window recorded, then a count. This replaces the optional allowance knob; do not add `AEGIS_AUDIT_IDENTIFIED_DENIAL_ALLOWANCE`.**
4. **Does the type need a migration?** **RESOLVED:** no. The suppressed-count column does (000003), with the `error_code` fallback if the user declines.
5. **Pre-`AuditMiddleware` rejections (P1 concurrency 429, P2 431, P3 workload bearer 401) are invisible to stdout, audit and request metrics, contradicting D-05's "always remain in stdout logs and metrics".**
   - Recommendation: out of scope for durable recording; either defer as a residual gap or add a metrics-only counter (`aegis_http_rejected_total{reason}`) via a small hook in `NewDualServer`. **RESOLVED (user, D-16): metrics-only counter; no audit rows, no middleware reordering.**
6. **Client disconnect status.** **RESOLVED (recommendation):** keep the captured status (502), set `error_code=CLIENT_CANCELED`; synthesising 499 would make audit disagree with the wire.
7. **Decision-flip fix in `AuditMiddleware`.** **RESOLVED:** `decisionSet` flag; explicit decisions win, legacy guess only when no handler set one.
8. **AUD-03 live evidence scope (dedupe replay, rotation).** **RESOLVED (user, D-17): include both the dedupe replay and the rotation run as live checks.**

## Common Pitfalls (summary for verification steps)
See "Pitfall catalogue". Verification must check: (1) poison inputs normalised in producer and worker; (2) lease/uninit classified anonymous; (3) backend 5xx stays `allow`; (4) fault recovers with no traffic; (5) 1xx ignored by the hook; (6) summary `request_id` valid; (7) metric series pre-initialised; (8) compose allowlist updated for any new compose env key; (9) denial recorded before the response; (10) `AppendPreForward` body diff is empty.

## Code Examples

### Hook on first WriteHeader (shape, not final code)
```go
// Source: internal/audit/logger.go (existing type), extended
func (rw *StatusCaptureResponseWriter) WriteHeader(code int) {
    if !rw.wroteHeader {
        rw.StatusCode = code
        rw.wroteHeader = true
        if rw.onFirstHeader != nil && code >= 200 {
            rw.onFirstHeader(code) // runs BEFORE any byte reaches the client (D-07)
        }
        rw.ResponseWriter.WriteHeader(code)
    }
}
```
Note: keep 1xx handling consistent (do not set `wroteHeader` for 100-199 except 101).

### Upstream error classification
```go
// WrapUpstreamErrorHandler sets ac.ErrorCode, then delegates to the proxy's 502 handler.
func WrapUpstreamErrorHandler(next func(http.ResponseWriter, *http.Request, error)) func(http.ResponseWriter, *http.Request, error) {
    return func(w http.ResponseWriter, r *http.Request, err error) {
        ac := FromContext(r.Context()) // r is the inbound request carrying the AuditContext
        var mbe *http.MaxBytesError
        var ne net.Error
        switch {
        case errors.As(err, &mbe):
            ac.SetErrorCode("REQUEST_BODY_TOO_LARGE")
        case errors.Is(err, context.Canceled):
            ac.SetErrorCode("CLIENT_CANCELED")
        case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
            ac.SetErrorCode("UPSTREAM_TIMEOUT")
        default:
            ac.SetErrorCode("UPSTREAM_UNAVAILABLE")
        }
        next(w, r, err)
    }
}
// main.go, both handlers, after NewReverseProxyWithMTLS:
// rp.ErrorHandler = audit.WrapUpstreamErrorHandler(rp.ErrorHandler)
```
`ac.SetErrorCode` is nil-safe (`ac == nil` guarded), so no extra check is needed.

### Normalisation chokepoint
```go
func (e *CompletionEvent) Normalize() {
    e.PrincipalID = clip(e.PrincipalID, 128)  // clip = strip NUL, ToValidUTF8, truncate on rune boundary
    e.PrincipalKind = clip(e.PrincipalKind, 32)
    e.ServiceID, e.RouteID = clip(e.ServiceID, 64), clip(e.RouteID, 64)
    e.HTTPMethod = clip(e.HTTPMethod, 16)
    e.CanonicalPath = clipBytes(e.CanonicalPath, 2048)
    e.Decision, e.ReasonCode = clip(e.Decision, 16), clip(e.ReasonCode, 64)
    e.ClientIP, e.ErrorCode = clip(e.ClientIP, 64), clip(e.ErrorCode, 64)
    e.EventType = clip(e.EventType, 32)
}
```

### Shutdown (see "Shutdown ordering")

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| fsync per record under a mutex | group commit (leader batches frames, one `Write`, one `Sync`) | standard WAL practice (Postgres `commit_delay`, etcd/raft WAL batching) [ASSUMED: training knowledge] | Throughput bounded by flush rate, not request rate. |
| status-based event type guess | explicit `event_type` set by producer | this phase | Denials no longer mislabeled `completion`. |

**Deprecated/outdated:** none relevant.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Cloud network-disk fsync (1-10 ms) remains acceptable with a 2 ms linger | Defaults | Denial latency higher than "few ms" on slow disks; mitigated by natural batching and configurability |
| A2 | `rm` exists in the audit-worker container for the dedupe-replay smoke step | Verification step 5 | Use a one-off `docker compose run`/volume mount or `dc exec ... sh -c` alternative; verify during execution |
| A3 | Group commit is the standard WAL technique (Postgres/etcd) | State of the Art | Informational only |
| A4 | A 10/s per-reason unauthenticated cap is enough forensic signal for operators | Defaults | Too low -> brute-force bursts under-recorded; adjustable by env |
| A5 | 60 s suppression window is acceptable summary latency for operators | Defaults | Adjustable by env |
| A6 | Control-plane health implies migrations have run (so worker never races migration 000003) | Worker changes | Worker retries with cursor held; no loss, only delay |

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | build/tests | ✓ | go1.26.0 | — |
| `-race` | tests | ✓ | works | — |
| Docker / compose | live smoke | ✓ | 29.1.3 / v5.0.0 | not used by research (rules); planner/executor use it |
| curl, jq, psql client | smoke script | ✓ | present | — |
| Postgres 14 server binaries (`/usr/lib/postgresql/14/bin`) | throwaway SQL experiments (done, cleaned up) | ✓ | 14 | production compose uses its own image |
| Host disk headroom | tests | ⚠ root at 88 percent | — | `TMPDIR=/dev/shm/aegis-gotmp`; new tests inject `StatfsFunc` so they do not depend on real usage |

**Missing dependencies with no fallback:** none.

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no (existing) | unchanged |
| V3 Session Management | no | — |
| V4 Access Control | no | audit API roles unchanged (`sec-ops`, `auditor`) |
| V5 Input Validation | **yes** | `CompletionEvent.Normalize()` at producer and worker (NUL, UTF-8, column widths); closed enum for reason labels |
| V6 Cryptography | no | — |
| V7 Logging and Error Handling (ASVS) | **yes** | durable denial/completion records; no secrets (never log bearer tokens, assertion JWTs, query strings); PII-free metrics |
| V11/V12 Business logic and resource handling | **yes** | unauthenticated cap, bounded queues/maps, hard limit; fail-closed on audit loss |

### Known Threat Patterns

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Unauthenticated flood fills disk via denial records | DoS | D-05 token bucket per reason; path truncation; hard limit; metrics |
| Crafted method/path wedges the audit worker (poison row) | DoS / Tampering | Normalise at producer + worker (Pitfall 1) |
| Authenticated user hammers 404/revoked exits | DoS | Optional per-key allowance (Q3), rate of valid tokens |
| Log injection via path/method in JSON | Tampering | JSON encoding via `encoding/json` + `slog` (no string concat); NUL stripped |
| Repudiation if denial lost in crash | Repudiation | Accepted by D-07 (last few ms); metrics expose drops |
| Client spoofing IP via headers | Spoofing | existing: `RemoteAddr` only (middleware); keep |
| Audit blind spot while denials/completions fail | Repudiation | D-12 fail-closed fault gate |

## Sources

### Primary (HIGH confidence)
- Codebase read in this session: `cmd/gateway/main.go`, `internal/audit/{spool,logger,worker,cursor,event}.go` and tests, `internal/proxy/{server,limiter,proxy,probe,validator,errors}.go`, `internal/telemetry/{metrics,http_middleware}.go` and tests, `internal/config/config.go`, `internal/storage/audit_repo.go`, `migrations/000002_*.sql`, `scripts/compose-smoke.sh`, `tests/compose/compose_test.go`, compose files (prune env), `api/openapi/control-v1.yaml`, `web/dashboard/src/api/types.ts`.
- Local experiments (reproduced, scratch dir only, repo untouched): (a) fsync latency on the host NVMe and 64-writer serialized throughput; (b) JSON size of a typical event (462 bytes, 475 framed); (c) Go `ReverseProxy` + `MaxBytesReader` returns 502 via `ErrorHandler` with `*http.MaxBytesError`; (d) Go server delivers `r.URL.Path` containing NUL for `/api/%00x` and accepts a 28-character method; (e) throwaway Postgres 14 with migration 000002: `event_type='denial'` accepted, no CHECK, overlong `http_method` rolls back the whole transaction, NUL rejected, `ALTER TABLE ... ADD COLUMN` on the partitioned table works. Instance and scratch data removed.
- PostgreSQL docs, character types: https://www.postgresql.org/docs/current/datatype-character.html (NUL cannot be stored; `varchar(n)` overflow is an error).
- pgx v5 docs: https://pkg.go.dev/github.com/jackc/pgx/v5#Conn.SendBatch ("All queries are run in an implicit transaction unless explicit transaction control statements are executed").
- ADR-0006, ADR-0005, `.planning/v1.0-MILESTONE-AUDIT.md`, `.planning/REQUIREMENTS.md`, Phase 7 research/verification, `08-CONTEXT.md`.

### Secondary (MEDIUM confidence)
- Group commit as standard WAL practice (training knowledge, not re-verified).

### Tertiary (LOW confidence)
- Behaviour of slower cloud disks (A1).

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH, no new dependencies, all versions read from go.mod.
- Architecture: HIGH for the seam, inventory and spool hazards (code read, hooks traced); MEDIUM for the exact committer tuning.
- Pitfalls: HIGH, the worker-poison, anonymous-503 and decision-flip findings are reproduced or read directly.
- Defaults: MEDIUM, one local disk measurement; all are env-configurable.

**Research date:** 2026-10-09
**Valid until:** 2026-11-08 (code-bound; re-check if `cmd/gateway/main.go` or `internal/audit` change before planning)
