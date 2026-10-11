---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
plan: 04
subsystem: control-plane
tags: [identity-mappings, compose, startup-wiring]
requires: [10-02]
provides:
  - Control plane loads AEGIS_IDENTITY_MAPPINGS_FILE at startup (fatal on malformed)
  - seed_roles.json mounted read-only in mvp, distributed, hardened
key-files:
  modified:
    - cmd/control-plane/main.go
    - deployments/compose/docker-compose.mvp.yml
    - deployments/compose/docker-compose.distributed.yml
    - deployments/compose/docker-compose.hardened.yml
    - tests/compose/compose_test.go
requirements-completed: [CTRL-02]
completed: 2026-10-11
---

# Phase 10 Plan 04: Mappings source wiring Summary

Control plane reads AEGIS_IDENTITY_MAPPINGS_FILE after NewAPIServer, calls LoadIdentityMappings (log.Fatalf on error) and SetIdentityMappings; all three compose profiles mount policies/data/seed_roles.json at /etc/aegis/seed_roles.json:ro and set the env var.

## Tasks
1. Startup load - cb57e73
2. Compose mounts and TestControlPlaneMountsIdentityMappings - 9310bc9

## Deviations from Plan
None. No control-plane env allowlist existed in compose_test.go (only a gateway allowlist), so only the new mount test was added.

## Verification
`go build ./...`, `go vet ./cmd/control-plane/`, `go test ./tests/compose/... ./tests/manifests/...` pass. (Pre-existing gofmt diffs in unrelated files left alone.)

## Self-Check: PASSED
