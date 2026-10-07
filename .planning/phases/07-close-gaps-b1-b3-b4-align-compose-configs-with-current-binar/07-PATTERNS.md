# Phase 7: Close gaps B1, B3, B4 (align compose configs with current binaries) - Pattern Map

**Mapped:** 2026-10-08
**Files analyzed:** 9 (3 modified compose files, 1 modified Dockerfile, 1 modified Makefile, 4 new: seed script, compose lint test, smoke script, plus the Dockerfile `seed` target counted with the Dockerfile)
**Analogs found:** 8 / 9 (the shell seed/smoke scripts have only a loose bash-style analog)

Project instructions: no `./CLAUDE.md` exists (a `GEMINI.md` is present at repo root but is not a GSD directive file). `.agents/skills/` holds only generic GSD workflow links. No project skills apply.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `deployments/compose/docker-compose.mvp.yml` (rewrite) | config (compose topology) | request-response + event-driven (WAL drain) | `deployments/compose/docker-compose.hardened.yml` | exact (target topology) |
| `deployments/compose/docker-compose.hardened.yml` (modify) | config | request-response | itself; sibling `docker-compose.distributed.yml` | exact |
| `deployments/compose/docker-compose.distributed.yml` (modify) | config | request-response + event-driven | itself (gateway-N and audit-worker blocks) | exact |
| `deployments/compose/Dockerfile` (add `seed` target) | config (image build) | batch | existing `control-plane` / `audit-worker` targets in same file | exact |
| `deployments/compose/seed/seed.sh` (new) | utility (one-shot script) | request-response (REST client) | `scripts/generate.sh` (bash conventions only) | partial |
| `tests/compose/compose_test.go` (new) | test | transform (parse YAML, assert) | `tests/manifests/manifest_test.go` | exact |
| `scripts/compose-smoke.sh` (new) | utility (live smoke) | request-response | `scripts/generate.sh` (bash style) + `tests/integration/mvp_test.go` (what to assert) | partial |
| `Makefile` (modify) | config | batch | existing `up-hardened` / `distributed-up` targets | exact |
| `tests/integration/bypass_test.go` (read-only; constrains naming) | test | request-response | n/a | constraint only |

## Pattern Assignments

### `deployments/compose/docker-compose.mvp.yml` (config, rewritten to hardened-style topology)

**Analog:** `/home/logan78/Desktop/Aegis/deployments/compose/docker-compose.hardened.yml` (192 lines). Copy it nearly whole, then add the `seed` service. Keep service names `gateway`, `orders`, `payments`, `admin`, `demo-issuer` (required by `tests/integration/bypass_test.go`, which uses `docker compose port gateway 8080`, `exec orders curl`). Keep each file standalone (no `include:`).

**Current mvp gateway (what to replace)** (mvp.yml lines 1-34): has no `AEGIS_CONTROL_PLANE_GRPC_ADDR`, no `AEGIS_REDIS_ADDR`, no spool volume, `depends_on` is a plain list, and sets dead `AEGIS_ROUTES_PATH` / `AEGIS_POLICY_PATH` (lines 20-21). Only `demo-issuer`, `orders`, `payments`, `admin` exist.

**postgres + redis blocks** (hardened.yml lines 2-27; copy verbatim):
```yaml
  postgres:
    image: postgres:16-alpine
    environment:
      - POSTGRES_DB=aegis
      - POSTGRES_USER=aegis
      - POSTGRES_PASSWORD=aegis-secret-pw
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U aegis -d aegis"]
      interval: 5s
      timeout: 5s
      retries: 5
    networks:
      - aegis-internal

  redis:
    image: redis:7.2-alpine
    command: ["redis-server", "--maxmemory", "128mb", "--maxmemory-policy", "noeviction"]
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5
    networks:
      - aegis-internal
```

**control-plane block with the B4 fix** (hardened.yml lines 29-52; ADD the `AEGIS_REDIS_ADDR` line, absent today). For mvp, do NOT publish 9090 (research security note); 8084 only if tests/seed-from-host need it (seed runs in-network, so can be omitted):
```yaml
  control-plane:
    build:
      context: ../../
      dockerfile: deployments/compose/Dockerfile
      target: control-plane
    ports:
      - "8084:8084"
      - "9090:9090"
    environment:
      - AEGIS_PORT=8084
      - AEGIS_GRPC_PORT=9090
      - AEGIS_REDIS_ADDR=redis:6379          # <-- ADD (B4)
      - AEGIS_DB_HOST=postgres
      - AEGIS_DB_PORT=5432
      - AEGIS_DB_USER=aegis
      - AEGIS_DB_PASSWORD=aegis-secret-pw
      - AEGIS_DB_NAME=aegis
      - AEGIS_DB_SSLMODE=disable
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    networks:
      - aegis-internal
```

