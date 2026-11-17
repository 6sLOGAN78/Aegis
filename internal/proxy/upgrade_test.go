package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"aegis/internal/telemetry"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A backend that answers 101 must not get a hijacked tunnel through the
// gateway, even behind the metrics middleware (whose recorder unwraps).
func TestUpgradeResponseRefused(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer backend.Close()
	u, err := url.Parse(backend.URL)
	require.NoError(t, err)

	for name, rp := range map[string]http.Handler{
		"mtls":  NewReverseProxyWithMTLS(u, "/ws", "", "", nil),
		"plain": NewReverseProxy(u, "/ws", ""),
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(telemetry.MetricsMiddleware(telemetry.NewMetrics())(rp))
			defer srv.Close()

			req, err := http.NewRequest(http.MethodGet, srv.URL+"/ws", nil)
			require.NoError(t, err)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		})
	}
}
