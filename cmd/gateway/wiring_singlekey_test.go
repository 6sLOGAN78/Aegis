package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func singleKeyEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// WR-04: a configured but malformed single-key variable is fatal; the demo
// key is used only when the variable is unset.
func TestLoadSingleKeys(t *testing.T) {
	demo := ed25519.NewKeyFromSeed(defaultDemoSeed).Public().(ed25519.PublicKey)

	iss, cp, err := loadSingleKeys(singleKeyEnv(nil))
	require.NoError(t, err)
	require.True(t, iss.Equal(demo))
	require.True(t, cp.Equal(demo))

	pubA, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pubB, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	a := base64.StdEncoding.EncodeToString(pubA)
	b := base64.StdEncoding.EncodeToString(pubB)

	iss, cp, err = loadSingleKeys(singleKeyEnv(map[string]string{"AEGIS_ISSUER_PUBLIC_KEY": a}))
	require.NoError(t, err)
	require.True(t, iss.Equal(pubA))
	require.True(t, cp.Equal(pubA), "control-plane key defaults to the issuer key")

	iss, cp, err = loadSingleKeys(singleKeyEnv(map[string]string{"AEGIS_ISSUER_PUBLIC_KEY": a, "AEGIS_CONTROL_PLANE_PUBLIC_KEY": b}))
	require.NoError(t, err)
	require.True(t, iss.Equal(pubA))
	require.True(t, cp.Equal(pubB))

	short := base64.StdEncoding.EncodeToString(pubA[:16])
	for _, bad := range []string{"not-base64!!", short, "  "} {
		_, _, err = loadSingleKeys(singleKeyEnv(map[string]string{"AEGIS_ISSUER_PUBLIC_KEY": bad}))
		require.Error(t, err, "issuer %q", bad)
		_, _, err = loadSingleKeys(singleKeyEnv(map[string]string{"AEGIS_ISSUER_PUBLIC_KEY": a, "AEGIS_CONTROL_PLANE_PUBLIC_KEY": bad}))
		require.Error(t, err, "control plane %q", bad)
	}
}