**gateway block** (hardened.yml lines 54-96): copy; REMOVE lines 75-76 (`AEGIS_ROUTES_PATH`, `AEGIS_POLICY_PATH`); KEEP `AEGIS_UPSTREAM_SCHEME=https` (live, read in `internal/proxy/router.go`); ADD `stop_grace_period: 45s`; ADD `seed` to depends_on:
```yaml
    environment:
      - AEGIS_PORT=8080
      - AEGIS_WORKLOAD_PORT=9443
      - AEGIS_CONTROL_PLANE_GRPC_ADDR=control-plane:9090
      - AEGIS_REDIS_ADDR=redis:6379
      - AEGIS_SPOOL_DIR=/var/log/aegis/wal
      - AEGIS_TLS_CERT_PATH=/certs/gateway-server.crt
      ...
      - AEGIS_ISSUER=aegis-issuer
      - AEGIS_AUDIENCE=aegis-gateway
    volumes:
      - ../certs:/certs:ro
      - aegis_wal_spool:/var/log/aegis/wal
    stop_grace_period: 45s                     # <-- ADD (M10)
    depends_on:
      control-plane:
        condition: service_started
      seed:                                    # <-- ADD (Pattern 2)
        condition: service_completed_successfully
      redis:
        condition: service_healthy
      orders: { condition: service_started }   # keep existing long form
      ...
```

**audit-worker block** (hardened.yml lines 98-118; copy; add `control-plane: service_started` to depends_on per Pitfall 3; mount stays rw, never `:ro`).

**seed service (new, no analog in compose files)**: model on the other `build:` services (same `context`/`dockerfile`/`target` triple) with `restart: "no"`, env-injected credentials, no `ports`:
```yaml
  seed:
    build:
      context: ../../
      dockerfile: deployments/compose/Dockerfile
      target: seed
    restart: "no"
    environment:
      - AEGIS_CP_URL=http://control-plane:8084
      - AEGIS_SEED_USER=admin
      - AEGIS_SEED_PASSWORD=admin-secret
    depends_on:
      postgres: { condition: service_healthy }
      redis: { condition: service_healthy }
      control-plane: { condition: service_started }
    networks: [aegis-internal]
```

**Bottom of file** (hardened.yml lines 186-192): `volumes: {postgres_data:, aegis_wal_spool:}` and `networks: aegis-internal: driver: bridge`. mvp must gain `postgres_data` and `aegis_wal_spool`.

**Backends/demo-issuer** (hardened.yml lines 120-184): identical to the existing mvp backends; leave unchanged (expose-only, certs `:ro`, no published ports - asserted by `TestBackendBypassPrevention`).

---

### `deployments/compose/docker-compose.hardened.yml` (modify)

**Analog:** itself. Three edits:
1. control-plane env (lines 36-44): insert `- AEGIS_REDIS_ADDR=redis:6379` (B4).
2. gateway env: delete lines 75-76 (`AEGIS_ROUTES_PATH`, `AEGIS_POLICY_PATH`); add `stop_grace_period: 45s` beside `volumes:`.
3. Add the same `seed` service and `gateway.depends_on.seed: service_completed_successfully` as in mvp. `audit-worker` (lines 98-118) is already one-per-spool; optionally add `control-plane: service_started`.

---

### `deployments/compose/docker-compose.distributed.yml` (modify)

**Analog:** itself (gateway-N blocks lines 71-207; audit-worker lines 209-229; volumes 297-301).

**Edits:**
- control-plane (lines 44-69): add `- AEGIS_REDIS_ADDR=redis:6379` to env (B4). Keep published 9092.
- gateway-1/2/3: delete `AEGIS_ROUTES_PATH`/`AEGIS_POLICY_PATH` at lines 94-95, 140-141, 186-187; add `stop_grace_period: 45s` (they already set `AEGIS_DRAIN_TIMEOUT=30s`, so grace >= 35s holds); add `seed` dependency.
- Replace the single `audit-worker` (lines 209-229, mounts only `aegis_wal_spool_1` at line 224) with three workers using a shared extension anchor.

