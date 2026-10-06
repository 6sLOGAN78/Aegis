package identity

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseURI(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func TestSPIFFE_ExtractValid(t *testing.T) {
	tests := []struct {
		name          string
		uri           string
		trustDomain   string
		expectedID    string
	}{
		{
			name:        "standard workload URI",
			uri:         "spiffe://aegis.local/workload/orders",
			trustDomain: "aegis.local",
			expectedID:  "spiffe://aegis.local/workload/orders",
		},
		{
			name:        "gateway service account URI",
			uri:         "spiffe://aegis.local/ns/gateway/sa/aegis-gateway",
			trustDomain: "aegis.local",
			expectedID:  "spiffe://aegis.local/ns/gateway/sa/aegis-gateway",
		},
		{
			name:        "case-insensitive scheme and domain",
			uri:         "SPIFFE://AEGIS.LOCAL/workload/payments",
			trustDomain: "aegis.local",
			expectedID:  "spiffe://AEGIS.LOCAL/workload/payments",
		},
		{
			name:        "empty expected trust domain skips check",
			uri:         "spiffe://custom.domain/workload/orders",
			trustDomain: "",
			expectedID:  "spiffe://custom.domain/workload/orders",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cert := &x509.Certificate{
				URIs: []*url.URL{parseURI(tc.uri)},
			}
			id, err := ExtractSPIFFEID(cert, tc.trustDomain)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedID, id)
		})
	}
}

func TestSPIFFE_NilCert(t *testing.T) {
	_, err := ExtractSPIFFEID(nil, "aegis.local")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoPeerCertificate)
}

func TestSPIFFE_MissingURI(t *testing.T) {
	cert := &x509.Certificate{
		Subject: pkix.Name{CommonName: "orders.aegis.local"},
		DNSNames: []string{"orders.aegis.local"},
		URIs:     []*url.URL{},
	}
	_, err := ExtractSPIFFEID(cert, "aegis.local")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingSPIFFEID)
}

func TestSPIFFE_InvalidScheme(t *testing.T) {
	cert := &x509.Certificate{
		URIs: []*url.URL{
			parseURI("https://aegis.local/workload/orders"),
			parseURI("urn:example:orders"),
		},
	}
	_, err := ExtractSPIFFEID(cert, "aegis.local")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingSPIFFEID)
}

func TestSPIFFE_TrustDomainMismatch(t *testing.T) {
	cert := &x509.Certificate{
		URIs: []*url.URL{parseURI("spiffe://evil.com/workload/orders")},
	}
	_, err := ExtractSPIFFEID(cert, "aegis.local")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTrustDomain)
	assert.Contains(t, err.Error(), `got "evil.com", expected "aegis.local"`)
}

func TestSPIFFE_MalformedPath(t *testing.T) {
	malformed := []string{
		"spiffe://aegis.local",
		"spiffe://aegis.local/",
	}

	for _, uriStr := range malformed {
		t.Run(uriStr, func(t *testing.T) {
			cert := &x509.Certificate{
				URIs: []*url.URL{parseURI(uriStr)},
			}
			_, err := ExtractSPIFFEID(cert, "aegis.local")
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrMalformedSPIFFEURI)
		})
	}
}

func TestSPIFFE_IgnoresCommonName(t *testing.T) {
	// A certificate with a spoofed Common Name but no SPIFFE URI SAN must be rejected
	cert := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "spiffe://aegis.local/workload/admin",
		},
		URIs: []*url.URL{},
	}
	_, err := ExtractSPIFFEID(cert, "aegis.local")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingSPIFFEID)
}

func TestSPIFFE_MultipleURIs(t *testing.T) {
	// Non-SPIFFE URIs followed by a valid SPIFFE URI
	cert := &x509.Certificate{
		URIs: []*url.URL{
			parseURI("https://internal.local/orders"),
			parseURI("spiffe://aegis.local/workload/orders"),
		},
	}
	id, err := ExtractSPIFFEID(cert, "aegis.local")
	require.NoError(t, err)
	assert.Equal(t, "spiffe://aegis.local/workload/orders", id)
}
