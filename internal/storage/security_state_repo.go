package storage

import (
	"context"
	"fmt"
	"time"
)

// QuarantineRecord is a durable principal quarantine (source of truth for Redis restore).
type QuarantineRecord struct {
	PrincipalID string    `json:"principal_id"`
	Reason      string    `json:"reason"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// RevokedTokenRecord is a durable JTI revocation.
type RevokedTokenRecord struct {
	JTI       string    `json:"jti"`
	Reason    string    `json:"reason"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SecurityStateRepo persists quarantines and revoked tokens in Postgres.
type SecurityStateRepo struct {
	db DBPool
}

// NewSecurityStateRepo constructs a SecurityStateRepo.
func NewSecurityStateRepo(db DBPool) *SecurityStateRepo {
	return &SecurityStateRepo{db: db}
}

const upsertQuarantineQuery = `
INSERT INTO principal_quarantines (principal_id, reason, created_by, created_at, expires_at)
VALUES ($1, $2, $3, NOW(), $4)
ON CONFLICT (principal_id) DO UPDATE SET
    reason = EXCLUDED.reason,
    created_by = EXCLUDED.created_by,
    expires_at = EXCLUDED.expires_at;`

// UpsertQuarantine idempotently records a quarantine.
func (r *SecurityStateRepo) UpsertQuarantine(ctx context.Context, q QuarantineRecord) error {
	if _, err := r.db.Exec(ctx, upsertQuarantineQuery, q.PrincipalID, q.Reason, q.CreatedBy, q.ExpiresAt); err != nil {
		return fmt.Errorf("failed to upsert quarantine for principal %q: %w", q.PrincipalID, err)
	}
	return nil
}

const deleteQuarantineQuery = `DELETE FROM principal_quarantines WHERE principal_id = $1;`

// DeleteQuarantine removes a quarantine; a missing row is not an error.
func (r *SecurityStateRepo) DeleteQuarantine(ctx context.Context, principalID string) error {
	if _, err := r.db.Exec(ctx, deleteQuarantineQuery, principalID); err != nil {
		return fmt.Errorf("failed to delete quarantine for principal %q: %w", principalID, err)
	}
	return nil
}

const listQuarantinesQuery = `
SELECT principal_id, reason, created_by, created_at, expires_at
FROM principal_quarantines
WHERE expires_at > NOW()
ORDER BY principal_id;`

// ListUnexpiredQuarantines returns all quarantines that have not expired.
func (r *SecurityStateRepo) ListUnexpiredQuarantines(ctx context.Context) ([]QuarantineRecord, error) {
	rows, err := r.db.Query(ctx, listQuarantinesQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to list quarantines: %w", err)
	}
	defer rows.Close()

	out := []QuarantineRecord{}
	for rows.Next() {
		var q QuarantineRecord
		if err := rows.Scan(&q.PrincipalID, &q.Reason, &q.CreatedBy, &q.CreatedAt, &q.ExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan quarantine row: %w", err)
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate quarantine rows: %w", err)
	}
	return out, nil
}

const upsertRevokedTokenQuery = `
INSERT INTO revoked_tokens (jti, reason, created_by, created_at, expires_at)
VALUES ($1, $2, $3, NOW(), $4)
ON CONFLICT (jti) DO UPDATE SET
    reason = EXCLUDED.reason,
    created_by = EXCLUDED.created_by,
    expires_at = EXCLUDED.expires_at;`

// UpsertRevokedToken idempotently records a revoked JTI.
func (r *SecurityStateRepo) UpsertRevokedToken(ctx context.Context, t RevokedTokenRecord) error {
	if _, err := r.db.Exec(ctx, upsertRevokedTokenQuery, t.JTI, t.Reason, t.CreatedBy, t.ExpiresAt); err != nil {
		return fmt.Errorf("failed to upsert revoked token %q: %w", t.JTI, err)
	}
	return nil
}

const listRevokedTokensQuery = `
SELECT jti, COALESCE(reason, ''), created_by, created_at, expires_at
FROM revoked_tokens
WHERE expires_at > NOW()
ORDER BY jti;`

// ListUnexpiredRevokedTokens returns all revoked tokens that have not expired.
func (r *SecurityStateRepo) ListUnexpiredRevokedTokens(ctx context.Context) ([]RevokedTokenRecord, error) {
	rows, err := r.db.Query(ctx, listRevokedTokensQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to list revoked tokens: %w", err)
	}
	defer rows.Close()

	out := []RevokedTokenRecord{}
	for rows.Next() {
		var t RevokedTokenRecord
		if err := rows.Scan(&t.JTI, &t.Reason, &t.CreatedBy, &t.CreatedAt, &t.ExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan revoked token row: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate revoked token rows: %w", err)
	}
	return out, nil
}

const deleteExpiredQuarantinesQuery = `DELETE FROM principal_quarantines WHERE expires_at <= NOW();`
const deleteExpiredTokensQuery = `DELETE FROM revoked_tokens WHERE expires_at <= NOW();`

// DeleteExpired removes expired rows from both tables and returns the total affected.
func (r *SecurityStateRepo) DeleteExpired(ctx context.Context) (int64, error) {
	tag1, err := r.db.Exec(ctx, deleteExpiredQuarantinesQuery)
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired quarantines: %w", err)
	}
	tag2, err := r.db.Exec(ctx, deleteExpiredTokensQuery)
	if err != nil {
		return tag1.RowsAffected(), fmt.Errorf("failed to delete expired revoked tokens: %w", err)
	}
	return tag1.RowsAffected() + tag2.RowsAffected(), nil
}
