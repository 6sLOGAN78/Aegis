package middleware_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"aegis/internal/identity"
	"aegis/internal/pki"
	"aegis/services/middleware"
	"github.com/stretchr/testify/require"
)

func TestAuthMiddleware(t *testing.T) {
	// 1. Setup PKI
	ca, err := pki.NewCA("Aegis Test CA")
	require.NoError(t, err)

	serverCert, err := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	gatewaySPIFFE := "spiffe://aegis.local/ns/gateway/sa/aegis-gateway"
	gwCert, err := ca.IssueWorkloadCert(gatewaySPIFFE)
	require.NoError(t, err)

	ordersCert, err := ca.IssueWorkloadCert("spiffe://aegis.local/workload/orders")
	require.NoError(t, err)

	// 2. Setup Assertion Keys
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	minter := identity.NewAssertionMinter(privKey)

	// 3. Setup Test Handler
	var lastSeenClaims *identity.AssertionClaims
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := middleware.FromContext(r.Context())
		if ok {
			lastSeenClaims = claims
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mw := middleware.BackendAuthMiddleware(
		gatewaySPIFFE,
		"orders",
		pubKey,
		"/health",
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/api/orders", testHandler)

	ts := httptest.NewUnstartedServer(mw(mux))
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    ca.CertPool,
		ClientAuth:   tls.RequestClientCert,
	}
	ts.StartTLS()
	defer ts.Close()

	// Helper client builder
	newClient := func(cert *tls.Certificate) *http.Client {
		tlsConfig := &tls.Config{
			RootCAs: ca.CertPool,
		}
		if cert != nil {
			tlsConfig.Certificates = []tls.Certificate{*cert}
		}
		return &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		}
	}

	t.Run("Plain HTTP connection (r.TLS == nil) returns HTTP 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
		rec := httptest.NewRecorder()
		mw(mux).ServeHTTP(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

		var body map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &body)
		require.NoError(t, err)
		require.Equal(t, "mTLS client certificate required", body["detail"])
	})

	t.Run("HTTPS without client certificate returns HTTP 401", func(t *testing.T) {
		client := newClient(nil)
		resp, err := client.Get(ts.URL + "/api/orders")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))

		var body map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&body)
		require.NoError(t, err)
		require.Equal(t, "mTLS client certificate required", body["detail"])
	})

	t.Run("Peer workload certificate (direct orders bypass) returns HTTP 403", func(t *testing.T) {
		client := newClient(&ordersCert)
		resp, err := client.Get(ts.URL + "/api/orders")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))

		var body map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&body)
		require.NoError(t, err)
		require.Equal(t, "unauthorized client identity: peer is not the Aegis Gateway", body["detail"])
	})

	t.Run("Gateway cert but missing X-Aegis-Assertion header returns HTTP 401", func(t *testing.T) {
		client := newClient(&gwCert)
		resp, err := client.Get(ts.URL + "/api/orders")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))

		var body map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&body)
		require.NoError(t, err)
		require.Equal(t, "missing required X-Aegis-Assertion header", body["detail"])
	})

	t.Run("Gateway cert with assertion for mismatched audience returns HTTP 403", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"user_1", "user", []string{"user"},
			"payments", // Mismatched audience (orders expected)
			http.MethodGet, "/api/orders",
			"req-1", 1,
		)
		require.NoError(t, err)

		client := newClient(&gwCert)
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/orders", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
	})

	t.Run("Gateway cert with assertion for mismatched method returns HTTP 403", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"user_1", "user", []string{"user"},
			"orders",
			http.MethodPost, // Mismatched method (GET expected)
			"/api/orders",
			"req-1", 1,
		)
		require.NoError(t, err)

		client := newClient(&gwCert)
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/orders", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("Gateway cert with assertion for mismatched path returns HTTP 403", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"user_1", "user", []string{"user"},
			"orders",
			http.MethodGet,
			"/api/wrong-path", // Mismatched path
			"req-1", 1,
		)
		require.NoError(t, err)

		client := newClient(&gwCert)
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/orders", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("Valid gateway cert and matching assertion returns HTTP 200 with claims in context", func(t *testing.T) {
		lastSeenClaims = nil
		token, err := minter.MintAssertion(
			"user_1", "user", []string{"user"},
			"orders",
			http.MethodGet,
			"/api/orders",
			"req-valid-123", 1,
		)
		require.NoError(t, err)

		client := newClient(&gwCert)
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/orders", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, `{"status":"ok"}`, string(body))

		require.NotNil(t, lastSeenClaims)
		require.Equal(t, "user_1", lastSeenClaims.Subject)
		require.Equal(t, "req-valid-123", lastSeenClaims.RequestID)
		require.Equal(t, "orders", lastSeenClaims.Audience[0])
	})

	t.Run("Unauthenticated request to exempt path (/health) returns HTTP 200", func(t *testing.T) {
		client := newClient(nil) // No client certificate, no assertion
		resp, err := client.Get(ts.URL + "/health")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, "ok", string(body))
	})
}
