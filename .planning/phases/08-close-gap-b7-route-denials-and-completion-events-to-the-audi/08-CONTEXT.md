# Phase 8: Close gap B7: route denials and completion events to the audit spool (AUD-03, AUD-04) - Context

**Gathered:** 2026-10-09
**Status:** Ready for planning

<domain>
## Phase Boundary

Make the gateway write denial records and completion records (backend HTTP status, duration, error code) into the durable disk spool, so the existing audit worker delivers them to PostgreSQL. This closes milestone-audit blocker B7: today `cmd/gateway/main.go:107` builds the audit logger with `audit.NewLogger(nil)`, so completions and every denial go to stdout only, and the spool holds only pre-forward "allow" records.

In scope: the gateway's audit write path (user and workload listeners), the spool's write API and saturation handling for the new record kinds, the record format's type field, the worker's handling of that field, and the metrics for dropped records.

Out of scope (other closure phases): `jti` in the demo issuer, percent-encoded SPIFFE quarantine keys, B8 (quarantine reconstruction, key rotation wiring), B5/B6 (dashboard convergence and publish), B2 (Kubernetes), a multi-directory audit worker, extracting the gateway pipeline from `main()` for testability, and changes to how the dashboard displays audit rows.

</domain>

<decisions>
## Implementation Decisions

### Record shape per request
- **D-01:** An allowed request produces two linked rows: the existing pre-forward decision row, unchanged, plus a separate completion row with its own `event_id`, linked by `request_id`. The completion must not reuse the decision's `event_id` (the worker's `ON CONFLICT (event_date, event_id) DO NOTHING` would silently drop it).
- **D-02:** A denied request produces one row carrying `decision=deny`, the reason code, the HTTP status and the duration.
- **D-03:** The record type is explicit: the gateway sets it when writing the record (decision, completion, denial) and the worker stores it as given. Records already in a spool without a type fall back to today's status-based guess.

### Which denials are recorded
- **D-04:** Every rejection of an identified principal is recorded durably: revoked or quarantined principal, no matching route, rate limit (429), policy deny (403), and fail-closed 503s (stale lease, saturation).
- **D-05:** Unauthenticated rejections (400 malformed request, 401 missing or invalid credentials) are recorded only up to a fixed cap per gateway; beyond the cap they are dropped and counted. They always remain in stdout logs and metrics.
- **D-06:** Rate-limit denials are suppressed per principal and route per window: the first 429 in a window is recorded, repeats are counted, and the count is recorded when the window closes.

### Durability of new records
- **D-07:** A denial record is appended to the spool before the denial response is sent, with fsyncs shared across concurrent denials within a few milliseconds (group flush). Losing the last few milliseconds of denial records in a crash is acceptable.
- **D-08:** A completion record is written off the request path: handed to a bounded in-memory queue drained by a writer that batches fsyncs. It must never delay a response.
- **D-09:** The pre-forward allow record keeps its current guarantee: synchronous fsync before forwarding (AUD-01). This phase must not weaken it.
- **D-10:** On graceful shutdown, queued denial and completion records are written and fsynced within the existing drain window before the spool closes.

### Behavior at spool saturation
- **D-11:** The 90% gate continues to stop allowed traffic (AUD-02). Denial and completion records may keep writing into reserved headroom above 90%, up to a higher hard limit; past the hard limit they are dropped and counted.
- **D-12:** When a completion record cannot be written (queue full, hard limit reached, or write error), the gateway counts it, logs it, and treats itself as saturated: new allowed requests get 503 until writes succeed again. No traffic is admitted while audit is losing data.
- **D-13:** Saturation 503s are recorded like rate-limit denials (first per principal and route per window, then a count), written into the reserved headroom.

### Claude's Discretion
- Exact numbers: group-flush interval, completion queue size, the hard limit above 90%, the cap rate for unauthenticated rejections, and the suppression window length. Research should propose values and justify them; they should be configurable with safe defaults.
- Names of the type values and of the new metrics.
- How the suppressed-count record is represented (a field on the event versus a dedicated summary record).
- Whether the cap in D-05 and the suppression in D-06/D-13 share one mechanism.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Gap definition
- `.planning/v1.0-MILESTONE-AUDIT.md` — blocker B7 and the AUD-03 / AUD-04 / OPS-03 evidence; closure group 2 "Audit completeness"
- `.planning/REQUIREMENTS.md` — AUD-01 through AUD-04 wording; AUD-03 and AUD-04 are unchecked and marked "Gap closure"

