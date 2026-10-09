package storage

import (
	"io/fs"
	"strings"
	"testing"
	"time"

	"aegis/migrations"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPoolConfig_Defaults(t *testing.T) {
	cfg := DefaultPoolConfig()
	poolCfg, err := BuildPoolConfig(cfg)
	require.NoError(t, err)
	require.NotNil(t, poolCfg)

	assert.Equal(t, int32(25), poolCfg.MaxConns)
	assert.Equal(t, int32(5), poolCfg.MinConns)
	assert.Equal(t, 1*time.Hour, poolCfg.MaxConnLifetime)
	assert.Equal(t, 30*time.Minute, poolCfg.MaxConnIdleTime)
}

func TestBuildPoolConfig_Custom(t *testing.T) {
	cfg := PoolConfig{
		Host:            "db.example.internal",
		Port:            5433,
		User:            "aegis_user",
		Password:        "super_secret",
		Database:        "aegis_prod",
		SSLMode:         "require",
		MaxConns:        50,
		MinConns:        10,
		MaxConnLifetime: 2 * time.Hour,
		MaxConnIdleTime: 15 * time.Minute,
	}

	poolCfg, err := BuildPoolConfig(cfg)
	require.NoError(t, err)
	require.NotNil(t, poolCfg)

	assert.Equal(t, int32(50), poolCfg.MaxConns)
	assert.Equal(t, int32(10), poolCfg.MinConns)
	assert.Equal(t, 2*time.Hour, poolCfg.MaxConnLifetime)
	assert.Equal(t, 15*time.Minute, poolCfg.MaxConnIdleTime)
	assert.Equal(t, "db.example.internal", poolCfg.ConnConfig.Host)
	assert.Equal(t, uint16(5433), poolCfg.ConnConfig.Port)
	assert.Equal(t, "aegis_user", poolCfg.ConnConfig.User)
	assert.Equal(t, "aegis_prod", poolCfg.ConnConfig.Database)
}

func TestEmbeddedMigrations_Integrity(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	var fileNames []string
	for _, entry := range entries {
		fileNames = append(fileNames, entry.Name())
	}

	assert.Contains(t, fileNames, "000001_create_control_plane_tables.sql")
	assert.Contains(t, fileNames, "000002_create_partitioned_audit_tables.sql")
	assert.Contains(t, fileNames, "000003_add_audit_suppressed_count.sql")

	for _, fname := range fileNames {
		content, err := fs.ReadFile(migrations.FS, fname)
		require.NoError(t, err, "failed to read embedded migration: %s", fname)
		str := string(content)
		assert.True(t, strings.Contains(str, "-- +goose Up"), "migration %s missing '-- +goose Up'", fname)
		assert.True(t, strings.Contains(str, "-- +goose Down"), "migration %s missing '-- +goose Down'", fname)
	}
}
