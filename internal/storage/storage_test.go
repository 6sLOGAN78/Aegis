package storage

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStorage_RouteRepo_UpsertAndGet(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewRouteRepo(mock)
	ctx := context.Background()

	// 1. Test UpsertService
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO services")).
		WithArgs("orders", "production", true, json.RawMessage(`{"owner":"orders-team"}`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.UpsertService(ctx, ServiceRecord{
		ID:          "orders",
		Environment: "production",
		Enabled:     true,
		Metadata:    json.RawMessage(`{"owner":"orders-team"}`),
	})
	require.NoError(t, err)

	// 2. Test GetService
	now := time.Now().Truncate(time.Second)
	serviceRows := mock.NewRows([]string{"id", "environment", "enabled", "metadata", "created_at", "updated_at"}).
		AddRow("orders", "production", true, json.RawMessage(`{"owner":"orders-team"}`), now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, environment, enabled, metadata, created_at, updated_at FROM services WHERE id = $1;")).
		WithArgs("orders").
		WillReturnRows(serviceRows)

	svc, err := repo.GetService(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, "orders", svc.ID)
	assert.Equal(t, "production", svc.Environment)
	assert.True(t, svc.Enabled)

	// 3. Test UpsertRoute
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO routes")).
		WithArgs("orders.list", "orders", "GET", "/api/orders", "https://orders:8081",
			"spiffe://aegis.local/service/orders", 100, 200, 5000, true).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.UpsertRoute(ctx, RouteRecord{
		RouteID:              "orders.list",
		ServiceID:            "orders",
		HTTPMethod:           "GET",
		PathTemplate:         "/api/orders",
		UpstreamURL:          "https://orders:8081",
		UpstreamSPIFFEID:     "spiffe://aegis.local/service/orders",
		RateLimitRPS:         100,
		RateLimitBurst:       200,
		TimeoutMS:            5000,
		RequiresWorkloadMTLS: true,
	})
	require.NoError(t, err)

	// 4. Test GetRoute
	routeRows := mock.NewRows([]string{
		"route_id", "service_id", "http_method", "path_template", "upstream_url", "upstream_spiffe_id",
		"rate_limit_rps", "rate_limit_burst", "timeout_ms", "requires_workload_mtls", "created_at", "updated_at",
	}).AddRow("orders.list", "orders", "GET", "/api/orders", "https://orders:8081",
		"spiffe://aegis.local/service/orders", 100, 200, 5000, true, now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT route_id, service_id, http_method, path_template, upstream_url, upstream_spiffe_id")).
		WithArgs("orders.list").
		WillReturnRows(routeRows)

	route, err := repo.GetRoute(ctx, "orders.list")
	require.NoError(t, err)
	assert.Equal(t, "orders.list", route.RouteID)
	assert.Equal(t, "orders", route.ServiceID)
	assert.Equal(t, "GET", route.HTTPMethod)
	assert.Equal(t, "/api/orders", route.PathTemplate)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStorage_RouteRepo_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewRouteRepo(mock)
	ctx := context.Background()

	// Service Not Found
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, environment, enabled, metadata, created_at, updated_at FROM services WHERE id = $1;")).
		WithArgs("non-existent").
		WillReturnError(pgx.ErrNoRows)

	svc, err := repo.GetService(ctx, "non-existent")
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, ErrNotFound)

	// Route Not Found
	mock.ExpectQuery(regexp.QuoteMeta("SELECT route_id, service_id, http_method, path_template, upstream_url, upstream_spiffe_id")).
		WithArgs("non.existent.route").
		WillReturnError(pgx.ErrNoRows)

	route, err := repo.GetRoute(ctx, "non.existent.route")
	assert.Nil(t, route)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStorage_RouteRepo_ListAndDelete(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewRouteRepo(mock)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// ListRoutes
	routeRows := mock.NewRows([]string{
		"route_id", "service_id", "http_method", "path_template", "upstream_url", "upstream_spiffe_id",
		"rate_limit_rps", "rate_limit_burst", "timeout_ms", "requires_workload_mtls", "created_at", "updated_at",
	}).AddRow("admin.view", "admin", "GET", "/api/admin", "https://admin:8083",
		"spiffe://aegis.local/service/admin", 50, 100, 3000, true, now, now).
		AddRow("orders.list", "orders", "GET", "/api/orders", "https://orders:8081",
			"spiffe://aegis.local/service/orders", 100, 200, 5000, true, now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT route_id, service_id, http_method, path_template, upstream_url, upstream_spiffe_id")).
		WillReturnRows(routeRows)

	routes, err := repo.ListRoutes(ctx)
	require.NoError(t, err)
	assert.Len(t, routes, 2)
	assert.Equal(t, "admin.view", routes[0].RouteID)
	assert.Equal(t, "orders.list", routes[1].RouteID)

	// DeleteRoute Success
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM routes WHERE route_id = $1;")).
		WithArgs("orders.list").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	err = repo.DeleteRoute(ctx, "orders.list")
	require.NoError(t, err)

	// DeleteRoute Not Found
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM routes WHERE route_id = $1;")).
		WithArgs("orders.list").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	err = repo.DeleteRoute(ctx, "orders.list")
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStorage_SnapshotRepo_SaveAndGet(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewSnapshotRepo(mock)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(24 * time.Hour)

	env := &snapshotv1.SnapshotEnvelope{
		Version:       42,
		SchemaVersion: 1,
		PayloadSha256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Payload:       []byte("test-payload-bytes"),
		SigningKeyId:  "key-v1",
		Signature:     []byte("test-signature-bytes"),
		CreatedAt:     timestamppb.New(now),
		ExpiresAt:     timestamppb.New(expires),
	}

	// 1. SaveSnapshot
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO snapshots")).
		WithArgs(
			int64(42),
			int32(1),
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			[]byte("test-payload-bytes"),
			"key-v1",
			[]byte("test-signature-bytes"),
			now,
			expires,
			"admin-user",
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.SaveSnapshot(ctx, env, "admin-user")
	require.NoError(t, err)

	// 2. GetLatestSnapshot
	snapRows := mock.NewRows([]string{
		"version", "schema_version", "payload_sha256", "payload_bytes",
		"signing_key_id", "signature", "created_at", "expires_at",
	}).AddRow(int64(42), int32(1), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		[]byte("test-payload-bytes"), "key-v1", []byte("test-signature-bytes"), now, expires)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at FROM snapshots ORDER BY version DESC LIMIT 1;")).
		WillReturnRows(snapRows)

	latest, err := repo.GetLatestSnapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(42), latest.Version)
	assert.Equal(t, int32(1), latest.SchemaVersion)
	assert.Equal(t, "key-v1", latest.SigningKeyId)
	assert.Equal(t, []byte("test-payload-bytes"), latest.Payload)

	// 3. GetSnapshotByVersion
	snapVersionRows := mock.NewRows([]string{
		"version", "schema_version", "payload_sha256", "payload_bytes",
		"signing_key_id", "signature", "created_at", "expires_at",
	}).AddRow(int64(42), int32(1), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		[]byte("test-payload-bytes"), "key-v1", []byte("test-signature-bytes"), now, expires)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at FROM snapshots WHERE version = $1;")).
		WithArgs(int64(42)).
		WillReturnRows(snapVersionRows)

	byVer, err := repo.GetSnapshotByVersion(ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), byVer.Version)

	// 4. GetSnapshotByVersion Not Found
	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at FROM snapshots WHERE version = $1;")).
		WithArgs(int64(999)).
		WillReturnError(pgx.ErrNoRows)

	missing, err := repo.GetSnapshotByVersion(ctx, 999)
	assert.Nil(t, missing)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStorage_SnapshotRepo_GatewayAck(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewSnapshotRepo(mock)
	ctx := context.Background()

	// 1. RecordGatewayAck
	ack := &snapshotv1.SnapshotAck{
		GatewayId:     "gateway-node-1",
		ActiveVersion: 42,
		Status:        snapshotv1.AckStatus_ACK_STATUS_ACTIVATED,
		ErrorMessage:  "",
	}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gateway_acks")).
		WithArgs("gateway-node-1", int64(42), "ACK_STATUS_ACTIVATED", (*string)(nil)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.RecordGatewayAck(ctx, ack)
	require.NoError(t, err)

	// 2. ListGatewayAcks
	now := time.Now().Truncate(time.Second)
	ackRows := mock.NewRows([]string{
		"gateway_id", "active_version", "status", "error_message", "lease_expires_at", "last_heartbeat_at", "connected_at",
	}).AddRow("gateway-node-1", int64(42), "ACK_STATUS_ACTIVATED", nil, nil, now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT gateway_id, active_version, status, error_message, lease_expires_at, last_heartbeat_at, connected_at FROM gateway_acks")).
		WillReturnRows(ackRows)

	statuses, err := repo.ListGatewayAcks(ctx)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "gateway-node-1", statuses[0].GatewayID)
	assert.Equal(t, int64(42), statuses[0].ActiveVersion)
	assert.Equal(t, "ACK_STATUS_ACTIVATED", statuses[0].Status)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStorage_HostileSQLInjectionPayloads(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewRouteRepo(mock)
	ctx := context.Background()

	hostileID := "'; DROP TABLE routes; --"
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, environment, enabled, metadata, created_at, updated_at FROM services WHERE id = $1;")).
		WithArgs(hostileID).
		WillReturnError(pgx.ErrNoRows)

	svc, err := repo.GetService(ctx, hostileID)
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, mock.ExpectationsWereMet())
}
