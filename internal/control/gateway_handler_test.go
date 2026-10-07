package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	controlv1 "aegis/pkg/api/control/v1"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGatewayConvergence(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()

	handler := h.server.Handler()
	cookie, _ := loginOperator(t, handler, "admin", "admin-secret")

	// Set active snapshot version to 5 on control plane
	activeSnapshot := &snapshotv1.SnapshotEnvelope{
		Version: 5,
	}
	h.distServer.BroadcastSnapshot(activeSnapshot)

	now := time.Now().UTC()

	// Setup in-memory replica acknowledgments
	// 1. gw-healthy: version 5, recent ack -> healthy
	_ = h.distServer.AckTracker().RecordAck(context.Background(), &snapshotv1.SnapshotAck{
		GatewayId:      "gw-healthy",
		ActiveVersion:  5,
		Status:         snapshotv1.AckStatus_ACK_STATUS_ACTIVATED,
		AcknowledgedAt: timestamppb.New(now.Add(-2 * time.Second)),
	})

	// 2. gw-degraded: version 3 (< 5), recent ack -> degraded
	_ = h.distServer.AckTracker().RecordAck(context.Background(), &snapshotv1.SnapshotAck{
		GatewayId:      "gw-degraded",
		ActiveVersion:  3,
		Status:         snapshotv1.AckStatus_ACK_STATUS_ACTIVATED,
		AcknowledgedAt: timestamppb.New(now.Add(-3 * time.Second)),
	})

	// 3. gw-partitioned-stale: version 5, heartbeat > 60s ago -> partitioned
	_ = h.distServer.AckTracker().RecordAck(context.Background(), &snapshotv1.SnapshotAck{
		GatewayId:      "gw-partitioned-stale",
		ActiveVersion:  5,
		Status:         snapshotv1.AckStatus_ACK_STATUS_ACTIVATED,
		AcknowledgedAt: timestamppb.New(now.Add(-75 * time.Second)),
	})

	// 4. gw-partitioned-rejected: version 5, status rejected -> partitioned
	_ = h.distServer.AckTracker().RecordAck(context.Background(), &snapshotv1.SnapshotAck{
		GatewayId:      "gw-partitioned-rejected",
		ActiveVersion:  5,
		Status:         snapshotv1.AckStatus_ACK_STATUS_REJECTED,
		AcknowledgedAt: timestamppb.New(now.Add(-5 * time.Second)),
	})

	// Also mock DB query to return empty or 1 row
	ackRows := h.mockDB.NewRows([]string{
		"gateway_id", "active_version", "status", "error_message", "lease_expires_at", "last_heartbeat_at", "connected_at",
	})
	h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT gateway_id, active_version, status, error_message")).
		WillReturnRows(ackRows)

	req := httptest.NewRequest(http.MethodGet, "/control/v1/gateways", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp controlv1.GatewayListResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Len(t, resp.Gateways, 4)

	statusMap := make(map[string]controlv1.GatewayStatusStatus)
	for _, gw := range resp.Gateways {
		statusMap[gw.GatewayId] = gw.Status
	}

	assert.Equal(t, controlv1.Healthy, statusMap["gw-healthy"])
	assert.Equal(t, controlv1.Degraded, statusMap["gw-degraded"])
	assert.Equal(t, controlv1.Partitioned, statusMap["gw-partitioned-stale"])
	assert.Equal(t, controlv1.Partitioned, statusMap["gw-partitioned-rejected"])
}

func TestConvergenceAndQuarantine(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()

	handler := h.server.Handler()
	cookie, csrfToken := loginOperator(t, handler, "admin", "admin-secret")

	t.Run("Quarantine Principal and Verify Redis", func(t *testing.T) {
		body := `{"reason":"Compromised API Key Detected"}`
		req := httptest.NewRequest(http.MethodPost, "/control/v1/principals/bad-actor-42/quarantine", bytes.NewBufferString(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrfToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var record controlv1.QuarantineRecord
		err := json.Unmarshal(rec.Body.Bytes(), &record)
		require.NoError(t, err)
		assert.Equal(t, "bad-actor-42", record.PrincipalId)
		assert.Equal(t, controlv1.Active, record.Status)

		// Verify in Redis
		val, err := h.rdb.Get(context.Background(), "quarantine:principal:bad-actor-42").Result()
		require.NoError(t, err)
		assert.Equal(t, "Compromised API Key Detected", val)
	})

	t.Run("List Quarantined Principals", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/control/v1/principals/quarantine", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var listResp struct {
			Quarantines []controlv1.QuarantineRecord `json:"quarantines"`
		}
		err := json.Unmarshal(rec.Body.Bytes(), &listResp)
		require.NoError(t, err)
		require.NotEmpty(t, listResp.Quarantines)
		assert.Equal(t, "bad-actor-42", listResp.Quarantines[0].PrincipalId)
	})

	t.Run("Remove Quarantine Block", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/control/v1/principals/bad-actor-42/quarantine", nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrfToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var unqResp controlv1.UnquarantineResponse
		err := json.Unmarshal(rec.Body.Bytes(), &unqResp)
		require.NoError(t, err)
		assert.Equal(t, "bad-actor-42", unqResp.PrincipalId)

		// Verify removed from Redis
		exists, err := h.rdb.Exists(context.Background(), "quarantine:principal:bad-actor-42").Result()
		require.NoError(t, err)
		assert.Equal(t, int64(0), exists)
	})

	t.Run("Revoke Token JTI", func(t *testing.T) {
		body := `{"jti":"tok-evil-token-99","reason":"Compromised OAuth Token","ttl_seconds":3600}`
		req := httptest.NewRequest(http.MethodPost, "/control/v1/revocations", bytes.NewBufferString(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrfToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		// Verify in Redis
		val, err := h.rdb.Get(context.Background(), "revocation:jti:tok-evil-token-99").Result()
		require.NoError(t, err)
		assert.Equal(t, "1", val)
	})
}
