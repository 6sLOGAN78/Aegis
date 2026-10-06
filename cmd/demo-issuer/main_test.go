package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aegis/internal/identity"

	"github.com/stretchr/testify/require"
)

func TestDemoIssuer(t *testing.T) {
	privKey := ed25519.NewKeyFromSeed(DefaultDeterministicSeed)
	server := NewIssuerServer(privKey, "aegis-issuer", "aegis-gateway")
	ts := httptest.NewServer(server.Routes())
	defer ts.Close()

	validator := identity.NewTokenValidator("aegis-issuer", "aegis-gateway", privKey.Public())

	t.Run("Valid logins for all seeded accounts", func(t *testing.T) {
		roles := []struct {
			role    string
			userID  string
			expRole string
		}{
			{"developer", "usr_developer_01", "developer"},
			{"finance", "usr_finance_01", "finance"},
			{"application-admin", "usr_admin_01", "application-admin"},
		}

		for _, tc := range roles {
			t.Run(tc.role, func(t *testing.T) {
				body, _ := json.Marshal(map[string]string{"role": tc.role})
				resp, err := http.Post(ts.URL+"/login", "application/json", bytes.NewReader(body))
				require.NoError(t, err)
				defer resp.Body.Close()

				require.Equal(t, http.StatusOK, resp.StatusCode)

				var res map[string]interface{}
				err = json.NewDecoder(resp.Body).Decode(&res)
				require.NoError(t, err)

				tokenStr, ok := res["access_token"].(string)
				require.True(t, ok)
				require.NotEmpty(t, tokenStr)

				claims, err := validator.ValidateBearerToken("Bearer " + tokenStr)
				require.NoError(t, err)
				require.Equal(t, tc.userID, claims.Subject)
				require.Contains(t, claims.Roles, tc.expRole)
			})
		}
	})

	t.Run("Rejects unknown role with 401", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"role": "unknown-intruder"})
		resp, err := http.Post(ts.URL+"/login", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		var res map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&res)
		require.Equal(t, "UNKNOWN_ROLE", res["error"])
	})

	t.Run("Rate limiting enforces max 20 attempts per minute per IP", func(t *testing.T) {
		// New server instance to isolate rate limit test
		isolatedServer := NewIssuerServer(privKey, "aegis-issuer", "aegis-gateway")
		isolatedTs := httptest.NewServer(isolatedServer.Routes())
		defer isolatedTs.Close()

		body, _ := json.Marshal(map[string]string{"role": "developer"})

		// 20 rapid requests should all succeed
		for i := 1; i <= 20; i++ {
			resp, err := http.Post(isolatedTs.URL+"/login", "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode, "request %d should succeed", i)
			resp.Body.Close()
		}

		// 21st and 22nd requests must be rate limited with 429
		for i := 21; i <= 22; i++ {
			resp, err := http.Post(isolatedTs.URL+"/login", "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "request %d should be rate limited", i)
			var res map[string]string
			_ = json.NewDecoder(resp.Body).Decode(&res)
			require.Equal(t, "RATE_LIMITED", res["error"])
			resp.Body.Close()
		}
	})

	t.Run("Public key endpoint returns valid base64 key", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/public-key")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		var res map[string]string
		err = json.NewDecoder(resp.Body).Decode(&res)
		require.NoError(t, err)
		require.Equal(t, "EdDSA", res["algorithm"])

		pubBytes, err := base64.StdEncoding.DecodeString(res["public_key"])
		require.NoError(t, err)
		require.Equal(t, ed25519.PublicKey(pubBytes), privKey.Public())
	})
}
