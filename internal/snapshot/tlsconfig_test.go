package snapshot

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aegis/internal/testpki"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCredentials_NewClientTransportCredentials(t *testing.T) {
	pki := testpki.New(t)
	dir := t.TempDir()
	pki.WriteFiles(dir, "gateway-1")
	crt, key, ca := filepath.Join(dir, "cp-client-gateway-1.crt"), filepath.Join(dir, "cp-client-gateway-1.key"), filepath.Join(dir, "ca.crt")

	creds, err := NewClientTransportCredentials(crt, key, ca, testpki.ServerName)
	require.NoError(t, err)
	assert.Equal(t, testpki.ServerName, creds.Info().ServerName)

	_, err = NewClientTransportCredentials("", "", "", "")
	assert.Error(t, err)
	_, err = NewClientTransportCredentials(filepath.Join(dir, "missing.crt"), key, ca, testpki.ServerName)
	assert.Error(t, err)
	bad := filepath.Join(dir, "bad.pem")
	require.NoError(t, os.WriteFile(bad, []byte("garbage"), 0o600))
	_, err = NewClientTransportCredentials(crt, key, bad, testpki.ServerName)
	assert.Error(t, err)
}

func TestCredentials_GatewayIDFromClientCert(t *testing.T) {
	pki := testpki.New(t)
	dir := t.TempDir()
	pki.WriteFiles(dir, "gateway-7")

	id, err := GatewayIDFromClientCert(filepath.Join(dir, "cp-client-gateway-7.crt"), filepath.Join(dir, "cp-client-gateway-7.key"))
	require.NoError(t, err)
	assert.Equal(t, "gateway-7", id)

	// The server cert carries no SPIFFE SAN, so it is not a gateway identity.
	_, err = GatewayIDFromClientCert(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"))
	assert.Error(t, err)

	_, err = GatewayIDFromClientCert(filepath.Join(dir, "none.crt"), filepath.Join(dir, "none.key"))
	assert.Error(t, err)
}

func TestCredentials_NoInsecureCredentialsInProductionCode(t *testing.T) {
	needle := "insecure." + "NewCredentials"
	for _, root := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			assert.NotContains(t, string(b), needle, p)
			return nil
		})
		require.NoError(t, err)
	}
}
