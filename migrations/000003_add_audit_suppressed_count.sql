-- +goose Up
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS suppressed_count INT;

-- +goose Down
ALTER TABLE audit_events DROP COLUMN IF EXISTS suppressed_count;
