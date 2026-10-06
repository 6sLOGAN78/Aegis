package identity

import (
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNoPeerCertificate  = errors.New("no peer certificate presented")
	ErrMissingSPIFFEID    = errors.New("certificate does not contain a SPIFFE URI SAN")
	ErrInvalidTrustDomain = errors.New("certificate SPIFFE trust domain mismatch")
	ErrMalformedSPIFFEURI = errors.New("malformed SPIFFE URI structure")
)

// ExtractSPIFFEID extracts and validates the SPIFFE ID from an authenticated peer certificate.
// It verifies the URI scheme is "spiffe" and host matches the expected trust domain.
// Common Name (CN) is ignored completely, and ingress headers are not trusted.
func ExtractSPIFFEID(peerCert *x509.Certificate, expectedTrustDomain string) (string, error) {
	if peerCert == nil {
		return "", ErrNoPeerCertificate
	}

	for _, uri := range peerCert.URIs {
		if uri == nil {
			continue
		}
		if strings.EqualFold(uri.Scheme, "spiffe") {
			if expectedTrustDomain != "" && !strings.EqualFold(uri.Host, expectedTrustDomain) {
				return "", fmt.Errorf("%w: got %q, expected %q", ErrInvalidTrustDomain, uri.Host, expectedTrustDomain)
			}
			if uri.Path == "" || uri.Path == "/" {
				return "", ErrMalformedSPIFFEURI
			}
			return uri.String(), nil
		}
	}

	return "", ErrMissingSPIFFEID
}
