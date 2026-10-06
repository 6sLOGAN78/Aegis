package policy

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadStaticSnapshot(t *testing.T) {
	policyPath := filepath.Join("..", "..", "policies", "rego", "authz.rego")
	routesPath := filepath.Join("..", "..", "policies", "data", "routes.json")

	snapshot, err := LoadStaticSnapshot(policyPath, routesPath, 1)
	require.NoError(t, err)
	require.NotNil(t, snapshot)

	assert.Equal(t, int64(1), snapshot.Version)
	assert.NotEmpty(t, snapshot.RegoPolicy)
	assert.NotEmpty(t, snapshot.Routes)
	assert.Contains(t, snapshot.RegoPolicy, "package aegis.authz")
	assert.Contains(t, string(snapshot.Routes), "orders.list")
}

func TestSnapshotLoader(t *testing.T) {
	t.Run("valid snapshot load", func(t *testing.T) {
		policyPath := filepath.Join("..", "..", "policies", "rego", "authz.rego")
		routesPath := filepath.Join("..", "..", "policies", "data", "routes.json")

		snapshot, err := LoadStaticSnapshot(policyPath, routesPath, 42)
		require.NoError(t, err)
		require.NotNil(t, snapshot)
		assert.Equal(t, int64(42), snapshot.Version)
		assert.NotEmpty(t, snapshot.RegoPolicy)
		assert.NotEmpty(t, snapshot.Routes)
	})

	t.Run("missing policy file", func(t *testing.T) {
		routesPath := filepath.Join("..", "..", "policies", "data", "routes.json")
		snapshot, err := LoadStaticSnapshot("nonexistent.rego", routesPath, 1)
		assert.Error(t, err)
		assert.Nil(t, snapshot)
	})

	t.Run("missing routes file", func(t *testing.T) {
		policyPath := filepath.Join("..", "..", "policies", "rego", "authz.rego")
		snapshot, err := LoadStaticSnapshot(policyPath, "nonexistent.json", 1)
		assert.Error(t, err)
		assert.Nil(t, snapshot)
	})

	t.Run("empty paths", func(t *testing.T) {
		snapshot, err := LoadStaticSnapshot("", "", 1)
		assert.Error(t, err)
		assert.Nil(t, snapshot)

		snapshot, err = LoadStaticSnapshot("some/path", "", 1)
		assert.Error(t, err)
		assert.Nil(t, snapshot)
	})
}
