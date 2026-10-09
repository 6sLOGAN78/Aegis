---
phase: 08-close-gap-b7-route-denials-and-completion-events-to-the-audi
plan: 04
subsystem: audit
tags: [audit, governor, suppression, token-bucket, drift-guard]
requires: ["08-01"]
provides:
  - "audit.Governor with Admit/Sweep/Flush taking an explicit clock (contract for plans 07 and 08)"
  - "Disposition (Record, Suppress, Drop), GovernorConfig, Admission{Disposition, Label, Overflow}"
  - "ReasonLabel and ClosedReasonLabels (closed metric label set including OTHER)"
  - "Source-level drift guards for main.go deny reasons, policy engine reasons and rego DENIED_* codes"
affects: [08-05, 08-06, 08-07, 08-08]
tech-stack:
  added: []
  patterns: ["explicit-clock admission policy", "bounded map plus per-label overflow bucket", "go/parser table drift guard"]
key-files:
  created:
    - internal/audit/governor.go
    - internal/audit/governor_test.go
    - internal/audit/main_reasons_test.go
  modified: []
key-decisions:
  - "Outage reasons are keyed by reason only (D-14 literal); saturation stays keyed per principal and route (D-13)"
  - "Map-full overflow goes to a per-reason-label bucket outside MaxKeys: first event Record, rest Suppress, both with Overflow true, so overflow can never amplify fsyncs"
  - "Pending-summary stash is keyed like the entries and merges counts, bounded by MaxKeys + len(ClosedReasonLabels())"
  - "Anonymous events whose reason is not in the unauthenticated class share the OTHER token bucket; Admission.Label still reports the real reason label"
requirements-completed: []
duration: ~25min
completed: 2026-10-09
---

# Phase 8 Plan 04: Denial governor Summary

In-memory admission policy for denial records: one tumbling-window suppressor for identified denials and outage-class reasons, one per-reason token bucket for unauthenticated rejections, all under an injected clock, plus drift guards that keep the classification table honest. AUD-03 and AUD-04 are intentionally not marked complete here (only plan 08-12 decides that).

## Tasks and commits

| Task | Commit | Files |
|------|--------|-------|
| 1. Suppressor, overflow bucket, stash, token buckets | 8b7fa76 | internal/audit/governor.go, internal/audit/governor_test.go |
| 2. Drift guards (main.go, policy engine, rego) | 2c599f1 | internal/audit/main_reasons_test.go |

The Task 2 unauthenticated token-bucket tests (TestGovernorUnauthCap, independence, OTHER sharing) were written alongside the Task 1 implementation and are therefore in the Task 1 commit; Task 2's commit holds only the drift guards. governor.go needed no change for Task 2.

## Exported contract (consumed by plans 07/08)

- `type Disposition int`: `Record`, `Suppress`, `Drop`
- `type GovernorConfig struct { SuppressWindow time.Duration; MaxKeys int; UnauthRate float64; UnauthBurst int }` (zero values default to 60s, 4096, 10, 50)
- `type Admission struct { Disposition Disposition; Label string; Overflow bool }`
- `NewGovernor(cfg) *Governor`; `(*Governor).Admit(ev *CompletionEvent, now time.Time) Admission`; `Sweep(now) []*CompletionEvent`; `Flush(now) []*CompletionEvent`
- `ReasonLabel(reason) string`, `ClosedReasonLabels() []string` (sorted, includes "OTHER")
- Summary events: copy of the first event, fresh UUID EventID, EventType denial, SuppressedCount = repeats only, Timestamp = window end (Flush: min(window end, now)), DurationMS 0, first event's RequestID. Nil event input returns Drop.
- Unexported test accessors: keyCount, overflowCount, stashLen, bucketCount.

## Verification

- `TMPDIR=/dev/shm/aegis-gotmp go test -race -count=1 ./internal/audit/` ok
- `go test -race -count=1 -v ./internal/audit/ -run 'TestGovernor|TestReasonLabel'`: 16 PASS (including TestGovernorOutageByReason, TestGovernorOverflowBucket, TestGovernorStashBounded, TestGovernorConcurrent)
- TestGovernorUnauthCap, TestEveryMainReasonIsClassified (23 literal deny reasons found, minimum 20), TestEveryPolicyReasonIsClassified all PASS
- `go vet ./internal/audit/` clean; `git diff --name-only -- cmd` empty; no time.Now, telemetry or config imports in governor.go; POLICY_EVALUATION_ERROR is not a table key.

## Deviations from Plan

None. Notes: test helpers are all `gov`-prefixed (govNew, govEvent, govAnon, govT0, govWindow, govParse, govStringLit, govClassifyHint). In addition to the plan's checks, TestEveryMainReasonIsClassified also asserts that the default values main.go assigns to its variable `reason` (DENIED_DEFAULT, DENIED_WORKLOAD_FORBIDDEN) are classified. `gofmt -l` reports internal/audit/logger.go as unformatted; that file is not touched by this plan (left alone).

## Known Stubs

None.

## Threat Flags

None.

## Self-Check: PASSED

Files governor.go, governor_test.go, main_reasons_test.go exist; commits 8b7fa76 and 2c599f1 exist.
