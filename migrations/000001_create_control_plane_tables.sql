-- +goose Up
CREATE TABLE IF NOT EXISTS services (
    id VARCHAR(64) PRIMARY KEY,
    environment VARCHAR(32) NOT NULL DEFAULT 'production',
    enabled BOOLEAN NOT NULL DEFAULT true,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS routes (
    route_id VARCHAR(64) PRIMARY KEY,
    service_id VARCHAR(64) NOT NULL REFERENCES services(id),
    http_method VARCHAR(16) NOT NULL,
    path_template VARCHAR(256) NOT NULL,
    upstream_url TEXT NOT NULL,
    upstream_spiffe_id TEXT NOT NULL,
    rate_limit_rps INT NOT NULL DEFAULT 100,
    rate_limit_burst INT NOT NULL DEFAULT 200,
    timeout_ms INT NOT NULL DEFAULT 5000,
    requires_workload_mtls BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS policy_drafts (
    draft_id VARCHAR(64) PRIMARY KEY,
    package_name VARCHAR(128) NOT NULL,
    module_name VARCHAR(128) NOT NULL,
    source_rego TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    created_by VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS snapshots (
    version BIGINT PRIMARY KEY,
    schema_version INT NOT NULL DEFAULT 1,
    payload_sha256 VARCHAR(64) NOT NULL,
    payload_bytes BYTEA NOT NULL,
    signing_key_id VARCHAR(64) NOT NULL,
    signature BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    published_by VARCHAR(64) NOT NULL
);

CREATE TABLE IF NOT EXISTS gateway_acks (
    gateway_id VARCHAR(64) PRIMARY KEY,
    active_version BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL,
    error_message TEXT,
    lease_expires_at TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    connected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS gateway_acks;
DROP TABLE IF EXISTS snapshots;
DROP TABLE IF EXISTS policy_drafts;
DROP TABLE IF EXISTS routes;
DROP TABLE IF EXISTS services;
