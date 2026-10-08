# Phase 8: Close gap B7: route denials and completion events to the audit spool (AUD-03, AUD-04) - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-10-09
**Phase:** 8-Close gap B7: route denials and completion events to the audit spool
**Areas discussed:** Record shape per request, Which denials are recorded, Durability of new records, Behavior at spool saturation

Questions were asked in one batch of three per area rather than one at a time.

---

## Record shape per request

**Allowed request rows**

| Option | Description | Selected |
|--------|-------------|----------|
| Two linked rows | Keep the pre-forward decision row; add a completion row with its own event_id, linked by request_id | ✓ |
| One row, completed later | Completion overwrites the decision row (same event_id, upsert) | |
| You decide | Leave to research and planning | |

**Denied request rows**

| Option | Description | Selected |
|--------|-------------|----------|
| One row | Single record with decision=deny, reason code, HTTP status, duration | ✓ |
| Two rows, like allows | Decision row then completion row | |
| You decide | Leave to research and planning | |

**Row type**

| Option | Description | Selected |
|--------|-------------|----------|
| Explicit type on the event | Gateway sets the type; worker stores it; untyped old records fall back to the status guess | ✓ |
| Keep the status-based guess | No format change; a denial row would be labelled 'completion' | |
| You decide | Leave to research and planning | |

**User's choice:** Recommended option on all three.
**Notes:** None.

---

## Which denials are recorded

**Scope**

| Option | Description | Selected |
|--------|-------------|----------|
| Everything after authentication | Any rejection of an identified principal; unauthenticated 400/401 stay in logs and metrics | ✓ |
| Every rejection | Including 400s and 401s from anonymous callers | |
| Policy and revocation only | Only 403 policy denials and revoked/quarantined principals | |

**Failed authentication attempts**

| Option | Description | Selected |
|--------|-------------|----------|
| Sampled / rate-capped | Record 401s up to a fixed cap per gateway, drop and count the rest | ✓ |
| No, logs and metrics only | 401s and 400s never reach the spool | |
| Yes, all of them | Every failed authentication recorded durably | |

**429 volume**

| Option | Description | Selected |
|--------|-------------|----------|
| First per window, then count | Record the first 429 per principal and route in a window, then a count | ✓ |
| Every 429 | One record per rate-limited request | |
| You decide | Leave to research and planning | |

**User's choice:** Recommended option on all three.
**Notes:** None.

---

## Durability of new records

**Denials**

| Option | Description | Selected |
|--------|-------------|----------|
| Written, group-flushed | Append before responding; share fsyncs across concurrent denials within a few ms | ✓ |
| fsync before responding | Every denial on disk before the response | |
| Buffered, async | Queue in memory, write in the background | |

**Completions**

| Option | Description | Selected |
|--------|-------------|----------|
| Off the request path | Bounded in-memory queue drained by a batching writer | ✓ |
| Inline with fsync | Write and fsync in the request goroutine | |
| You decide | Leave to research and planning | |

**Shutdown**

| Option | Description | Selected |
|--------|-------------|----------|
| Flush before exit | Drain waits for the queue to be written and fsynced | ✓ |
| Best effort | Do not extend shutdown for the queue | |

**User's choice:** Recommended option on all three.
**Notes:** None.

---

## Behavior at spool saturation

**Denial record at 90%**

| Option | Description | Selected |
|--------|-------------|----------|
| Write into reserved headroom | Denials and completions keep writing up to a higher hard limit, then drop and count | ✓ |
| Drop and count | Nothing new written at 90% | |
| You decide | Leave to research and planning | |

**Completion record cannot be written**

| Option | Description | Selected |
|--------|-------------|----------|
| Count, log, and trip admission | Treat the gateway as saturated; 503 for new allowed requests until writes succeed | ✓ |
| Count and log only | Keep serving | |
| You decide | Leave to research and planning | |

**Saturation 503 records**

| Option | Description | Selected |
|--------|-------------|----------|
| Rate-capped, in headroom | First per principal and route per window, then a count | ✓ |
| No | Metrics and logs only | |
| Every one | One record per refused request | |

**User's choice:** Recommended option on all three.
**Notes:** None.

---

## Claude's Discretion

- Exact tuning numbers (flush interval, queue size, hard limit, cap rate, window length).
- Names of type values and new metrics.
- Representation of the suppressed-count record.
- Whether the unauthenticated cap and the 429/503 suppression share one mechanism.

## Deferred Ideas

None raised. The user declined to explore further gray areas (verification approach, workload-listener parity, tuning numbers were offered).
