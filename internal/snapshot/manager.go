package snapshot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"aegis/internal/policy"
	"aegis/internal/proxy"
	snapshotv1 "aegis/pkg/api/snapshot/v1"
)

// ActiveState encapsulates the immutable snapshot configuration currently active on the gateway hot path.
type ActiveState struct {
	Version          int64
	Router           *proxy.Router
	PolicyEngine     *policy.Engine
	IdentityMappings map[string][]string
	ActivatedAt      time.Time
}

// Manager manages live configuration snapshots on the gateway replica (CTRL-02, Invariant 9).
// It coordinates out-of-band validation, in-memory precompilation, and lock-free atomic swaps.
type Manager struct {
	active              atomic.Pointer[ActiveState]
	lastLeaseRenewedAt  atomic.Int64 // UnixNano
	verifier            *Verifier
}

// NewManager creates a new Manager configured with a snapshot Verifier.
func NewManager(verifier *Verifier) *Manager {
	m := &Manager{
		verifier: verifier,
	}
	m.RecordLeaseRenewal()
	return m
}

// Active returns the currently active configuration state, or nil if uninitialized.
func (m *Manager) Active() *ActiveState {
	return m.active.Load()
}

// RecordLeaseRenewal records the current timestamp as the latest valid lease renewal.
func (m *Manager) RecordLeaseRenewal() {
	m.lastLeaseRenewedAt.Store(time.Now().UnixNano())
}

// SetLastLeaseRenewedAt overrides the lease renewal timestamp (useful for failure testing).
func (m *Manager) SetLastLeaseRenewedAt(t time.Time) {
	m.lastLeaseRenewedAt.Store(t.UnixNano())
}

// IsLeaseExpired checks if the duration since the last valid lease renewal exceeds the timeout.
// When expired, gateway replicas fail closed and drop readiness (Invariant 1, ADR-0004).
func (m *Manager) IsLeaseExpired(timeout time.Duration) bool {
	nano := m.lastLeaseRenewedAt.Load()
	if nano == 0 {
		return true
	}
	lastRenewed := time.Unix(0, nano)
	return time.Since(lastRenewed) > timeout
}

// ValidateAndActivate validates an incoming snapshot envelope out-of-band, precompiles Rego policies,
// constructs the new router, and performs a lock-free atomic swap to activate the new configuration.
func (m *Manager) ValidateAndActivate(ctx context.Context, env *snapshotv1.SnapshotEnvelope) error {
	if env == nil {
		return errors.New("cannot activate nil snapshot envelope")
	}
	if m.verifier == nil {
		return errors.New("manager verifier uninitialized")
	}

	// 1. Determine active version baseline
	current := m.active.Load()
	var currentVer int64 = 0
	if current != nil {
		currentVer = current.Version
	}

	// 2. Validate envelope integrity, signature, and monotonic version
	payload, err := m.verifier.VerifySnapshot(env, currentVer)
	if err != nil {
		return fmt.Errorf("snapshot verification failed: %w", err)
	}

	// 3. Precompile Rego authorization policies out-of-band
	var regoCode string
	if len(payload.PolicyModules) > 0 {
		var sb strings.Builder
		for i, mod := range payload.PolicyModules {
			if i > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(mod.SourceRego)
		}
		regoCode = sb.String()
	} else {
		// Default fallback deny policy if snapshot defines no policy modules
		regoCode = "package aegis.authz\n\ndefault allow := false\ndefault reason_code := \"DENIED_DEFAULT\"\ndecision := {\"allow\": allow, \"reason_code\": reason_code, \"snapshot_version\": input.snapshot_version}\n"
	}

	// Ensure structured decision rule is present if omitted from input policy module
	if !strings.Contains(regoCode, "decision :=") && !strings.Contains(regoCode, "decision =") {
		regoCode += "\n\ndefault reason_code := \"DENIED_DEFAULT\"\ndecision := {\"allow\": allow, \"reason_code\": reason_code, \"snapshot_version\": input.snapshot_version}\n"
	}

	engine, err := policy.NewEngine(ctx, regoCode)
	if err != nil {
		return fmt.Errorf("failed to precompile snapshot policy: %w", err)
	}

	// 4. Construct new route catalog
	router, err := proxy.NewRouterFromProtobuf(payload.Routes)
	if err != nil {
		return fmt.Errorf("failed to build router from snapshot: %w", err)
	}

	// 5. Extract role-to-permission identity mappings
	mappings := make(map[string][]string, len(payload.IdentityMappings))
	for _, mapping := range payload.IdentityMappings {
		mappings[mapping.Role] = append([]string(nil), mapping.Permissions...)
	}

	// 6. Lock-free atomic swap (Invariant 9)
	nextState := &ActiveState{
		Version:          env.Version,
		Router:           router,
		PolicyEngine:     engine,
		IdentityMappings: mappings,
		ActivatedAt:      time.Now(),
	}

	m.active.Store(nextState)
	m.RecordLeaseRenewal()

	return nil
}
