package benchmarks

import (
	"context"
	"sort"
	"testing"
	"time"

	"aegis/internal/policy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const benchmarkRegoPolicy = `package aegis.authz

default allow := false
default reason_code := "DENIED_DEFAULT"

allow if {
	input.principal.kind == "user"
	"developer" in input.principal.roles
}

reason_code := "ALLOWED" if { allow }

decision := {
	"allow": allow,
	"reason_code": reason_code,
	"snapshot_version": input.snapshot_version,
}
`

func setupBenchmarkEngine(tb testing.TB) (*policy.Engine, policy.PolicyInput) {
	tb.Helper()
	ctx := context.Background()
	engine, err := policy.NewEngine(ctx, benchmarkRegoPolicy)
	if err != nil {
		tb.Fatalf("Failed to initialize policy engine: %v", err)
	}

	input := policy.PolicyInput{
		Principal: policy.PrincipalInput{
			ID:    "bench-user-123",
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

	return engine, input
}

// BenchmarkPolicyEngine_EvaluateParallel benchmarks in-memory Rego policy evaluation
// under highly concurrent parallel goroutines, proving <0.2ms p50 and <2ms p99 latency budget.
func BenchmarkPolicyEngine_EvaluateParallel(b *testing.B) {
	engine, input := setupBenchmarkEngine(b)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			decision, err := engine.Evaluate(ctx, input)
			if err != nil || !decision.Allow {
				b.Fatalf("Unexpected evaluation failure: %v (decision: %+v)", err, decision)
			}
		}
	})
}

// BenchmarkPolicyEngine_EvaluateSequential benchmarks single-threaded policy evaluation.
func BenchmarkPolicyEngine_EvaluateSequential(b *testing.B) {
	engine, input := setupBenchmarkEngine(b)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		decision, err := engine.Evaluate(ctx, input)
		if err != nil || !decision.Allow {
			b.Fatalf("Unexpected evaluation failure: %v (decision: %+v)", err, decision)
		}
	}
}

// TestPolicyEngine_LatencyBudget verifies the strict SLO requirements from spec.md §15:
// In-memory OPA query evaluation must execute in <0.2ms (200µs) at p50 and <2ms (2000µs) at p99.
func TestPolicyEngine_LatencyBudget(t *testing.T) {
	engine, input := setupBenchmarkEngine(t)
	ctx := context.Background()

	// Warm up precompiled query
	for i := 0; i < 100; i++ {
		_, err := engine.Evaluate(ctx, input)
		require.NoError(t, err)
	}

	const samples = 10000
	latencies := make([]time.Duration, samples)

	for i := 0; i < samples; i++ {
		start := time.Now()
		dec, err := engine.Evaluate(ctx, input)
		latencies[i] = time.Since(start)

		require.NoError(t, err)
		require.True(t, dec.Allow)
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p50 := latencies[int(float64(samples)*0.50)]
	p90 := latencies[int(float64(samples)*0.90)]
	p95 := latencies[int(float64(samples)*0.95)]
	p99 := latencies[int(float64(samples)*0.99)]

	t.Logf("Policy Evaluation Latency Profile (%d samples):", samples)
	t.Logf("  p50: %v (budget <200µs / 0.2ms)", p50)
	t.Logf("  p90: %v", p90)
	t.Logf("  p95: %v", p95)
	t.Logf("  p99: %v (budget <2000µs / 2.0ms)", p99)

	// Assertions matching requirements: <0.2ms p50, <2ms p99
	assert.Less(t, p50, 200*time.Microsecond, "p50 evaluation latency must be <0.2ms (200µs)")
	assert.Less(t, p99, 2*time.Millisecond, "p99 evaluation latency must be <2.0ms (2000µs)")
}
