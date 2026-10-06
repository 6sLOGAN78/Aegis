package proxy

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"aegis/internal/config"
	"aegis/internal/identity"
	"aegis/internal/pki"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenerLimits(t *testing.T) {
	cfg := &config.Config{
		Port:                  8080,
		MaxHeaderBytes:        16 * 1024,   // 16 KiB
		MaxBodyBytes:          1024 * 1024, // 1 MiB
		MaxConcurrentRequests: 100,
	}

	downstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"read":%d}`, len(body))))
	})

	server := NewServer(cfg, downstreamHandler)

	ts := httptest.NewUnstartedServer(server.Handler())
	ts.Config.MaxHeaderBytes = cfg.MaxHeaderBytes
	ts.Start()
	defer ts.Close()

	client := ts.Client()

	t.Run("valid request receives UUID v4 correlation ID", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/test", nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		reqID := resp.Header.Get("X-Request-ID")
		require.NotEmpty(t, reqID)

		parsed, err := uuid.Parse(reqID)
		require.NoError(t, err)
		assert.Equal(t, uuid.Version(4), parsed.Version())
	})

	t.Run("header block exceeding 16 KiB rejected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/oversized-header", nil)
		require.NoError(t, err)

		// Create header block > 16 KiB
		largeHeaderVal := strings.Repeat("X", 18*1024)
		req.Header.Set("X-Oversized-Header", largeHeaderVal)

		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			// Should be 431 Request Header Fields Too Large or 400 Bad Request
			assert.True(t, resp.StatusCode == http.StatusRequestHeaderFieldsTooLarge || resp.StatusCode == http.StatusBadRequest,
				"Expected 431 or 400, got %d", resp.StatusCode)
		}
	})

	t.Run("body exceeding 1 MiB rejected", func(t *testing.T) {
		// 1.5 MiB payload
		largeBody := bytes.Repeat([]byte("A"), 1536*1024)
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/oversized-body", bytes.NewReader(largeBody))
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.True(t, resp.StatusCode == http.StatusRequestEntityTooLarge || resp.StatusCode == http.StatusBadRequest,
			"Expected 413 or 400, got %d", resp.StatusCode)
	})

	t.Run("body within 1 MiB succeeds", func(t *testing.T) {
		smallBody := bytes.Repeat([]byte("B"), 512*1024) // 512 KiB
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/normal-body", bytes.NewReader(smallBody))
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestMTLSForwarding(t *testing.T) {
	// 1. Generate in-memory PKI
	ca, err := pki.NewCA("Aegis Internal Root CA")
	require.NoError(t, err)

	backendServerCert, err := ca.IssueServerCert("orders", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	gwClientCert, err := ca.IssueWorkloadCert("spiffe://aegis.local/ns/gateway/sa/aegis-gateway")
	require.NoError(t, err)

	// 2. Start mock upstream HTTPS server with client certificate verification
	var mu sync.Mutex
	var capturedHeaders http.Header
	var clientSPIFFE string
	requestCount := 0

	backendHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requestCount++
		capturedHeaders = r.Header.Clone()

		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			spiffeID, err := identity.ExtractSPIFFEID(r.TLS.PeerCertificates[0], "aegis.local")
			if err == nil {
				clientSPIFFE = spiffeID
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"orders-ok"}`))
	})

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{backendServerCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.CertPool,
		MinVersion:   tls.VersionTLS13,
	}

	backendServer := httptest.NewUnstartedServer(backendHandler)
	backendServer.TLS = serverTLS
	backendServer.StartTLS()
	defer backendServer.Close()

	backendURL, err := url.Parse(backendServer.URL)
	require.NoError(t, err)

	// 3. Create shared upstream transport
	transport := CreateUpstreamTransport(gwClientCert, ca.CertPool)
	defer transport.CloseIdleConnections()

	// Verify transport configuration parameters
	assert.Equal(t, []tls.Certificate{gwClientCert}, transport.TLSClientConfig.Certificates)
	assert.Equal(t, ca.CertPool, transport.TLSClientConfig.RootCAs)
	assert.Equal(t, uint16(tls.VersionTLS13), transport.TLSClientConfig.MinVersion)
	assert.False(t, transport.TLSClientConfig.InsecureSkipVerify, "InsecureSkipVerify must be false (Invariant 4)")
	assert.Equal(t, 1000, transport.MaxIdleConns)
	assert.Equal(t, 100, transport.MaxIdleConnsPerHost)
	assert.Equal(t, 90*time.Second, transport.IdleConnTimeout)

	t.Run("successful mTLS forwarding and header hygiene", func(t *testing.T) {
		testReqID := "req-mtls-forward-123"
		testAssertion := "jwt.assertion.payload.signature"

		rp := NewReverseProxyWithMTLS(backendURL, "/api/orders", testReqID, testAssertion, transport)

		// Simulate inbound client request with headers to be stripped
		inboundReq := httptest.NewRequest(http.MethodPost, "http://aegis-gateway:8080/api/orders", strings.NewReader(`{}`))
		inboundReq.Header.Set("Authorization", "Bearer client-raw-bearer-token")
		inboundReq.Header.Set("X-Aegis-User", "admin-spoof")
		inboundReq.Header.Set("x-aegis-roles", "superuser")
		inboundReq.Header.Set("Forwarded", "for=192.0.2.1")
		inboundReq.Header.Set("X-Forwarded-For", "192.0.2.1")
		inboundReq.Header.Set("X-Forwarded-Host", "attacker.com")
		inboundReq.Header.Set("X-Forwarded-Proto", "http")
		inboundReq.Header.Set("X-Forwarded-Server", "gateway")

		rec := httptest.NewRecorder()
		rp.ServeHTTP(rec, inboundReq)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "orders-ok")

		mu.Lock()
		defer mu.Unlock()

		// Verify client cert identity at backend
		assert.Equal(t, "spiffe://aegis.local/ns/gateway/sa/aegis-gateway", clientSPIFFE)

		// Verify headers received by upstream
		require.NotNil(t, capturedHeaders)
		assert.Equal(t, testAssertion, capturedHeaders.Get("X-Aegis-Assertion"))
		assert.Equal(t, testReqID, capturedHeaders.Get("X-Request-ID"))

		// Verify stripped headers
		assert.Empty(t, capturedHeaders.Get("Authorization"), "Authorization must be stripped")
		assert.Empty(t, capturedHeaders.Get("X-Aegis-User"), "X-Aegis-* headers must be stripped")
		assert.Empty(t, capturedHeaders.Get("X-Aegis-Roles"), "X-Aegis-* headers must be stripped")
		assert.Empty(t, capturedHeaders.Get("Forwarded"), "Forwarded must be stripped")
		assert.Empty(t, capturedHeaders.Get("X-Forwarded-For"), "X-Forwarded-For must be stripped")
		assert.Empty(t, capturedHeaders.Get("X-Forwarded-Host"), "X-Forwarded-Host must be stripped")
		assert.Empty(t, capturedHeaders.Get("X-Forwarded-Proto"), "X-Forwarded-Proto must be stripped")
		assert.Empty(t, capturedHeaders.Get("X-Forwarded-Server"), "X-Forwarded-Server must be stripped")
	})

	t.Run("connection pooling reuses idle connections", func(t *testing.T) {
		rp := NewReverseProxyWithMTLS(backendURL, "/api/orders", "req-pool-1", "assert-token", transport)

		// First request: establishes connection
		rec1 := httptest.NewRecorder()
		req1 := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
		rp.ServeHTTP(rec1, req1)
		require.Equal(t, http.StatusOK, rec1.Code)

		// Second request: trace connection reuse
		var reused bool
		trace := &httptrace.ClientTrace{
			GotConn: func(connInfo httptrace.GotConnInfo) {
				reused = connInfo.Reused
			},
		}

		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
		req2 = req2.WithContext(httptrace.WithClientTrace(req2.Context(), trace))

		rp.ServeHTTP(rec2, req2)
		require.Equal(t, http.StatusOK, rec2.Code)
		assert.True(t, reused, "Expected transport to reuse pooled connection for sequential requests")
	})
}

