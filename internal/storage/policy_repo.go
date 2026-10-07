package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PolicyDraftRecord represents a draft Rego policy stored in the database.
type PolicyDraftRecord struct {
	DraftID     string    `json:"draft_id"`
	PackageName string    `json:"package_name"`
	ModuleName  string    `json:"module_name"`
	SourceRego  string    `json:"source_rego"`
	Status      string    `json:"status"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PolicyRepo handles persistence for policy drafts.
type PolicyRepo struct {
	db DBPool
}

// NewPolicyRepo constructs a PolicyRepo backed by a database pool.
func NewPolicyRepo(db DBPool) *PolicyRepo {
	return &PolicyRepo{db: db}
}

const createDraftQuery = `
INSERT INTO policy_drafts (
    draft_id, package_name, module_name, source_rego, status, created_by, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
ON CONFLICT (draft_id) DO UPDATE SET
    package_name = EXCLUDED.package_name,
    module_name = EXCLUDED.module_name,
    source_rego = EXCLUDED.source_rego,
    status = EXCLUDED.status,
    updated_at = NOW();`

// CreateDraft inserts or updates a policy draft record.
func (r *PolicyRepo) CreateDraft(ctx context.Context, draft *PolicyDraftRecord) error {
	if draft == nil {
		return errors.New("cannot create nil policy draft")
	}
	if draft.Status == "" {
		draft.Status = "draft"
	}
	if draft.PackageName == "" {
		draft.PackageName = "aegis.authz"
	}
	if draft.ModuleName == "" {
		draft.ModuleName = "policy.rego"
	}
	if draft.CreatedBy == "" {
		draft.CreatedBy = "system"
	}

	_, err := r.db.Exec(ctx, createDraftQuery,
		draft.DraftID,
		draft.PackageName,
		draft.ModuleName,
		draft.SourceRego,
		draft.Status,
		draft.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("failed to save policy draft %q: %w", draft.DraftID, err)
	}
	return nil
}

const getDraftQuery = `
SELECT draft_id, package_name, module_name, source_rego, status, created_by, created_at, updated_at
FROM policy_drafts
WHERE draft_id = $1;`

// GetDraft retrieves a draft by its ID, returning ErrNotFound if absent.
func (r *PolicyRepo) GetDraft(ctx context.Context, draftID string) (*PolicyDraftRecord, error) {
	var draft PolicyDraftRecord
	err := r.db.QueryRow(ctx, getDraftQuery, draftID).Scan(
		&draft.DraftID,
		&draft.PackageName,
		&draft.ModuleName,
		&draft.SourceRego,
		&draft.Status,
		&draft.CreatedBy,
		&draft.CreatedAt,
		&draft.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get policy draft %q: %w", draftID, err)
	}
	return &draft, nil
}

const listDraftsQuery = `
SELECT draft_id, package_name, module_name, source_rego, status, created_by, created_at, updated_at
FROM policy_drafts
ORDER BY updated_at DESC;`

// ListDrafts returns all policy drafts ordered by updated_at descending.
func (r *PolicyRepo) ListDrafts(ctx context.Context) ([]PolicyDraftRecord, error) {
	rows, err := r.db.Query(ctx, listDraftsQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query policy drafts: %w", err)
	}
	defer rows.Close()

	drafts := make([]PolicyDraftRecord, 0)
	for rows.Next() {
		var draft PolicyDraftRecord
		if err := rows.Scan(
			&draft.DraftID,
			&draft.PackageName,
			&draft.ModuleName,
			&draft.SourceRego,
			&draft.Status,
			&draft.CreatedBy,
			&draft.CreatedAt,
			&draft.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan policy draft row: %w", err)
		}
		drafts = append(drafts, draft)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return drafts, nil
}

const updateDraftQuery = `
UPDATE policy_drafts
SET source_rego = $2, module_name = $3, package_name = $4, status = $5, updated_at = NOW()
WHERE draft_id = $1;`

// UpdateDraft updates the source code and metadata of an existing draft.
func (r *PolicyRepo) UpdateDraft(ctx context.Context, draft *PolicyDraftRecord) error {
	if draft == nil {
		return errors.New("cannot update nil policy draft")
	}

	cmdTag, err := r.db.Exec(ctx, updateDraftQuery,
		draft.DraftID,
		draft.SourceRego,
		draft.ModuleName,
		draft.PackageName,
		draft.Status,
	)
	if err != nil {
		return fmt.Errorf("failed to update policy draft %q: %w", draft.DraftID, err)
	}
	if cmdTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
