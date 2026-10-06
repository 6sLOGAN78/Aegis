-- +goose Up
CREATE TABLE IF NOT EXISTS audit_events (
    event_id UUID NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    request_id UUID NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    event_date DATE NOT NULL,
    principal_id VARCHAR(128) NOT NULL,
    principal_kind VARCHAR(32) NOT NULL,
    roles JSONB NOT NULL DEFAULT '[]',
    service_id VARCHAR(64) NOT NULL,
    route_id VARCHAR(64) NOT NULL,
    http_method VARCHAR(16) NOT NULL,
    request_path TEXT NOT NULL,
    decision VARCHAR(16) NOT NULL,
    reason_code VARCHAR(64) NOT NULL,
    snapshot_version BIGINT NOT NULL,
    http_status INT,
    duration_ms DOUBLE PRECISION,
    client_ip VARCHAR(64),
    error_code VARCHAR(64),
    PRIMARY KEY (event_date, event_id)
) PARTITION BY RANGE (event_date);

CREATE TABLE IF NOT EXISTS audit_events_default PARTITION OF audit_events DEFAULT;

CREATE INDEX IF NOT EXISTS idx_audit_events_request_id ON audit_events (request_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_principal_time ON audit_events (principal_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_events_route_time ON audit_events (route_id, timestamp);

-- +goose Down
DROP TABLE IF EXISTS audit_events;