func TestMTLSForwarding_UntrustedBackendReturns502(t *testing.T) {
	// Gateway trusted Root CA
	gatewayCA, err := pki.NewCA("Aegis Gateway CA")
	require.NoError(t, err)

	gwClientCert, err := gatewayCA.IssueWorkloadCert("spiffe://aegis.local/ns/gateway/sa/aegis-gateway")
	require.NoError(t, err)

	// Rogue/untrusted CA generating backend server certificate
	rogueCA, err := pki.NewCA("Rogue Untrusted CA")
	require.NoError(t, err)

	rogueBackendCert, err := rogueCA.IssueServerCert("rogue-service", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	// Start HTTPS server with untrusted certificate
	untrustedServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	untrustedServer.TLS = &tls.Config{
		Certificates: []tls.Certificate{rogueBackendCert},
		MinVersion:   tls.VersionTLS13,
	}
	untrustedServer.StartTLS()
	defer untrustedServer.Close()

	untrustedURL, err := url.Parse(untrustedServer.URL)
	require.NoError(t, err)

	// Gateway transport trusts only gatewayCA, NOT rogueCA
	transport := CreateUpstreamTransport(gwClientCert, gatewayCA.CertPool)
	defer transport.CloseIdleConnections()

	rp := NewReverseProxyWithMTLS(untrustedURL, "/api/orders", "req-untrusted-cert", "jwt-token", transport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)

	// Proxy should handle handshake failure gracefully without panic
	rp.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

	var problem RFC7807Problem
	err = json.Unmarshal(rec.Body.Bytes(), &problem)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/bad-gateway", problem.Type)
	assert.Equal(t, "Bad Gateway", problem.Title)
	assert.Equal(t, http.StatusBadGateway, problem.Status)
	assert.Equal(t, "Upstream backend unreachable or TLS handshake failed", problem.Detail)
	assert.Equal(t, "req-untrusted-cert", problem.Instance)
}
