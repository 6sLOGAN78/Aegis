package control

import (
	"context"
	"errors"
	"fmt"

	"aegis/internal/snapshot"
	"aegis/internal/storage"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"google.golang.org/protobuf/proto"
)

// RollbackEngine manages monotonic rollbacks of configuration snapshots.
// In accordance with CTRL-06 and Invariant 9, rollbacks never decrement the version number;
// instead, historical configuration is republished under a strictly higher monotonic version (N+1).
type RollbackEngine struct {
	repo   *storage.SnapshotRepo
	signer *snapshot.Signer
}

// NewRollbackEngine instantiates a new RollbackEngine.
func NewRollbackEngine(repo *storage.SnapshotRepo, signer *snapshot.Signer) *RollbackEngine {
	return &RollbackEngine{
		repo:   repo,
		signer: signer,
	}
}

// RollbackToVersion recovers historical snapshot configuration at targetVersion and republishes it
// under a strictly monotonic version N+1 where N is the current latest snapshot version.
func (r *RollbackEngine) RollbackToVersion(ctx context.Context, targetVersion int64, publishedBy string) (*snapshotv1.SnapshotEnvelope, error) {
	if r.repo == nil || r.signer == nil {
		return nil, errors.New("rollback engine uninitialized: repo and signer are required")
	}

	// 1. Fetch historical snapshot content
	targetEnv, err := r.repo.GetSnapshotByVersion(ctx, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve historical snapshot version %d: %w", targetVersion, err)
	}
	if targetEnv == nil {
		return nil, fmt.Errorf("target snapshot version %d not found", targetVersion)
	}

	// 2. Fetch current latest version N
	latestEnv, err := r.repo.GetLatestSnapshot(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("failed to fetch latest active snapshot: %w", err)
	}

	var latestVersion int64
	if latestEnv != nil {
		latestVersion = latestEnv.Version
	} else {
		latestVersion = targetVersion
	}

	// Calculate strictly higher monotonic version N+1
	newVersion := latestVersion + 1
	if newVersion <= targetVersion {
		newVersion = targetVersion + 1
	}

	// 3. Unmarshal historical payload
	var payload snapshotv1.SnapshotPayload
	if err := proto.Unmarshal(targetEnv.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal historical snapshot payload: %w", err)
	}

	// 4. Assign strictly higher monotonic version N+1 (CTRL-06)
	payload.Version = newVersion

	// 5. Sign the new snapshot envelope
	newEnv, err := r.signer.SignSnapshot(&payload)
	if err != nil {
		return nil, fmt.Errorf("failed to sign rolled-back snapshot version %d: %w", newVersion, err)
	}

	if publishedBy == "" {
		publishedBy = "control-plane-rollback"
	}

	// 6. Persist to repository
	if err := r.repo.SaveSnapshot(ctx, newEnv, publishedBy); err != nil {
		return nil, fmt.Errorf("failed to persist rolled-back snapshot version %d: %w", newVersion, err)
	}

	return newEnv, nil
}
