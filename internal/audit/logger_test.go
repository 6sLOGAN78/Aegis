package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletionAuditLogging(t *testing.T) {
	testStatuses := []struct {
		statusCode int
		decision   string
		reason     string
		errCode    string
	}{
		{http.StatusOK, "allow", "ALLOWED", ""},
		{http.StatusBadRequest, "deny", "BAD_REQUEST_INVALID_PATH", "INVALID_PATH"},
		{http.StatusUnauthorized, "deny", "UNAUTHORIZED", "UNAUTHORIZED"},
		{http.StatusForbidden, "deny", "DENIED_DEVELOPER_ADMIN_FORBIDDEN", "FORBIDDEN"},
		{http.StatusTooManyRequests, "deny", "RATE_LIMITED", "TOO_MANY_REQUESTS"},
		{http.StatusBadGateway, "deny", "UPSTREAM_UNREACHABLE", "BAD_GATEWAY"},
	}

	t.Run("Direct LogCompletion emission across 200 400 401 403 429 502 responses", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLogger(&buf)

		for _, tc := range testStatuses {
			event := CompletionEvent{
				EventID:         uuid.NewString(),
				Timestamp:       time.Now().UTC(),
				RequestID:       uuid.NewString(),
				PrincipalID:     "usr_dev_01",
				PrincipalKind:   "user",
				PrincipalRoles:  []string{"developer"},
				ClientIP:        "192.168.1.100",
				HTTPMethod:      "GET",
				CanonicalPath:   "/api/orders",
				RouteID:         "route_orders_read",
				ServiceID:       "orders",
				Decision:        tc.decision,
				ReasonCode:      tc.reason,
				HTTPStatus:      tc.statusCode,
				DurationMS:      1.45,
				SnapshotVersion: 1,
				ErrorCode:       tc.errCode,
			}

			buf.Reset()
			logger.LogCompletion(event)

			line := buf.String()
			require.NotEmpty(t, line)

			// Parse as generic map to check JSON field existence and types
			var parsedMap map[string]interface{}
			err := json.Unmarshal(buf.Bytes(), &parsedMap)
			require.NoError(t, err, "Log output must be valid JSON: %s", line)

			// 1. Assert message is "audit_completion"
			assert.Equal(t, "audit_completion", parsedMap["msg"])

			// 2. Assert event_id and request_id are valid non-empty UUIDs
			eventID, ok := parsedMap["event_id"].(string)
			require.True(t, ok, "event_id must be a string")
			_, err = uuid.Parse(eventID)
			require.NoError(t, err, "event_id must parse as valid UUID: %s", eventID)

			reqID, ok := parsedMap["request_id"].(string)
			require.True(t, ok, "request_id must be a string")
			_, err = uuid.Parse(reqID)
			require.NoError(t, err, "request_id must parse as valid UUID: %s", reqID)

			// 3. Assert decision matches "allow" or "deny"
			dec, ok := parsedMap["decision"].(string)
			require.True(t, ok)
			assert.Contains(t, []string{"allow", "deny"}, dec)
			assert.Equal(t, tc.decision, dec)

			// 4. Assert duration_ms is positive float
			dur, ok := parsedMap["duration_ms"].(float64)
			require.True(t, ok, "duration_ms must be a float64")
			assert.Greater(t, dur, 0.0)

			// 5. Assert status matches response code
			statusFloat, ok := parsedMap["status"].(float64)
			require.True(t, ok, "status must be numeric")
			assert.Equal(t, float64(tc.statusCode), statusFloat)

			httpStatusFloat, ok := parsedMap["http_status"].(float64)
			require.True(t, ok, "http_status must be numeric")
			assert.Equal(t, float64(tc.statusCode), httpStatusFloat)

			// 6. Assert presence and types of all 17 schema fields
			requiredFields := []string{
				"event_id", "timestamp", "request_id", "principal_id", "principal_kind",
				"principal_roles", "client_ip", "http_method", "canonical_path", "route_id",
				"service_id", "decision", "reason_code", "http_status", "duration_ms",
				"snapshot_version", "error_code",
			}
			for _, field := range requiredFields {
				_, exists := parsedMap[field]
				assert.True(t, exists, "Required field %q must be present in log output", field)
			}

			// 7. Unmarshal directly into CompletionEvent struct
			var parsedEvent CompletionEvent
			err = json.Unmarshal(buf.Bytes(), &parsedEvent)
			require.NoError(t, err)
			assert.Equal(t, tc.statusCode, parsedEvent.HTTPStatus)
			assert.Equal(t, tc.decision, parsedEvent.Decision)
			assert.Equal(t, tc.reason, parsedEvent.ReasonCode)
			assert.Equal(t, "usr_dev_01", parsedEvent.PrincipalID)
		}
	})

	t.Run("AuditMiddleware captures execution duration and status for HTTP pipeline", func(t *testing.T) {
		for _, tc := range testStatuses {
			t.Run(fmt.Sprintf("HTTP %d", tc.statusCode), func(t *testing.T) {
				var buf bytes.Buffer
				logger := NewLogger(&buf)

				middleware := AuditMiddleware(logger, 42)
				handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ac := FromContext(r.Context())
					require.NotNil(t, ac)
					ac.SetPrincipal("usr_test_user", "user", []string{"tester"})
					ac.SetRoute("route_test", "service_test")
					ac.SetCanonicalPath("/test/endpoint")
					ac.SetDecision(tc.decision, tc.reason)
					if tc.errCode != "" {
						ac.SetErrorCode(tc.errCode)
					}

					// Simulate small execution delay to ensure positive duration
					time.Sleep(1 * time.Millisecond)
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(`{"status":"response"}`))
				}))

				req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/test/endpoint", nil)
				req.RemoteAddr = "10.0.0.5:54321"
				// Invariant 8 / AUD-04: client header should NOT overwrite client_ip
				req.Header.Set("X-Forwarded-For", "203.0.113.195")
				w := httptest.NewRecorder()

				handler.ServeHTTP(w, req)

				require.Equal(t, tc.statusCode, w.Code)
				respReqID := w.Header().Get("X-Request-ID")
				require.NotEmpty(t, respReqID)

				line := buf.String()
				require.NotEmpty(t, line)

				var parsedMap map[string]interface{}
				err := json.Unmarshal(buf.Bytes(), &parsedMap)
				require.NoError(t, err)

				assert.Equal(t, "10.0.0.5", parsedMap["client_ip"], "client_ip must come from RemoteAddr, not X-Forwarded-For")
				assert.Equal(t, respReqID, parsedMap["request_id"])
				assert.Equal(t, float64(tc.statusCode), parsedMap["status"])
				assert.Equal(t, tc.decision, parsedMap["decision"])
				assert.Equal(t, tc.reason, parsedMap["reason_code"])
				assert.Equal(t, float64(42), parsedMap["snapshot_version"])

				dur := parsedMap["duration_ms"].(float64)
				assert.Greater(t, dur, 0.0)
			})
		}
	})
}
