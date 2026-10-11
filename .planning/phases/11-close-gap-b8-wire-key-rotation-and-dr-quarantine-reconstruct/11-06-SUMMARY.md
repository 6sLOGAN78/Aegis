---
phase: 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
plan: 06
subsystem: keyset-reload
tags: [keyset, rotation, ed25519, sighup, demo-issuer]
requires: [11-01]
provides:
  - keyset.LoadDir, keyset.New, Reloader.Load/Reload/Run, keyset.WithMetrics, keyset.ParseInterval
  - demo issuer loadIssuerKey (AEGIS_ISSUER_PRIVATE_KEY_PATH)
affects: [11-08]
key-files:
  created:
    - internal/keyset/reloader.go
    - internal/keyset/reloader_test.go
    - cmd/demo-issuer/loadkey_test.go
  modified:
    - cmd/demo-issuer/main.go
    - tests/rotation/rotation_test.go
decisions:
  - "Every regular non-dot file is a key; symlinks followed; any bad file rejects the whole set (last good kept)."
  - "Hash change detection over sorted, deduped keys; unchanged reloads skip apply."
metrics:
  tasks: 3
  completed: 2026-10-12
requirements: [AUTH-01, CTRL-02]
---

# Phase 11 Plan 06: Keyset reloader Summary

Directory-based Ed25519 public keyset reloader (poll plus SIGHUP, atomic swap into Verifier/TokenValidator), file-based demo issuer signing key, and rotation drills run through the real reloader.

## Commits

- 09598b7: keyset reloader and tests
- ce39b21: demo issuer key loading
- 38c6518: rotation drills via reloader

## Deviations from Plan

- Issuer key tests live in new `cmd/demo-issuer/loadkey_test.go` rather than main_test.go (tooling constraint on appending); no behavior difference.
- Metric assertions in reloader tests read values via `prometheus/client_model` (testutil would have required go.mod changes).
- Worktree base was reset to 52cbdd0 per the startup check (it contained wave 1).

## Verification

`go vet` and `go test ./tests/rotation ./internal/keyset ./cmd/demo-issuer ./internal/identity ./internal/snapshot -race -count=1` all pass.

## Known Stubs

None.

## Self-Check: PASSED
