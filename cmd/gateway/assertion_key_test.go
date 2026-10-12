package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAssertionKey(t *testing.T) {
	noEnv := func(string) string { return "" }
	_, want, _ := ed25519.GenerateKey(nil)

	t.Run("demo key only when nothing is configured", func(t *testing.T) {
		key, usedDemo, err := loadAssertionKey("", noEnv)
		require.NoError(t, err)
		assert.True(t, usedDemo)
		assert.Equal(t, ed25519.NewKeyFromSeed(defaultDemoSeed), key)
	})

	t.Run("valid path is loaded", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "assertion.key")
		require.NoError(t, os.WriteFile(p, []byte(base64.StdEncoding.EncodeToString(want)), 0o600))
		key, usedDemo, err := loadAssertionKey(p, noEnv)
		require.NoError(t, err)
		assert.False(t, usedDemo)
		assert.Equal(t, want, key)
	})

	t.Run("configured but unreadable path is fatal", func(t *testing.T) {
		_, _, err := loadAssertionKey(filepath.Join(t.TempDir(), "missing.key"), noEnv)
		require.Error(t, err)
	})

	t.Run("configured but malformed path is fatal", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "bad.key")
		require.NoError(t, os.WriteFile(p, []byte("not a key"), 0o600))
		_, _, err := loadAssertionKey(p, noEnv)
		require.Error(t, err)
	})

	t.Run("malformed env var is fatal", func(t *testing.T) {
		_, _, err := loadAssertionKey("", func(k string) string {
			if k == "AEGIS_ASSERTION_PRIVATE_KEY" {
				return "!!!"
			}
			return ""
		})
		require.Error(t, err)
	})

	t.Run("valid env var is loaded", func(t *testing.T) {
		key, usedDemo, err := loadAssertionKey("", func(k string) string {
			if k == "AEGIS_ASSERTION_PRIVATE_KEY" {
				return base64.StdEncoding.EncodeToString(want)
			}
			return ""
		})
		require.NoError(t, err)
		assert.False(t, usedDemo)
		assert.Equal(t, want, key)
	})
}
