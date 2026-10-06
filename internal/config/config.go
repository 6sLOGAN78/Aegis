package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config represents runtime configuration for the Aegis gateway.
type Config struct {
	Port                  int
	MaxHeaderBytes        int
	MaxBodyBytes          int64
	ReadHeaderTimeout     time.Duration
	WriteTimeout          time.Duration
	MaxConcurrentRequests int
	RoutesFilePath        string
}

// LoadConfig initializes Config with safe defaults and environment variable overrides.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:                  8080,
		MaxHeaderBytes:        16 * 1024,      // 16 KiB
		MaxBodyBytes:          1024 * 1024,    // 1 MiB
		ReadHeaderTimeout:     5 * time.Second,
		WriteTimeout:          15 * time.Second,
		MaxConcurrentRequests: 1000,
		RoutesFilePath:        "policies/data/routes.json",
	}

	if portStr := os.Getenv("AEGIS_PORT"); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid AEGIS_PORT %q: %w", portStr, err)
		}
		cfg.Port = port
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

	return cfg, nil
}
