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

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func login(t *testing.T, url string, body map[string]string) (*http.Response, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url+"/login", "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	var res map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&res)
	return resp, res
}

func TestDemoIssuerJti(t *testing.T) {
	t.Setenv("AEGIS_DEMO_PASSWORD_DEVELOPER", "dev-pw")
	t.Setenv("AEGIS_DEMO_PASSWORD_FINANCE", "fin-pw")
	t.Setenv("AEGIS_DEMO_PASSWORD_APPLICATION_ADMIN", "adm-pw")
	loadSeedPasswords()
	t.Cleanup(func() {
		t.Setenv("AEGIS_DEMO_PASSWORD_DEVELOPER", "")
		t.Setenv("AEGIS_DEMO_PASSWORD_FINANCE", "")
		t.Setenv("AEGIS_DEMO_PASSWORD_APPLICATION_ADMIN", "")
		loadSeedPasswords()
	})

	privKey := ed25519.NewKeyFromSeed(DefaultDeterministicSeed)
	ts := httptest.NewServer(NewIssuerServer(privKey, "aegis-issuer", "aegis-gateway").Routes())
	defer ts.Close()
	validator := identity.NewTokenValidator("aegis-issuer", "aegis-gateway", privKey.Public())

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		resp, res := login(t, ts.URL, map[string]string{"username": "developer", "password": "dev-pw"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		claims, err := validator.ValidateBearerToken("Bearer " + res["access_token"].(string))
		require.NoError(t, err)
		_, err = uuid.Parse(claims.ID)
		require.NoError(t, err)
		require.Equal(t, claims.ID, res["jti"])
		require.False(t, seen[claims.ID])
		seen[claims.ID] = true
	}

	for _, tc := range []struct{ user, pw string }{{"finance", "fin-pw"}, {"application-admin", "adm-pw"}} {
		resp, _ := login(t, ts.URL, map[string]string{"username": tc.user, "password": tc.pw})
		require.Equal(t, http.StatusOK, resp.StatusCode, tc.user)
	}

	for name, body := range map[string]map[string]string{
		"wrong":   {"username": "developer", "password": "nope"},
		"missing": {"username": "developer"},
		"unknown": {"username": "intruder", "password": "dev-pw"},
	} {
		resp, res := login(t, ts.URL, body)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
		require.Equal(t, "INVALID_CREDENTIALS", res["error"], name)
	}

	resp, res := login(t, ts.URL, map[string]string{"role": "developer"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Equal(t, "INVALID_REQUEST", res["error"])

	r2, err := http.Post(ts.URL+"/login", "application/json", bytes.NewReader(nil))
	require.NoError(t, err)
	r2.Body.Close()
	require.Equal(t, http.StatusBadRequest, r2.StatusCode)
}

func TestDemoIssuer(t *testing.T) {
	loadSeedPasswords()
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
				body, _ := json.Marshal(map[string]string{"username": tc.role, "password": seedDatabase[tc.role].Password})
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

	t.Run("Rejects unknown user with 401", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"username": "unknown-intruder", "password": "x"})
		resp, err := http.Post(ts.URL+"/login", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		var res map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&res)
		require.Equal(t, "INVALID_CREDENTIALS", res["error"])
	})

	t.Run("Rate limiting enforces max 20 attempts per minute per IP", func(t *testing.T) {
		// New server instance to isolate rate limit test
		isolatedServer := NewIssuerServer(privKey, "aegis-issuer", "aegis-gateway")
		isolatedTs := httptest.NewServer(isolatedServer.Routes())
		defer isolatedTs.Close()

		body, _ := json.Marshal(map[string]string{"username": "developer", "password": seedDatabase["developer"].Password})

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
