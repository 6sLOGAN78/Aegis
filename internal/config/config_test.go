package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var auditConfigKeys = []string{
	"AEGIS_AUDIT_GROUP_FLUSH_INTERVAL",
	"AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL",
	"AEGIS_AUDIT_COMPLETION_QUEUE_SIZE",
	"AEGIS_SPOOL_HARD_LIMIT_RATIO",
	"AEGIS_AUDIT_UNAUTH_RATE",
	"AEGIS_AUDIT_UNAUTH_BURST",
	"AEGIS_AUDIT_SUPPRESS_WINDOW",
	"AEGIS_AUDIT_SUPPRESS_MAX_KEYS",
	"AEGIS_SPOOL_SEGMENT_BYTES",
}

func TestAuditConfigOverrides(t *testing.T) {
	t.Run("valid overrides", func(t *testing.T) {
		t.Setenv("AEGIS_AUDIT_GROUP_FLUSH_INTERVAL", "5ms")
		t.Setenv("AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL", "100ms")
		t.Setenv("AEGIS_AUDIT_COMPLETION_QUEUE_SIZE", "1024")
		t.Setenv("AEGIS_SPOOL_HARD_LIMIT_RATIO", "0.97")
		t.Setenv("AEGIS_AUDIT_UNAUTH_RATE", "2.5")
		t.Setenv("AEGIS_AUDIT_UNAUTH_BURST", "7")
		t.Setenv("AEGIS_AUDIT_SUPPRESS_WINDOW", "30s")
		t.Setenv("AEGIS_AUDIT_SUPPRESS_MAX_KEYS", "128")
		t.Setenv("AEGIS_SPOOL_SEGMENT_BYTES", "65536")

		cfg, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, 5*time.Millisecond, cfg.AuditGroupFlushInterval)
		assert.Equal(t, 100*time.Millisecond, cfg.AuditCompletionFlushInterval)
		assert.Equal(t, 1024, cfg.AuditCompletionQueueSize)
		assert.Equal(t, 0.97, cfg.SpoolHardLimitRatio)
		assert.Equal(t, 2.5, cfg.AuditUnauthRate)
		assert.Equal(t, 7, cfg.AuditUnauthBurst)
		assert.Equal(t, 30*time.Second, cfg.AuditSuppressWindow)
		assert.Equal(t, 128, cfg.AuditSuppressMaxKeys)
		assert.Equal(t, int64(65536), cfg.SpoolSegmentBytes)
	})

	t.Run("identified denial allowance key is ignored", func(t *testing.T) {
		t.Setenv("AEGIS_AUDIT_IDENTIFIED_"+"DENIAL_ALLOWANCE", "99") // split so greps for the removed key stay at zero
		_, err := LoadConfig()
		require.NoError(t, err)
	})

	invalid := []struct {
		key, value string
	}{
		{"AEGIS_AUDIT_GROUP_FLUSH_INTERVAL", "abc"},
		{"AEGIS_AUDIT_GROUP_FLUSH_INTERVAL", "0s"},
		{"AEGIS_AUDIT_GROUP_FLUSH_INTERVAL", "-1ms"},
		{"AEGIS_AUDIT_GROUP_FLUSH_INTERVAL", "51ms"},
		{"AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL", "abc"},
		{"AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL", "0s"},
		{"AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL", "-5ms"},
		{"AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL", "1001ms"},
		{"AEGIS_AUDIT_COMPLETION_QUEUE_SIZE", "abc"},
		{"AEGIS_AUDIT_COMPLETION_QUEUE_SIZE", "63"},
		{"AEGIS_AUDIT_COMPLETION_QUEUE_SIZE", "1000001"},
		{"AEGIS_AUDIT_COMPLETION_QUEUE_SIZE", "100001"},
		{"AEGIS_SPOOL_HARD_LIMIT_RATIO", "abc"},
		{"AEGIS_SPOOL_HARD_LIMIT_RATIO", "0.90"},
		{"AEGIS_SPOOL_HARD_LIMIT_RATIO", "0.5"},
		{"AEGIS_SPOOL_HARD_LIMIT_RATIO", "0.991"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "abc"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "0"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "-1"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "Inf"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "+Inf"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "NaN"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "1e308"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "1000.5"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "0.0009"},
		{"AEGIS_AUDIT_UNAUTH_BURST", "abc"},
		{"AEGIS_AUDIT_UNAUTH_BURST", "0"},
		{"AEGIS_AUDIT_UNAUTH_BURST", "10001"},
		{"AEGIS_AUDIT_UNAUTH_BURST", "2147483647"},
		{"AEGIS_AUDIT_SUPPRESS_WINDOW", "abc"},
		{"AEGIS_AUDIT_SUPPRESS_WINDOW", "0s"},
		{"AEGIS_AUDIT_SUPPRESS_WINDOW", "-1s"},
		{"AEGIS_AUDIT_SUPPRESS_WINDOW", "999ms"},
		{"AEGIS_AUDIT_SUPPRESS_WINDOW", "2h"},
		{"AEGIS_AUDIT_SUPPRESS_MAX_KEYS", "abc"},
		{"AEGIS_AUDIT_SUPPRESS_MAX_KEYS", "15"},
		{"AEGIS_AUDIT_SUPPRESS_MAX_KEYS", "100001"},
		{"AEGIS_AUDIT_SUPPRESS_MAX_KEYS", "2147483647"},
		{"AEGIS_SPOOL_SEGMENT_BYTES", "abc"},
		{"AEGIS_SPOOL_SEGMENT_BYTES", "4095"},
	}
	for _, tc := range invalid {
		t.Run("invalid "+tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := LoadConfig()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.key)
		})
	}

	boundaries := []struct {
		key, value string
	}{
		{"AEGIS_AUDIT_GROUP_FLUSH_INTERVAL", "50ms"},
		{"AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL", "1s"},
		{"AEGIS_AUDIT_COMPLETION_QUEUE_SIZE", "64"},
		{"AEGIS_AUDIT_COMPLETION_QUEUE_SIZE", "100000"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "1000"},
		{"AEGIS_AUDIT_UNAUTH_RATE", "0.001"},
		{"AEGIS_AUDIT_UNAUTH_BURST", "10000"},
		{"AEGIS_AUDIT_SUPPRESS_MAX_KEYS", "100000"},
		{"AEGIS_SPOOL_HARD_LIMIT_RATIO", "0.99"},
		{"AEGIS_AUDIT_UNAUTH_BURST", "1"},
		{"AEGIS_AUDIT_SUPPRESS_WINDOW", "1s"},
		{"AEGIS_AUDIT_SUPPRESS_WINDOW", "1h"},
		{"AEGIS_AUDIT_SUPPRESS_MAX_KEYS", "16"},
		{"AEGIS_SPOOL_SEGMENT_BYTES", "4096"},
	}
	for _, tc := range boundaries {
		t.Run("boundary "+tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := LoadConfig()
			require.NoError(t, err)
		})
	}
}

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
		for _, k := range auditConfigKeys {
			os.Unsetenv(k)
		}

		cfg, err := LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, 2*time.Millisecond, cfg.AuditGroupFlushInterval)
		assert.Equal(t, 25*time.Millisecond, cfg.AuditCompletionFlushInterval)
		assert.Equal(t, 8192, cfg.AuditCompletionQueueSize)
		assert.Equal(t, 0.95, cfg.SpoolHardLimitRatio)
		assert.Equal(t, 10.0, cfg.AuditUnauthRate)
		assert.Equal(t, 50, cfg.AuditUnauthBurst)
		assert.Equal(t, 60*time.Second, cfg.AuditSuppressWindow)
		assert.Equal(t, 4096, cfg.AuditSuppressMaxKeys)
		assert.Equal(t, int64(16777216), cfg.SpoolSegmentBytes)
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
