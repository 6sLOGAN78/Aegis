package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scrapeDurationLabels(t *testing.T, m *Metrics) string {
	t.Helper()
	mfs, err := m.Registry.Gather()
	require.NoError(t, err)
	var sb strings.Builder
	for _, mf := range mfs {
		if mf.GetName() != "aegis_http_request_duration_seconds" {
			continue
		}
		for _, mm := range mf.GetMetric() {
			for _, lp := range mm.GetLabel() {
				sb.WriteString(lp.GetName() + "=" + lp.GetValue() + ";")
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func TestHTTPRequestDuration_NormalizedLabels(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequestDuration("BREW", "", 503, 5*time.Millisecond)
	m.ObserveRequestDuration("GET", "orders.list", 200, time.Millisecond)

	out := scrapeDurationLabels(t, m)
	assert.Contains(t, out, "method=OTHER")
	assert.Contains(t, out, "status_class=5xx")
	assert.Contains(t, out, "route_id=unknown")
	assert.Contains(t, out, "route_id=orders.list")
	assert.NotContains(t, out, "BREW")

	mfs, err := m.Registry.Gather()
	require.NoError(t, err)
	found := false
	for _, mf := range mfs {
		if mf.GetName() != "aegis_http_request_duration_seconds" {
			continue
		}
		found = true
		for _, mm := range mf.GetMetric() {
			names := map[string]bool{}
			for _, lp := range mm.GetLabel() {
				names[lp.GetName()] = true
			}
			assert.Equal(t, map[string]bool{"method": true, "route_id": true, "status_class": true}, names)
			for _, bad := range []string{"principal", "ip", "path", "jti", "user"} {
				assert.False(t, names[bad])
			}
		}
	}
	assert.True(t, found)
}

func TestNormalizeMethodAndStatusClass(t *testing.T) {
	for _, mth := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		assert.Equal(t, mth, normalizeMethod(mth))
	}
	assert.Equal(t, "OTHER", normalizeMethod("get"))
	assert.Equal(t, "OTHER", normalizeMethod("PROPFIND"))
	assert.Equal(t, "1xx", statusClass(101))
	assert.Equal(t, "2xx", statusClass(204))
	assert.Equal(t, "3xx", statusClass(302))
	assert.Equal(t, "4xx", statusClass(404))
	assert.Equal(t, "5xx", statusClass(599))
	assert.Equal(t, "other", statusClass(99))
	assert.Equal(t, "other", statusClass(600))
}

func TestMetricsMiddleware_ObservesDuration(t *testing.T) {
	m := NewMetrics()
	h := MetricsMiddleware(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRouteID(r.Context(), "payments.create")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/x/secret", nil))

	out := scrapeDurationLabels(t, m)
	assert.Contains(t, out, "method=OTHER")
	assert.Contains(t, out, "route_id=payments.create")
	assert.Contains(t, out, "status_class=5xx")
	assert.NotContains(t, out, "secret")
}

// CR-02: aegis_http_requests_total uses the same closed method set as the histogram, so
// an arbitrary client-chosen method cannot mint a new series.
func TestHTTPRequestsTotal_MethodNormalized(t *testing.T) {
	m := NewMetrics()
	m.RecordRequest("FOO1", "orders.list", 200)
	m.RecordRequest("FOO2", "orders.list", 200)
	m.RecordRequest("GET", "orders.list", 200)

	mfs, err := m.Registry.Gather()
	require.NoError(t, err)
	methods := map[string]float64{}
	for _, mf := range mfs {
		if mf.GetName() != "aegis_http_requests_total" {
			continue
		}
		for _, mm := range mf.GetMetric() {
			for _, lp := range mm.GetLabel() {
				if lp.GetName() == "method" {
					methods[lp.GetValue()] += mm.GetCounter().GetValue()
				}
			}
		}
	}
	assert.Equal(t, map[string]float64{"OTHER": 2, "GET": 1}, methods)
}