**Current single worker (to be replaced)** (lines 209-229):
```yaml
  audit-worker:
    build:
      context: ../../
      dockerfile: deployments/compose/Dockerfile
      target: audit-worker
    environment:
      - AEGIS_SPOOL_DIR=/var/log/aegis/wal
      - AEGIS_DB_HOST=postgres
      - AEGIS_DB_PORT=5432
      - AEGIS_DB_USER=aegis
      - AEGIS_DB_PASSWORD=aegis-secret-pw
      - AEGIS_DB_NAME=aegis
      - AEGIS_DB_SSLMODE=disable
      - AEGIS_PRUNE_ARCHIVED=true
    volumes:
      - aegis_wal_spool_1:/var/log/aegis/wal
    depends_on:
      postgres:
        condition: service_healthy
    networks:
      - aegis-internal
```

**Target pattern (B3)**: no existing compose file uses `x-` anchors yet; use the RESEARCH.md Pattern 1 form (top-level `x-audit-worker: &audit-worker`, then `audit-worker-1/2/3: { <<: *audit-worker, volumes: ["aegis_wal_spool_N:/var/log/aegis/wal"] }`). Mount rw; each spool volume appears in exactly one gateway and one worker. Confirm with `docker compose config -q`. Existing per-gateway volume wiring to preserve: `aegis_wal_spool_1` line 100, `_2` line 146, `_3` line 192.

---

### `deployments/compose/Dockerfile` (add `seed` target)

**Analog:** existing runtime targets in the same file, e.g. `control-plane` and `audit-worker` (lines 58-72). Pattern: `FROM alpine:3.21 AS <name>`, `RUN apk add --no-cache ...`, `WORKDIR /app`, `COPY`, `ENTRYPOINT`.
```dockerfile
# Target: control-plane  (Dockerfile lines 58-65)
FROM alpine:3.21 AS control-plane
RUN apk add --no-cache curl
WORKDIR /app
COPY --from=builder /bin/control-plane /usr/local/bin/control-plane
COPY policies ./policies
EXPOSE 8084 9090
ENTRYPOINT ["/usr/local/bin/control-plane"]
```
New target shape (append after `audit-worker`; builder stage already `COPY . .`, but runtime stages copy from build context so copy script and policy inputs directly):
```dockerfile
# Target: seed
FROM alpine:3.21 AS seed
RUN apk add --no-cache curl jq
WORKDIR /app
COPY deployments/compose/seed/seed.sh /usr/local/bin/seed.sh
COPY policies ./policies
ENTRYPOINT ["/bin/sh", "/usr/local/bin/seed.sh"]
```
Notes: runtime images run as root with no `USER` (Pitfall 6; do not add one). `jq` is ASSUMED available in Alpine 3.21 main; fallback is a Go seed in the builder stage (builder lines 10-16 show the `go build -o /bin/<name> ./cmd/...` convention). `policies/` is already COPYed into gateway and control-plane images with `COPY policies ./policies`.

---

### `deployments/compose/seed/seed.sh` (utility, REST client one-shot)

**Analog (style only):** `/home/logan78/Desktop/Aegis/scripts/generate.sh` lines 1-5, the only shell script in the repo.
```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
...
echo "=== Generating Protobuf Go Stubs ==="
```
Conventions to copy: `set -euo pipefail`, `=== Section ===` echo banners. The seed image is alpine, so use `#!/bin/sh` + `set -eu` (no bash; busybox has no `pipefail` in older versions, verify), and a hard non-zero exit on any non-2xx.

**Core pattern** (RESEARCH.md "Seed script core", verified against the live API; endpoints in `internal/control/api.go:98-145`): login (`POST /control/v1/auth/login`) -> capture `csrf_token` from body and `aegis_session` from `Set-Cookie` -> `POST /control/v1/routes` x5 -> `POST /control/v1/policies` -> `POST /control/v1/policies/{id}/publish` with `If-Match`.

Traps the planner must encode (from RESEARCH.md):
- Session cookie is `Secure`: send `-H "Cookie: aegis_session=$SID"` and `-H "X-CSRF-Token: $CSRF"` explicitly; never use a curl cookie jar.
- `policies/data/routes.json` fails verbatim; transform with `burst = max(burst, rps)`:
  `jq -c '.[] | {route_id,service_id,http_method,path_template,upstream_url,upstream_spiffe_id, rate_limit_rps:.rate_limit.requests_per_second, rate_limit_burst:([.rate_limit.burst,.rate_limit.requests_per_second]|max), timeout_ms:.timeout.upstream_timeout_ms, requires_workload_mtls}'`
