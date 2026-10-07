# Phase 7: Close gaps B1, B3, B4 - align compose configs with current binaries - Research

**Researched:** 2026-10-08
**Domain:** Docker Compose deployment wiring for a Go zero-trust gateway (gateway, control-plane, audit-worker, demo-issuer, backends)
**Confidence:** HIGH (every audit claim re-checked against code, and the key behaviours reproduced live with the real binaries)

## User Constraints

No CONTEXT.md exists for this phase (the user chose to plan without discuss-phase). There are no locked decisions, discretion areas or deferred ideas from a discussion. Scope comes from the ROADMAP Phase 7 entry and `.planning/v1.0-MILESTONE-AUDIT.md` closure group 1 (B1, B3, B4, port drift, `stop_grace_period`). Requirement IDs to address: REV-03, AUD-03. No `./CLAUDE.md` exists, and `.agents/skills/` holds only generic GSD workflow symlinks, so there are no project-specific directives.

Out of scope (the audit assigns these to other closure groups): demo-issuer `jti`, percent-encoded SPIFFE quarantine keys, B2 (Kubernetes), B5, B6, B7 (`audit.NewLogger(nil)`), B8, and any Go change to the pipeline.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| REV-03 | Operator quarantine/revoke reaches the gateway fleet | B4 confirmed and reproduced (control plane has no `AEGIS_REDIS_ADDR`, quarantine returns 503). Fix is config-only. After the fix the endpoint returns 200 (reproduced). Requirement stays UNSATISFIED because of the jti, encoded-SPIFFE-key and B8 gaps. |
| AUD-03 | Audit events are durably drained from every gateway WAL to Postgres | B3 confirmed (worker reads one flat dir). Fix is config-only: one audit-worker per gateway spool. First live proof that worker output reaches real Postgres (1 row). Requirement stays UNSATISFIED because of B7 and the unproven multi-writer story. |
</phase_requirements>

## Summary

All three blockers are real as the audit states them, and all three are fixable with compose and Dockerfile and shell changes only. No Go change is required to close B1, B3 or B4. The audit's wording for B1 needs one correction: the MVP gateway does not just "fail to load policy". Without `AEGIS_CONTROL_PLANE_GRPC_ADDR` it dials `localhost:9090` inside its own container, never receives a snapshot, and answers every request `503 UNINITIALIZED` (reproduced). Without `AEGIS_REDIS_ADDR` it defaults to `localhost:6379`, so its revocation lookup fails closed. `AEGIS_ROUTES_PATH` is read into `cfg.RoutesFilePath` and consumed by nothing. `AEGIS_POLICY_PATH` is read by nothing. The gateway takes routes and policy only from the signed snapshot.

The decisive finding for the MVP compose is that it can be made to start and be ready by config alone, but it cannot serve a single allowed request by config alone. The control plane has no env var, flag or code path that loads `policies/rego/authz.rego` or `policies/data/routes.json`; with no snapshot in Postgres it signs an empty deny-all v1 (`cmd/control-plane/main.go:190-225`). I proved a config-and-script-only workaround live: a one-shot `seed` service that calls the existing control-plane REST API (login, `POST /routes` x5, `POST /policies`, `POST /policies/{id}/publish`) produced snapshot v3, after which a developer token got through to the upstream (502 only because `orders` does not resolve on my host) and an admin-path request got `403 DENIED_DEVELOPER_ADMIN_FORBIDDEN`. This does not depend on out-of-scope work, but it is a new artifact (a seed script plus a Dockerfile target), and it has two traps: `routes.json` is rejected verbatim by the API (burst < rps on all 5 routes), and the API session cookie is `Secure` so a curl cookie jar will drop it over plain HTTP. A code change (control plane loading the files at bootstrap) is the alternative and belongs to POL-03 closure; it is not needed here.

For B3 the audit-worker reads exactly one flat directory (`AEGIS_SPOOL_DIR`), only top-level `wal-*.log` files, and keeps its cursor in `<dir>/wal.cursor`. Mounting three volumes as subdirectories silently ingests nothing (reproduced: 0 rows, no error). Sharing one volume among three gateways is unsafe. The config-only answer is three audit-worker services (one per spool volume, each rw). A code change is not unavoidable, so none is recommended.

**Primary recommendation:** Rework `docker-compose.mvp.yml` to the hardened-style topology (postgres, redis, control-plane with `AEGIS_REDIS_ADDR`, gateway with control-plane/Redis/spool wiring, one audit-worker, a one-shot `seed` that the gateway waits on); split the distributed audit-worker into `audit-worker-1/2/3`; add `AEGIS_REDIS_ADDR` to the control-plane in all three compose files; remove the dead `AEGIS_ROUTES_PATH`/`AEGIS_POLICY_PATH`; set `stop_grace_period: 45s` on every gateway; verify with a hermetic Go test over the compose YAML plus a rebuilt-image compose smoke run.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Snapshot (routes + policy) distribution | API / Backend (control-plane gRPC :9090) | Gateway (consumer) | Gateway has no static route/policy loader; only the verified snapshot stream |
| Initial routes/policy content (MVP) | API / Backend (control-plane REST) | Compose one-shot `seed` | No binary reads the files; seed drives the existing REST API |
| Quarantine / revocation state | Database / Storage (Redis) | Control-plane (writer), gateway (reader) | Both binaries need the same `AEGIS_REDIS_ADDR` |
| Audit durability | Gateway-local WAL volume | audit-worker (drain) -> Postgres | One worker per spool dir (worker reads a single flat dir) |
| Audit schema | control-plane (goose migrations at startup) | audit-worker (inserts) | `audit_events` is created by the control plane, not the worker |
| Graceful drain | Gateway process (30s) | Compose runtime (`stop_grace_period`) | Runtime SIGKILLs at 10s by default, before the 30s drain ends |

## Standard Stack

No new libraries. Everything in this phase is YAML, Dockerfile, shell and (for tests) the existing Go test stack.

### Core
| Component | Version | Purpose | Why Standard |
|-----------|---------|---------|--------------|
| Docker Compose | v5.0.0 on this host [VERIFIED: `docker compose version`] | Orchestration; supports `x-` extension fields/anchors, `depends_on.condition: service_completed_successfully`, `up --wait` | Already the project's deployment tool |
| Go test + testify + gopkg.in/yaml.v3 | go 1.26.0, yaml.v3 v3.0.1 in go.mod [VERIFIED: go.mod, `go version`] | Hermetic compose lint test, mirroring `tests/manifests/manifest_test.go` | Existing precedent in this repo |
| alpine `jq` (apk) | n/a | Build JSON for the seed script (rego needs JSON escaping; burst fix-up) | Only needed if the seed script lives in an alpine image; `curl` is already installed in the gateway/control-plane images |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| One audit-worker per spool (config-only) | One worker over a shared spool volume | Unsafe: `NewDiskSpool` reopens the lexicographically latest segment for append, so two gateways can interleave writes into one file. The worker also treats any non-latest segment as complete and never revisits it, so records written later to a "non-latest" file owned by another gateway are lost. Reject. |
| One audit-worker per spool | Worker with multi-dir support (`AEGIS_SPOOL_DIRS`) | Go change in `internal/audit/worker.go` and `cmd/audit-worker/main.go`. Not needed for B3; belongs to closure group 2 ("all-replica draining") if wanted. |
| Seed via REST in a one-shot service | Control plane loads `AEGIS_ROUTES_PATH` / `AEGIS_POLICY_PATH` at bootstrap | Go change; also closes POL-03 but is outside this phase. |
| Rewrite mvp.yml | Point `make compose-up`/test at hardened.yml | `tests/integration/bypass_test.go:23` hardcodes the mvp path, but hardened has the same service names (`gateway`, `orders`, `payments`, `admin`), so either works; hardened has the same B4 gap and no seed, so it needs the same fixes anyway. |

