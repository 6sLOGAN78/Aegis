package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusMetrics(t *testing.T) {
	metrics := NewMetrics()
	require.NotNil(t, metrics)

	t.Run("Metric Registration and Increments", func(t *testing.T) {
		metrics.RecordRequest("GET", "orders.list", 200)
		metrics.RecordRequest("POST", "payments.create", 201)
		metrics.RecordPolicyDecision("allow", "ROLE_AUTHORIZED")
		metrics.RecordPolicyDecision("deny", "POLICY_DENY_DEFAULT")
		metrics.RecordRateLimitRejection("orders.list")
		metrics.RecordPolicyEval(500 * time.Microsecond)
		metrics.RecordPolicyEval(2 * time.Millisecond)
		metrics.SetActiveSnapshotVersion(42)
		metrics.SetLeaseAge(1.5)
		metrics.SetSpoolUtilization(0.25)
		metrics.RecordSpoolBytes(1024)
		metrics.SetConnectedGateways(3)
		metrics.RecordSnapshotPublish()
	})

	t.Run("HTTP Metrics Middleware Execution", func(t *testing.T) {
		mw := MetricsMiddleware(metrics)
		innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		})
		handler := mw(innerHandler)

		req := httptest.NewRequest("POST", "/api/v1/payments", nil)
		ctx := WithRouteID(req.Context(), "payments.create")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusAccepted, rec.Code)
	})

	t.Run("Concurrent Metric Updates Under Race Detector", func(t *testing.T) {
		var wg sync.WaitGroup
		numWorkers := 10
		iterations := 100

		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					metrics.RecordRequest("GET", "orders.list", 200)
					metrics.RecordPolicyEval(time.Duration(j*10) * time.Microsecond)
					metrics.RecordPolicyDecision("allow", "ROLE_AUTHORIZED")
					metrics.RecordSpoolBytes(64)
				}
			}(i)
		}

		wg.Wait()
	})

	t.Run("Scrape /metrics Exposition Format", func(t *testing.T) {
		server := NewServer(":0", metrics)
		ts := httptest.NewServer(server.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/metrics")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		content := string(body)

		// Assert presence of all required metric identifiers
		assert.Contains(t, content, "aegis_http_requests_total")
		assert.Contains(t, content, "aegis_policy_eval_duration_seconds")
		assert.Contains(t, content, "aegis_policy_decisions_total")
		assert.Contains(t, content, "aegis_ratelimit_rejections_total")
		assert.Contains(t, content, "aegis_snapshot_active_version")
		assert.Contains(t, content, "aegis_snapshot_lease_age_seconds")
		assert.Contains(t, content, "aegis_spool_utilization_ratio")
		assert.Contains(t, content, "aegis_spool_bytes_written_total")
		assert.Contains(t, content, "aegis_control_plane_connected_gateways")
		assert.Contains(t, content, "aegis_control_plane_snapshot_publish_total")

		// Assert value presence
		assert.Contains(t, content, "aegis_snapshot_active_version 42")
		assert.Contains(t, content, "aegis_control_plane_connected_gateways 3")
	})

	t.Run("Negative Security Test: Zero High-Cardinality Labels", func(t *testing.T) {
		server := NewServer(":0", metrics)
		ts := httptest.NewServer(server.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/metrics")
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		lines := strings.Split(string(body), "\n")
		forbiddenLabels := []string{
			"principal_id",
			"client_ip",
			"user_id",
			"ip",
			"path",
			"url",
			"query",
			"bearer",
			"token",
		}

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || line == "" {
				continue
			}

			// If line contains label curly braces e.g. aegis_http_requests_total{method="GET",...}
			startIdx := strings.Index(line, "{")
			endIdx := strings.LastIndex(line, "}")
			if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
				labelsStr := line[startIdx+1 : endIdx]
				labelPairs := strings.Split(labelsStr, ",")
				for _, pair := range labelPairs {
					parts := strings.SplitN(pair, "=", 2)
					if len(parts) == 2 {
						labelKey := strings.TrimSpace(parts[0])
						for _, forbidden := range forbiddenLabels {
							assert.NotEqual(t, forbidden, labelKey, "Forbidden high-cardinality label found in line: %s", line)
						}
					}
				}
			}
		}
	})

	t.Run("Server Lifecycle and Shutdown", func(t *testing.T) {
		srv := NewServer("127.0.0.1:0", metrics)
		srv.Start()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err := srv.Shutdown(ctx)
		assert.NoError(t, err)
	})
}
