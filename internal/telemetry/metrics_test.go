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

// rejectionRecorder mirrors the consumer-side interface declared in the proxy package.
type rejectionRecorder interface{ RecordRejection(reason string) }

var _ rejectionRecorder = (*Metrics)(nil)

func scrapeMetrics(t *testing.T, m *Metrics) string {
	t.Helper()
	ts := httptest.NewServer(NewServer(":0", m).Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func TestAuditMetrics(t *testing.T) {
	t.Run("series are exposed and zero-initialized before any increment", func(t *testing.T) {
		m := NewMetrics()
		m.InitAuditSuppressedLabels([]string{"RATE_LIMIT_EXCEEDED", "POLICY_DENY_DEFAULT"})
		content := scrapeMetrics(t, m)

		for _, name := range []string{
			"aegis_audit_records_written_total",
			"aegis_audit_records_dropped_total",
			"aegis_audit_records_suppressed_total",
			"aegis_audit_suppressor_overflow_total",
			"aegis_audit_completion_queue_depth",
			"aegis_audit_degraded",
			"aegis_audit_flush_duration_seconds_bucket",
			"aegis_http_rejected_total",
		} {
			assert.Contains(t, content, name)
		}

		for _, kind := range []string{"completion", "denial"} {
			assert.Contains(t, content, `aegis_audit_records_written_total{kind="`+kind+`"} 0`)
			for _, reason := range []string{"queue_full", "hard_limit", "write_error", "closed", "timeout", "unauth_cap"} {
				assert.Contains(t, content, `aegis_audit_records_dropped_total{kind="`+kind+`",reason="`+reason+`"} 0`)
			}
		}
		for _, reason := range []string{"concurrency", "header_too_large", "ambiguous_credentials"} {
			assert.Contains(t, content, `aegis_http_rejected_total{reason="`+reason+`"} 0`)
		}
		for _, code := range []string{"RATE_LIMIT_EXCEEDED", "POLICY_DENY_DEFAULT", "OTHER"} {
			assert.Contains(t, content, `aegis_audit_records_suppressed_total{reason_code="`+code+`"} 0`)
		}
	})

	t.Run("recorders update the series", func(t *testing.T) {
		m := NewMetrics()
		m.RecordAuditWritten("completion", 3)
		m.RecordAuditWritten("completion", 0)
		m.RecordAuditWritten("completion", -4)
		m.RecordAuditDropped("denial", "unauth_cap", 2)
		m.RecordAuditSuppressed("RATE_LIMIT_EXCEEDED", 5)
		m.RecordAuditSuppressed("", 1)
		m.RecordAuditSuppressorOverflow()
		m.SetAuditDegraded(true)
		m.SetAuditQueueDepth(17)
		m.ObserveAuditFlush(3 * time.Millisecond)
		m.RecordRejection("concurrency")
		m.RecordRejection("header_too_large")
		m.RecordRejection("ambiguous_credentials")
		m.RecordRejection("something-attacker-chose")

		content := scrapeMetrics(t, m)
		assert.Contains(t, content, `aegis_audit_records_written_total{kind="completion"} 3`)
		assert.Contains(t, content, `aegis_audit_records_dropped_total{kind="denial",reason="unauth_cap"} 2`)
		assert.Contains(t, content, `aegis_audit_records_suppressed_total{reason_code="RATE_LIMIT_EXCEEDED"} 5`)
		assert.Contains(t, content, `aegis_audit_records_suppressed_total{reason_code="OTHER"} 1`)
		assert.Contains(t, content, "aegis_audit_suppressor_overflow_total 1")
		assert.Contains(t, content, "aegis_audit_degraded 1")
		assert.Contains(t, content, "aegis_audit_completion_queue_depth 17")
		assert.Contains(t, content, "aegis_audit_flush_duration_seconds_count 1")
		assert.Contains(t, content, `aegis_http_rejected_total{reason="concurrency"} 1`)
		assert.Contains(t, content, `aegis_http_rejected_total{reason="header_too_large"} 1`)
		assert.Contains(t, content, `aegis_http_rejected_total{reason="ambiguous_credentials"} 1`)
		assert.Contains(t, content, `aegis_http_rejected_total{reason="other"} 1`)
		assert.NotContains(t, content, "something-attacker-chose")

		m.SetAuditDegraded(false)
		assert.Contains(t, scrapeMetrics(t, m), "aegis_audit_degraded 0")
	})

	t.Run("no forbidden label keys on the new series", func(t *testing.T) {
		m := NewMetrics()
		m.InitAuditSuppressedLabels([]string{"RATE_LIMIT_EXCEEDED"})
		m.RecordAuditDropped("completion", "queue_full", 1)
		forbidden := map[string]bool{
			"principal_id": true, "client_ip": true, "user_id": true, "ip": true,
			"path": true, "url": true, "query": true, "bearer": true, "token": true,
		}
		for _, line := range strings.Split(scrapeMetrics(t, m), "\n") {
			if !strings.HasPrefix(line, "aegis_audit_") && !strings.HasPrefix(line, "aegis_http_rejected_total") {
				continue
			}
			s, e := strings.Index(line, "{"), strings.LastIndex(line, "}")
			if s == -1 || e <= s {
				continue
			}
			for _, pair := range strings.Split(line[s+1:e], ",") {
				kv := strings.SplitN(pair, "=", 2)
				assert.False(t, forbidden[strings.TrimSpace(kv[0])], "forbidden label in %s", line)
			}
		}
	})
}
