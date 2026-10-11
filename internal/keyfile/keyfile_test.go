package keyfile

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEd25519Public(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	got, err := ParseEd25519Public([]byte(base64.StdEncoding.EncodeToString(pub) + "\n"))
	require.NoError(t, err)
	assert.Equal(t, pub, got)

	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	got, err = ParseEd25519Public(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	require.NoError(t, err)
	assert.Equal(t, pub, got)

	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	rder, err := x509.MarshalPKIXPublicKey(&rk.PublicKey)
	require.NoError(t, err)

	for name, in := range map[string][]byte{
		"empty":     nil,
		"spaces":    []byte("  \n"),
		"short":     []byte(base64.StdEncoding.EncodeToString(pub[:16])),
		"notb64":    []byte("!!!not base64!!!"),
		"rsa":       pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: rder}),
		"wrongtype": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pub}),
	} {
		_, err := ParseEd25519Public(in)
		assert.Error(t, err, name)
	}
}

func TestParseEd25519Private(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	for name, in := range map[string][]byte{
		"pem":    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		"raw":    []byte(priv),
		"b64":    []byte(base64.StdEncoding.EncodeToString(priv) + "\n"),
		"b64sd":  []byte(base64.StdEncoding.EncodeToString(priv.Seed())),
		"pemraw": pem.EncodeToMemory(&pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: priv}),
	} {
		got, err := ParseEd25519Private(in)
		require.NoError(t, err, name)
		assert.Equal(t, priv, got, name)
	}

	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	rder, err := x509.MarshalPKCS8PrivateKey(rk)
	require.NoError(t, err)
	for name, in := range map[string][]byte{
		"empty":  nil,
		"short":  []byte(base64.StdEncoding.EncodeToString([]byte("abc"))),
		"notb64": []byte("!!!not base64!!!"),
		"rsa":    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rder}),
	} {
		_, err := ParseEd25519Private(in)
		assert.Error(t, err, name)
	}
}

func TestLoadFiles(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	dir := t.TempDir()
	pp := filepath.Join(dir, "pub")
	kp := filepath.Join(dir, "priv")
	require.NoError(t, os.WriteFile(pp, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o600))
	require.NoError(t, os.WriteFile(kp, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600))

	gp, err := LoadEd25519Public(pp)
	require.NoError(t, err)
	assert.Equal(t, pub, gp)
	gk, err := LoadEd25519Private(kp)
	require.NoError(t, err)
	assert.Equal(t, priv, gk)

	_, err = LoadEd25519Public(filepath.Join(dir, "missing"))
	assert.ErrorContains(t, err, "missing")
	_, err = LoadEd25519Private(filepath.Join(dir, "missing"))
	assert.Error(t, err)
}
