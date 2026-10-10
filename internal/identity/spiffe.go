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

// maxSPIFFEIDLen bounds a canonical SPIFFE id. It matches the control
// plane's principal id limit so every id the gateway accepts can be
// quarantined.
const maxSPIFFEIDLen = 128

// ExtractSPIFFEID extracts and validates the SPIFFE ID from an authenticated peer certificate.
// It verifies the URI scheme is "spiffe" and host matches the expected trust domain.
// Common Name (CN) is ignored completely, and ingress headers are not trusted.
//
// The returned id is canonicalised by the same strict rules as ParseSPIFFEID,
// so the gateway's quarantine key is byte-identical to the key the control
// plane writes for the same id. A SPIFFE URI SAN that fails strict parsing
// (userinfo, port, query, fragment, encoded or unusual path characters) is
// rejected at authentication (fail closed) rather than yielding an id that
// cannot be quarantined.
func ExtractSPIFFEID(peerCert *x509.Certificate, expectedTrustDomain string) (string, error) {
	if peerCert == nil {
		return "", ErrNoPeerCertificate
	}

	for _, uri := range peerCert.URIs {
		if uri == nil {
			continue
		}
		if strings.EqualFold(uri.Scheme, "spiffe") {
			return canonicalSPIFFE(uri, expectedTrustDomain, true)
		}
	}

	return "", ErrMissingSPIFFEID
}

// ParseSPIFFEID strictly validates a string-form SPIFFE ID against the trust
// domain and returns its canonical form (lowercase scheme and host). It is
// intended for operator-supplied identifiers (e.g. quarantine keys) and shares
// canonicalSPIFFE with ExtractSPIFFEID, so both yield the same value for the
// same id.
func ParseSPIFFEID(id, trustDomain string) (string, error) {
	if len(id) == 0 || len(id) > maxSPIFFEIDLen {
		return "", fmt.Errorf("%w: invalid length", ErrMalformedSPIFFEURI)
	}
	u, err := url.Parse(id)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMalformedSPIFFEURI, err)
	}
	return canonicalSPIFFE(u, trustDomain, false)
}

// canonicalSPIFFE is the single canonicalisation for SPIFFE ids. When
// allowAnyDomain is true an empty trustDomain skips the trust-domain check
// (ExtractSPIFFEID's historical contract); otherwise an empty trustDomain
// never matches.
func canonicalSPIFFE(u *url.URL, trustDomain string, allowAnyDomain bool) (string, error) {
	if !strings.EqualFold(u.Scheme, "spiffe") || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("%w: unexpected URI components", ErrMalformedSPIFFEURI)
	}
	if u.Host == "" || strings.ContainsAny(u.Host, ":@") {
		return "", fmt.Errorf("%w: invalid host", ErrMalformedSPIFFEURI)
	}
	if !(allowAnyDomain && trustDomain == "") && !strings.EqualFold(u.Host, trustDomain) {
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
	canon := "spiffe://" + strings.ToLower(u.Host) + u.Path
	if len(canon) > maxSPIFFEIDLen {
		return "", fmt.Errorf("%w: invalid length", ErrMalformedSPIFFEURI)
	}
	return canon, nil
}
