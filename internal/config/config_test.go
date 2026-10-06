package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		os.Unsetenv("AEGIS_PORT")
		os.Unsetenv("AEGIS_MAX_CONCURRENT")
		os.Unsetenv("AEGIS_ROUTES_PATH")

		cfg, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, 8080, cfg.Port)
		assert.Equal(t, 16384, cfg.MaxHeaderBytes)
		assert.Equal(t, int64(1048576), cfg.MaxBodyBytes)
		assert.Equal(t, 5*time.Second, cfg.ReadHeaderTimeout)
		assert.Equal(t, 15*time.Second, cfg.WriteTimeout)
		assert.Equal(t, 1000, cfg.MaxConcurrentRequests)
		assert.Equal(t, "policies/data/routes.json", cfg.RoutesFilePath)
	})

	t.Run("environment variable overrides", func(t *testing.T) {
		t.Setenv("AEGIS_PORT", "9090")
		t.Setenv("AEGIS_MAX_CONCURRENT", "500")
		t.Setenv("AEGIS_ROUTES_PATH", "/custom/routes.json")

		cfg, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, 9090, cfg.Port)
		assert.Equal(t, 500, cfg.MaxConcurrentRequests)
		assert.Equal(t, "/custom/routes.json", cfg.RoutesFilePath)
	})

	t.Run("invalid port returns error", func(t *testing.T) {
		t.Setenv("AEGIS_PORT", "invalid")
		_, err := LoadConfig()
		assert.Error(t, err)
	})

	t.Run("invalid max concurrent returns error", func(t *testing.T) {
		t.Setenv("AEGIS_PORT", "8080")
		t.Setenv("AEGIS_MAX_CONCURRENT", "invalid")
		_, err := LoadConfig()
		assert.Error(t, err)
	})
}
