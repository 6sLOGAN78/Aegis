package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadIssuerKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	dir := t.TempDir()
	good := filepath.Join(dir, "issuer.key")
	require.NoError(t, os.WriteFile(good, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600))

	k, warn, err := loadIssuerKey(func(string) string { return good })
	require.NoError(t, err)
	require.False(t, warn)
	require.True(t, priv.Equal(k))

	// the issuer serves the file key, not the seed key
	srv := NewIssuerServer(k, "aegis-issuer", "aegis-gateway")
	rec := httptest.NewRecorder()
	srv.HandlePublicKey(rec, httptest.NewRequest(http.MethodGet, "/public-key", nil))
	require.Contains(t, rec.Body.String(), base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))

	bad := filepath.Join(dir, "bad.key")
	require.NoError(t, os.WriteFile(bad, []byte("garbage!"), 0o600))
	for _, p := range []string{bad, filepath.Join(dir, "missing")} {
		p := p
		_, _, err = loadIssuerKey(func(string) string { return p })
		require.Error(t, err)
	}

	k, warn, err = loadIssuerKey(func(string) string { return "" })
	require.NoError(t, err)
	require.True(t, warn)
	require.True(t, ed25519.NewKeyFromSeed(DefaultDeterministicSeed).Equal(k))
}
