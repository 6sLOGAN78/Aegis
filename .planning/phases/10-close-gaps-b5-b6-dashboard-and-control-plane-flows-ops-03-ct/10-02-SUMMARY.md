---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 02
subsystem: control-plane
tags: [publish, rego-validation, identity-mappings, hardening]
requires: []
provides:
  - LoadIdentityMappings validating loader and APIServer.SetIdentityMappings
  - Publish-time Rego validation (422), nil-repo 503, identity mappings in every snapshot
affects: [10-04 main.go wiring]
key-files:
  created:
    - internal/control/identity_mappings.go
    - internal/control/identity_mappings_test.go
    - internal/control/publish_handler_test.go
  modified:
    - internal/control/publish_handler.go
    - internal/control/policy_handler.go
    - internal/control/api.go
key-decisions:
  - "Configured mappings are authoritative; otherwise carry forward from the active snapshot (D-A)"
  - "SetIdentityMappings setter used so NewAPIServer keeps 11 params"
requirements-completed: [CTRL-01, CTRL-02, OPS-03]
duration: 20min
completed: 2026-10-11
---

# Phase 10 Plan 02: Publish path correctness Summary

Publish now validates Rego (syntax plus embedded test_ rules) before signing, stamps sorted identity mappings into every snapshot, and fails closed on nil repos.

## Tasks
1. Identity mappings loader and setter - 3d25c14
2. Publish validation, mappings in payload, nil guards, draft-id validation - b18a22f

## Behaviour
- Invalid Rego or failing test rule: 422, nothing signed, persisted or broadcast.
- Unknown draft: 404. Nil repos/validator: 503. SaveSnapshot error message is generic; real error logged.
- Create draft rejects empty or invalid policy_id with 400; nil policyRepo gives 503.
- Rollback keeps historical payload mappings (tested).

## Deviations from Plan
None. Note: the rollback test seeds the distServer active snapshot at v1 via BroadcastSnapshot so If-Match matches.

## Verification
`go test -race ./internal/control/...` passes.

## Self-Check: PASSED
