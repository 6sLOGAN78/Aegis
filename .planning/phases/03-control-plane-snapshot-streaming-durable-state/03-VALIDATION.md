---
phase: 3
slug: control-plane-snapshot-streaming-durable-state
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-10-06
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go Test CLI (`go test -race`) + `miniredis/v2` + `pgxmock/v4` + `bufconn` |
| **Config file** | `deployments/compose/docker-compose.hardened.yml` |
| **Quick run command** | `go test -v -race ./internal/control/... ./internal/snapshot/... ./internal/ratelimit/... ./internal/revocation/... ./internal/audit/...` |
| **Full suite command** | `go test -v -race ./...` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick run command (`go test -v -race ./internal/control/... ./internal/snapshot/... ./internal/ratelimit/... ./internal/revocation/... ./internal/audit/...`)
- **After every plan wave:** Run full suite command (`go test -v -race ./...`)
- **Before `/gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 03-01-01 | 01 | 1 | CTRL-01 | T-03-01 | PostgreSQL repository & goose migrations initialize tables with connection pooling | unit | `go test -v -race ./internal/storage/ -run TestStorage` | ❌ W0 | ⬜ pending |
| 03-01-02 | 01 | 1 | CTRL-01 | T-03-01 | Control plane validator checks route schemas and executes Rego unit tests before draft publication | unit | `go test -v -race ./internal/control/ -run TestValidator` | ❌ W0 | ⬜ pending |
| 03-02-01 | 02 | 2 | CTRL-02, CTRL-06 | T-03-02, T-03-03 | Monotonic Ed25519 signer computes SHA-256 digests, increments version strictly ($N+1$), and rollback republishes with higher version | unit | `go test -v -race ./internal/snapshot/ -run TestSigner` | ❌ W0 | ⬜ pending |
| 03-02-02 | 02 | 2 | CTRL-03, CTRL-05 | T-03-02 | gRPC streaming pushes snapshots via bufconn, gateway performs atomic swap with `sync/atomic.Pointer`, and reports `SnapshotAck` | integration | `go test -v -race ./tests/integration/ -run TestSnapshotStreaming` | ❌ W0 | ⬜ pending |
| 03-02-03 | 02 | 2 | CTRL-04 | T-03-04 | Gateway tracks 10s freshness leases and fails closed (HTTP 503 `POLICY_LEASE_EXPIRED`) after 60s without lease renewal | security | `go test -v -race ./tests/failure/ -run TestLeaseExpiry` | ❌ W0 | ⬜ pending |
| 03-03-01 | 03 | 3 | REV-01 | T-03-05 | Redis GCRA token bucket rate limiter enforces limits per principal and returns HTTP 429 with `Retry-After` | unit | `go test -v -race ./internal/ratelimit/ -run TestRateLimiter` | ❌ W0 | ⬜ pending |
| 03-03-02 | 03 | 3 | REV-03 | T-03-06 | Redis pipelined JTI and principal quarantine checks block callers in <5s with HTTP 403 (`TOKEN_REVOKED`, `PRINCIPAL_QUARANTINED`) | unit | `go test -v -race ./internal/revocation/ -run TestRevocationStore` | ❌ W0 | ⬜ pending |
| 03-03-03 | 03 | 3 | REV-04 | T-03-07 | Redis checks timeout at 200ms and fail closed (HTTP 503 `DEPENDENCY_OUTAGE_REDIS`) on Redis partition or outage | security | `go test -v -race ./tests/failure/ -run TestRedisOutage` | ❌ W0 | ⬜ pending |
| 03-04-01 | 04 | 4 | AUD-01 | T-03-08 | Gateway writes authorization records to disk WAL with CRC32 and executes `fsync` before upstream dispatch | unit | `go test -v -race ./internal/audit/ -run TestDiskSpoolPreForward` | ❌ W0 | ⬜ pending |
| 03-04-02 | 04 | 4 | AUD-02 | T-03-09 | 90% spool saturation gate halts admissions of permitted requests with HTTP 503 (`AUDIT_SPOOL_SATURATED`) | security | `go test -v -race ./tests/failure/ -run TestSpoolSaturation` | ❌ W0 | ⬜ pending |
| 03-04-03 | 04 | 4 | AUD-03 | T-03-08 | Async audit worker tails WAL, batches events via `pgx.Batch` to partitioned PostgreSQL, and saves durable cursor | unit | `go test -v -race ./internal/audit/ -run TestAuditWorkerBatching` | ❌ W0 | ⬜ pending |
| 03-05-01 | 05 | 5 | CTRL-04, REV-04, AUD-02 | T-03-04, T-03-07, T-03-09 | Comprehensive failure mode integration suite verifies all fail-closed boundaries (Redis kill, lease timeout, spool full) | integration | `go test -v -race ./tests/failure/...` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Go dependencies installed:
  - `github.com/jackc/pgx/v5`
  - `github.com/pressly/goose/v3`
  - `github.com/redis/go-redis/v9`
  - `github.com/go-redis/redis_rate/v10`
  - `github.com/alicebob/miniredis/v2`
  - `github.com/pashagolub/pgxmock/v4`
- [ ] Directory scaffolding:
  - `migrations/`
  - `internal/storage/`
  - `internal/control/`
  - `internal/snapshot/`
  - `internal/ratelimit/`
  - `internal/revocation/`
  - `cmd/control-plane/`
  - `cmd/audit-worker/`
  - `tests/failure/`
- [ ] Database migration files:
  - `migrations/000001_create_control_plane_tables.sql`
  - `migrations/000002_create_partitioned_audit_tables.sql`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| None | N/A | N/A | All Phase 3 behaviors have automated verification via Go test suite, miniredis, pgxmock, and bufconn. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-10-06
