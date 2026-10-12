---
title: Close the multi-replica quarantine/reconcile race (WR-01 follow-up)
created: 2026-10-12
source: phase 11 code review WR-01
severity: medium
---

## Problem

WR-01 serialises quarantine/unquarantine/revoke write-through with Reconcile
via the in-process SecurityState mutex (internal/control/securitystate.go). With
more than one control-plane replica, replica A's Reconcile can snapshot Redis,
replica B can unquarantine and re-quarantine the same principal, and A's prune
can delete the fresh Redis key. The principal is then unquarantined until the
next reconcile re-asserts the Postgres row (up to the 30s reconcile interval),
even though the API returned 200.

Safe today only with a single control-plane replica.

## Fix outline

Option A: take a Postgres advisory lock (pg_advisory_xact_lock on a fixed key)
around Reconcile's list-then-prune and around each write-through.
Option B: compare-and-delete prune: before deleting a stray Redis key, re-check
the Postgres row for that principal inside the same lock-free step (and use a
Redis WATCH/MULTI or Lua script so a key re-written after the check survives).
Add a two-replica regression test.
