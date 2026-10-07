# Phase 5: Distributed Resilience, Chaos & Benchmark Evidence - Research

**Researched:** 2026-10-07  
**Domain:** Distributed Systems, High Availability, Chaos Engineering, Load Balancing, and Performance Benchmarking  
**Confidence:** HIGH  

<user_constraints>
## User Constraints (from PROJECT.md, REQUIREMENTS.md, and ROADMAP.md)

### Locked Decisions
- **Phase Goal:** Validate distributed multi-replica gateway operation (3 instances) behind a load balancer, demonstrating graceful draining, gRPC jitter reconnect, automated chaos fault tolerance, and reproducible k6 performance benchmarks.
- **Requirements (MUST address):**
  - **DIST-01**: Multi-replica deployment behind load balancer with health checking, graceful drain (30s), and reconnect jitter.
  - **DIST-03**: Automated test suite proving negative authentication, header spoofing, path traversal, bypass prevention, and dependency outages.
- **Mandatory Invariants:**
  - *Invariant 1 (Default-Deny Authorization):* Missing, expired, or degraded security state must never fail open.
  - *Invariant 3 (Bounded Latency):* In-memory OPA evaluation must complete in <2ms p99; gateway added latency must remain <20ms p99 under 1,000 RPS.
  - *Invariant 4 (Signed Monotonic Freshness Leases):* 10s freshness leases with 60s hard fail-closed boundary; reconnect jitter prevents thundering herd on Control Plane.
  - *Invariant 5 (Atomic Snapshot Activation):* New snapshot versions must activate atomically in memory with zero connection resets.
  - *Invariant 10 (Pre-Forward Durable Audit):* WAL append before upstream dispatch; spool saturation at 90% halts permitted admissions (503).
  - *Invariant 11 (Management Isolation):* Port :8084 remains isolated from public ingress.
  - *Invariant 12 (Low-Cardinality Metrics):* Prometheus metrics on private listeners :9091/:9092 without PII labels.

### the agent's Discretion
- Selection of Edge Load Balancer for multi-replica Docker Compose topology (HAProxy vs NGINX vs Envoy) -> HAProxy 2.8+ recommended for clean Layer 7 HTTP health-checking, sub-millisecond dispatch, and built-in stats interface.
- Chaos harness architecture -> Pure Go automated test suite (`tests/chaos/chaos_test.go`) utilizing concurrent goroutines, mock network partitions, process lifecycle control, and client traffic generation.
- k6 Benchmark runner -> Both Docker-based k6 execution (`grafana/k6`) and Go microbenchmarks (`benchmarks/engine_bench_test.go`) for reproducible continuous integration.

### Deferred Ideas (OUT OF SCOPE)
- Kubernetes NetworkPolicies and production manifests (K8S-01) -> Deferred to Phase 6.
- External OIDC PKCE BFF (ID-01) -> Deferred to v2.
- Automated SPIRE workload attestation (PKI-01) -> Deferred to v2.
- Kafka / RabbitMQ broker replacement for WAL spool -> Deferred to v2.
</user_constraints>

<architectural_responsibility_map>
## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Edge Load Balancing & Health Check Routing | Edge Proxy (HAProxy) | Gateway HTTP Listener | Distributes ingress across 3 gateway replicas, detects failed/draining replicas via `/readyz`, and dynamically routes traffic with zero client downtime. |
| Graceful Connection Draining (30s) | Gateway Core Daemon | Edge Proxy (HAProxy) | When SIGTERM is received, gateway marks readiness as 503 so HAProxy immediately stops sending new traffic, while allowing up to 30 seconds for in-flight requests to finish. |
| gRPC Reconnect Backoff & Full Jitter | Gateway Snapshot Client | Control Plane gRPC Server | Exponential backoff with uniform random jitter (100ms base, 5s max) avoids thundering herd on Control Plane when network blips or restarts occur. |
| Fleet Snapshot Convergence (<5s) | Control Plane Distribution Server | Gateway Replica Grid | Monotonic snapshot broadcast over gRPC multiplexed streams; replica acknowledgment telemetry tracks fleet convergence percentage and per-replica lag. |
| Automated Chaos & Fault Injection | Integration Test Suite (`tests/chaos`) | Gateway & Control Plane | Simulates node death, network partitions, Redis outages, DB outages, and disk saturation to prove 100% fail-closed adherence without manual intervention. |
| Performance & SLO Benchmarking | k6 & Go Benchmarks (`benchmarks/`) | Gateway Replicas | Generates 1,000 req/s sustained traffic, verifying <2ms OPA evaluation and <20ms p99 gateway added latency. |
</architectural_responsibility_map>

