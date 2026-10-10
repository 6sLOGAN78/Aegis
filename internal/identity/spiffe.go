package identity

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
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

// ParseSPIFFEID strictly validates a string-form SPIFFE ID against the trust
// domain and returns its canonical form (lowercased host). It is intended for
// operator-supplied identifiers (e.g. quarantine keys) so they match the value
// ExtractSPIFFEID yields from peer certificates.
func ParseSPIFFEID(id, trustDomain string) (string, error) {
	if len(id) == 0 || len(id) > 128 {
		return "", fmt.Errorf("%w: invalid length", ErrMalformedSPIFFEURI)
	}
	u, err := url.Parse(id)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMalformedSPIFFEURI, err)
	}
	if u.Scheme != "spiffe" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("%w: unexpected URI components", ErrMalformedSPIFFEURI)
	}
	if strings.ContainsAny(u.Host, ":@") {
		return "", fmt.Errorf("%w: invalid host", ErrMalformedSPIFFEURI)
	}
	if !strings.EqualFold(u.Host, trustDomain) {
		return "", fmt.Errorf("%w: got %q, expected %q", ErrInvalidTrustDomain, u.Host, trustDomain)
	}
	if u.Path == "" || u.Path == "/" || u.RawPath != "" {
		return "", fmt.Errorf("%w: missing or encoded path", ErrMalformedSPIFFEURI)
	}
	for _, seg := range strings.Split(u.Path[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: invalid path segment", ErrMalformedSPIFFEURI)
		}
	}
	for _, c := range u.Path {
		ok := c == '/' || c == '.' || c == '_' || c == '-' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !ok {
			return "", fmt.Errorf("%w: invalid path character", ErrMalformedSPIFFEURI)
		}
	}
	return "spiffe://" + strings.ToLower(u.Host) + u.Path, nil
}
