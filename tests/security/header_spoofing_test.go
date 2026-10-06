package security

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"aegis/internal/config"
	"aegis/internal/proxy"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderScrubbing(t *testing.T) {
	var capturedHeaders http.Header
	var mu sync.Mutex

	// Mock private backend microservice
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedHeaders = r.Header.Clone()
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer mockBackend.Close()

	backendURL, err := url.Parse(mockBackend.URL)
	require.NoError(t, err)

	// Test Gateway wrapping reverse proxy with Server middleware pipeline
	gatewayHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp := proxy.NewReverseProxy(backendURL, "/api/orders", r.Header.Get("X-Request-ID"))
		rp.ServeHTTP(w, r)
	})

	cfg := &config.Config{
		Port:                  8080,
		MaxHeaderBytes:        16 * 1024,
		MaxBodyBytes:          1024 * 1024,
		MaxConcurrentRequests: 100,
	}
	server := proxy.NewServer(cfg, gatewayHandler)
	testGateway := httptest.NewServer(server.Handler())
	defer testGateway.Close()

	// Dispatch request with spoofed/injected headers
	client := &http.Client{}
	req, err := http.NewRequest(http.MethodGet, testGateway.URL+"/api/orders", nil)
	require.NoError(t, err)

	req.Header.Set("X-Aegis-User", "admin")
	req.Header.Set("X-Aegis-Roles", "superuser")
	req.Header.Set("x-aegis-tenant", "tenant-alpha")
	req.Header.Set("Forwarded", "for=192.0.2.60;proto=http;by=203.0.113.43")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	req.Header.Set("X-Forwarded-Host", "evil.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Server", "proxy-attacker")
	req.Header.Set("Authorization", "Bearer attacker-untrusted-token")
	req.Header.Set("Connection", "close, X-Custom-Hop")
	req.Header.Set("X-Custom-Hop", "leak")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify gateway response contains valid UUID v4 X-Request-ID
	respReqID := resp.Header.Get("X-Request-ID")
	require.NotEmpty(t, respReqID)
	parsedUUID, err := uuid.Parse(respReqID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(4), parsedUUID.Version())

	// Inspect headers received by mock upstream backend
	mu.Lock()
	backendHeaders := capturedHeaders
	mu.Unlock()

	require.NotNil(t, backendHeaders)

	// 1. All X-Aegis-* headers must be completely stripped
	for k := range backendHeaders {
		assert.False(t, strings.HasPrefix(strings.ToLower(k), "x-aegis-"),
			"Upstream received unstripped header: %s", k)
	}

	// 2. Forwarded and X-Forwarded-* headers must be stripped
	assert.Empty(t, backendHeaders.Get("Forwarded"))
	assert.Empty(t, backendHeaders.Get("X-Forwarded-For"))
	assert.Empty(t, backendHeaders.Get("X-Forwarded-Host"))
	assert.Empty(t, backendHeaders.Get("X-Forwarded-Proto"))
	assert.Empty(t, backendHeaders.Get("X-Forwarded-Server"))

	// 3. User Bearer token must be stripped in private network forward
	assert.Empty(t, backendHeaders.Get("Authorization"))

	// 4. Hop-by-hop headers must be stripped by Rewrite hook
	assert.Empty(t, backendHeaders.Get("X-Custom-Hop"))

	// 5. Outbound request must contain gateway correlation ID matching UUID v4
	upstreamReqID := backendHeaders.Get("X-Request-ID")
	require.NotEmpty(t, upstreamReqID)
	upstreamUUID, err := uuid.Parse(upstreamReqID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(4), upstreamUUID.Version())
	assert.Equal(t, respReqID, upstreamReqID)
}

func TestReverseProxyBadGateway(t *testing.T) {
	// Point reverse proxy to an unreachable target (closed socket)
	unreachableURL, err := url.Parse("http://127.0.0.1:54321")
	require.NoError(t, err)

	rp := proxy.NewReverseProxy(unreachableURL, "/api/orders", "req-err-123")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)

	rp.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

	var problem proxy.RFC7807Problem
	err = json.Unmarshal(rec.Body.Bytes(), &problem)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/bad-gateway", problem.Type)
	assert.Equal(t, "Bad Gateway", problem.Title)
	assert.Equal(t, http.StatusBadGateway, problem.Status)
	assert.Equal(t, "Upstream backend unreachable", problem.Detail)
	assert.Equal(t, "req-err-123", problem.Instance)
}