**Installation:** none (no new packages). If the seed image installs `jq`: `apk add --no-cache curl jq` inside a new Dockerfile target.

## Package Legitimacy Audit

No external language packages (npm/PyPI/crates) are added. The only new third-party artifact is the Alpine `jq` apk package, installed from the official Alpine 3.21 repository in a Dockerfile target. slopcheck is not applicable to apk packages. `jq` is a long-established Alpine main-repo package [ASSUMED: from training knowledge; not checked against pkgs.alpinelinux.org in this session]. If the planner prefers zero new dependencies, write the seed in Go (`go run`-built binary in the Dockerfile builder stage) instead of shell+jq.

| Package | Registry | Age | Downloads | Source Repo | slopcheck | Disposition |
|---------|----------|-----|-----------|-------------|-----------|-------------|
| jq | Alpine apk (main) | many years | n/a | github.com/jqlang/jq | n/a (not npm/PyPI/crates) | Approved, tagged [ASSUMED] |

**Packages removed due to slopcheck [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

## Architecture Patterns

### System Architecture Diagram (target MVP compose)

```
host :8085 --> demo-issuer (PORT=8085) --- JWT (EdDSA, demo seed) ---> client
host :8080 / :9443 --> gateway
                         |  (1) gRPC snapshot stream  AEGIS_CONTROL_PLANE_GRPC_ADDR=control-plane:9090
                         |  (2) revocation/quarantine/ratelimit  AEGIS_REDIS_ADDR=redis:6379
                         |  (3) pre-forward WAL fsync  AEGIS_SPOOL_DIR=/var/log/aegis/wal (volume aegis_wal_spool)
                         |  (4) mTLS forward --> orders:8081 / payments:8082 / admin:8083 (no published ports)
                         v
control-plane :8084 REST, :9090 gRPC, :9092 metrics
   | AEGIS_REDIS_ADDR=redis:6379   (B4 fix)       | AEGIS_DB_* --> postgres:5432 (runs goose migrations incl. audit_events)
   | bootstrap: empty deny-all snapshot v1 if DB has none
   ^
seed (one-shot, restart: "no", exits 0)  -> login admin -> POST /routes x5 -> POST /policies -> POST /policies/{id}/publish (If-Match "<current>")
   gateway depends_on: seed: service_completed_successfully  (so the first snapshot the gateway sees already has routes+policy)

aegis_wal_spool volume --> audit-worker (AEGIS_SPOOL_DIR=/var/log/aegis/wal, rw because wal.cursor lives there) --> postgres.audit_events
```

Distributed profile: same, with `aegis_wal_spool_{1,2,3}` each mounted into the matching `gateway-N` and `audit-worker-N`.

### Recommended Project Structure
```
deployments/compose/
├── Dockerfile                    # add a `seed` target (alpine + curl + jq + script)  [or reuse control-plane target + mounted script]
├── docker-compose.mvp.yml        # rewritten: postgres, redis, control-plane, seed, gateway, audit-worker, issuer, backends
├── docker-compose.hardened.yml   # B4 + stop_grace_period + seed + dead env removal
├── docker-compose.distributed.yml# B4 + audit-worker-1..3 + stop_grace_period + seed + dead env removal
└── seed/seed.sh                  # idempotent REST seeding script (new)
tests/compose/compose_test.go     # hermetic YAML lint (new), modelled on tests/manifests
```

### Pattern 1: one audit-worker per spool using a compose extension anchor
**What:** A shared `x-audit-worker` block (env, build target, depends_on) merged into three services that differ only in the mounted volume.
**When to use:** Distributed profile (3 spools). Mirrors worker semantics: one flat dir, one cursor.
**Example:**
```yaml
# Source: pattern derived from internal/audit/worker.go (single SpoolDir) and verified to parse with Compose v5.0.0 `config`
x-audit-worker: &audit-worker
  build: { context: ../../, dockerfile: deployments/compose/Dockerfile, target: audit-worker }
  environment:
    - AEGIS_SPOOL_DIR=/var/log/aegis/wal
    - AEGIS_DB_HOST=postgres
    - AEGIS_DB_PORT=5432
    - AEGIS_DB_USER=aegis
    - AEGIS_DB_PASSWORD=aegis-secret-pw
    - AEGIS_DB_NAME=aegis
    - AEGIS_DB_SSLMODE=disable
    - AEGIS_PRUNE_ARCHIVED=true
  depends_on:
    postgres: { condition: service_healthy }
    control-plane: { condition: service_started }   # control plane creates audit_events; worker only warns+retries if absent
  networks: [aegis-internal]
services:
  audit-worker-1: { <<: *audit-worker, volumes: ["aegis_wal_spool_1:/var/log/aegis/wal"] }
  audit-worker-2: { <<: *audit-worker, volumes: ["aegis_wal_spool_2:/var/log/aegis/wal"] }
  audit-worker-3: { <<: *audit-worker, volumes: ["aegis_wal_spool_3:/var/log/aegis/wal"] }
```
(Note: `<<:` shallow-merges keys; `volumes`/`depends_on` overrides replace, which is what is wanted here. The planner should confirm with `docker compose config`.)

### Pattern 2: gateway waits for the seed, not the other way round
**What:** `gateway.depends_on.seed.condition: service_completed_successfully`. The seed needs only postgres, redis and control-plane, never the gateway.
**Why:** removes the race between "gateway ready on bootstrap snapshot v1" and "routes published". Verified on Compose v5.0.0 with a toy stack: `up -d --wait` returns success with an exited-0 one-shot dependency, and the dependent service starts after it. [VERIFIED: local experiment, Compose v5.0.0]
**Idempotency requirement:** `up` can be re-run against a persisted `postgres_data` volume (snapshot already at v>=2). Seed must tolerate that: policy draft ID conflict => treat as already seeded; on `412` publish response read the `ETag` header (current version) rather than assuming `"1"`.

### Pattern 3: healthchecks for readiness (optional but recommended)
No Aegis service defines a healthcheck today (only postgres and redis do). Candidates, all using `curl` that is already in the images:
- gateway: `curl -sf http://127.0.0.1:8080/readyz` (200 only with fresh lease + snapshot + unsaturated spool; `internal/proxy/probe.go`).
- control-plane: `curl -s -o /dev/null http://127.0.0.1:8084/control/v1/auth/me` (no `-f`: 401 is fine, refused connection is nonzero). Do not use `/livez` or `/readyz` on the control plane; they are not registered there (audit B2 note).

### Anti-Patterns to Avoid
- **Mounting multiple spool volumes into one audit-worker as subdirs:** ingests 0 rows with no error (reproduced).
- **Sharing one spool volume across gateways:** interleaved appends and lost records (see Alternatives).
- **Posting `policies/data/routes.json` verbatim to `POST /control/v1/routes`:** all 5 routes are rejected (400 INVALID_ROUTE: burst must be >= rps; the file has burst < rps everywhere). Seed must use `burst = max(burst, rps)`.
- **Using a curl cookie jar against the plain-HTTP control plane:** the session cookie is `Secure`; send `-H "Cookie: aegis_session=<id>"` and `-H "X-CSRF-Token: <csrf>"` explicitly (login returns the csrf token in the JSON body).
- **Relying on `localhost:6379` defaults inside containers:** a host Redis can mask B4 in native runs; it cannot in a container, but it did mask it in my first native repro.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Draining N spools | A new multi-dir worker or a shared-volume scheme | N audit-worker services (YAML anchor) | Worker is single-dir by design; cursor lives in the dir |
| Loading routes/policy into the fleet | A new control-plane file loader (for this phase) | Existing REST flow: routes -> policy draft -> publish | Same code path operators use; already produces signed snapshots |
| Waiting for startup order | `sleep N` in scripts | `depends_on` conditions (`service_healthy`, `service_completed_successfully`) | Deterministic; verified on Compose v5 |
| Compose validity checks | Eyeballing YAML | `docker compose -f X config -q` + a Go YAML lint test | Fast, hermetic, repeatable |

**Key insight:** every gap here is wiring between pieces that already work in isolation (the audit's own conclusion). Do not add features; make the shipped configs name the env vars the binaries actually read.

## Runtime State Inventory

Not a rename/refactor/migration phase. However, two runtime-state facts matter for execution and verification:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | Named volumes `postgres_data`, `aegis_wal_spool*` persist across `up`. A persisted control-plane DB already holds snapshot >= v2, so the seed must be idempotent; `down -v` resets. | Seed idempotency; use `down -v` in verification |
| Live service config | A stale MVP stack from the pre-Phase-3 image is currently running on this machine (`compose-gateway-1`, `compose-orders-1`, `compose-payments-1`, `compose-admin-1`, `compose-demo-issuer-1`, publishing 8080, 9443, 8085) [VERIFIED: `docker ps`]. | Must `docker compose -f ...mvp.yml down` before verifying, or the Go tests will find ports already answering and skip `up` |
| OS-registered state | None | none |
| Secrets/env vars | None renamed. `AEGIS_ROUTES_PATH` / `AEGIS_POLICY_PATH` are removed from compose only; nothing else references them (grep: only `internal/config/config.go`, its test, and the compose files). | Remove from compose; leave `internal/config` untouched (dead-code cleanup is separate tech debt) |
| Build artifacts | Stale images `compose-gateway:latest` etc. (built 2026-10-06, before Phase 3). No `compose-control-plane` or `compose-audit-worker` image exists locally. | Use `up --build` / `compose-build` in verification |

## Common Pitfalls

### Pitfall 1: Tests pass against stale images
**What goes wrong:** `ensureComposeCluster` (`tests/integration/bypass_test.go:27-51`) first probes `localhost:8080/health` and `localhost:8085/health`; if both answer (any HTTP reply), it skips `up`. Even when it runs `docker compose up -d`, there is no `--build`, so existing `compose-*:latest` images are reused.
**Why it happens:** compose services use `build:` with no `image:` and no `--build`.
**How to avoid:** verify with `docker compose -f ...mvp.yml down -v && docker compose -f ...mvp.yml up -d --build --wait` before running the Go tests, and change `Makefile` `compose-up` to `up -d --build --wait` (and `--wait` is safe, see Pattern 2).
**Warning signs:** `docker images` shows `compose-gateway` older than the latest commit; `docker compose ps` shows no control-plane.

### Pitfall 2: Seed rejected or half-applied
**What goes wrong:** routes 400 (burst < rps), draft succeeds, publish publishes a snapshot with zero routes; the gateway then answers `404 No matching upstream route found` for everything (reproduced).
**How to avoid:** apply `burst=max(burst,rps)`; make the seed fail (non-zero exit) if any POST returns non-2xx so the gateway never starts on a half-seeded snapshot.

### Pitfall 3: Worker starts before the audit table exists
**What goes wrong:** `audit_events` is created by the control plane's goose migration (`internal/storage/migration.go`, run at `cmd/control-plane/main.go:167`), not by the worker. Worker only depends on postgres in current compose, so it logs per-tick batch errors until the control plane is up.
**How to avoid:** `depends_on: control-plane: service_started` (soft; log noise is harmless) or a control-plane healthcheck with `service_healthy`.

### Pitfall 4: Spool volume mounted read-only for the worker
**What goes wrong:** worker writes `wal.cursor` and `wal.cursor.tmp` into the spool dir (`internal/audit/cursor.go:26-28`) and prunes archived segments. A `:ro` mount breaks checkpointing.
**How to avoid:** mount rw.

### Pitfall 5: stop_grace_period shorter than drain
**What goes wrong:** gateway drains for up to `AEGIS_DRAIN_TIMEOUT` (default 30s, `cmd/gateway/main.go:922-927`), then `streamClient.Stop()` and up to 5s for the metrics server (`:953-956`), i.e. up to ~35s. Compose default is 10s, then SIGKILL, so in-flight requests are cut and `diskSpool.Close()` may not run.
**How to avoid:** `stop_grace_period: 45s` on every gateway service in all three files (margin for non-default `AEGIS_DRAIN_TIMEOUT` is the planner's call; the hermetic test should assert `grace >= drain + 5s`). The control plane uses `grpcServer.GracefulStop()` after a 5s shutdown (`main.go:294-302`), which waits for long-lived gateway streams; compose stops gateways first (reverse `depends_on`) so `down` is fine, but an isolated `docker compose restart control-plane` can hit the grace limit. A `stop_grace_period: 15s` on the control plane is reasonable. Audit-worker needs its final 2s drain only; default is OK.

### Pitfall 6: Mixed spool-dir semantics in the gateway
The gateway's spool is created `0700` by whichever process starts first. Both gateway and audit-worker images run as root (no `USER` in Dockerfile), so ownership is not a problem today; do not add a `USER` to one image without the other.

## Code Examples

### Seed script core (shell), verified shape against the live API
```sh
# Source: reproduced live against cmd/control-plane (this research); endpoints from internal/control/api.go:98-145
B=http://control-plane:8084
LOGIN=$(curl -s -X POST "$B/control/v1/auth/login" -d '{"username":"admin","password":"admin-secret"}' -D /tmp/h)
CSRF=$(echo "$LOGIN" | jq -r .csrf_token)
SID=$(grep -i '^set-cookie: aegis_session=' /tmp/h | sed 's/.*aegis_session=\([^;]*\).*/\1/')
H="-H Cookie:aegis_session=$SID -H X-CSRF-Token:$CSRF -H Content-Type:application/json"
# routes: transform policies/data/routes.json, fix burst
jq -c '.[] | {route_id,service_id,http_method,path_template,upstream_url,upstream_spiffe_id,
     rate_limit_rps:.rate_limit.requests_per_second,
     rate_limit_burst:([.rate_limit.burst,.rate_limit.requests_per_second]|max),
     timeout_ms:.timeout.upstream_timeout_ms, requires_workload_mtls}' routes.json |
 while read -r r; do curl -sf $H -X POST "$B/control/v1/routes" -d "$r" >/dev/null || exit 1; done
# policy draft + publish
jq -n --rawfile s authz.rego '{policy_id:"mvp-authz",name:"authz.rego",source_rego:$s}' |
  curl -sf $H -X POST "$B/control/v1/policies" -d @-
curl -sf $H -X POST "$B/control/v1/policies/mvp-authz/publish" -H 'If-Match: "1"'   # on 412 retry with the ETag from the response
```
(Operator credentials `admin` / `admin-secret` are the hardcoded demo operators in `internal/control/auth.go:16-20`; pass them to the seed through env vars, not literals, to keep them in one place.)

### Hermetic compose lint test (outline)
```go
// Source: modelled on tests/manifests/manifest_test.go (yaml.v3 + testify)
// For each compose file: parse services.*.environment (list form "K=V").
// Assertions:
//  - every AEGIS_* key on gateway* is in the gateway-readable allowlist (config.go + main.go list below)
//  - control-plane has AEGIS_REDIS_ADDR; gateway* have AEGIS_REDIS_ADDR and AEGIS_CONTROL_PLANE_GRPC_ADDR
//  - number of audit-worker* services == number of gateway* services in distributed, and each mounts the same
//    named volume as its gateway at /var/log/aegis/wal; no volume is mounted by two gateways
//  - every gateway has stop_grace_period >= drain(30s default or AEGIS_DRAIN_TIMEOUT) + 5s
//  - no service sets AEGIS_POLICY_PATH or AEGIS_ROUTES_PATH
```

## Verified Findings

### 1. Compose files and Dockerfile inventory

Files (`git ls-files` + `ls`): `deployments/compose/docker-compose.mvp.yml` (102 lines), `docker-compose.hardened.yml` (192), `docker-compose.distributed.yml` (305), `haproxy/haproxy.cfg` (44), `Dockerfile` (single multi-target file). Makefile targets: `compose-build|up|down`, `test-e2e` -> mvp; `up-hardened|down-hardened` -> hardened (`up -d --build --wait`); `distributed-up|down|logs|status` -> distributed (`up -d --build --wait`). `compose-up` is `docker compose ... up -d` with no `--build` (`Makefile:33-34`). All three files `docker compose config -q` cleanly today [VERIFIED].

Dockerfile (`deployments/compose/Dockerfile`): builder `golang:alpine` (unpinned) builds gateway, demo-issuer, control-plane, audit-worker, orders, payments, admin; runtime targets `alpine:3.21`; no `USER`, no `HEALTHCHECK`. EXPOSE: gateway 8080 9443; demo-issuer 8085; orders 8081; payments 8082; admin 8083; control-plane 8084 9090 (9092 not listed); audit-worker none. `curl` installed in gateway, orders, payments, admin, control-plane, audit-worker; not in demo-issuer. `policies/` is COPYed into gateway and control-plane images (used by no code path).

Per-service settings (E = env, P = published ports, V = volumes, H = healthcheck, G = stop_grace_period). No file sets any `stop_grace_period` or any healthcheck on an Aegis service [VERIFIED: grep].

**docker-compose.mvp.yml**
| Service | Env | Ports | Volumes | Notes |
|---|---|---|---|---|
| gateway | AEGIS_PORT=8080, AEGIS_WORKLOAD_PORT=9443, TLS/CLIENT/CA/ASSERTION paths, AEGIS_UPSTREAM_SCHEME=https, AEGIS_ROUTES_PATH, AEGIS_POLICY_PATH, AEGIS_ISSUER, AEGIS_AUDIENCE | 8080, 9443 | ../certs:/certs:ro | depends_on orders, payments, admin, demo-issuer. No CP addr, no Redis, no spool volume, G default |
| demo-issuer | PORT=8085 | 8085 | none | |
| orders/payments/admin | PORT, TLS_CERT_PATH, TLS_KEY_PATH, CA_CERT_PATH, GATEWAY_ASSERTION_PUBKEY_PATH | expose only | ../certs:/certs:ro | |
| (absent) | | | | no postgres, redis, control-plane, audit-worker |

**docker-compose.hardened.yml**: postgres:16-alpine (healthcheck pg_isready, volume postgres_data); redis:7.2-alpine (healthcheck redis-cli ping); control-plane (ports 8084, 9090; env AEGIS_PORT, AEGIS_GRPC_PORT, AEGIS_DB_*; no AEGIS_REDIS_ADDR); gateway (adds AEGIS_CONTROL_PLANE_GRPC_ADDR=control-plane:9090, AEGIS_REDIS_ADDR=redis:6379, AEGIS_SPOOL_DIR, volume aegis_wal_spool; still sets AEGIS_ROUTES_PATH/AEGIS_POLICY_PATH; no AEGIS_DRAIN_TIMEOUT, so default 30s); audit-worker (AEGIS_SPOOL_DIR, DB env, AEGIS_PRUNE_ARCHIVED=true, volume aegis_wal_spool); demo-issuer (8085 published); backends as above.

**docker-compose.distributed.yml**: haproxy:2.8-alpine (8080, 9443, 8404; cfg bind-mounted; httpchk `/readyz`, tcp check on 9443); postgres, redis as above; control-plane (8084, 9090, 9092 published; env AEGIS_PORT, AEGIS_GRPC_PORT, AEGIS_METRICS_PORT=9092, DB env; no Redis); gateway-1/2/3 (expose 8080 9443; env as hardened plus AEGIS_GATEWAY_ID, AEGIS_DRAIN_TIMEOUT=30s; volumes certs + aegis_wal_spool_N; still sets the two dead vars); audit-worker (single; volume aegis_wal_spool_1 only, line 224); demo-issuer (expose 8085 only, not published); backends.

### 2. Binary inputs (file:line, default) and mismatch table

Gateway (`cmd/gateway/main.go` + `internal/config/config.go`): AEGIS_PORT (config.go:55, 8080); AEGIS_WORKLOAD_PORT (config.go:63, 9443); AEGIS_MAX_CONCURRENT (config.go:71, 1000); AEGIS_ROUTES_PATH (config.go:79, "policies/data/routes.json", field consumed by nothing); AEGIS_TLS_CERT_PATH/KEY_PATH (config.go:83,87); AEGIS_CLIENT_CERT_PATH/KEY_PATH (config.go:91,95); AEGIS_WORKLOAD_CA_PATH (config.go:99); AEGIS_ASSERTION_PRIVATE_KEY_PATH (config.go:103); AEGIS_ASSERTION_PUBLIC_KEY_PATH (config.go:107, consumed by nothing); AEGIS_CONTROL_PLANE_GRPC_ADDR (config.go:111, "localhost:9090"); AEGIS_REDIS_ADDR (config.go:115, "localhost:6379"); AEGIS_REDIS_PASSWORD (config.go:119); AEGIS_SPOOL_DIR (config.go:123, "/var/log/aegis/wal"); AEGIS_SPOOL_MAX_BYTES (config.go:127, 1 GiB); AEGIS_ISSUER (main.go:45, "aegis-issuer"); AEGIS_AUDIENCE (main.go:50, "aegis-gateway"); AEGIS_ISSUER_PUBLIC_KEY (main.go:56, default = demo-seed pubkey); AEGIS_CONTROL_PLANE_PUBLIC_KEY (main.go:66, falls back to the issuer pubkey, i.e. demo seed); AEGIS_GATEWAY_ID (main.go:81, random uuid); AEGIS_METRICS_PORT (main.go:121, ":9091"); AEGIS_ASSERTION_PRIVATE_KEY (main.go:176, fallback to demo seed); AEGIS_DRAIN_TIMEOUT (main.go:923, 30s). AEGIS_UPSTREAM_SCHEME is read in `internal/proxy/router.go:42` and `:67` (not in cmd/ or config.go) and is live; keep it in compose.

Control plane (`cmd/control-plane/main.go`): flags `-grpc-port`, `-http-port`, `-metrics-port` (35-37); AEGIS_GRPC_PORT (42, :9090); AEGIS_PORT then AEGIS_HTTP_PORT (53-55, :8084); AEGIS_METRICS_PORT (67, :9092); AEGIS_SIGNING_KEY_PATH (80), AEGIS_SIGNING_KEY_SEED (96), default demo seed (104); AEGIS_SIGNING_KEY_ID (107, "control-plane-key-v1"); AEGIS_REDIS_ADDR (114, **"localhost:6379"**); AEGIS_REDIS_PASSWORD (118); AEGIS_DB_HOST/PORT/USER/PASSWORD/NAME/SSLMODE (133-152; defaults from `storage.DefaultPoolConfig()`).

Audit worker (`cmd/audit-worker/main.go`): AEGIS_SPOOL_DIR (21, /var/log/aegis/wal); AEGIS_AUDIT_BATCH_SIZE (27, 500); AEGIS_AUDIT_FLUSH_INTERVAL_MS (34, 200ms); AEGIS_PRUNE_ARCHIVED (40, == "true"); AEGIS_DB_PORT (44, 5432), HOST (50, localhost), USER (55, postgres), NAME (60, aegis), SSLMODE (65, disable), PASSWORD (74).

demo-issuer: PORT only (`main.go:163`, 8085). Issuer/audience are hardcoded "aegis-issuer"/"aegis-gateway" (line 169) and the seed is the same demo seed as the gateway default pubkey, so compose does not need to pass any key.

Backends (orders/payments/admin): PORT (orders 104, defaults 8081/8082/8083), TLS_CERT_PATH, TLS_KEY_PATH, CA_CERT_PATH (112-114 in orders; same pattern in the others), GATEWAY_ASSERTION_PUBKEY_PATH (49) / GATEWAY_ASSERTION_PUBKEY (69).

Mismatch table:

| # | Type | Where | Item | Detail / Action |
|---|------|-------|------|-----------------|
| M1 | set-but-unread | mvp gateway; hardened gateway; distributed gateway-1/2/3 | AEGIS_POLICY_PATH | Read by no code anywhere. Remove. |
| M2 | set-but-unused | same 5 services | AEGIS_ROUTES_PATH | Loaded into `cfg.RoutesFilePath`, consumed by nothing (`NewRouterFromJSON` has only a test caller). Remove. |
| M3 | NOT a mismatch (keep) | same 5 services | AEGIS_UPSTREAM_SCHEME | Read at `internal/proxy/router.go:42` and `:67` (`AddProtobufRoute`, the snapshot path) and overrides the route's upstream URL scheme. Live; routes seeded as `https://orders:8081` already match, so it is redundant but harmless. Keep. |
| M4 | read-but-unset (required) | mvp gateway | AEGIS_CONTROL_PLANE_GRPC_ADDR | Default localhost:9090 -> never gets a snapshot -> 503 UNINITIALIZED (reproduced). |
| M5 | read-but-unset (required) | mvp gateway | AEGIS_REDIS_ADDR | Default localhost:6379 -> revocation fails closed. |
| M6 | read-but-unset (required) | mvp gateway | AEGIS_SPOOL_DIR volume | Default path works but is unmounted/unshared, so nothing could drain it. |
| M7 | read-but-unset (B4, required) | control-plane in mvp (absent), hardened, distributed | AEGIS_REDIS_ADDR | Default localhost:6379 inside the CP container. Quarantine/revoke 503 (reproduced). |
| M8 | structural | mvp | no control-plane, redis, postgres, audit-worker, seed | B1. |
| M9 | structural | distributed audit-worker | spool 2 and 3 never mounted | B3. |
| M10 | runtime | all gateways | stop_grace_period | default 10s < 30s drain + 5s. |
| M11 | read-but-unset (optional) | gateway | AEGIS_METRICS_PORT (:9091), AEGIS_SPOOL_MAX_BYTES, AEGIS_MAX_CONCURRENT | Defaults fine; no port is published for 9091 in any file. |
| M12 | cosmetic | Dockerfile | control-plane EXPOSE lacks 9092; gateway EXPOSE lacks 9091 | Optional. |
| M13 | cross-file | distributed | demo-issuer 8085 not published | `tests/integration` logins go to localhost:8085, so the distributed profile cannot be driven by them; audit tech debt, optional to publish. |

Port drift verdict: in the compose files there is no drift. 8080 (user), 9443 (workload), 8084 (CP REST), 9090 (CP gRPC), 9092 (CP metrics in distributed, matches the default), 8085 (issuer) and 8081/8082/8083 (backends) all agree with the binaries and with `haproxy.cfg` (8080/9443/8404). The `8443` drift lives only in `deployments/kubernetes/base/backends/networkpolicy.yaml`, `docs/threat-model/threat-model.md`, `docs/audit/security-audit-report.md`, `.planning/**` and the requirement text. That is B2/docs territory; recommend this phase make no port change and record that the compose files were checked and agree (the hermetic test can pin the ports).

### 3. B1, B3, B4 verification

- **B1: CONFIRMED, with a correction.** `docker-compose.mvp.yml` has no control-plane, redis, postgres or audit-worker; gateway lines 20-21 set the two dead vars (`AEGIS_POLICY_PATH` is read nowhere, `AEGIS_ROUTES_PATH` is read but unused). Correction to the audit: the first-order failure is not policy loading, it is that the gateway never receives a snapshot (`AEGIS_CONTROL_PLANE_GRPC_ADDR` unset, M4), so every request is `503 UNINITIALIZED`. Reproduced live: gateway started against a control plane that was listening on the wrong port returned `503 {"type":".../uninitialized"}` and `/readyz` 503. `TestMVPEndToEnd` and `TestBackendBypassPrevention` both call `ensureComposeCluster` on this file (`bypass_test.go:23-71`), so `make test-e2e` cannot pass against a rebuilt image.
- **B3: CONFIRMED.** `docker-compose.distributed.yml:224` mounts only `aegis_wal_spool_1` into the single audit-worker; gateway-2 and gateway-3 write to `aegis_wal_spool_2/3` (lines 149, 195) that nothing reads, so those spools grow to the 90% gate (`internal/audit/spool.go:136-147`), where `/readyz` returns `SPOOL_SATURATED` and HAProxy ejects them. Same single-spool wiring in hardened is correct (1 gateway, 1 worker).
- **B4: CONFIRMED, wider than stated.** No `AEGIS_REDIS_ADDR` on the control-plane in hardened (lines 44-56) or distributed (lines 59-70); mvp has no control plane at all. `cmd/control-plane/main.go:114-117` defaults to `localhost:6379` and always builds `revStore` (line 123), so handlers take the `revStore != nil` branch and return 503 DEPENDENCY_FAILURE on dial failure (`quarantine_handler.go:47-50,77-80,111-114,129-132`). Reproduced: with `AEGIS_REDIS_ADDR` pointing nowhere, `POST /control/v1/principals/user-dev/quarantine` returned `503 DEPENDENCY_FAILURE ... dial tcp ...: connection refused`; with `AEGIS_REDIS_ADDR` set to a live Redis it returned `200 {"status":"active"}`. The hardened file is not named by the audit but needs the identical fix.

### 4. How the audit-worker consumes spools

Single directory only. `NewAuditWorker` requires non-empty `SpoolDir`; `ProcessAvailable` does `os.ReadDir(SpoolDir)` (`worker.go:176`) and keeps top-level entries matching `wal-*.log` (`worker.go:183`, not recursive), sorts them, and walks from the cursor. The cursor is one file `<SpoolDir>/wal.cursor` (`cursor.go:26-28`), single-segment-plus-offset, so one cursor can track only one writer's sequence. A segment that is not the lexicographically latest is considered finished and pruned (`PruneArchived`), which is exactly why multiple concurrent writers into one dir are unsafe. Env knobs: `AEGIS_SPOOL_DIR` only; no multi-dir variable. Live test (this research): pointing the worker at a root containing `g1/` and `g2/` subdirs ingested 0 rows silently; pointing it at the real spool dir inserted 1 row (`event_type=decision`) into `audit_events` and wrote `wal.cursor`.

**Recommendation: three audit-worker services (audit-worker-1/2/3), config-only, each rw-mounting exactly one spool volume.** Cost: 3 DB pools (MaxConns 10 each = 30 conns vs Postgres default max_connections 100) [CITED: `cmd/audit-worker/main.go:77-78`; Postgres default is a training-knowledge figure, ASSUMED]. A Go change is not unavoidable. Closure group 2's "all-replica draining" may later replace this with a multi-dir worker; this phase should not.

### 5. What the MVP compose needs, and what config alone can and cannot do

Required to run the current gateway: postgres (control-plane `storage.NewPool`; without it the CP runs in-memory with repo handlers dereferencing nil, per audit CTRL-01 note), redis (revocation, rate limit, quarantine), control-plane with `AEGIS_REDIS_ADDR` and DB env, signing keys (none to configure: CP default key = demo seed, gateway default pinned CP pubkey = demo-seed pubkey [VERIFIED: both `defaultDemoSeed = "aegis-demo-issuer-secret-seed-32"`, `control-plane/main.go:31`, `gateway/main.go:37`]; demo-issuer uses the same seed), gateway env `AEGIS_CONTROL_PLANE_GRPC_ADDR`, `AEGIS_REDIS_ADDR`, `AEGIS_SPOOL_DIR` (+ named volume), and an audit-worker if audit durability is to be exercised. Snapshot bootstrap is automatic (empty deny-all v1, `main.go:190-225`) and is persisted to Postgres, so a restarted CP loads the latest version (reproduced: "Loaded latest active snapshot version 2 from database").

**Can it be functional by config alone?** Yes for "starts, becomes ready, enforces default-deny, accepts quarantine, drains audit". No for "serves allowed traffic", unless seeded. Two honest options, and the first needs no Go:

1. Seed container (recommended, in scope): proven live (see Summary). Data from `policies/data/routes.json` and `policies/rego/authz.rego` with the burst fix. Result observed: developer -> `/api/orders` passes policy (reached the proxy; 502 only because upstream hostnames don't resolve on the host); developer -> `/api/admin/users` -> `403 DENIED_DEVELOPER_ADMIN_FORBIDDEN`. Note the audit tech-debt item "Dashboard default policy template uses input.method" does not apply: `authz.rego` is the right input shape. The seed is a workaround, not a fix for POL-03; document that POL-03 stays "partial".
2. Control plane loads the files at bootstrap: Go change, belongs to a later phase.

If the planner/user declines a seed, the MVP profile can only be verified up to readiness plus default-deny, and `TestMVPEndToEnd` / `TestBackendBypassPrevention` subtests expecting HTTP 200 will fail. So the seed is effectively required to make `make test-e2e` meaningful.

### 6. Existing tests that touch compose

- `tests/integration/bypass_test.go:23` `composeFilePath = ../../deployments/compose/docker-compose.mvp.yml`; `ensureComposeCluster` (generates certs if missing, probes 8080/8085, runs `docker compose up -d` without `--build`, waits <=30s for 8085 + TCP 8080/9443). It is called by `TestBackendBypassPrevention` (7 subtests; uses `docker compose port` and `docker compose exec orders curl`, relying on service names `gateway`, `orders`, `payments`, `admin` and certs at `/certs/workload-orders.*`) and by `TestMVPEndToEnd` in `mvp_test.go` (login at localhost:8085, then 200/403/401 assertions through localhost:8080 and, in bypass_test, https://localhost:9443 with the workload cert). It is the only Go file that shells out to docker [VERIFIED: grep]. It runs under plain `go test ./...` with no gate (existing tech debt), and the `ensureComposeCluster` readiness wait is only 30s: on a cold build plus first start this is tight, so pre-start the stack in verification instead of letting the test do it.
- All other `tests/` packages (chaos, failure, security, dr, rotation, manifests) are in-process or YAML-only and do not use compose [VERIFIED: grep for docker/compose].
- Stale-image evidence: local images `compose-gateway`, `compose-orders`, `compose-payments`, `compose-admin`, `compose-demo-issuer` exist and a stack from them is currently running; `compose-control-plane` and `compose-audit-worker` do not exist [VERIFIED: `docker images`, `docker ps`].

### 7. Proposed verification for this phase

1. Static, per file: `docker compose -f deployments/compose/docker-compose.{mvp,hardened,distributed}.yml config -q`.
2. Hermetic Go lint test `tests/compose/compose_test.go` (new Wave 0 file): the assertions listed under Code Examples. Runs in under a second, no docker.
3. Clean live smoke (once per profile, sequential, reproduces what was proven natively): `docker compose -f <file> down -v; docker compose -f <file> up -d --build --wait`; then (a) `curl -s localhost:8080/readyz` -> 200 (or via HAProxy for distributed); (b) login as `admin` and `POST /control/v1/principals/<id>/quarantine` -> 200, not 503 (B4); (c) issue a developer token from :8085 and `GET /api/orders` through the gateway -> 200 and admin path -> 403 (B1); (d) distributed only: send requests through HAProxy until each of the three gateways has handled at least one allow (check `docker compose logs gateway-N`), then assert `SELECT count(*) FROM audit_events` increases and each `aegis_wal_spool_N` has an advanced `wal.cursor` (B3); (e) `docker compose stop -t 60 gateway-1` and confirm exit code is 0 and not 137 with `docker inspect` (stop_grace_period).
4. Existing Go tests against the freshly built mvp stack: `make test-bypass` and `go test -v -race -count=1 ./tests/integration/ -run 'TestMVPEndToEnd|TestBackendBypassPrevention'`, run after step 3 so `ensureComposeCluster` sees a live, current stack. Note the test file currently asserts `docker compose port gateway 8080` (service named `gateway`), which constrains the MVP service naming: keep `gateway`, `orders`, `payments`, `admin`, `demo-issuer`.
5. Regression: `go build ./... && go vet ./... && go test -count=1 ./tests/manifests/... ./internal/config/...`.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| mvp.yml (static routes/policy files per Phase 1) | Signed snapshot over gRPC from the control plane (Phase 3) | Phase 3 | mvp.yml was never updated; `AEGIS_ROUTES_PATH`/`AEGIS_POLICY_PATH` became dead |
| `docker compose up` w/o healthchecks | `depends_on` conditions incl. `service_completed_successfully` | Compose v2.x+ | Lets a one-shot seed gate the gateway [VERIFIED: v5.0.0 locally] |

**Deprecated/outdated:** `AEGIS_ROUTES_PATH`, `AEGIS_POLICY_PATH` in all compose files (`AEGIS_UPSTREAM_SCHEME` is still live); `proxy.NewRouterFromJSON` is dead code (audit tech debt, out of scope).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Alpine `jq` is available in the 3.21 main repo | Package Legitimacy Audit | Seed image build fails; fallback is a Go seed binary |
| A2 | Postgres default `max_connections` is 100, so 3 workers x 10 conns is safe | Findings 4 | Connection exhaustion; mitigate by lowering worker pool or setting max_connections |
| A4 | Quarantining a user principal through the CP makes the gateway deny that user once both share Redis | Summary/REV-03 | Not live-verified with a gateway in this research (control plane side only); verification step 3(b) should also re-request through the gateway and expect a deny |
| A5 | `stop_grace_period: 45s` is enough for a gateway under real load | Pitfall 5 | Derived from code (30s drain + 5s metrics shutdown), not measured |

## Open Questions (RESOLVED)

1. **Does the user want the seed (a new artifact) or only a "boots and denies" MVP profile?**
   - Known: config-only cannot serve allowed traffic; the seed works (proven live); tests expect 200s.
   - Unclear: whether adding `deployments/compose/seed/` plus a Dockerfile target is acceptable in a "config alignment" phase.
   - Recommendation: include the seed; call it out in the plan as the one non-pure-config artifact. Alternative: defer 200-path tests and mark them blocked on POL-03 closure.
   - RESOLVED: seed included (plan 07-01 adds `deployments/compose/seed/seed.sh` and a Dockerfile `seed` target; called out as the one non-pure-config artifact). Orchestrator default; the user can overturn.
2. **Rewrite mvp.yml or retarget `make compose-up`/`test-e2e` to hardened.yml?**
   - Both have identical service names. Recommendation: keep mvp.yml as the path (the test constant and Makefile do not change) and make it a superset-lite of hardened; avoid `include:` to keep each file standalone.
   - RESOLVED: rewrite `docker-compose.mvp.yml` in place, keeping path and service names, standalone with no `include:` (plan 07-03). Orchestrator default; the user can overturn.
3. **Scope of hardened.yml:** not named by the audit, has the same B4/dead-var/grace issues. Recommendation: fix it in this phase (cheap, same edits).
   - RESOLVED: `docker-compose.hardened.yml` is fixed in this phase (plan 07-03 Task 2). Orchestrator default; the user can overturn.
4. **Metrics ports:** keep unpublished (gateway 9091). `docker-compose.distributed.yml` publishes CP 9092; leave as is.
   - RESOLVED: metrics ports stay unpublished; distributed control-plane 9092 publication is left as is (no port changes, S7). Orchestrator default; the user can overturn.
5. **Orders/payments/admin Dockerfile EXPOSE cosmetics (M12):** optional.
   - RESOLVED: deferred, not done in this phase (cosmetic only, no behavioral effect). Orchestrator default; the user can overturn.

## What remains open after this phase

- **REV-03 stays unsatisfied.** Closed: B4 (CP can reach Redis; quarantine/revoke no longer 503). Still open: demo issuer emits no `jti` (`internal/revocation/store.go:80` skips empty jti, so JTI revocation never matches); SPIFFE IDs arrive percent-encoded so workload quarantine keys never match; `ReconstructQuarantines` has no production caller and no durable source table (B8); quarantine state is Redis-only.
- **AUD-03 stays unsatisfied.** Closed: B3 (every gateway's spool has a drain). Still open: B7 (`cmd/gateway/main.go:107` `audit.NewLogger(nil)`; only pre-forward allow events reach the spool, completions and all denials are stdout-only); behaviour with long-running multi-segment rotation under load is untested; and this phase's Postgres evidence is a smoke test, not coverage.
- **BYP-01 / DIST-03:** B1 closed for compose, but the "pipeline is an inline closure re-implemented by tests" gap in DIST-03 remains (closure group 6).
- **POL-03:** unchanged (files are still loaded by no binary; the seed is a workaround).
- **DIST-01:** `stop_grace_period` closed; the "drain is not two-phase" part (`main.go:940-946`) remains.
- **Port drift:** compose is consistent; 8443 text remains in k8s manifests/docs (B2 group).
- **OPS-01:** quarantine endpoints stop returning 503 in shipped compose; OpenAPI drift and `POST /control/v1/quarantine` always 400 remain.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Docker Engine | compose smoke runs, Go integration tests | yes | 29.1.3 [VERIFIED] | none needed |
| Docker Compose | all compose files | yes | v5.0.0 [VERIFIED] | none |
| Go | build, hermetic tests, native repro | yes | go1.26.0 [VERIFIED] | none |
| opa | `make test-quick` | yes | `~/.local/bin/opa` | none |
| Images postgres:16-alpine, redis:7-alpine, golang:alpine, haproxy:2.8-alpine | live runs | yes (local) | n/a | `redis:7.2-alpine` (used by compose) is not cached locally; will be pulled on first `up` (needs network) |
| `alpine:3.21` | runtime targets | pulled on demand (a toy run pulled it successfully) | n/a | none |
| Free host ports 8080, 9443, 8085, 8084, 9090 | live runs | **No: a stale MVP stack is currently bound to 8080, 9443, 8085** [VERIFIED: `docker ps`] | n/a | Stop it first: `docker compose -f deployments/compose/docker-compose.mvp.yml down` |
| A host Redis on 127.0.0.1:6379 and Postgres on 127.0.0.1:5432 | native runs only | yes (host services) | n/a | Can mask B4 in native (non-container) repros; use a closed port when reproducing |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** port-holding stale stack (stop it), uncached `redis:7.2-alpine` (pull).

Research artifacts: the experiments ran in the session scratchpad only (native control-plane/gateway/audit-worker binaries built to the scratchpad, throwaway postgres/redis containers on ports 15432/16379, now stopped). No repo files were modified.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` + testify (require/assert) + gopkg.in/yaml.v3; Go 1.26.0 |
| Config file | none; tests are plain `go test` packages (`go.mod` module `aegis`) |
| Quick run command | `go test -count=1 ./tests/compose/... ./tests/manifests/... ./internal/config/...` |
| Full suite command | `go build ./... && go vet ./... && go test -count=1 ./...` (and `opa test policies/rego policies/tests` via `make test-quick`) |
| Static config check | `for f in mvp hardened distributed; do docker compose -f deployments/compose/docker-compose.$f.yml config -q; done` |
| Live (docker) check | `docker compose -f deployments/compose/docker-compose.mvp.yml down -v && docker compose -f deployments/compose/docker-compose.mvp.yml up -d --build --wait && go test -count=1 -v -race ./tests/integration/ -run 'TestMVPEndToEnd|TestBackendBypassPrevention'` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| REV-03 (B4) | control-plane service sets `AEGIS_REDIS_ADDR` in mvp, hardened, distributed | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run TestControlPlaneHasRedis` | No, Wave 0 |
| REV-03 (B4) | quarantine returns 200 (not 503) through the shipped stack | live smoke | `curl` login + `POST /control/v1/principals/smoke-user/quarantine` -> 200 (script in plan; run after `up --wait`) | No, Wave 0 (script) |
| REV-03 (B4) | gateway denies the quarantined principal | live smoke | issue token for that principal (subject from demo seed), request `/api/orders` -> 403 (A4, unverified) | No, Wave 0 |
| AUD-03 (B3) | distributed has one audit-worker per gateway spool, each volume mounted by exactly one gateway and one worker | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run TestAuditWorkerPerSpool` | No, Wave 0 |
| AUD-03 (B3) | events from all 3 gateways reach `audit_events` | live smoke | drive requests through HAProxy, then `docker compose exec postgres psql -U aegis -d aegis -tc "select count(*) from audit_events"` increases; each `wal.cursor` present in all 3 volumes | No, Wave 0 (script) |
| B1 / BYP-01 / DIST-03 | mvp has control-plane, redis, postgres, audit-worker and gateway env wiring; no dead vars | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run 'TestMVPTopology|TestNoDeadEnv'` | No, Wave 0 |
| B1 | MVP E2E and bypass tests pass on a rebuilt image | live integration | `make test-e2e` after changing `compose-up` to `up -d --build --wait` | Yes (`tests/integration/*`), but needs seed |
| DIST-01 (grace) | every gateway `stop_grace_period >= drain + 5s` | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run TestGatewayStopGracePeriod` | No, Wave 0 |
| DIST-01 (grace) | gateway exits 0 within grace | live | `docker compose stop gateway-1; docker inspect -f '{{.State.ExitCode}}'` expect 0 | manual/script |
| port drift | published/exposed compose ports equal binary defaults | hermetic YAML lint | `go test -count=1 ./tests/compose/ -run TestPortsMatchBinaries` | No, Wave 0 |
| all | compose files parse | static | `docker compose -f ... config -q` | n/a |

### Sampling Rate
- **Per task commit:** `go test -count=1 ./tests/compose/...` plus `docker compose -f <edited file> config -q`
- **Per wave merge:** full static check on all three files + `go build ./... && go vet ./...` + `go test -count=1 ./tests/manifests/... ./internal/config/...`
- **Phase gate:** clean `down -v` + `up -d --build --wait` of mvp and distributed, the live smoke steps above, `make test-bypass`/`TestMVPEndToEnd` green on a fresh image, then `go test -count=1 ./...` before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `tests/compose/compose_test.go` (new package `compose`): hermetic assertions listed above; reuse the `loadYAML` helper pattern from `tests/manifests/manifest_test.go`
- [ ] `deployments/compose/seed/seed.sh` (+ Dockerfile `seed` target) and a live smoke script, e.g. `scripts/compose-smoke.sh` (login, quarantine 200, developer allow, admin deny, audit row count)
- [ ] Makefile: `compose-up` -> `up -d --build --wait`; optionally a `compose-smoke` target
- [ ] No framework install needed (yaml.v3 already in `go.mod`, used by `tests/manifests`)

## Security Domain

`security_enforcement` is not set to false in `.planning/config.json` (key absent), so this section applies.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | yes (seed logs in as an operator) | Pass operator credentials via env, not literals; demo operators are hardcoded (`internal/control/auth.go:16-20`), a known tech-debt item, out of scope |
| V3 Session Management | limited | Seed handles the `Secure` session cookie and CSRF token explicitly; do not weaken cookie flags to make a cookie jar work |
| V4 Access Control | yes | Seed needs `sec-ops`; no new endpoints, no RBAC changes |
| V5 Input Validation | limited | Build JSON with `jq` (no string interpolation of rego/routes into JSON) |
| V6 Cryptography | no new crypto | Do not introduce new keys; default demo seed remains (existing tech debt) |
| V14 Configuration | yes | Do not publish more ports than today (backends stay unpublished, which `TestBackendBypassPrevention` asserts); do not publish CP 9090/Redis/Postgres to the host in mvp; keep spool volumes private to gateway+worker |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Seed script credentials baked into an image | Information disclosure | Env-injected, one-shot container, no host port |
| Fail-open drift: gateway falling back to `localhost:*` defaults | Elevation / Spoofing | Explicit `AEGIS_*_ADDR` in compose + hermetic test asserting presence |
| Audit loss when a worker cannot see a spool | Repudiation | One worker per spool; test that every spool volume has a worker |
| SIGKILL during drain losing in-flight audited requests | Repudiation / DoS | `stop_grace_period` > drain |
| Shared spool volume across gateways | Tampering (interleaved writes) | Never share; one volume per gateway |
| Exposing control-plane gRPC/REST on the host in a "secure vertical slice" profile | Spoofing | Already published in hardened/distributed (existing tech debt); do not add to mvp unless the tests need 8084 |

## Sources

### Primary (HIGH confidence)
- Repo code read in full or at the cited lines: `cmd/gateway/main.go`, `cmd/control-plane/main.go`, `cmd/audit-worker/main.go`, `cmd/demo-issuer/main.go`, `internal/config/config.go`, `internal/audit/{worker,cursor,spool}.go`, `internal/proxy/probe.go`, `internal/control/{api,auth,session,publish_handler,routes_handler,policy_handler,quarantine_handler,etag}.go`, `deployments/compose/*`, `Makefile`, `tests/integration/*.go`, `tests/manifests/manifest_test.go`, `migrations/`
- Live experiments in this session (native binaries + throwaway Postgres/Redis containers, toy compose stack on Compose v5.0.0): B4 503 vs 200; gateway UNINITIALIZED without snapshot; REST seed flow works end to end; routes.json burst rejection; worker on subdirs ingests 0 rows vs flat dir 1 row; `up -d --wait` with `service_completed_successfully`
- `.planning/v1.0-MILESTONE-AUDIT.md`, `.planning/REQUIREMENTS.md`, `.planning/STATE.md`, `.planning/ROADMAP.md`

### Secondary (MEDIUM confidence)
- None used (no web or Context7 lookups were needed; everything was verifiable in-repo or locally)

### Tertiary (LOW confidence)
- Alpine `jq` availability and Postgres `max_connections` default (training knowledge, tagged ASSUMED)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH, no new libraries; tooling versions verified locally
- Architecture: HIGH, topology and config-only fixes reproduced with the real binaries
- Pitfalls: HIGH for items reproduced (B4, burst rejection, worker subdir, UNINITIALIZED), MEDIUM for stop_grace sizing and the multi-writer reasoning (derived from code, not stress-tested)

**Research date:** 2026-10-08
**Valid until:** 2026-11-07 (stable; invalidated by any change to `cmd/*/main.go`, `internal/config`, or the audit worker)
