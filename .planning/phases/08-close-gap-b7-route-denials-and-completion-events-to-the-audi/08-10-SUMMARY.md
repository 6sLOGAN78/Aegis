# Phase 8 Plan 10: Hermetic gate and Docker teardown gate

Status: complete. Task 1 gate green apart from the known latency-benchmark flake; Task 2 checkpoint answered "Approved" by the user. No mutating Docker command was run in this plan.

## Task 1: hermetic gate (phase base 94cbd2f)

All `go test` runs used `TMPDIR=/dev/shm/aegis-gotmp` (removed afterwards).

| Check | Command | Result |
|-------|---------|--------|
| Build | `go build ./...` | exit 0 |
| Vet | `go vet ./...` | exit 0 |
| Scoped gofmt | changed + untracked Go files since 94cbd2f piped to `gofmt -l` | prints nothing |
| Full race suite | `go test -race -count=1 $(go list ./... | grep -v /tests/integration)` | exit 1: 25 packages ok, ONE failure: `benchmarks/TestPolicyEngine_LatencyBudget` (p50 286.778us vs 200us budget; p99 944.615us within 2ms) |
| Flake isolation | `go test -count=1 -run TestPolicyEngine_LatencyBudget ./benchmarks/` | exit 0 (ok) - known timing flake under the parallel race suite, not a regression |
| Compose + wiring | `go test -count=1 ./tests/compose/ ./cmd/gateway/` | ok, ok |
| Compose lint | `docker compose -f deployments/compose/docker-compose.{mvp,hardened,distributed}.yml config -q` | exit 0 for each |
| Script syntax | `bash -n scripts/compose-smoke.sh` | exit 0 |
| D-09 | AppendPreForward body sha256 now vs `94cbd2f:internal/audit/spool.go` | identical (8bd6714e...3211f) |
| D-09 call sites | `git diff -U0 94cbd2f -- cmd/gateway/main.go \| grep '^[+-]' \| grep -c AppendPreForward` | 0 |

`git diff --name-only 94cbd2f -- '*.go'` lists only files owned by Phase 8 plans (cmd/gateway, internal/audit, internal/config, internal/proxy, internal/storage/db_test.go, internal/telemetry, pkg/api/control/v1/types.gen.go, tests/compose, tests/dr); no unrelated reformatting.

Note: some packages in the suite (internal/storage) start short-lived testcontainers (postgres + ryuk). That is test-owned, ephemeral and unrelated to the `compose` project.

## Disk headroom (orchestrator amendment: spool-gate ratio)

`/` and the Docker root dir (`/var/lib/docker`) are the same filesystem, `/dev/nvme0n1p5` mounted on `/`.

```
Filesystem      Size  Used Avail Use% Mounted on
/dev/nvme0n1p5  183G  151G   23G  87% /
```

- `df` Use%: 87% (Avail 23G)
- GATE_RATIO = (f_blocks - f_bfree) / f_blocks = 82.5%
- Available to non-root (f_bavail): 24.3 GB (free including reserved: 34.3 GB)
- Amended precondition (GATE_RATIO <= 85.0 and >= 10 GB available): MET. Headroom to the 90% gate is about 7.5 points.
- `docker system df`: Images 17 (8.414GB), Containers 25 (8 active), Local volumes 18 (810MB), Build cache 33 entries (1.189GB).

## Docker inventory (read-only, taken this run)

