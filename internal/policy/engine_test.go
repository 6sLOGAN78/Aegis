package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadTestPolicy(t testing.TB) string {
	policyPath := filepath.Join("..", "..", "policies", "rego", "authz.rego")
	regoBytes, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatalf("failed to read policy file: %v", err)
	}
	return string(regoBytes)
}

func TestNewEngine_InvalidRego(t *testing.T) {
	_, err := NewEngine(context.Background(), "invalid syntax rego := %%%")
	assert.Error(t, err)
}

func TestDefaultDeny(t *testing.T) {
	regoCode := loadTestPolicy(t)
	engine, err := NewEngine(context.Background(), regoCode)
	require.NoError(t, err)

	input := PolicyInput{
		Principal: PrincipalInput{
			ID:    "usr_developer_01",
			Kind:  "user",
			Roles: []string{"developer"},
		},
		Resource: ResourceInput{
			Service: "inventory",
			Route:   "inventory.get",
		},
		Request: RequestInput{
			Method: "GET",
			Path:   "/api/inventory",
		},
		Context: ContextInput{
			RiskScore: 0,
			RiskState: "available",
		},
		SnapshotVersion: 10,
	}

	decision, err := engine.Evaluate(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, decision.Allow)
	assert.Equal(t, "DENIED_DEFAULT", decision.ReasonCode)
	assert.Equal(t, int64(10), decision.SnapshotVersion)
}

func TestFailClosed(t *testing.T) {
	regoCode := loadTestPolicy(t)
	engine, err := NewEngine(context.Background(), regoCode)
	require.NoError(t, err)

	t.Run("empty principal id returns invalid principal deny", func(t *testing.T) {
		input := PolicyInput{
			Principal: PrincipalInput{
				ID:    "",
				Kind:  "anonymous",
				Roles: []string{},
			},
			Resource: ResourceInput{
				Service: "orders",
				Route:   "orders.list",
			},
			Request: RequestInput{
				Method: "GET",
				Path:   "/api/orders",
			},
			Context: ContextInput{
				RiskScore: 0,
				RiskState: "available",
			},
			SnapshotVersion: 1,
		}

		decision, err := engine.Evaluate(context.Background(), input)
		require.NoError(t, err)
		assert.False(t, decision.Allow)
		assert.Equal(t, "DENIED_INVALID_PRINCIPAL", decision.ReasonCode)
		assert.Equal(t, int64(1), decision.SnapshotVersion)
	})

	t.Run("canceled context triggers evaluation error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		input := PolicyInput{
			Principal: PrincipalInput{
				ID:    "usr_developer_01",
				Kind:  "user",
				Roles: []string{"developer"},
			},
			Resource: ResourceInput{
				Service: "orders",
				Route:   "orders.list",
			},
			Request: RequestInput{
				Method: "GET",
				Path:   "/api/orders",
			},
			Context: ContextInput{
				RiskScore: 0,
				RiskState: "available",
			},
			SnapshotVersion: 2,
		}

		decision, err := engine.Evaluate(ctx, input)
		assert.Error(t, err)
		assert.False(t, decision.Allow)
		assert.Equal(t, "EVALUATION_ERROR", decision.ReasonCode)
		assert.Equal(t, int64(2), decision.SnapshotVersion)
	})
}

func BenchmarkOPAEval(b *testing.B) {
	regoCode := loadTestPolicy(b)
	engine, err := NewEngine(context.Background(), regoCode)
	if err != nil {
		b.Fatalf("failed to initialize engine: %v", err)
	}

	input := PolicyInput{
		Principal: PrincipalInput{
			ID:    "usr_developer_01",
			Kind:  "user",
			Roles: []string{"developer"},
		},
		Resource: ResourceInput{
			Service: "orders",
			Route:   "orders.list",
		},
		Request: RequestInput{
			Method: "GET",
			Path:   "/api/orders",
		},
		Context: ContextInput{
			RiskScore: 0,
			RiskState: "available",
		},
		SnapshotVersion: 1,
	}

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		decision, err := engine.Evaluate(ctx, input)
		if err != nil {
			b.Fatalf("evaluation returned error: %v", err)
		}
		if !decision.Allow {
			b.Fatalf("expected allow decision, got: %+v", decision)
		}
	}
}
