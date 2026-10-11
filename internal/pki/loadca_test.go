package pki

import (
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCA_RoundTrip(t *testing.T) {
	ca, err := NewCA("Aegis Root CA")
	require.NoError(t, err)
	keyPEM, err := EncodeKeyPEM(ca.PrivateKey)
	require.NoError(t, err)

	loaded, err := LoadCA(EncodeCertPEM(ca.Certificate.Raw), keyPEM)
	require.NoError(t, err)

	cert, err := loaded.IssueWorkloadCert("spiffe://aegis.local/ns/x/sa/y")
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:     loaded.CertPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	require.NoError(t, err)
}

func TestLoadCA_Errors(t *testing.T) {
	ca, err := NewCA("Aegis Root CA")
	require.NoError(t, err)
	keyPEM, err := EncodeKeyPEM(ca.PrivateKey)
	require.NoError(t, err)
	certPEM := EncodeCertPEM(ca.Certificate.Raw)

	_, err = LoadCA([]byte("garbage"), []byte("garbage"))
	assert.Error(t, err)
	_, err = LoadCA(certPEM, nil)
	assert.Error(t, err)
	_, err = LoadCA(certPEM, []byte("garbage"))
	assert.Error(t, err)

	leaf, err := ca.IssueServerCert("srv", []string{"srv"}, nil)
	require.NoError(t, err)
	_, err = LoadCA(EncodeCertPEM(leaf.Certificate[0]), keyPEM)
	assert.Error(t, err)
}
