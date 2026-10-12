---
title: Flaky DEDUPE section of compose smoke on the distributed profile
created: 2026-10-12
source: 11-15 live smoke
severity: low
---

## Problem
On the first distributed smoke run in plan 11-15, the DEDUPE section failed for all three workers: audit_events count changed 125 -> 159 (worker-1), 159 -> 187 (worker-2), 187 -> 216 (worker-3). A rerun from a clean `down -v` with no code change passed (count unchanged at 125 on all three workers). mvp and hardened passed first time.

## Hypothesis
The count is not stable when the quiesce check (2 equal consecutive 1s samples) passes: with 3 gateways and 3 workers draining independently, rows are still arriving. The monotonically increasing counts across the three worker checks suggest ongoing drain of earlier traffic, not duplicate inserts from replay. Cause is unconfirmed.

## Fix outline
- Before each replay, wait for a stable count across all workers (and all gateway spools drained) for a longer window.
- Better: compare the set of event_ids before and after replay (or assert no duplicate event_id) instead of comparing total row counts.
- Affected: scripts/compose-smoke.sh section 4c (DEDUPE).