### Audit design
- `docs/adr/0006-pre-forward-wal-audit-spool.md` — the pre-forward WAL decision this phase extends; Invariant 10
- `docs/adr/0005-redis-fail-closed-semantics.md` — the fail-closed posture D-12 follows

### Prior phase findings that constrain this one
- `.planning/phases/07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar/07-RESEARCH.md` — how the audit worker consumes a spool (one flat directory, top-level `wal-*.log`, cursor in `<dir>/wal.cursor`, never revisits a non-latest segment)
- `.planning/phases/07-close-gaps-b1-b3-b4-align-compose-configs-with-current-binar/07-VERIFICATION.md` — what Phase 7 proved live and what it left open for AUD-03
- `.planning/phases/03-control-plane-snapshot-streaming-durable-state/03-04-PLAN.md` and `03-04-SUMMARY.md` — original spool and worker design

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/audit/spool.go` — `DiskSpool` with `AppendPreForward` (CRC-framed record, fsync under a mutex, segment rotation), `CheckSaturation` (directory quota and `statfs`, 90%), `IsSaturated`, `UtilizationRatio`. New write paths should reuse the frame format so the worker reads them unchanged.
- `internal/audit/logger.go` — `AuditContext` already accumulates principal, route, decision, reason code, error code and snapshot version through the pipeline; `AuditMiddleware` already computes duration and captures the response status for every request and emits a `CompletionEvent` to the `Logger`. This is the natural hook for completion and denial records.
- `internal/audit/event.go` — `CompletionEvent`; needs the explicit type field (D-03).
- `internal/audit/worker.go` — `ProcessBatch` inserts into `audit_events` with `ON CONFLICT (event_date, event_id) DO NOTHING` and derives `event_type` from `HTTPStatus > 0`; must honor the explicit type.
- `internal/telemetry/metrics.go` — existing spool gauges and counters; add dropped-record counters here.

### Established Patterns
- Fail closed: a dependency or audit failure refuses traffic rather than admitting it unrecorded.
- Client IP is taken from the socket only, never from forwarding headers.
- Low-cardinality Prometheus metrics with negative PII assertions (Phase 4).
- Hand-rolled Go with `sync` primitives; no message broker (Kafka explicitly out of scope in REQUIREMENTS.md).

### Integration Points
- `cmd/gateway/main.go:107` — `audit.NewLogger(nil)`; lines 905 and 915 wrap the user and workload handlers with `AuditMiddleware`; lines 535 and 844 are the two pre-forward appends; line 911 passes `diskSpool.IsSaturated` to the admission gate; lines 940-962 are the drain and `diskSpool.Close()`.
- The request pipeline is an inline closure in `main()`, and tests re-implement it rather than running it (audit finding under DIST-03). Verification of this phase must exercise the real binary's write path, for example through the compose smoke script, not only package tests.
- `scripts/compose-smoke.sh` and `tests/compose/` from Phase 7 give a live harness: the smoke script already counts `audit_events` rows and can be extended to assert denial and completion rows.
- Migration `migrations/000002_create_partitioned_audit_tables.sql` defines `audit_events` (already has `event_type`, `http_status`, `duration_ms`, `error_code`).

</code_context>

<specifics>
## Specific Ideas

- The host running the tests can be over 90% full, which trips the `statfs`-based saturation gate in tests that use the default temp directory (seen in Phase 7). Tests for the new headroom behavior should control the quota explicitly rather than depend on real disk usage.
- All Phase 7 live evidence for AUD-03 was "row count rises"; this phase should be able to show a denied request and a completed request each appearing in `audit_events` with the right type, status and duration.

</specifics>

<deferred>
## Deferred Ideas

None raised during discussion. Items already tracked for other closure phases are listed under Phase Boundary.

</deferred>

---

*Phase: 8-Close gap B7: route denials and completion events to the audit spool (AUD-03, AUD-04)*
*Context gathered: 2026-10-09*
