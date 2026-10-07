---
phase: 5
slug: distributed-resilience-chaos
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-10-07
---

# Phase 5 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go Test CLI (`go test -race`) + `httptest` + `miniredis/v2` + Docker Compose + Grafana k6 + Go Benchmark (`testing.B`) |
| **Config file** | `deployments/compose/docker-compose.distributed.yml`, `deployments/compose/haproxy/haproxy.cfg` |
| **Quick run command** | `go test -v -race ./tests/chaos/...` |
| **Full suite command** | `go test -race ./... && docker compose -f deployments/compose/docker-compose.distributed.yml config` |
| **Estimated runtime** | ~20 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick run command (`go test -v -race ./tests/chaos/...` or specific package test)
- **After every plan wave:** Run full suite command (`go test -race ./...`)
- **Before `/gsd-verify-work`:** Full suite must be green and benchmarks verified
- **Max feedback latency:** 20 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 05-01-01 | 01 | 1 | DIST-01 | T-05-01 | Gateway `/livez` and `/readyz` probes check lease age, snapshot status, spool capacity, and draining state | unit/integration | `go test -v -race ./internal/proxy/ -run TestProbeHandler` | ❌ W0 | ⬜ pending |
| 05-01-02 | 01 | 1 | DIST-01 | T-05-02 | Gateway 30-second graceful connection draining on SIGTERM finishes in-flight requests while `/readyz` returns 503 | integration | `go test -v -race ./cmd/gateway/ -run TestGatewayGracefulDrain` | ❌ W0 | ⬜ pending |
| 05-01-03 | 01 | 1 | DIST-01 | T-05-01 | Distributed Docker Compose with 3 gateway replicas behind HAProxy load balancer with active HTTP health checks | config/integration | `docker compose -f deployments/compose/docker-compose.distributed.yml config` | ❌ W0 | ⬜ pending |
| 05-02-01 | 02 | 2 | DIST-01 | T-05-03 | Gateway gRPC snapshot client implements randomized exponential backoff with Full Jitter (100ms–5s) | unit | `go test -v -race ./internal/snapshot/ -run TestStreamClientJitterBackoff` | ❌ W0 | ⬜ pending |
| 05-02-02 | 02 | 2 | DIST-01 | T-05-04 | Fleet convergence under load: 3 gateway replicas converge to newly published snapshot within 5s at p99 | integration | `go test -v -race ./tests/chaos/ -run TestFleetConvergenceUnderLoad` | ❌ W0 | ⬜ pending |
| 05-03-01 | 03 | 3 | DIST-03 | T-05-05 | Chaos suite: killed gateway process failover through load balancer with zero dropped requests | chaos | `go test -v -race ./tests/chaos/ -run TestChaosGatewayProcessKill` | ❌ W0 | ⬜ pending |
| 05-03-02 | 03 | 3 | DIST-03 | T-05-06 | Chaos suite: control plane outage, Redis partition, DB outage, spool saturation adhere to fail-closed 503 | chaos | `go test -v -race ./tests/chaos/ -run "TestChaosDependencies|TestChaosSecurityInvariants"` | ❌ W0 | ⬜ pending |
| 05-03-03 | 03 | 3 | DIST-01 | T-05-07 | Reproducible k6 load tests and Go benchmarks verify <2ms OPA evaluation and <20ms p99 gateway added latency at 1,000 RPS | benchmark | `go test -v -bench=. -benchmem ./benchmarks/...` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Directory scaffolding:
  - `deployments/compose/haproxy/` with `haproxy.cfg`
  - `benchmarks/k6/` with load testing scripts
  - `benchmarks/` with Go engine microbenchmarks
  - `tests/chaos/` with automated chaos test harness
- [ ] Docker Compose profile:
  - `deployments/compose/docker-compose.distributed.yml` declared and validated

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| None | N/A | N/A | All Phase 5 behaviors have automated verification via Go test suite, chaos harness, Docker Compose config validation, and reproducible Go microbenchmarks / k6 scripts. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 20s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-10-07
