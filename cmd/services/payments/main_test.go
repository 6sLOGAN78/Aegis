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

func TestPaymentsService(t *testing.T) {
	ca, err := pki.NewCA("Aegis Payments Test CA")
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

	t.Run("GET /api/payments without credentials returns HTTP 401", func(t *testing.T) {
		resp, err := unauthenticatedClient.Get(ts.URL + "/api/payments")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("GET /api/payments returns HTTP 200 with JSON payment records when authenticated", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"usr_test", "user", []string{"user"},
			"payments",
			http.MethodGet, "/api/payments",
			"req-pay-01", 1,
		)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/payments", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := authenticatedClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var payments []Payment
		err = json.NewDecoder(resp.Body).Decode(&payments)
		require.NoError(t, err)
		require.Len(t, payments, 2)
		require.Equal(t, "pay_201", payments[0].ID)
	})

	t.Run("POST /api/payments returns HTTP 200 with confirmation when authenticated", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"usr_test", "user", []string{"user"},
			"payments",
			http.MethodPost, "/api/payments",
			"req-pay-02", 1,
		)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/payments", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := authenticatedClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var res map[string]string
		err = json.NewDecoder(resp.Body).Decode(&res)
		require.NoError(t, err)
		require.Equal(t, "processed", res["status"])
		require.Equal(t, "pay_201", res["payment_id"])
	})

	t.Run("DELETE /api/payments returns 405 Method Not Allowed when authenticated", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"usr_test", "user", []string{"user"},
			"payments",
			http.MethodDelete, "/api/payments",
			"req-pay-03", 1,
		)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/payments", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := authenticatedClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})
}