- Build the policy JSON with `jq -n --rawfile s authz.rego '{policy_id:"mvp-authz",name:"authz.rego",source_rego:$s}'` (no string interpolation).
- Idempotent on re-`up` against a persisted `postgres_data`: treat duplicate policy/route as already seeded; on `412` publish, read the `ETag` response header and retry with it.
- Credentials from env (`AEGIS_SEED_USER`, `AEGIS_SEED_PASSWORD`), never literals. Wait/retry for the control-plane REST port before login (depends_on is only `service_started`).

---

### `tests/compose/compose_test.go` (test, hermetic YAML lint)

**Analog:** `/home/logan78/Desktop/Aegis/tests/manifests/manifest_test.go` (exact: package of plain `go test`, testify + yaml.v3, relative `filepath.Join("..", "..", ...)` paths).

**Imports + loader helper** (lines 1-22; copy, rename package to `compose`):
```go
package manifests   // -> package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func loadYAML(t *testing.T, relPath string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(relPath)
	require.NoError(t, err, "failed to read manifest file: %s", relPath)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err, "failed to parse YAML from: %s", relPath)
	return doc
}
```

**Path convention** (line 25): `filepath.Join("..", "..", "deployments", "kubernetes", ...)` -> use `filepath.Join("..", "..", "deployments", "compose", "docker-compose."+name+".yml")` for `mvp`, `hardened`, `distributed`.

**Type-assertion style** (lines 29-36): `spec, ok := doc["spec"].(map[string]interface{}); require.True(t, ok, "...")`, lists are `[]interface{}`. Apply to `doc["services"]`, `svc["environment"]` (list form `"K=V"`; split on first `=`), `svc["volumes"]`, `svc["stop_grace_period"]` (a string like `"45s"`; parse with `time.ParseDuration`), `svc["depends_on"]` (map form with `condition`, or list form in old mvp).

**Behavior-style assertions:** one `func TestXxx(t *testing.T)` per research test name, each looping over the three files:
`TestControlPlaneHasRedis`, `TestMVPTopology`, `TestNoDeadEnv`, `TestAuditWorkerPerSpool`, `TestGatewayStopGracePeriod` (grace >= drain + 5s; drain default 30s unless `AEGIS_DRAIN_TIMEOUT`), `TestPortsMatchBinaries` (8080/9443/8084/9090/9092/8085/8081-8083).

**Gotcha:** yaml.v3 decodes `<<: *anchor` merge keys and `x-` extension top-level keys fine into `map[string]interface{}` (merge applied by decoder); the `x-audit-worker` key will appear in `doc` and must be ignored. Numeric YAML like `"8080:8080"` ports are strings; `expose` entries are quoted strings.

---

### `scripts/compose-smoke.sh` (utility, live smoke)

**Analog (style):** `scripts/generate.sh` (bash, `set -euo pipefail`, banner echos). **Analog (what to assert):** `tests/integration/mvp_test.go` `loginToken` (lines 17-38): `POST http://localhost:8085/login` with `{"role": "<role>"}` returns `access_token`; use it as `Authorization: Bearer` against `localhost:8080`.

Smoke steps (RESEARCH.md section 7.3): `/readyz` 200; operator login then `POST /control/v1/principals/<id>/quarantine` -> 200 not 503 (explicit Cookie + `X-CSRF-Token`, same cookie caveat as the seed); developer token `GET /api/orders` -> 200 and `/api/admin/users` -> 403; distributed only: `docker compose exec postgres psql -U aegis -d aegis -tc "select count(*) from audit_events"` and `wal.cursor` present per volume; `docker compose stop` then `docker inspect -f '{{.State.ExitCode}}'` == 0. Accept the compose file as an argument (default mvp) so one script serves all three profiles.

---

### `Makefile` (modify)

**Analog:** the existing `up-hardened` / `distributed-up` targets (Makefile lines 41-42, 50-51), which already use `up -d --build --wait`:
```make
up-hardened: certs
	docker compose -f $(COMPOSE_HARDENED_FILE) up -d --build --wait

distributed-up: certs
	docker compose -f $(COMPOSE_DISTRIBUTED_FILE) up -d --build --wait
```
Edit `compose-up` (line 33-34) from `docker compose -f $(COMPOSE_FILE) up -d` to `... up -d --build --wait`. Optional new target following the same shape plus an entry in `.PHONY` (line 5) and a `help` echo line (lines 70-93 format: `@echo "  make compose-smoke ... - ..."`), e.g. `compose-smoke:` invoking `scripts/compose-smoke.sh`. Optionally a `compose-test` target mirroring `manifest-test` (line 62: `go test -v ./tests/manifests/...`) as `go test -v ./tests/compose/...`.

