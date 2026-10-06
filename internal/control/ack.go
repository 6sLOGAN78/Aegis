package control

import (
	"context"
	"errors"
	"sync"

	"aegis/internal/storage"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"google.golang.org/protobuf/proto"
)

// AckTracker records and tracks gateway replica acknowledgments and convergence status (CTRL-05).
type AckTracker struct {
	mu       sync.RWMutex
	statuses map[string]*snapshotv1.SnapshotAck
	repo     *storage.SnapshotRepo
}

// NewAckTracker instantiates an AckTracker backed by an optional SnapshotRepo for persistence.
func NewAckTracker(repo *storage.SnapshotRepo) *AckTracker {
	return &AckTracker{
		statuses: make(map[string]*snapshotv1.SnapshotAck),
		repo:     repo,
	}
}

// RecordAck updates the in-memory acknowledgment state for the gateway and persists
// it to PostgreSQL if a repository is configured.
func (a *AckTracker) RecordAck(ctx context.Context, ack *snapshotv1.SnapshotAck) error {
	if ack == nil {
		return errors.New("cannot record nil snapshot ack")
	}
	if ack.GatewayId == "" {
		return errors.New("cannot record ack with empty gateway id")
	}

	cloned := proto.Clone(ack).(*snapshotv1.SnapshotAck)

	a.mu.Lock()
	a.statuses[ack.GatewayId] = cloned
	a.mu.Unlock()

	if a.repo != nil {
		return a.repo.RecordGatewayAck(ctx, ack)
	}

	return nil
}

// GetReplicaStatuses returns a point-in-time copy of all known gateway replica statuses.
func (a *AckTracker) GetReplicaStatuses() map[string]*snapshotv1.SnapshotAck {
	a.mu.RLock()
	defer a.mu.RUnlock()

	copyMap := make(map[string]*snapshotv1.SnapshotAck, len(a.statuses))
	for k, v := range a.statuses {
		copyMap[k] = proto.Clone(v).(*snapshotv1.SnapshotAck)
	}
	return copyMap
}
