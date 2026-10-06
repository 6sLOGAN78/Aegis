package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"aegis/internal/identity"
	"aegis/internal/pki"
	"github.com/stretchr/testify/require"
)

func TestOrdersService(t *testing.T) {
	ca, err := pki.NewCA("Aegis Orders Test CA")
	require.NoError(t, err)

	serverCert, err := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	gatewaySPIFFE := "spiffe://aegis.local/ns/gateway/sa/aegis-gateway"
	gwCert, err := ca.IssueWorkloadCert(gatewaySPIFFE)
	require.NoError(t, err)

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	minter := identity.NewAssertionMinter(privKey)

	handler := Routes(pubKey)
	ts := httptest.NewUnstartedServer(handler)
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    ca.CertPool,
		ClientAuth:   tls.RequestClientCert,
	}
	ts.StartTLS()
	defer ts.Close()

	authenticatedClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      ca.CertPool,
				Certificates: []tls.Certificate{gwCert},
			},
		},
	}

	unauthenticatedClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: ca.CertPool,
			},
		},
	}

	t.Run("GET /health succeeds without credentials", func(t *testing.T) {
		resp, err := unauthenticatedClient.Get(ts.URL + "/health")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("GET /api/orders without credentials returns HTTP 401", func(t *testing.T) {
		resp, err := unauthenticatedClient.Get(ts.URL + "/api/orders")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("GET /api/orders with valid gateway mTLS and assertion returns HTTP 200 with JSON orders array", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"usr_test", "user", []string{"user"},
			"orders",
			http.MethodGet, "/api/orders",
			"req-ord-01", 1,
		)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/orders", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := authenticatedClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var orders []Order
		err = json.NewDecoder(resp.Body).Decode(&orders)
		require.NoError(t, err)
		require.Len(t, orders, 2)
		require.Equal(t, "ord_101", orders[0].ID)
		require.Equal(t, "Cloud Scanner", orders[0].Item)
	})

	t.Run("POST /api/orders returns 405 Method Not Allowed when authenticated", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"usr_test", "user", []string{"user"},
			"orders",
			http.MethodPost, "/api/orders",
			"req-ord-02", 1,
		)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/orders", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := authenticatedClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})
}