<research_summary>
## Summary

Phase 5 transitions Aegis from a single-replica hardened gateway into a resilient distributed cluster. In production zero-trust environments, gateways must withstand infrastructure crashes, control-plane network partitions, database outages, and abrupt restarts without dropping in-flight traffic or failing open.

The standard industry approach for distributed zero-trust gateway resilience combines:
1. **Edge Load Balancing with Dynamic Readiness Checks:** Deploying an active-passive or round-robin edge proxy (such as HAProxy) fronting multiple gateway replicas. The load balancer polls dedicated HTTP health probes (`/healthz` for liveness, `/readyz` for readiness). When a replica receives `SIGTERM`, it marks `/readyz` as failing (HTTP 503) immediately, while the server continues processing in-flight requests for up to 30 seconds before closing connections.
2. **Exponential Backoff with Full Jitter on Control Plane gRPC Streams:** When the control plane restarts or a network partition occurs, replicas must reconnect using randomized exponential jitter ($T = \min(M, B \times 2^n) \times \text{Uniform}(0.8, 1.2)$) to prevent synchronized connection bursts from crashing the control plane.
3. **Automated Chaos Engineering:** A dedicated test suite that programmatically injects cascading failures (process SIGKILL, Redis network partition, PostgreSQL offline, WAL spool disk saturation) while asserting that security invariants never degrade (e.g. 503 returned, zero 500s or bypasses).
4. **Reproducible k6 Benchmarks:** Establishing baseline latency metrics (direct backend vs single gateway vs 3-replica cluster behind load balancer) demonstrating that policy evaluation executes in <0.2ms p50, <2ms p99, and added proxy latency remains <20ms at 1,000 RPS.

**Primary recommendation:** Build `deployments/compose/docker-compose.distributed.yml` pairing 3 gateway replicas with HAProxy, add explicit `/healthz` and `/readyz` probes with 30s connection draining in `cmd/gateway/main.go`, implement a comprehensive automated chaos test suite in `tests/chaos/chaos_test.go`, and author reproducible k6 test scenarios in `benchmarks/k6/`.
</research_summary>

<standard_stack>
## Standard Stack

### Core Technologies
| Technology | Version | Purpose | Why Standard |
|------------|---------|---------|--------------|
| **HAProxy** | `2.8-alpine` | Edge L7 Load Balancer fronting 3 gateway replicas | Industry standard for ultra-low latency (<0.1ms overhead), sub-second health checking (`inter 1s fall 2 rise 2`), native graceful reload, and zero-downtime maintenance. |
| **Go `net/http` Server Draining** | Go Standard Library | 30-second graceful connection draining on SIGTERM | Go 1.8+ `Server.Shutdown(ctx)` stops listening for new connections and waits for active requests to finish; integrated with atomic draining flag for instant readiness degradation. |
| **Grafana k6** | `v0.56.0` (Docker `grafana/k6:latest`) | Scriptable distributed load generation and latency benchmarking | Modern developer-first load testing tool with declarative thresholds (`http_req_duration p(99) < 20`), JavaScript scripting, virtual users (VUs), and reproducible JSON summary exports. |
| **Go `math/rand/v2` / `time`** | Go Standard Library | Reconnect backoff with full jitter | High-performance, cryptographically clean pseudorandom number generator in standard library for Decorrelated Jitter and Full Jitter algorithms without third-party dependencies. |

### Supporting Libraries
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| **`github.com/stretchr/testify`** | `v1.10.0` | Test assertion framework | Writing objective assertions in chaos and resilience tests (`require.NoError`, `assert.Equal`). |
| **`github.com/alicebob/miniredis/v2`** | `v2.34.0` | In-memory Redis simulation | Programmatically simulating Redis connection drops and network partitions during automated unit and chaos tests. |
| **`pgxmock/v4`** | `v4.4.0` | In-memory PostgreSQL mock pool | Simulating database connection loss and recovery during chaos testing. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| **HAProxy** | NGINX | NGINX open source requires third-party modules or external scripts for advanced active health checks on backend pools; HAProxy includes native HTTP active health checks (`option httpchk`) out of the box in the free community edition. |
| **k6 Container** | ApacheBench (`ab`) or `wrk` | `ab` lacks support for complex request bodies, token rotation, and percentile metrics thresholds; `wrk` requires Lua scripts. k6 supports ES6 JavaScript, multi-step user scenarios, and strict threshold assertions. |

</standard_stack>

<architecture_patterns>
## Architecture Patterns

### System Architecture Diagram: Distributed Resilience Grid

