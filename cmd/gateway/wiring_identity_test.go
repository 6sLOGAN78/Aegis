package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"aegis/internal/testpki"
)

func idEnv(dir, id string, extra map[string]string) func(string) string {
	m := map[string]string{
		"AEGIS_CP_CLIENT_CERT_PATH":     filepath.Join(dir, "cp-client-"+id+".crt"),
		"AEGIS_CP_CLIENT_KEY_PATH":      filepath.Join(dir, "cp-client-"+id+".key"),
		"AEGIS_CP_CA_PATH":              filepath.Join(dir, "ca.crt"),
		"AEGIS_CONTROL_PLANE_GRPC_ADDR": "controlplane.aegis.local:9090",
	}
	for k, v := range extra {
		m[k] = v
	}
	return kwGetenv(m)
}

func TestIdentityDerivedFromCert(t *testing.T) {
	dir := t.TempDir()
	testpki.New(t).WriteFiles(dir, "gw-7")

	id, creds, err := resolveGatewayIdentity(idEnv(dir, "gw-7", nil))
	require.NoError(t, err)
	require.Equal(t, "gw-7", id)
	require.NotNil(t, creds)
	require.Equal(t, "controlplane.aegis.local", creds.Info().ServerName)

	id, creds, err = resolveGatewayIdentity(idEnv(dir, "gw-7", map[string]string{"AEGIS_GATEWAY_ID": "gw-7", "AEGIS_CP_SERVER_NAME": "cp.internal"}))
	require.NoError(t, err)
	require.Equal(t, "gw-7", id)
	require.Equal(t, "cp.internal", creds.Info().ServerName)
}

func TestIdentityEnvMismatchFatal(t *testing.T) {
	dir := t.TempDir()
	testpki.New(t).WriteFiles(dir, "gw-7")
	_, _, err := resolveGatewayIdentity(idEnv(dir, "gw-7", map[string]string{"AEGIS_GATEWAY_ID": "gw-other"}))
	require.ErrorContains(t, err, "does not match")
}

func TestIdentityMissingPathsNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	testpki.New(t).WriteFiles(dir, "gw-7")
	for _, k := range []string{"AEGIS_CP_CLIENT_CERT_PATH", "AEGIS_CP_CLIENT_KEY_PATH", "AEGIS_CP_CA_PATH"} {
		_, creds, err := resolveGatewayIdentity(idEnv(dir, "gw-7", map[string]string{k: ""}))
		require.Error(t, err, k)
		require.Nil(t, creds)
	}
}

func TestIdentityWrongShapeCert(t *testing.T) {
	dir := t.TempDir()
	// server.crt carries no /gw/<id> SPIFFE URI.
	testpki.New(t).WriteFiles(dir, "gw-7")
	env := idEnv(dir, "gw-7", map[string]string{
		"AEGIS_CP_CLIENT_CERT_PATH": filepath.Join(dir, "server.crt"),
		"AEGIS_CP_CLIENT_KEY_PATH":  filepath.Join(dir, "server.key"),
	})
	_, _, err := resolveGatewayIdentity(env)
	require.Error(t, err)
}