Aegis (`compose` project) state: NOTHING exists.
- Containers: none (`docker compose -f deployments/compose/docker-compose.mvp.yml ps -a` is empty; no `compose-*` names).
- Images: no `compose-*`, `aegis*`, `golang` or `alpine` images present (consistent with the earlier `docker image prune -a -f` and `docker builder prune -a -f` run on the user's instruction).
- Volumes: no `compose_*` volumes (so `postgres_data` and `aegis_wal_spool` do not exist).
- Networks: no `compose_*` network.
- Ports 8080, 9443, 8085, 8084, 9090, 9091: nothing listening (`ss -ltn` matched none).
- Certs present: `deployments/certs/root-ca.crt` and `assertion-ed25519.pub` exist, so the cert generation step is skipped.

Not ours, will not be touched:
- devrag-stack containers: devrag-stack-app-1 (8088, 8443), devrag-stack-init-1 (exited), devrag-stack-mysql-1 (3306), devrag-stack-es01-1 (9200), devrag-stack-mailpit-1 (8025), devrag-stack-minio-1 (9000-9001), devrag-stack-redis-1 (6380).
- devrag-stack volumes: devrag-stack_esdata01, devrag-stack_minio_data, devrag-stack_mysql_data, devrag-stack_redis_data. Network: devrag-stack_ragflow.
- Other stopped or unrelated containers: flux-plan01-01-proof-postgres-1, flux-plan01-01-proof-redis-1, safar-pg-test, ragflow-*, nginx, mysql, nats, minio, redis, infinity, flux-redis, flux-postgres; volumes docker_*, flux_*, flux-plan01-01-proof_*, six anonymous volumes; networks docker_*, flux_*, devrag_ragflow-network.
- Transient testcontainers (postgres:17.11-alpine plus testcontainers/ryuk) appeared and disappear during this session; they come from Go tests (internal/storage) run in this session and by the concurrent code review. They are not part of `compose` and are not touched.

## What plan 08-11 will pull and build

- Pull: `redis:7.2-alpine` (not present), `golang:alpine` (builder), `alpine:3.21` (runtime stages). `postgres:16-alpine` is already local (a pull may still check it).
- Build: one Dockerfile (`deployments/compose/Dockerfile`), builder stage compiles 7 binaries (gateway, demo-issuer, control-plane, audit-worker, orders, payments, admin) after `go mod download`; 8 runtime stages run `apk add curl` (seed also jq). Needs network for Docker Hub, Go module proxy and Alpine mirrors. Build cache is empty, so expect a long first build (roughly 5 to 15 minutes, longer than Phase 7), then the smoke run (several minutes including 300 rotation requests, suppression-window waits and gateway restarts).
- Disk added: roughly 1 to 2 GB of images and cache, far inside the 24 GB available.

## Teardown approval

Recorded 2026-10-09 by the orchestrator. The user was shown the gate results, the disk numbers (`df` 87% used, 23 GB available; spool gate ratio 82.5% against the amended 85% limit), the current Docker state (no Aegis containers, images, volumes or network; unrelated `devrag-stack` containers running and not to be touched) and the commands plan 08-11 will run, and was asked: "Approve the Docker rebuild, live MVP run and teardown described above for plan 08-11?"

The user's reply, verbatim: "Approved" (approved).

Scope of the approval: `docker compose -f deployments/compose/docker-compose.mvp.yml` `down -v --remove-orphans`, `up -d --build --wait`, per-service `stop` / `restart` / `up -d --wait` / `exec` / `logs` as the smoke script and plan 08-11 require, pulling the base images and building the Aegis images, and the final `down -v --remove-orphans`. Not covered: any prune, the hardened or distributed profiles, or any non-Aegis container, image, volume or network.

No mutating Docker command was executed in this plan.

## Changes between the gate and the live run

The Phase 8 code review (08-REVIEW.md: 1 critical, 7 warnings, 6 info) finished after the Task 1 gate above. Before the live run, the critical and all seven warnings were fixed (08-REVIEW-FIX.md, commits 7dc9e65, 94da2bc, 677281a, 9a0ecac, eb91e6a, 05f1dcf), each with a regression test. After the fixes: `go build ./...` and `go vet ./...` pass; `go test -race -count=1` on everything except `tests/integration` passes apart from `benchmarks/TestPolicyEngine_LatencyBudget`, which passes in isolation; `go test -race -count=5 ./internal/audit/` passes; the `AppendPreForward` body is byte-identical to 94cbd2f. The live run in plan 08-11 therefore exercises the fixed code, not the code the Task 1 table measured.
