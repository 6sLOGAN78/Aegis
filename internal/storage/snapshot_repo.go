package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GatewayStatus represents the live acknowledgment and convergence state of a gateway replica.
type GatewayStatus struct {
	GatewayID       string     `json:"gateway_id"`
	ActiveVersion   int64      `json:"active_version"`
	Status          string     `json:"status"`
	ErrorMessage    *string    `json:"error_message,omitempty"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	LastHeartbeatAt time.Time  `json:"last_heartbeat_at"`
	ConnectedAt     time.Time  `json:"connected_at"`
}

// SnapshotRepo handles persistence for signed configuration snapshot envelopes and gateway acknowledgments.
type SnapshotRepo struct {
	db DBPool
}

// NewSnapshotRepo constructs a new SnapshotRepo backed by a database pool.
func NewSnapshotRepo(db DBPool) *SnapshotRepo {
	return &SnapshotRepo{db: db}
}

const saveSnapshotQuery = `
INSERT INTO snapshots (
    version, schema_version, payload_sha256, payload_bytes,
    signing_key_id, signature, created_at, expires_at, published_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);`

// SaveSnapshot inserts a new signed configuration snapshot into the snapshots table.
func (r *SnapshotRepo) SaveSnapshot(ctx context.Context, env *snapshotv1.SnapshotEnvelope, publishedBy string) error {
	if env == nil {
		return errors.New("cannot save nil snapshot envelope")
	}

	var createdAt time.Time
	if env.CreatedAt != nil {
		createdAt = env.CreatedAt.AsTime()
	} else {
		createdAt = time.Now()
	}

	var expiresAt time.Time
	if env.ExpiresAt != nil {
		expiresAt = env.ExpiresAt.AsTime()
	} else {
		expiresAt = time.Now().Add(24 * time.Hour)
	}

	if publishedBy == "" {
		publishedBy = "control-plane"
	}

	_, err := r.db.Exec(ctx, saveSnapshotQuery,
		env.Version,
		env.SchemaVersion,
		env.PayloadSha256,
		env.Payload,
		env.SigningKeyId,
		env.Signature,
		createdAt,
		expiresAt,
		publishedBy,
	)
	if err != nil {
		return fmt.Errorf("failed to save snapshot version %d: %w", env.Version, err)
	}
	return nil
}

const getLatestSnapshotQuery = `
SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at
FROM snapshots
ORDER BY version DESC
LIMIT 1;`

// GetLatestSnapshot retrieves the highest version signed snapshot envelope.
func (r *SnapshotRepo) GetLatestSnapshot(ctx context.Context) (*snapshotv1.SnapshotEnvelope, error) {
	var env snapshotv1.SnapshotEnvelope
	var createdAt, expiresAt time.Time

	err := r.db.QueryRow(ctx, getLatestSnapshotQuery).Scan(
		&env.Version,
		&env.SchemaVersion,
		&env.PayloadSha256,
		&env.Payload,
		&env.SigningKeyId,
		&env.Signature,
		&createdAt,
		&expiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get latest snapshot: %w", err)
	}

	env.CreatedAt = timestamppb.New(createdAt)
	env.ExpiresAt = timestamppb.New(expiresAt)
	return &env, nil
}

const getSnapshotByVersionQuery = `
SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at
FROM snapshots
WHERE version = $1;`

// GetSnapshotByVersion retrieves a specific snapshot envelope by its monotonic integer version.
func (r *SnapshotRepo) GetSnapshotByVersion(ctx context.Context, version int64) (*snapshotv1.SnapshotEnvelope, error) {
	var env snapshotv1.SnapshotEnvelope
	var createdAt, expiresAt time.Time

	err := r.db.QueryRow(ctx, getSnapshotByVersionQuery, version).Scan(
		&env.Version,
		&env.SchemaVersion,
		&env.PayloadSha256,
		&env.Payload,
		&env.SigningKeyId,
		&env.Signature,
		&createdAt,
		&expiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get snapshot version %d: %w", version, err)
	}

	env.CreatedAt = timestamppb.New(createdAt)
	env.ExpiresAt = timestamppb.New(expiresAt)
	return &env, nil
}

const recordGatewayAckQuery = `
INSERT INTO gateway_acks (gateway_id, active_version, status, error_message, last_heartbeat_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (gateway_id) DO UPDATE SET
    active_version = EXCLUDED.active_version,
    status = EXCLUDED.status,
    error_message = EXCLUDED.error_message,
    last_heartbeat_at = NOW();`

// RecordGatewayAck records an acknowledgment from a connected gateway replica.
func (r *SnapshotRepo) RecordGatewayAck(ctx context.Context, ack *snapshotv1.SnapshotAck) error {
	if ack == nil {
		return errors.New("cannot record nil gateway ack")
	}

	var errMsg *string
	if ack.ErrorMessage != "" {
		errMsg = &ack.ErrorMessage
	}

	_, err := r.db.Exec(ctx, recordGatewayAckQuery,
		ack.GatewayId,
		ack.ActiveVersion,
		ack.Status.String(),
		errMsg,
	)
	if err != nil {
		return fmt.Errorf("failed to record gateway ack for %q: %w", ack.GatewayId, err)
	}
	return nil
}

const listGatewayAcksQuery = `
SELECT gateway_id, active_version, status, error_message, lease_expires_at, last_heartbeat_at, connected_at
FROM gateway_acks
ORDER BY gateway_id ASC;`

// ListGatewayAcks returns all recorded gateway replica acknowledgments.
func (r *SnapshotRepo) ListGatewayAcks(ctx context.Context) ([]GatewayStatus, error) {
	rows, err := r.db.Query(ctx, listGatewayAcksQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query gateway acks: %w", err)
	}
	defer rows.Close()

	statuses := make([]GatewayStatus, 0)
	for rows.Next() {
		var s GatewayStatus
		if err := rows.Scan(
			&s.GatewayID,
			&s.ActiveVersion,
			&s.Status,
			&s.ErrorMessage,
			&s.LeaseExpiresAt,
			&s.LastHeartbeatAt,
			&s.ConnectedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan gateway ack row: %w", err)
		}
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return statuses, nil
}
