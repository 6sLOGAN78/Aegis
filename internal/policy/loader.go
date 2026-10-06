package policy

import (
	"errors"
	"fmt"
	"os"
)

// Snapshot represents an immutable policy and route bundle at a specific version.
type Snapshot struct {
	Version    int64
	RegoPolicy string
	Routes     []byte
}

// LoadStaticSnapshot reads policy source and route definitions from disk and constructs a Snapshot.
func LoadStaticSnapshot(policyPath, routesPath string, version int64) (*Snapshot, error) {
	if policyPath == "" {
		return nil, errors.New("policy path cannot be empty")
	}
	if routesPath == "" {
		return nil, errors.New("routes path cannot be empty")
	}

	policyBytes, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file at %q: %w", policyPath, err)
	}

	routesBytes, err := os.ReadFile(routesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read routes file at %q: %w", routesPath, err)
	}

	return &Snapshot{
		Version:    version,
		RegoPolicy: string(policyBytes),
		Routes:     routesBytes,
	}, nil
}
