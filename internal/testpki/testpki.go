// Package testpki provides a shared in-memory PKI for mTLS tests over bufconn.
//
// Spike result (see testpki_test.go): both grpc.DialContext with
// grpc.WithContextDialer and grpc.NewClient("passthrough:///bufnet", ...)
// work with TLS credentials over bufconn when ServerName is set. Production
// code keeps DialContext; NewClient connects lazily.
package testpki

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/credentials"

	"aegis/internal/identity"
	"aegis/internal/pki"
)

// ServerName is the TLS server name clients must use.
const ServerName = "control-plane"

// PKI bundles a root CA, a server certificate and an independent foreign CA.
type PKI struct {
	CA      *pki.CA
	Foreign *pki.CA
	Server  tls.Certificate

	t testing.TB
}

// New builds a fresh test PKI.
func New(t testing.TB) *PKI {
	t.Helper()
	ca, err := pki.NewCA("Aegis Test Root CA")
	if err != nil {
		t.Fatalf("testpki: new CA: %v", err)
	}
	foreign, err := pki.NewCA("Foreign Test CA")
	if err != nil {
		t.Fatalf("testpki: new foreign CA: %v", err)
	}
	srv, err := ca.IssueServerCert(ServerName, []string{ServerName, "aegis-control-plane", "localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("testpki: server cert: %v", err)
	}
	return &PKI{CA: ca, Foreign: foreign, Server: srv, t: t}
}

// ServerCreds returns server credentials requiring and verifying client certs.
func (p *PKI) ServerCreds() credentials.TransportCredentials {
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{p.Server},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    p.CA.CertPool,
		MinVersion:   tls.VersionTLS13,
	})
}

func (p *PKI) client(ca *pki.CA, spiffe string) credentials.TransportCredentials {
	cfg := &tls.Config{
		RootCAs:    p.CA.CertPool,
		ServerName: ServerName,
		MinVersion: tls.VersionTLS13,
	}
	if ca != nil {
		cert, err := ca.IssueWorkloadCert(spiffe)
		if err != nil {
			p.t.Fatalf("testpki: workload cert: %v", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return credentials.NewTLS(cfg)
}

// ClientCreds returns client credentials for the per-replica gateway identity.
func (p *PKI) ClientCreds(gatewayID string) credentials.TransportCredentials {
	p.t.Helper()
	return p.client(p.CA, identity.GatewaySPIFFEID(gatewayID))
}

// ClientCredsForSPIFFE returns client credentials for an arbitrary SPIFFE id.
func (p *PKI) ClientCredsForSPIFFE(uri string) credentials.TransportCredentials {
	p.t.Helper()
	return p.client(p.CA, uri)
}

// ForeignClientCreds returns credentials whose client cert is issued by a
// foreign CA while still trusting the real root for the server.
func (p *PKI) ForeignClientCreds(gatewayID string) credentials.TransportCredentials {
	p.t.Helper()
	return p.client(p.Foreign, identity.GatewaySPIFFEID(gatewayID))
}

// NoCertClientCreds returns credentials that present no client certificate.
func (p *PKI) NoCertClientCreds() credentials.TransportCredentials {
	return p.client(nil, "")
}

// RootPool returns the root CA pool.
func (p *PKI) RootPool() *x509.CertPool { return p.CA.CertPool }

// WriteFiles writes ca.crt, server.crt, server.key and cp-client-<id>.crt/key
// for each gateway id into dir.
func (p *PKI) WriteFiles(dir string, gatewayIDs ...string) {
	t := p.t
	t.Helper()
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("testpki: write %s: %v", name, err)
		}
	}
	write("ca.crt", pki.EncodeCertPEM(p.CA.Certificate.Raw))
	writeCert := func(prefix string, c tls.Certificate) {
		write(prefix+".crt", pki.EncodeCertPEM(c.Certificate[0]))
		key, err := pki.EncodeKeyPEM(c.PrivateKey.(*ecdsa.PrivateKey))
		if err != nil {
			t.Fatalf("testpki: marshal key: %v", err)
		}
		write(prefix+".key", key)
	}
	writeCert("server", p.Server)
	for _, id := range gatewayIDs {
		c, err := p.CA.IssueWorkloadCert(identity.GatewaySPIFFEID(id))
		if err != nil {
			t.Fatalf("testpki: client cert: %v", err)
		}
		writeCert("cp-client-"+id, c)
	}
}