```
                                    +-----------------------+
                                    | Client / k6 Benchmark |
                                    +-----------+-----------+
                                                |
                                                | HTTP :8080 / mTLS :9443
                                                v
                              +-----------------------------------+
                              |       Edge Load Balancer          |
                              |         (HAProxy 2.8)             |
                              |  - Health Probes: GET /readyz     |
                              |  - Algorithm: Round-Robin         |
                              +---+---------------+-----------+---+
                                  |               |           |
            +---------------------+               |           +---------------------+
            |                                     |                                 |
            v                                     v                                 v
+-----------------------+             +-----------------------+         +-----------------------+
|   Aegis Gateway #1    |             |   Aegis Gateway #2    |         |   Aegis Gateway #3    |
|   (Replica A)         |             |   (Replica B)         |         |   (Replica C)         |
| - In-Memory OPA       |             | - In-Memory OPA       |         | - In-Memory OPA       |
| - Pre-Forward WAL     |             | - Pre-Forward WAL     |         | - Pre-Forward WAL     |
| - Readiness: /readyz  |             | - Readiness: /readyz  |         | - Readiness: /readyz  |
| - 30s Graceful Drain  |             | - 30s Graceful Drain  |         | - 30s Graceful Drain  |
| - gRPC Jitter Client  |             | - gRPC Jitter Client  |         | - gRPC Jitter Client  |
+-----------+-----------+             +-----------+-----------+         +-----------+-----------+
            |                                     |                                 |
            +------------------+                  |                  +--------------+
                               |                  |                  |
                               v                  v                  v
                   +-----------------------------------------------------+
                   |                Aegis Control Plane                  |
                   |               (gRPC Stream on :9090)                |
                   |       - 10s Monotonic Freshness Leases              |
                   |       - Real-time Fleet Convergence Tracker         |
                   +--------------------------+--------------------------+
                                              |
                   +--------------------------+--------------------------+
                   |                                                     |
                   v                                                     v
       +-----------------------+                             +-----------------------+
       |   PostgreSQL 16 DB    |                             |      Redis 7.2        |
       | - Partitioned Audits  |                             | - GCRA Token Bucket   |
       | - Route & Policy Meta |                             | - <5s Quarantine Store|
       +-----------------------+                             +-----------------------+
```

### Recommended Directory Structure
```
deployments/
├── compose/
│   ├── docker-compose.mvp.yml
│   ├── docker-compose.hardened.yml
│   ├── docker-compose.distributed.yml      # NEW: 3 gateway replicas + HAProxy + full stack
│   └── haproxy/
│       └── haproxy.cfg                     # NEW: HAProxy load balancing & health checking config
benchmarks/
├── engine_bench_test.go                    # NEW: In-memory OPA policy evaluation microbenchmarks
├── k6/
│   ├── baseline.js                         # NEW: Direct backend latency baseline (1,000 RPS)
│   ├── single_gateway.js                   # NEW: 1-gateway proxy latency benchmark
│   └── distributed_cluster.js              # NEW: 3-gateway cluster behind HAProxy with convergence test
tests/
├── chaos/
│   ├── chaos_test.go                       # NEW: Programmatic chaos fault injection suite
│   └── test_helpers.go                     # NEW: Chaos harness utilities (kill, partition, mock)
internal/
├── snapshot/
│   └── client.go                           # EXTEND: Verified full jitter backoff loop
├── proxy/
│   └── server.go                           # EXTEND: Readiness probe handler and graceful drain tracker
cmd/
└── gateway/
    └── main.go                             # EXTEND: /healthz & /readyz mounting, 30s drain timeout
```

</architecture_patterns>

<dont_hand_roll>
## Don't Hand-Roll & Common Pitfalls

### Critical Traps to Avoid
1. **Don't Hand-Roll Load Balancers in Go for Multi-Replica Demos:**
   - *Pitfall:* Writing a custom Go reverse proxy script to balance traffic between the 3 gateway replicas introduces proxy bugs (hop-by-hop header leaking, unbuffered body issues, connection pooling starvation).
   - *Solution:* Use standard HAProxy (`haproxy:2.8-alpine`) in `docker-compose.distributed.yml`. It has battle-tested TCP connection reuse, active HTTP health checking, and transparent Layer 7 balancing.
2. **Don't Forget Draining In-Flight Traffic on SIGTERM:**
   - *Pitfall:* When a container receives SIGTERM, immediately calling `server.Close()` drops in-flight client requests, causing immediate 502/503 errors during deployments or autoscaling.
   - *Solution:* The gateway must enter a two-phase shutdown:
     - Phase 1: Mark atomic `draining = true`. `/readyz` immediately returns HTTP 503 so HAProxy cuts off new traffic.
     - Phase 2: Allow up to 30 seconds (`context.WithTimeout(ctx, 30*time.Second)`) for active requests to finish via `http.Server.Shutdown(ctx)`.
