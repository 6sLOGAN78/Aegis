package main

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"aegis/internal/identity"
)

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		t.Fatalf("no PEM in %s", path)
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestRunReusesCAAndReissuesLeaves(t *testing.T) {
	out := t.TempDir()
	if err := run(out, ""); err != nil {
		t.Fatal(err)
	}
	ca1, _ := os.ReadFile(filepath.Join(out, "root-ca.crt"))
	leaf1, _ := os.ReadFile(filepath.Join(out, "orders.crt"))
	if err := run(out, ""); err != nil {
		t.Fatal(err)
	}
	ca2, _ := os.ReadFile(filepath.Join(out, "root-ca.crt"))
	leaf2, _ := os.ReadFile(filepath.Join(out, "orders.crt"))
	if !bytes.Equal(ca1, ca2) {
		t.Fatal("root CA changed on second run")
	}
	if bytes.Equal(leaf1, leaf2) {
		t.Fatal("leaf cert was not re-issued")
	}
	pool := x509.NewCertPool()
	pool.AddCert(readCert(t, filepath.Join(out, "root-ca.crt")))
	for _, n := range []string{"orders", "admin", "payments", "gateway-server", "gateway-client", "workload-orders", "control-plane-server", "cp-client-gateway-1"} {
		c := readCert(t, filepath.Join(out, n+".crt"))
		if _, err := c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			t.Fatalf("%s does not verify: %v", n, err)
		}
	}
}

func TestControlPlaneCerts(t *testing.T) {
	out := t.TempDir()
	if err := run(out, ""); err != nil {
		t.Fatal(err)
	}
	srv := readCert(t, filepath.Join(out, "control-plane-server.crt"))
	want := []string{"control-plane", "aegis-control-plane", "aegis-control-plane.aegis-system.svc", "localhost"}
	if len(srv.DNSNames) != len(want) {
		t.Fatalf("dns sans %v", srv.DNSNames)
	}
	for i, d := range want {
		if srv.DNSNames[i] != d {
			t.Fatalf("dns sans %v", srv.DNSNames)
		}
	}
	if len(srv.IPAddresses) != 1 || srv.IPAddresses[0].String() != "127.0.0.1" {
		t.Fatalf("ip sans %v", srv.IPAddresses)
	}
	if len(srv.ExtKeyUsage) != 1 || srv.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("server eku %v", srv.ExtKeyUsage)
	}
	if mode(t, filepath.Join(out, "control-plane-server.key")) != 0600 {
		t.Fatal("server key mode")
	}
	if len(cpClientIDs) != 7 {
		t.Fatal("expected 7 ids")
	}
	for _, id := range cpClientIDs {
		c := readCert(t, filepath.Join(out, "cp-client-"+id+".crt"))
		if len(c.URIs) != 1 || c.URIs[0].String() != identity.GatewaySPIFFEID(id) {
			t.Fatalf("%s uri sans %v", id, c.URIs)
		}
		if len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
			t.Fatalf("%s eku %v", id, c.ExtKeyUsage)
		}
		if mode(t, filepath.Join(out, "cp-client-"+id+".key")) != 0600 {
			t.Fatalf("%s key mode", id)
		}
		if mode(t, filepath.Join(out, "cp-client-"+id+".crt")) != 0644 {
			t.Fatalf("%s crt mode", id)
		}
	}
}

func TestExistingOutputsPresent(t *testing.T) {
	out := t.TempDir()
	if err := run(out, ""); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"admin.crt", "admin.key", "orders.crt", "payments.key", "gateway-client.crt", "gateway-server.key", "workload-orders.crt", "assertion-ed25519.key", "assertion-ed25519.pub"} {
		if _, err := os.Stat(filepath.Join(out, n)); err != nil {
			t.Fatalf("missing %s", n)
		}
	}
}

func decodeLen(t *testing.T, p string, n int) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("%s missing trailing newline", p)
	}
	d, err := base64.StdEncoding.DecodeString(string(b[:len(b)-1]))
	if err != nil || len(d) != n {
		t.Fatalf("%s: bad base64 or length %d", p, len(d))
	}
	return b
}

func TestKeysDir(t *testing.T) {
	out, keys := t.TempDir(), t.TempDir()
	if err := run(out, keys); err != nil {
		t.Fatal(err)
	}
	decodeLen(t, filepath.Join(keys, "jwt-current.key"), 64)
	decodeLen(t, filepath.Join(keys, "snapshot-current.key"), 64)
	jp := decodeLen(t, filepath.Join(keys, "keyset-jwt/current.pub"), 32)
	sp := decodeLen(t, filepath.Join(keys, "keyset-snapshot/current.pub"), 32)
	if bytes.Equal(jp, sp) {
		t.Fatal("jwt and snapshot pubkeys identical")
	}
	if mode(t, filepath.Join(keys, "jwt-current.key")) != 0600 || mode(t, filepath.Join(keys, "snapshot-current.key")) != 0600 {
		t.Fatal("private key mode")
	}
	if mode(t, filepath.Join(keys, "keyset-jwt/current.pub")) != 0644 {
		t.Fatal("pub mode")
	}
	if err := run(out, keys); err != nil {
		t.Fatal(err)
	}
	jp2, _ := os.ReadFile(filepath.Join(keys, "keyset-jwt/current.pub"))
	if !bytes.Equal(jp, jp2) {
		t.Fatal("existing key overwritten")
	}
}

func TestNoKeysWhenKeysDirEmpty(t *testing.T) {
	out := t.TempDir()
	if err := run(out, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "jwt-current.key")); err == nil {
		t.Fatal("unexpected jwt key")
	}
}

func TestWriteKeypair(t *testing.T) {
	d := t.TempDir()
	pre := filepath.Join(d, "x")
	if err := writeEd25519(pre+".key", pre+".pub"); err != nil {
		t.Fatal(err)
	}
	decodeLen(t, pre+".key", 64)
	decodeLen(t, pre+".pub", 32)
	if mode(t, pre+".key") != 0600 {
		t.Fatal("mode")
	}
}
