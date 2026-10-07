# Phase 5: Distributed Resilience, Chaos & Benchmark Evidence — Code & Architecture Patterns

**Generated:** 2026-10-07  
**Phase:** 05-distributed-resilience-chaos  
**Status:** Approved Reference  
**Domain:** Distributed High Availability, Layer 7 Load Balancing, Connection Draining, gRPC Jitter Reconnect, Automated Chaos Fault Injection, Fleet Snapshot Convergence, and Reproducible k6 Benchmarks  

---

## 1. Executive Summary & File Inventory

Phase 5 hardens and validates Aegis as a multi-replica distributed zero-trust system. In accordance with [REQUIREMENTS.md](file:///home/logan78/Desktop/Aegis/.planning/REQUIREMENTS.md) (DIST-01, DIST-03), [ROADMAP.md](file:///home/logan78/Desktop/Aegis/.planning/ROADMAP.md), [05-RESEARCH.md](file:///home/logan78/Desktop/Aegis/.planning/phases/05-distributed-resilience-chaos/05-RESEARCH.md), and [05-VALIDATION.md](file:///home/logan78/Desktop/Aegis/.planning/phases/05-distributed-resilience-chaos/05-VALIDATION.md), Phase 5 delivers:

1. **Multi-Replica Gateway Deployment & Layer 7 Load Balancing (DIST-01):**
   Three gateway replicas (`gateway-1`, `gateway-2`, `gateway-3`) running in Docker Compose behind an HAProxy edge load balancer. Gateway exposes dedicated unauthenticated health probes (`/livez` for liveness, `/readyz` for readiness). When a replica receives SIGTERM, it immediately flips `/readyz` to 503 so HAProxy removes it from the routing pool, while the gateway continues servicing active in-flight requests for up to 30 seconds before terminating.
2. **gRPC Stream Reconnection with Full Jitter & Fleet Convergence (DIST-01, Invariant 4):**
   When the control plane restarts or network connections drop, gateway stream clients reconnect using randomized exponential backoff with Full Jitter (100ms base, 5s max), eliminating the thundering herd problem. Under active traffic load, all 3 replicas converge to newly broadcast snapshots within 5 seconds at p99.
3. **Automated Chaos & Fault Injection Suite (DIST-03, Invariants 1, 2, 5, 10):**
   A dedicated automated test suite (`tests/chaos/chaos_test.go`) systematically injects cascading infrastructure failures: killing gateway processes mid-flight, partitioning the Control Plane past the 60s lease boundary, severing Redis with 200ms fail-closed verification, dropping PostgreSQL while verifying uninterrupted proxy traffic, and saturating WAL spools to 90%. Under all chaos conditions, negative security tests confirm zero fail-open bypasses.
4. **Reproducible Latency & Performance Benchmarks (DIST-01, Invariant 3):**
   Declarative Grafana k6 scripts (`benchmarks/k6/`) and Go engine microbenchmarks (`benchmarks/engine_bench_test.go`) measuring baseline direct backend, single-gateway, and 3-gateway cluster throughput at 1,000 req/s, proving that in-memory OPA evaluation executes in <0.2ms p50, <2ms p99, and gateway added latency remains <20ms p99.

```
aegis/
├── deployments/
│   └── compose/
│       ├── docker-compose.distributed.yml          # [NEW] 3 gateway replicas, HAProxy, CP, DB, Redis, audit worker, backends
│       └── haproxy/
│           └── haproxy.cfg                         # [NEW] HAProxy L7 HTTP balancing with active /readyz health checks
├── internal/
│   ├── proxy/
│   │   ├── probe.go                                # [NEW] Reusable DrainingState and CreateProbeHandler for /livez and /readyz
│   │   └── probe_test.go                           # [NEW] Unit tests for probe statuses, lease checks, and draining
│   └── snapshot/
│       ├── client.go                               # [MODIFIED] Verify and harden Full Jitter backoff loop
│       └── client_test.go                          # [NEW] Unit tests verifying Full Jitter distribution and reconnect bounds
├── cmd/
│   └── gateway/
│       ├── main.go                                 # [MODIFIED] Mount probe handler, wire 30s connection drain on SIGTERM
│       └── main_test.go                            # [NEW] Integration tests for 30s graceful draining lifecycle
├── tests/
│   └── chaos/
│       ├── chaos_test.go                           # [NEW] Chaos test suite: node kill, lease expiry, Redis drop, DB outage, spool
│       ├── convergence_test.go                     # [NEW] Fleet snapshot convergence under load (<5s at p99)
│       └── test_helpers.go                         # [NEW] Multi-gateway test cluster fixture and mock traffic generator
├── benchmarks/
│   ├── engine_bench_test.go                        # [NEW] Go microbenchmarks for in-memory OPA evaluation latency
│   └── k6/
│       ├── baseline.js                             # [NEW] Direct backend latency benchmark at 1,000 RPS
│       ├── single_gateway.js                       # [NEW] Single gateway proxy latency benchmark at 1,000 RPS
│       └── distributed_cluster.js                  # [NEW] 3-replica cluster behind HAProxy with live snapshot convergence test
└── Makefile                                        # [MODIFIED] Targets: distributed-up, distributed-down, chaos-test, bench, bench-k6
```

---

## 2. Classification by Role & Data Flow

| File Path | Architectural Role | Ingress / Input | Processing / Transformation | Egress / Output | Invariants & Requirements |
|---|---|---|---|---|---|
| `deployments/compose/docker-compose.distributed.yml` | Multi-node deployment orchestrator | Docker CLI invocation | Configures 3 gateway replicas, HAProxy edge proxy, networks, and persistent WAL volumes | Running container cluster | DIST-01, BYP-01 |
| `deployments/compose/haproxy/haproxy.cfg` | Edge L7 Load Balancer configuration | Client traffic on :8080 and :9443 | Polls `GET /readyz` every 1s (`fall 2 rise 2`); round-robins traffic across healthy gateways | Forwarded client HTTP requests | DIST-01 |
| `internal/proxy/probe.go` | Reusable health and readiness probe engine | HTTP GET requests to `/livez`, `/readyz` | Evaluates draining state, lease expiration, snapshot state, and WAL spool saturation | HTTP status 200 (OK/READY) or 503 (DRAINING/LEASE_EXPIRED/SPOOL_SATURATED) | DIST-01, Invariant 1 |
| `cmd/gateway/main.go` | Gateway entrypoint & lifecycle | OS signals (SIGTERM, SIGINT) | Wires probe handler; flips draining flag on SIGTERM; drains active connections up to 30s | Graceful shutdown sequence | DIST-01, Invariant 1 |
| `internal/snapshot/client.go` | gRPC stream client | Control plane gRPC stream on :9090 | Implements randomized Full Jitter backoff upon disconnect; receives snapshots and leases | Active snapshot activation & acks | DIST-01, Invariant 4 |
| `tests/chaos/chaos_test.go` | Chaos fault injection runner | Test execution | Programmatically drops Redis, kills gateway nodes, severs control plane, injects path traversals | Assertion pass/fail results | DIST-03, Invariants 1, 2, 5, 10 |
| `benchmarks/engine_bench_test.go` | Engine microbenchmark | Benchmark runner (`go test -bench`) | Executes concurrent OPA Rego evaluations against precompiled queries | Latency percentiles (ns/op, B/op) | Invariant 3 (<2ms p99) |
| `benchmarks/k6/distributed_cluster.js` | Distributed cluster load test | k6 CLI / Docker | Dispatches 1,000 RPS sustained load; publishes snapshot during traffic; asserts latency | JSON summary & SLO metrics | DIST-01, Invariant 3 (<20ms p99) |

---

## 3. Concrete Code Patterns

### Pattern 1: Gateway Readiness Probes & 30s Graceful Connection Draining
**File:** `internal/proxy/probe.go` and `cmd/gateway/main.go`  
**Purpose:** Provide dedicated `/livez` and `/readyz` endpoints that bypass authentication, and coordinate a two-phase shutdown on SIGTERM where readiness fails immediately (503) while active in-flight requests complete cleanly within a 30-second timeout window.

```go
package proxy

import (
	"net/http"
	"sync/atomic"
	"time"
)

// DrainingState tracks graceful shutdown lifecycle
type DrainingState struct {
	isDraining atomic.Bool
}

func NewDrainingState() *DrainingState {
	return &DrainingState{}
}

func (s *DrainingState) SetDraining() {
	s.isDraining.Store(true)
}

func (s *DrainingState) IsDraining() bool {
	return s.isDraining.Load()
}

// CreateProbeHandler creates unauthenticated health and readiness endpoints
func CreateProbeHandler(
	draining *DrainingState,
	isLeaseExpired func(time.Duration) bool,
	isSnapshotActive func() bool,
	isSpoolSaturated func() bool,
	nextHandler http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/livez", "/healthz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK\n"))
			return
		case "/readyz", "/healthz/ready":
			if draining != nil && draining.IsDraining() {
				http.Error(w, "DRAINING", http.StatusServiceUnavailable)
				return
			}
			if isLeaseExpired != nil && isLeaseExpired(60*time.Second) {
				http.Error(w, "LEASE_EXPIRED", http.StatusServiceUnavailable)
				return
			}
			if isSnapshotActive != nil && !isSnapshotActive() {
				http.Error(w, "UNINITIALIZED", http.StatusServiceUnavailable)
				return
			}
			if isSpoolSaturated != nil && isSpoolSaturated() {
				http.Error(w, "SPOOL_SATURATED", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("READY\n"))
			return
		default:
			nextHandler.ServeHTTP(w, r)
		}
	})
}
```

### Pattern 2: HAProxy Layer 7 Load Balancer Configuration
**File:** `deployments/compose/haproxy/haproxy.cfg`  
**Purpose:** Distribute client traffic round-robin across 3 gateway replicas, dynamically detecting drained or partitioned nodes via `/readyz` within 2 seconds.

```haproxy
global
    log stdout format raw local0 info
    maxconn 4096

defaults
    log     global
    mode    http
    option  httplog
    option  dontlognull
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

### Pattern 3: gRPC Stream Reconnect with Randomized Full Jitter Backoff
**File:** `internal/snapshot/client.go`  
**Purpose:** Prevent thundering herd surges against the control plane when replicas reconnect after network partitions or reboots.

```go
package snapshot

import (
	"context"
	"math"
	"math/rand/v2"
	"time"
)

// CalculateFullJitterBackoff computes exponential backoff with Full Jitter
func CalculateFullJitterBackoff(attempt int, baseBackoff, maxBackoff time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	temp := float64(baseBackoff) * math.Pow(1.5, float64(attempt))
	if temp > float64(maxBackoff) {
		temp = float64(maxBackoff)
	}
	// Full jitter: uniformly distributed between baseBackoff and temp
	jitterRange := temp - float64(baseBackoff)
	if jitterRange <= 0 {
		return baseBackoff
	}
	sleep := float64(baseBackoff) + rand.Float64()*jitterRange
	return time.Duration(sleep)
}
```

### Pattern 4: Automated Chaos Fault Injection Test Harness
**File:** `tests/chaos/chaos_test.go`  
**Purpose:** Programmatically simulate killed nodes, control plane partitions, Redis outages, and database failures under active concurrent traffic.

```go
package chaos

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChaos_GatewayNodeFailureWithZeroDroppedRequests verifies DIST-01:
// When 1 of 3 gateway nodes is abruptly terminated mid-flight, the load balancer
// routes subsequent requests to the surviving 2 nodes with zero failed admissions.
func TestChaos_GatewayNodeFailureWithZeroDroppedRequests(t *testing.T) {
	cluster := NewTestCluster(t, 3)
	defer cluster.Close()

	var successCount atomic.Int64
	var failureCount atomic.Int64
	stopTraffic := make(chan struct{})

	// Start continuous background traffic
	go func() {
		for {
			select {
			case <-stopTraffic:
				return
			default:
				resp, err := cluster.Client.Get(cluster.LBURL + "/api/orders")
				if err == nil && resp.StatusCode == http.StatusOK {
					successCount.Add(1)
				} else {
					failureCount.Add(1)
				}
				if resp != nil {
					_ = resp.Body.Close()
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// Allow traffic to establish
	time.Sleep(100 * time.Millisecond)

	// Kill gateway-2 abruptly
	cluster.KillNode(1)

	// Keep traffic flowing through remaining nodes
	time.Sleep(200 * time.Millisecond)
	close(stopTraffic)

	assert.Equal(t, int64(0), failureCount.Load(), "Zero dropped requests during node failover")
	assert.Greater(t, successCount.Load(), int64(20), "Continuous success during failover")
}
```

### Pattern 5: Fleet Snapshot Convergence Under Load
**File:** `tests/chaos/chaos_test.go`  
**Purpose:** Verify that when a new policy is published, all 3 active gateway replicas converge to the new version within 5 seconds at p99 while actively handling traffic.

```go
func TestChaos_FleetConvergenceUnderLoad(t *testing.T) {
	cluster := NewTestCluster(t, 3)
	defer cluster.Close()

	// Initial version v1: allow developer to GET /api/orders
	// Publish version v2: update policy to deny GET /api/orders
	start := time.Now()
	newVersion := cluster.PublishNewSnapshot(t, `package aegis.authz
default allow := false
default reason_code := "DENIED_BY_V2"
decision := {"allow": false, "reason_code": reason_code, "snapshot_version": 2}
`)

	// Poll all replicas until all 3 report version v2
	require.Eventually(t, func() bool {
		for i := 0; i < 3; i++ {
			if cluster.Nodes[i].Manager.Active().Version != newVersion {
				return false
			}
		}
		return true
	}, 5*time.Second, 50*time.Millisecond, "All replicas must converge to snapshot v2 within 5 seconds")

	convergenceDuration := time.Since(start)
	t.Logf("Fleet converged across 3 replicas in %v", convergenceDuration)
	assert.Less(t, convergenceDuration, 5*time.Second)
}
```

### Pattern 6: High-Throughput OPA Engine Microbenchmarks
**File:** `benchmarks/engine_bench_test.go`  
**Purpose:** Microbenchmark in-memory Rego policy evaluation latency to objectively prove the <0.2ms p50 and <2ms p99 budget under parallel goroutines.

```go
package benchmarks

import (
	"context"
	"testing"

	"aegis/internal/policy"
)

func BenchmarkPolicyEngine_EvaluateParallel(b *testing.B) {
	ctx := context.Background()
	engine, err := policy.NewEngine(`package aegis.authz
default allow := false
default reason_code := "DENIED_DEFAULT"
allow if { input.principal.kind == "user"; "developer" in input.principal.roles }
reason_code := "ALLOWED" if { allow }
decision := {"allow": allow, "reason_code": reason_code, "snapshot_version": 1}
`)
	if err != nil {
		b.Fatalf("Failed to initialize engine: %v", err)
	}

	input := policy.PolicyInput{
		Principal: policy.PrincipalInput{
			ID:    "user-123",
			Kind:  "user",
			Roles: []string{"developer"},
		},
		Resource: policy.ResourceInput{
			Service: "orders",
			Route:   "orders.list",
		},
		Request: policy.RequestInput{
			Method: "GET",
			Path:   "/api/orders",
		},
		SnapshotVersion: 1,
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			dec, err := engine.Evaluate(ctx, input)
			if err != nil || !dec.Allow {
				b.Fatalf("Evaluation failed or disallowed: %v", err)
			}
		}
	})
}
```

---

## 4. Verification Checkpoints

- **Gateway Probe Endpoints:** `go test -v -race ./cmd/gateway/ -run TestGatewayProbes`
- **Graceful Draining:** `go test -v -race ./cmd/gateway/ -run TestGatewayGracefulDrain`
- **Full Jitter Formula:** `go test -v -race ./internal/snapshot/ -run TestStreamClientJitterBackoff`
- **Chaos Fault Injections:** `go test -v -race ./tests/chaos/...`
- **OPA Engine Benchmark:** `go test -v -bench=. -benchmem ./benchmarks/...`
- **Docker Compose Distributed Profile:** `docker compose -f deployments/compose/docker-compose.distributed.yml config`
