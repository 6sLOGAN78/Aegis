package identity

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"net/url"
	"strings"
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
		name        string
		uri         string
		trustDomain string
		expectedID  string
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
			expectedID:  "spiffe://aegis.local/workload/payments",
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
		Subject:  pkix.Name{CommonName: "orders.aegis.local"},
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

func TestSPIFFE_ParseSPIFFEID(t *testing.T) {
	long := "spiffe://aegis.local/" + string(make([]byte, 0)) + func() string {
		b := make([]byte, 130)
		for i := range b {
			b[i] = 'a'
		}
		return string(b)
	}()
	ok := []struct{ in, want string }{
		{"spiffe://aegis.local/workload/orders", "spiffe://aegis.local/workload/orders"},
		{"spiffe://AEGIS.LOCAL/workload/orders", "spiffe://aegis.local/workload/orders"},
	}
	for _, c := range ok {
		got, err := ParseSPIFFEID(c.in, "aegis.local")
		require.NoError(t, err, c.in)
		assert.Equal(t, c.want, got)
	}
	_, err := ParseSPIFFEID("spiffe://evil.local/workload/orders", "aegis.local")
	assert.ErrorIs(t, err, ErrInvalidTrustDomain)

	bad := []string{
		"spiffe://aegis.local", "spiffe://aegis.local/", "spiffe://aegis.local/a?x=1",
		"spiffe://aegis.local/a#f", "spiffe://aegis.local:8080/a", "spiffe://u@aegis.local/a",
		"spiffe://aegis.local/a%zz", "spiffe://aegis.local/a/../b", "spiffe://aegis.local/a//b",
		"spiffe://aegis.local/a/./b", "spiffe://aegis.local/a b", "spiffe://aegis.local/a%2541",
		long, "https://aegis.local/a", "", "workload/orders",
	}
	for _, in := range bad {
		_, err := ParseSPIFFEID(in, "aegis.local")
		require.Error(t, err, in)
		assert.ErrorIs(t, err, ErrMalformedSPIFFEURI, in)
	}
}

// TestSPIFFE_ExtractRejectsNonCanonical guards WR-01: a SPIFFE URI SAN that
// the control plane could never quarantine is rejected at authentication.
func TestSPIFFE_ExtractRejectsNonCanonical(t *testing.T) {
	for _, raw := range []string{
		"spiffe://u@aegis.local/workload/orders",
		"spiffe://aegis.local:8443/workload/orders",
		"spiffe://aegis.local/workload/orders?x=1",
		"spiffe://aegis.local/workload/orders#f",
		"spiffe://aegis.local/workload/or%20ders",
		"spiffe://aegis.local/workload/or%2Fders",
		"spiffe://aegis.local/workload//orders",
		"spiffe://aegis.local/workload/../admin",
		"spiffe://aegis.local/" + strings.Repeat("a", 130),
	} {
		cert := &x509.Certificate{URIs: []*url.URL{parseURI(raw)}}
		_, err := ExtractSPIFFEID(cert, "aegis.local")
		require.Error(t, err, raw)
		assert.ErrorIs(t, err, ErrMalformedSPIFFEURI, raw)
	}
}

// TestSPIFFE_ExtractAndParseAgree guards WR-01/CR-01: the gateway key
// (ExtractSPIFFEID on a peer cert) and the control plane's canonical form
// (ParseSPIFFEID on the operator string) must be byte-identical, and both
// must accept or reject the same ids.
func TestSPIFFE_ExtractAndParseAgree(t *testing.T) {
	for _, raw := range []string{
		"spiffe://aegis.local/workload/orders",
		"SPIFFE://aegis.local/workload/orders",
		"Spiffe://AEGIS.LOCAL/workload/orders",
		"spiffe://Aegis.Local/ns/gateway/sa/aegis-gateway",
		"spiffe://aegis.local/service/payments_v2.1-x",
		"spiffe://aegis.local/WorkLoad/Orders",
		"spiffe://evil.local/workload/orders",
		"spiffe://aegis.local",
		"spiffe://aegis.local/",
		"spiffe://u@aegis.local/workload/orders",
		"spiffe://aegis.local:8443/workload/orders",
		"spiffe://aegis.local/workload/orders?x=1",
		"spiffe://aegis.local/workload/orders#f",
		"spiffe://aegis.local/workload/or%2Fders",
		"spiffe://aegis.local/a/./b",
	} {
		cert := &x509.Certificate{URIs: []*url.URL{parseURI(raw)}}
		extracted, extErr := ExtractSPIFFEID(cert, "aegis.local")
		parsed, parseErr := ParseSPIFFEID(raw, "aegis.local")
		if parseErr != nil {
			assert.Error(t, extErr, "parse rejected %q but extract accepted it as %q", raw, extracted)
			continue
		}
		require.NoError(t, extErr, raw)
		assert.Equal(t, parsed, extracted, raw)
		assert.Equal(t, "quarantine:principal:"+parsed, "quarantine:principal:"+extracted, raw)
	}
}