Note: `test-e2e` (line 36) depends on `compose-up`, then runs `go test -v -race ./tests/integration/...`; it does not pass `--build`, hence the `compose-up` change.

---

## Shared Patterns

### Compose service shape (build target triple)
**Source:** every Aegis service in `docker-compose.hardened.yml`
**Apply to:** `seed`, `audit-worker-N`, any new service
```yaml
    build:
      context: ../../
      dockerfile: deployments/compose/Dockerfile
      target: <target>
    networks:
      - aegis-internal
```

### Env var format
**Source:** all compose files
**Apply to:** all edits. Environment is always **list form** `- KEY=value` (the lint test depends on this, so keep it; do not switch to map form). `AEGIS_DB_*` block (host=postgres, port=5432, user=aegis, password=aegis-secret-pw, name=aegis, sslmode=disable) is duplicated verbatim across control-plane and audit-worker; keep identical.

### depends_on with health conditions
**Source:** hardened.yml lines 46-51 and 82-96 (long form with `condition: service_healthy | service_started`).
**Apply to:** mvp gateway (currently a short list), plus new `service_completed_successfully` on `seed`. All Aegis services lack healthchecks (only postgres/redis have them); if adding optional ones, use `curl` (already in images): gateway `curl -sf http://127.0.0.1:8080/readyz`; control-plane `curl -s -o /dev/null http://127.0.0.1:8084/control/v1/auth/me` (no `-f`).

### Dead env removal
**Source/Apply to:** `AEGIS_ROUTES_PATH` + `AEGIS_POLICY_PATH` appear in mvp gateway (lines 20-21), hardened gateway (75-76), distributed gateway-1/2/3 (94-95, 140-141, 186-187). Remove all; keep `AEGIS_UPSTREAM_SCHEME=https`.

### Volume discipline
**Apply to:** gateway and audit-worker. Spool volume mounted rw at `/var/log/aegis/wal` in exactly one gateway and exactly one audit-worker; certs `../certs:/certs:ro` for gateway and backends; never share a spool across gateways.

### Go test conventions
**Source:** `tests/manifests/manifest_test.go` - testify `require` for preconditions and type assertions, `assert` for value checks, `t.Helper()` in helpers, no build tags, no docker. Module path is `aegis` (go.mod).

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `deployments/compose/seed/seed.sh` | utility | REST client | Only shell script in repo is `scripts/generate.sh` (codegen); use RESEARCH.md seed core instead. Style conventions only. |
| `scripts/compose-smoke.sh` | utility | request-response | Same; live-check steps come from RESEARCH.md section 7 and `tests/integration/mvp_test.go` assertions. |
| `x-audit-worker` YAML anchor | config | n/a | No compose file in the repo uses `x-` extension fields or `<<:` merges yet; use RESEARCH.md Pattern 1 and validate with `docker compose config -q`. |
| One-shot compose service (`restart: "no"`, `service_completed_successfully`) | config | batch | No existing one-shot service. Verified working on Compose v5.0.0 per RESEARCH.md. |

## Constraints for the Planner

- `tests/integration/bypass_test.go` line 23 hardcodes `docker-compose.mvp.yml`, and `ensureComposeCluster` (lines 27-51) runs `docker compose up -d` without `--build` and skips `up` if 8080 and 8085 already answer. A stale MVP stack is bound to 8080/9443/8085 on this host; verification must start with `down -v` then `up -d --build --wait`.
- Service names for the MVP are fixed: `gateway`, `orders`, `payments`, `admin`, `demo-issuer`.
- Only Wave 0 artifacts that are truly new: `tests/compose/compose_test.go`, `deployments/compose/seed/seed.sh` (+ Dockerfile `seed` target), `scripts/compose-smoke.sh`. No Go source under `cmd/` or `internal/` changes.

## Metadata

**Analog search scope:** `deployments/compose/`, `tests/` (manifests, integration), `scripts/`, `Makefile`
**Files scanned:** about 12 (3 compose files, Dockerfile, Makefile, manifest_test.go, bypass_test.go, mvp_test.go, scripts/generate.sh, api.go excerpt)
**Pattern extraction date:** 2026-10-08
