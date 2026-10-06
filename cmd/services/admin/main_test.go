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

func TestAdminService(t *testing.T) {
	ca, err := pki.NewCA("Aegis Admin Test CA")
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

	t.Run("GET /api/admin/users without credentials returns HTTP 401", func(t *testing.T) {
		resp, err := unauthenticatedClient.Get(ts.URL + "/api/admin/users")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("GET /api/admin/users returns HTTP 200 with JSON admin data when authenticated", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"usr_admin", "user", []string{"application-admin"},
			"admin",
			http.MethodGet, "/api/admin/users",
			"req-adm-01", 1,
		)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/admin/users", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := authenticatedClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var users []AdminUser
		err = json.NewDecoder(resp.Body).Decode(&users)
		require.NoError(t, err)
		require.Len(t, users, 2)
		require.Equal(t, "usr_admin_01", users[0].ID)
		require.Equal(t, "application-admin", users[0].Role)
	})

	t.Run("POST /api/admin/users returns HTTP 200 with confirmation when authenticated", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"usr_admin", "user", []string{"application-admin"},
			"admin",
			http.MethodPost, "/api/admin/users",
			"req-adm-02", 1,
		)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/admin/users", nil)
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
		require.Equal(t, "success", res["status"])
	})

	t.Run("PUT /api/admin/users returns 405 Method Not Allowed when authenticated", func(t *testing.T) {
		token, err := minter.MintAssertion(
			"usr_admin", "user", []string{"application-admin"},
			"admin",
			http.MethodPut, "/api/admin/users",
			"req-adm-03", 1,
		)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/admin/users", nil)
		require.NoError(t, err)
		req.Header.Set("X-Aegis-Assertion", token)

		resp, err := authenticatedClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})
}
