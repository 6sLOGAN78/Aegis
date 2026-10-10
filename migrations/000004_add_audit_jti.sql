-- +goose Up
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS jti VARCHAR(128);
CREATE INDEX IF NOT EXISTS idx_audit_events_jti ON audit_events (jti) WHERE jti IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_audit_events_jti;
ALTER TABLE audit_events DROP COLUMN IF EXISTS jti;
