package config

import (
	"fmt"
	"math"
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

	// Audit pipeline tuning (phase 08). All have safe defaults and are
	// range-validated; invalid values are startup errors, never silent fallbacks.
	AuditGroupFlushInterval      time.Duration // spool group-commit window
	AuditCompletionFlushInterval time.Duration // completion batch flush window
	AuditCompletionQueueSize     int           // bounded completion queue length
	SpoolHardLimitRatio          float64       // statfs ratio above which appends are refused
	AuditUnauthRate              float64       // unauthenticated denial rows per second
	AuditUnauthBurst             int           // unauthenticated denial burst
	AuditSuppressWindow          time.Duration // repeated-denial suppression window
	AuditSuppressMaxKeys         int           // suppressor key-table cap
	SpoolSegmentBytes            int64         // WAL segment rotation size
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

		AuditGroupFlushInterval:      2 * time.Millisecond,
		AuditCompletionFlushInterval: 25 * time.Millisecond,
		AuditCompletionQueueSize:     8192,
		SpoolHardLimitRatio:          0.95,
		AuditUnauthRate:              10,
		AuditUnauthBurst:             50,
		AuditSuppressWindow:          60 * time.Second,
		AuditSuppressMaxKeys:         4096,
		SpoolSegmentBytes:            16777216, // 16 MiB
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

	if err := loadAuditConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Upper (and lower) bounds for the audit pipeline settings. They keep the defaults
// (8192 queue, 10/s, burst 50, 4096 keys) well inside the range while making sure the
// unauthenticated cap and the suppression table can be neither disabled nor made
// effectively unbounded by configuration (WR-05).
const (
	// maxAuditCompletionQueue bounds the completion queue: 100k frames is roughly
	// 60-100 MB at typical frame sizes.
	maxAuditCompletionQueue = 100000
	// minAuditUnauthRate / maxAuditUnauthRate bound the per-reason refill rate. +Inf or a
	// huge rate would refill the bucket on every call and remove the D-05 cap.
	minAuditUnauthRate = 0.001
	maxAuditUnauthRate = 1000.0
	// maxAuditUnauthBurst bounds the bucket size for the same reason.
	maxAuditUnauthBurst = 10000
	// maxAuditSuppressKeys bounds the suppression table; every entry holds a copy of the
	// first event (up to a few KiB), so 100k keys is a few hundred MB at the very worst.
	maxAuditSuppressKeys = 100000
)

// loadAuditConfig applies the audit pipeline environment overrides. Every key is
// parse-or-error with an explicit allowed range so a misconfiguration cannot
// silently disable a protection (T-08-11).
func loadAuditConfig(cfg *Config) error {
	var err error
	if cfg.AuditGroupFlushInterval, err = envDuration("AEGIS_AUDIT_GROUP_FLUSH_INTERVAL", cfg.AuditGroupFlushInterval, time.Nanosecond, 50*time.Millisecond); err != nil {
		return err
	}
	if cfg.AuditCompletionFlushInterval, err = envDuration("AEGIS_AUDIT_COMPLETION_FLUSH_INTERVAL", cfg.AuditCompletionFlushInterval, time.Nanosecond, time.Second); err != nil {
		return err
	}
	if cfg.AuditCompletionQueueSize, err = envInt("AEGIS_AUDIT_COMPLETION_QUEUE_SIZE", cfg.AuditCompletionQueueSize, 64, maxAuditCompletionQueue); err != nil {
		return err
	}
	if v := os.Getenv("AEGIS_SPOOL_HARD_LIMIT_RATIO"); v != "" {
		r, perr := strconv.ParseFloat(v, 64)
		if perr != nil {
			return fmt.Errorf("invalid AEGIS_SPOOL_HARD_LIMIT_RATIO %q: %w", v, perr)
		}
		if !(r > 0.90 && r <= 0.99) {
			return fmt.Errorf("invalid AEGIS_SPOOL_HARD_LIMIT_RATIO %q: must be > 0.90 and <= 0.99", v)
		}
		cfg.SpoolHardLimitRatio = r
	}
	if v := os.Getenv("AEGIS_AUDIT_UNAUTH_RATE"); v != "" {
		r, perr := strconv.ParseFloat(v, 64)
		if perr != nil {
			return fmt.Errorf("invalid AEGIS_AUDIT_UNAUTH_RATE %q: %w", v, perr)
		}
		if math.IsNaN(r) || math.IsInf(r, 0) || r < minAuditUnauthRate || r > maxAuditUnauthRate {
			return fmt.Errorf("invalid AEGIS_AUDIT_UNAUTH_RATE %q: must be a finite number between %g and %g", v, minAuditUnauthRate, maxAuditUnauthRate)
		}
		cfg.AuditUnauthRate = r
	}
	if cfg.AuditUnauthBurst, err = envInt("AEGIS_AUDIT_UNAUTH_BURST", cfg.AuditUnauthBurst, 1, maxAuditUnauthBurst); err != nil {
		return err
	}
	if cfg.AuditSuppressWindow, err = envDuration("AEGIS_AUDIT_SUPPRESS_WINDOW", cfg.AuditSuppressWindow, time.Second, time.Hour); err != nil {
		return err
	}
	if cfg.AuditSuppressMaxKeys, err = envInt("AEGIS_AUDIT_SUPPRESS_MAX_KEYS", cfg.AuditSuppressMaxKeys, 16, maxAuditSuppressKeys); err != nil {
		return err
	}
	if v := os.Getenv("AEGIS_SPOOL_SEGMENT_BYTES"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return fmt.Errorf("invalid AEGIS_SPOOL_SEGMENT_BYTES %q: %w", v, perr)
		}
		if n < 4096 {
			return fmt.Errorf("invalid AEGIS_SPOOL_SEGMENT_BYTES %q: must be >= 4096", v)
		}
		cfg.SpoolSegmentBytes = n
	}
	return nil
}

func envDuration(key string, def, min, max time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, v, err)
	}
	if d < min || d > max {
		return 0, fmt.Errorf("invalid %s %q: must be between %s and %s", key, v, min, max)
	}
	return d, nil
}

func envInt(key string, def, min, max int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, v, err)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("invalid %s %q: must be between %d and %d", key, v, min, max)
	}
	return n, nil
}
