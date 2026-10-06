package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config represents runtime configuration for the Aegis gateway.
type Config struct {
	Port                    int
	WorkloadPort            int
	MaxHeaderBytes          int
	MaxBodyBytes            int64
	ReadHeaderTimeout       time.Duration
	WriteTimeout            time.Duration
	MaxConcurrentRequests   int
	RoutesFilePath          string
	TLSCertPath             string
	TLSKeyPath              string
	ClientCertPath          string
	ClientKeyPath           string
	WorkloadCACertPath      string
	AssertionPrivateKeyPath string
	AssertionPublicKeyPath  string

	// Control plane and infrastructure configuration
	ControlPlaneGRPCAddr string
	RedisAddr            string
	RedisPassword        string
	SpoolDir             string
	SpoolMaxBytes        int64
}

// LoadConfig initializes Config with safe defaults and environment variable overrides.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:                  8080,
		WorkloadPort:          9443,
		MaxHeaderBytes:        16 * 1024,   // 16 KiB
		MaxBodyBytes:          1024 * 1024, // 1 MiB
		ReadHeaderTimeout:     5 * time.Second,
		WriteTimeout:          15 * time.Second,
		MaxConcurrentRequests: 1000,
		RoutesFilePath:        "policies/data/routes.json",

		ControlPlaneGRPCAddr: "localhost:9090",
		RedisAddr:            "localhost:6379",
		RedisPassword:        "",
		SpoolDir:             "/var/log/aegis/wal",
		SpoolMaxBytes:        1073741824, // 1 GiB
	}

	if portStr := os.Getenv("AEGIS_PORT"); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid AEGIS_PORT %q: %w", portStr, err)
		}
		cfg.Port = port
	}

	if workloadPortStr := os.Getenv("AEGIS_WORKLOAD_PORT"); workloadPortStr != "" {
		port, err := strconv.Atoi(workloadPortStr)
		if err != nil {
			return nil, fmt.Errorf("invalid AEGIS_WORKLOAD_PORT %q: %w", workloadPortStr, err)
		}
		cfg.WorkloadPort = port
	}

	if maxConcurrentStr := os.Getenv("AEGIS_MAX_CONCURRENT"); maxConcurrentStr != "" {
		maxConcurrent, err := strconv.Atoi(maxConcurrentStr)
		if err != nil {
			return nil, fmt.Errorf("invalid AEGIS_MAX_CONCURRENT %q: %w", maxConcurrentStr, err)
		}
		cfg.MaxConcurrentRequests = maxConcurrent
	}

	if routesPath := os.Getenv("AEGIS_ROUTES_PATH"); routesPath != "" {
		cfg.RoutesFilePath = routesPath
	}

	if certPath := os.Getenv("AEGIS_TLS_CERT_PATH"); certPath != "" {
		cfg.TLSCertPath = certPath
	}

	if keyPath := os.Getenv("AEGIS_TLS_KEY_PATH"); keyPath != "" {
		cfg.TLSKeyPath = keyPath
	}

	if clientCertPath := os.Getenv("AEGIS_CLIENT_CERT_PATH"); clientCertPath != "" {
		cfg.ClientCertPath = clientCertPath
	}

	if clientKeyPath := os.Getenv("AEGIS_CLIENT_KEY_PATH"); clientKeyPath != "" {
		cfg.ClientKeyPath = clientKeyPath
	}

	if caPath := os.Getenv("AEGIS_WORKLOAD_CA_PATH"); caPath != "" {
		cfg.WorkloadCACertPath = caPath
	}

	if privKeyPath := os.Getenv("AEGIS_ASSERTION_PRIVATE_KEY_PATH"); privKeyPath != "" {
		cfg.AssertionPrivateKeyPath = privKeyPath
	}

	if pubKeyPath := os.Getenv("AEGIS_ASSERTION_PUBLIC_KEY_PATH"); pubKeyPath != "" {
		cfg.AssertionPublicKeyPath = pubKeyPath
	}

	if cpAddr := os.Getenv("AEGIS_CONTROL_PLANE_GRPC_ADDR"); cpAddr != "" {
		cfg.ControlPlaneGRPCAddr = cpAddr
	}

	if redisAddr := os.Getenv("AEGIS_REDIS_ADDR"); redisAddr != "" {
		cfg.RedisAddr = redisAddr
	}

	if redisPassword := os.Getenv("AEGIS_REDIS_PASSWORD"); redisPassword != "" {
		cfg.RedisPassword = redisPassword
	}

	if spoolDir := os.Getenv("AEGIS_SPOOL_DIR"); spoolDir != "" {
		cfg.SpoolDir = spoolDir
	}

	if spoolMaxBytesStr := os.Getenv("AEGIS_SPOOL_MAX_BYTES"); spoolMaxBytesStr != "" {
		maxBytes, err := strconv.ParseInt(spoolMaxBytesStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid AEGIS_SPOOL_MAX_BYTES %q: %w", spoolMaxBytesStr, err)
		}
		cfg.SpoolMaxBytes = maxBytes
	}

	return cfg, nil
}
