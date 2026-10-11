-- +goose Up
CREATE TABLE IF NOT EXISTS principal_quarantines (
    principal_id VARCHAR(128) PRIMARY KEY,
    reason TEXT NOT NULL,
    created_by VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_principal_quarantines_expires ON principal_quarantines (expires_at);
CREATE TABLE IF NOT EXISTS revoked_tokens (
    jti VARCHAR(128) PRIMARY KEY,
    reason TEXT,
    created_by VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_revoked_tokens_expires ON revoked_tokens (expires_at);

-- +goose Down
DROP TABLE IF EXISTS revoked_tokens;
DROP TABLE IF EXISTS principal_quarantines;
