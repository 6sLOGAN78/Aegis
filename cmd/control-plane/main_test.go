package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aegis/internal/testpki"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadGRPCCredentialsMissingPaths(t *testing.T) {
	full := map[string]string{
		"AEGIS_GRPC_TLS_CERT_PATH": "a", "AEGIS_GRPC_TLS_KEY_PATH": "b", "AEGIS_GRPC_CLIENT_CA_PATH": "c",
	}
	if _, err := loadGRPCCredentials(envFrom(nil)); err == nil {
		t.Fatal("expected error with no env")
	}
	for k := range full {
		m := map[string]string{}
		for kk, v := range full {
			if kk != k {
				m[kk] = v
			}
		}
		if _, err := loadGRPCCredentials(envFrom(m)); err == nil {
			t.Fatalf("expected error when %s empty", k)
		}
	}
	if _, err := loadGRPCCredentials(envFrom(full)); err == nil {
		t.Fatal("expected error for unreadable files")
	}
}

func TestLoadGRPCCredentialsValid(t *testing.T) {
	dir := t.TempDir()
	testpki.New(t).WriteFiles(dir, "gateway-1")
	creds, err := loadGRPCCredentials(envFrom(map[string]string{
		"AEGIS_GRPC_TLS_CERT_PATH":  filepath.Join(dir, "server.crt"),
		"AEGIS_GRPC_TLS_KEY_PATH":   filepath.Join(dir, "server.key"),
		"AEGIS_GRPC_CLIENT_CA_PATH": filepath.Join(dir, "ca.crt"),
	}))
	if err != nil || creds == nil {
		t.Fatalf("creds=%v err=%v", creds, err)
	}
}

func TestLoadSigningKeyFromPath(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemPath := filepath.Join(dir, "k.pem")
	if err := os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	b64Path := filepath.Join(dir, "k.b64")
	if err := os.WriteFile(b64Path, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{pemPath, b64Path} {
		k, used, err := loadSigningKey(envFrom(map[string]string{"AEGIS_SIGNING_KEY_PATH": p}))
		if err != nil || used || !k.Equal(priv) {
			t.Fatalf("path %s: used=%v err=%v", p, used, err)
		}
	}
}

func TestLoadSigningKeyInvalidPathIsFatal(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(bad, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "missing"), bad} {
		// Seed must NOT be used as a fallback when a path is configured.
		_, _, err := loadSigningKey(envFrom(map[string]string{
			"AEGIS_SIGNING_KEY_PATH": p, "AEGIS_SIGNING_KEY_SEED": strings.Repeat("s", 32),
		}))
		if err == nil {
			t.Fatalf("expected error for %s", p)
		}
	}
}

func TestLoadSigningKeySeedAndDemo(t *testing.T) {
	seed := strings.Repeat("s", 32)
	k, used, err := loadSigningKey(envFrom(map[string]string{"AEGIS_SIGNING_KEY_SEED": seed}))
	if err != nil || used || !k.Equal(ed25519.NewKeyFromSeed([]byte(seed))) {
		t.Fatalf("seed: used=%v err=%v", used, err)
	}
	k, used, err = loadSigningKey(envFrom(nil))
	if err != nil || !used || !k.Equal(ed25519.NewKeyFromSeed(defaultDemoSeed)) {
		t.Fatalf("demo: used=%v err=%v", used, err)
	}
}

func TestRequirePostgresFailureIsError(t *testing.T) {
	boom := errors.New("boom")
	_, err := requirePostgres(context.Background(), func(context.Context) (*int, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	v := 1
	p, err := requirePostgres(context.Background(), func(context.Context) (*int, error) { return &v, nil })
	if err != nil || p != &v {
		t.Fatalf("p=%v err=%v", p, err)
	}
}

func TestMainWiring(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "grpc.NewServer()") {
		t.Fatal("bare grpc.NewServer() forbidden; mTLS creds required")
	}
	for _, want := range []string{
		"grpc.Creds(", "NewSecurityState(", "SetSecurityState(", "SetSecurityRepo(",
		"go securityState.Run(", "distServer.SetMetrics(", "WithPinger(", "WithSecurityStateMetrics(",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("main.go missing %q", want)
		}
	}
	recon := strings.Index(src, ".Reconstruct(")
	if recon < 0 {
		t.Fatal("Reconstruct call missing")
	}
	for _, l := range []string{"ListenAndServe(", ".Serve("} {
		if i := strings.Index(src, l); i < 0 || i < recon {
			t.Fatalf("%s must appear after Reconstruct (recon=%d, idx=%d)", l, recon, i)
		}
	}
	if !strings.Contains(src, "log.Fatalf(\"security state reconstruction failed") {
		t.Fatal("Reconstruct failure must be fatal")
	}
}

func TestMainHasNoInMemoryFallback(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "in-memory state") {
		t.Fatal("in-memory fallback text still present")
	}
}
