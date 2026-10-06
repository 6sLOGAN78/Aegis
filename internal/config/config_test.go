package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		os.Unsetenv("AEGIS_PORT")
		os.Unsetenv("AEGIS_WORKLOAD_PORT")
		os.Unsetenv("AEGIS_MAX_CONCURRENT")
		os.Unsetenv("AEGIS_ROUTES_PATH")
		os.Unsetenv("AEGIS_TLS_CERT_PATH")
		os.Unsetenv("AEGIS_TLS_KEY_PATH")
		os.Unsetenv("AEGIS_CLIENT_CERT_PATH")
		os.Unsetenv("AEGIS_CLIENT_KEY_PATH")
		os.Unsetenv("AEGIS_WORKLOAD_CA_PATH")
		os.Unsetenv("AEGIS_ASSERTION_PRIVATE_KEY_PATH")
		os.Unsetenv("AEGIS_ASSERTION_PUBLIC_KEY_PATH")

		cfg, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, 8080, cfg.Port)
		assert.Equal(t, 9443, cfg.WorkloadPort)
		assert.Equal(t, 16384, cfg.MaxHeaderBytes)
		assert.Equal(t, int64(1048576), cfg.MaxBodyBytes)
		assert.Equal(t, 5*time.Second, cfg.ReadHeaderTimeout)
		assert.Equal(t, 15*time.Second, cfg.WriteTimeout)
		assert.Equal(t, 1000, cfg.MaxConcurrentRequests)
		assert.Equal(t, "policies/data/routes.json", cfg.RoutesFilePath)
		assert.Empty(t, cfg.TLSCertPath)
		assert.Empty(t, cfg.TLSKeyPath)
		assert.Empty(t, cfg.ClientCertPath)
		assert.Empty(t, cfg.ClientKeyPath)
		assert.Empty(t, cfg.WorkloadCACertPath)
		assert.Empty(t, cfg.AssertionPrivateKeyPath)
		assert.Empty(t, cfg.AssertionPublicKeyPath)
	})

	t.Run("environment variable overrides", func(t *testing.T) {
		t.Setenv("AEGIS_PORT", "9090")
		t.Setenv("AEGIS_WORKLOAD_PORT", "9444")
		t.Setenv("AEGIS_MAX_CONCURRENT", "500")
		t.Setenv("AEGIS_ROUTES_PATH", "/custom/routes.json")
		t.Setenv("AEGIS_TLS_CERT_PATH", "/certs/server.crt")
		t.Setenv("AEGIS_TLS_KEY_PATH", "/certs/server.key")
		t.Setenv("AEGIS_CLIENT_CERT_PATH", "/certs/client.crt")
		t.Setenv("AEGIS_CLIENT_KEY_PATH", "/certs/client.key")
		t.Setenv("AEGIS_WORKLOAD_CA_PATH", "/certs/ca.crt")
		t.Setenv("AEGIS_ASSERTION_PRIVATE_KEY_PATH", "/certs/assertion.key")
		t.Setenv("AEGIS_ASSERTION_PUBLIC_KEY_PATH", "/certs/assertion.pub")

		cfg, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, 9090, cfg.Port)
		assert.Equal(t, 9444, cfg.WorkloadPort)
		assert.Equal(t, 500, cfg.MaxConcurrentRequests)
		assert.Equal(t, "/custom/routes.json", cfg.RoutesFilePath)
		assert.Equal(t, "/certs/server.crt", cfg.TLSCertPath)
		assert.Equal(t, "/certs/server.key", cfg.TLSKeyPath)
		assert.Equal(t, "/certs/client.crt", cfg.ClientCertPath)
		assert.Equal(t, "/certs/client.key", cfg.ClientKeyPath)
		assert.Equal(t, "/certs/ca.crt", cfg.WorkloadCACertPath)
		assert.Equal(t, "/certs/assertion.key", cfg.AssertionPrivateKeyPath)
		assert.Equal(t, "/certs/assertion.pub", cfg.AssertionPublicKeyPath)
	})

	t.Run("invalid port returns error", func(t *testing.T) {
		t.Setenv("AEGIS_PORT", "invalid")
		_, err := LoadConfig()
		assert.Error(t, err)
	})

	t.Run("invalid workload port returns error", func(t *testing.T) {
		t.Setenv("AEGIS_WORKLOAD_PORT", "invalid")
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