3. **Don't Use Constant or Linear Reconnect Intervals (Thundering Herd):**
   - *Pitfall:* If 3+ gateways lose connection to the Control Plane and all retry every 1 second simultaneously, the Control Plane gets hit with a thundering herd upon reboot, repeatedly exhausting file descriptors or gRPC worker threads.
   - *Solution:* Implement Full Jitter exponential backoff: $T = \text{rand}(0, \min(T_{\max}, T_{\text{base}} \times 2^n))$.
4. **Don't Bypass Readiness Checks on Health Endpoints:**
   - *Pitfall:* Returning HTTP 200 on `/healthz` even when the gateway's snapshot lease has expired (>60s) or WAL spool has reached 90% disk saturation. The load balancer continues directing traffic to a broken node that rejects every request with 503.
   - *Solution:* Decouple liveness from readiness:
     - `/livez` or `/healthz`: returns 200 OK as long as the HTTP listener is responsive.
     - `/readyz`: returns 200 OK ONLY if snapshot is loaded, lease age is <60s, spool is <90% capacity, and draining is false. If any condition fails, returns 503.
5. **Don't Forget to Test Chaos Security Invariants (Fail-Closed):**
   - *Pitfall:* Testing chaos only for uptime/availability without verifying authorization integrity.
   - *Solution:* Under every chaos scenario (node killed, Redis down, DB partitioned), negative security tests must prove that default-deny is 100% maintained and unauthenticated/unauthorized requests NEVER slip through.

</dont_hand_roll>

<code_examples>
## Code Examples & Reference Implementation

### 1. Dual Probe Pattern & 30s Graceful Drain (`cmd/gateway/main.go`)
```go
// Atomic state tracker for gateway readiness
var isDraining atomic.Bool

// Health check mux handles probes without requiring Bearer token authentication
probeMux := http.NewServeMux()

probeMux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte("OK"))
})

probeMux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
    if isDraining.Load() {
        http.Error(w, "DRAINING", http.StatusServiceUnavailable)
        return
    }
    if snapManager.IsLeaseExpired(60 * time.Second) {
        http.Error(w, "LEASE_EXPIRED", http.StatusServiceUnavailable)
        return
    }
    if snapManager.Active() == nil {
        http.Error(w, "UNINITIALIZED", http.StatusServiceUnavailable)
        return
    }
    if diskSpool.IsSaturated() {
        http.Error(w, "SPOOL_SATURATED", http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte("READY"))
})
```

### 2. HAProxy Load Balancer Configuration (`deployments/compose/haproxy/haproxy.cfg`)
```haproxy
global
    log stdout format raw local0 info
    maxconn 4096

defaults
    log global
    mode http
    option httplog
    option dontlognull
    timeout connect 5000ms
    timeout client  50000ms
    timeout server  50000ms

frontend aegis_http_front
    bind *:8080
    default_backend aegis_http_back

backend aegis_http_back
    balance roundrobin
    option httpchk GET /readyz
    http-check expect status 200
    default-server inter 1s fall 2 rise 2
    server gateway-1 gateway-1:8080 check
    server gateway-2 gateway-2:8080 check
    server gateway-3 gateway-3:8080 check

listen stats
    bind *:8404
    stats enable
    stats uri /stats
    stats refresh 5s
```

### 3. Full Jitter Reconnect Backoff Formula
```go
// Exponential backoff with Full Jitter (AWS Architecture recommendation)
func calculateJitterBackoff(attempt int, baseBackoff, maxBackoff time.Duration) time.Duration {
    temp := float64(baseBackoff) * math.Pow(2, float64(attempt))
    if temp > float64(maxBackoff) {
        temp = float64(maxBackoff)
    }
    // Random duration between 0 and temp
    sleep := time.Duration(rand.Float64() * temp)
    if sleep < baseBackoff {
        sleep = baseBackoff
    }
    return sleep
}
```

