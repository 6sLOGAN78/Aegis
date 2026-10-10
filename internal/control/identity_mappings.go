package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	snapshotv1 "aegis/pkg/api/snapshot/v1"
)

// LoadIdentityMappings reads and strictly validates a role->permissions JSON file.
// Any malformed content is rejected so tampered config is never signed into a snapshot.
func LoadIdentityMappings(path string) ([]*snapshotv1.IdentityMapping, error) {
	if path == "" {
		return nil, errors.New("identity mappings path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read identity mappings: %w", err)
	}
	var raw map[string][]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse identity mappings: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("identity mappings file defines no roles")
	}
	out := make([]*snapshotv1.IdentityMapping, 0, len(raw))
	for role, perms := range raw {
		if role == "" {
			return nil, errors.New("identity mappings contain an empty role")
		}
		if len(perms) == 0 {
			return nil, fmt.Errorf("role %q has no permissions", role)
		}
		for _, p := range perms {
			if p == "" {
				return nil, fmt.Errorf("role %q has an empty permission", role)
			}
		}
		out = append(out, &snapshotv1.IdentityMapping{Role: role, Permissions: append([]string(nil), perms...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out, nil
}

// SetIdentityMappings configures the authoritative identity mappings published in snapshots.
func (s *APIServer) SetIdentityMappings(m []*snapshotv1.IdentityMapping) {
	s.identityMappings = m
}
