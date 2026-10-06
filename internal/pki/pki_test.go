package pki

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPKI_NewCA(t *testing.T) {
	ca, err := NewCA("Aegis Root CA")
	require.NoError(t, err)
	require.NotNil(t, ca)
	assert.NotNil(t, ca.Certificate)
	assert.NotNil(t, ca.PrivateKey)
	assert.NotNil(t, ca.CertPool)
	assert.True(t, ca.Certificate.IsCA)
	assert.Equal(t, "Aegis Root CA", ca.Certificate.Subject.CommonName)
	assert.Equal(t, []string{"Aegis Zero-Trust PKI"}, ca.Certificate.Subject.Organization)
	assert.Equal(t, x509.KeyUsageCertSign|x509.KeyUsageCRLSign|x509.KeyUsageDigitalSignature, ca.Certificate.KeyUsage)
}

func TestPKI_IssueWorkloadCert(t *testing.T) {
	ca, err := NewCA("Aegis Root CA")
	require.NoError(t, err)

	spiffeURI := "spiffe://aegis.local/workload/orders"
	clientCert, err := ca.IssueWorkloadCert(spiffeURI)
	require.NoError(t, err)
	require.NotEmpty(t, clientCert.Certificate)

	parsedCert, err := x509.ParseCertificate(clientCert.Certificate[0])
	require.NoError(t, err)

	assert.Equal(t, "/workload/orders", parsedCert.Subject.CommonName)
	assert.Equal(t, []string{"Aegis Workload"}, parsedCert.Subject.Organization)
	assert.Equal(t, x509.KeyUsageDigitalSignature, parsedCert.KeyUsage)
	assert.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, parsedCert.ExtKeyUsage)

	require.Len(t, parsedCert.URIs, 1)
	assert.Equal(t, spiffeURI, parsedCert.URIs[0].String())

	// Verify against CA pool
	chains, err := parsedCert.Verify(x509.VerifyOptions{
		Roots:     ca.CertPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, chains)
}

func TestPKI_IssueWorkloadCert_InvalidURI(t *testing.T) {
	ca, err := NewCA("Aegis Root CA")
	require.NoError(t, err)

	_, err = ca.IssueWorkloadCert(":% invalid url")
	assert.Error(t, err)
}

func TestPKI_IssueServerCert(t *testing.T) {
	ca, err := NewCA("Aegis Root CA")
	require.NoError(t, err)

	dnsNames := []string{"localhost", "gateway.aegis.local"}
	ipAddresses := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}

	serverCert, err := ca.IssueServerCert("gateway.aegis.local", dnsNames, ipAddresses)
	require.NoError(t, err)
	require.NotEmpty(t, serverCert.Certificate)

	parsedCert, err := x509.ParseCertificate(serverCert.Certificate[0])
	require.NoError(t, err)

	assert.Equal(t, "gateway.aegis.local", parsedCert.Subject.CommonName)
	assert.Equal(t, []string{"Aegis Service"}, parsedCert.Subject.Organization)
	assert.Equal(t, dnsNames, parsedCert.DNSNames)
	require.Len(t, parsedCert.IPAddresses, 2)
	assert.True(t, ipAddresses[0].Equal(parsedCert.IPAddresses[0]))
	assert.True(t, ipAddresses[1].Equal(parsedCert.IPAddresses[1]))
	assert.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, parsedCert.ExtKeyUsage)

	chains, err := parsedCert.Verify(x509.VerifyOptions{
		Roots:     ca.CertPool,
		DNSName:   "gateway.aegis.local",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, chains)
}

func TestPKI_PEMEncoding(t *testing.T) {
	ca, err := NewCA("Aegis Root CA")
	require.NoError(t, err)

	certPEM := EncodeCertPEM(ca.Certificate.Raw)
	require.NotEmpty(t, certPEM)

	block, rest := pem.Decode(certPEM)
	require.NotNil(t, block)
	assert.Empty(t, rest)
	assert.Equal(t, "CERTIFICATE", block.Type)
	assert.Equal(t, ca.Certificate.Raw, block.Bytes)

	keyPEM, err := EncodeKeyPEM(ca.PrivateKey)
	require.NoError(t, err)
	require.NotEmpty(t, keyPEM)

	keyBlock, restKey := pem.Decode(keyPEM)
	require.NotNil(t, keyBlock)
	assert.Empty(t, restKey)
	assert.Equal(t, "EC PRIVATE KEY", keyBlock.Type)

	parsedKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
	assert.Equal(t, ca.PrivateKey.D, parsedKey.D)
}

func TestPKI_MutualTLSHandshake(t *testing.T) {
	startTime := time.Now()

	ca, err := NewCA("Aegis Handshake Root CA")
	require.NoError(t, err)

	serverCert, err := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	workloadCert, err := ca.IssueWorkloadCert("spiffe://aegis.local/workload/test-client")
	require.NoError(t, err)

	// Configure server requiring client cert
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotNil(t, r.TLS)
		require.NotEmpty(t, r.TLS.PeerCertificates)
		peer := r.TLS.PeerCertificates[0]
		require.Len(t, peer.URIs, 1)
		assert.Equal(t, "spiffe://aegis.local/workload/test-client", peer.URIs[0].String())

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("handshake-ok"))
	}))

	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.CertPool,
		MinVersion:   tls.VersionTLS13,
	}
	ts.StartTLS()
	defer ts.Close()

	// Configure client with workload certificate and CA pool
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{workloadCert},
				RootCAs:      ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
	}

	resp, err := client.Get(ts.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "handshake-ok", string(body))

	elapsed := time.Since(startTime)
	t.Logf("Total PKI setup and mTLS handshake duration: %s", elapsed)
}