### 4. k6 Latency Benchmark Script (`benchmarks/k6/distributed_cluster.js`)
```javascript
import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  scenarios: {
    constant_rate: {
      executor: 'constant-arrival-rate',
      rate: 1000, // 1,000 req/s
      timeUnit: '1s',
      duration: '30s',
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    'http_req_duration{status:200}': ['p(95)<10', 'p(99)<20'], // <20ms p99 SLA
    'http_req_failed': ['rate<0.01'], // 99% success rate
  },
};

export default function () {
  const params = {
    headers: {
      'Authorization': 'Bearer ' + __ENV.TOKEN,
      'X-Request-ID': 'k6-' + __VU + '-' + __ITER,
    },
  };
  const res = http.get('http://haproxy:8080/api/orders', params);
  check(res, {
    'status is 200': (r) => r.status === 200,
  });
}
```

</code_examples>

<environment_availability>
## Environment Availability

- **Go Runtime:** Go 1.25.5 available on Linux (`/usr/local/go/bin/go`).
- **Docker Engine:** Docker 29.1.3 installed and active on host.
- **Node / npm:** Node 22.23.3 and npm 10.8.2 available for dashboard/tooling.
- **k6 Execution:** Can be executed seamlessly via Docker container (`docker run --rm --network host grafana/k6:latest run ...`) or Go microbenchmarks.

</environment_availability>

<validation_architecture>
## Validation Architecture

### Task-to-Requirement Verification Mapping
| Task / Plan | Requirement | Validation Method |
|-------------|-------------|-------------------|
| Multi-replica cluster behind HAProxy with health checking & 30s drain | DIST-01 | Automated integration test verifying HAProxy routes traffic, drops draining node, and completes in-flight requests without errors. |
| gRPC Reconnect Jitter & Fleet Convergence (<5s) | DIST-01 | Test verifying randomized reconnect intervals across 3 replicas, and verifying fleet convergence to snapshot $N+1$ in $<5$s. |
| Automated Chaos Suite (killed nodes, severed CP, Redis partition, DB down, spool saturation) | DIST-03 | Comprehensive Go test suite (`tests/chaos/chaos_test.go`) executing cascading fault injections and verifying 100% fail-closed adherence. |
| Performance Benchmark Suite (1,000 RPS, <2ms OPA, <20ms p99 gateway added latency) | DIST-01, Invariant 3 | Go microbenchmarks (`benchmarks/engine_bench_test.go`) and k6 load tests validating latency SLOs. |

### Verification Commands
- Quick test: `go test -v -race ./tests/chaos/...`
- Full test suite: `go test -v -race ./internal/... ./tests/...`
- Benchmark: `go test -v -bench=. -benchmem ./benchmarks/...`
- Docker Compose configuration validation: `docker compose -f deployments/compose/docker-compose.distributed.yml config`

</validation_architecture>

<security_domain>
## Security Domain & Threat Model

### STRIDE Chaos Security Analysis
| Threat Category | Scenario during Chaos | Mitigating Architecture |
|---|---|---|
| **Spoofing (S)** | Client spoofs bearer tokens while Redis is unreachable or partitioned. | Redis client 200ms timeout fails closed; revoked tokens or unknown principals return HTTP 503 (`REDIS_OUTAGE_FAIL_CLOSED`), never 200. |
| **Tampering (T)** | Attacker injects encoded path traversal (`%2F..%2F`) during high load or node failover. | Strict zero-repair path validation runs on every gateway before proxying, returning 400 Bad Request regardless of cluster state. |
| **Repudiation (R)** | Requests bypass WAL audit logging when PostgreSQL is down or connection pool exhausted. | Gateway appends to local append-only WAL with synchronous `fsync` before upstream forwarding. PostgreSQL outage pauses async worker drainage without halting gateway ingress. |
| **Information Disclosure (I)** | Unhandled node failure leaks internal stack traces or database connection strings. | Error responses always return standardized RFC 7807 problem details with correlation UUID. |
| **Denial of Service (D)** | Control plane restart causes all 3 gateway replicas to flood connection attempts simultaneously. | Randomized exponential backoff with full jitter (100ms base, 5s max) spreads reconnect requests evenly over time. |
| **Elevation of Privilege (E)** | Draining replica fails open and forwards unauthenticated traffic. | Readiness drop only stops new ingress at load balancer; internal authorization pipeline enforces default-deny on every single in-flight request. |

</security_domain>

<sources>
## Sources & Metadata
- **NIST SP 800-207**: Zero Trust Architecture (Section 3.3 Gateway Deployment & Resilience).
- **HAProxy 2.8 Documentation**: Layer 7 HTTP health checks (`option httpchk`) and connection draining.
- **AWS Architecture Blog**: Exponential Backoff and Jitter algorithms (Marc Brooker).
- **Grafana k6 Documentation**: Thresholds, scenarios, and constant-arrival-rate executors.
- **Go Standard Library**: `net/http` server shutdown, `sync/atomic`, and `math/rand/v2`.
</sources>
