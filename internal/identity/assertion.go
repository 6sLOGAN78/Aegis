package identity

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	AssertionIssuer   = "aegis-gateway"
	AssertionLifetime = 15 * time.Second
	ClockSkewLeeway   = 5 * time.Second
)

var (
	ErrMissingAssertionToken = errors.New("missing assertion token")
	ErrInvalidAssertionToken = errors.New("invalid assertion token")
)

// AssertionClaims defines claims embedded in gateway-to-backend assertions.
type AssertionClaims struct {
	jwt.RegisteredClaims
	Method        string   `json:"method"`
	Path          string   `json:"path"`
	RequestID     string   `json:"req_id"`
	PrincipalKind string   `json:"principal_kind"`
	Roles         []string `json:"roles,omitempty"`
	SnapshotVer   int64    `json:"snapshot_ver,omitempty"`
}

// AssertionMinter mints short-lived signed assertion JWTs at the gateway edge.
type AssertionMinter struct {
	privateKey ed25519.PrivateKey
	issuer     string
}

// NewAssertionMinter initializes a minter with the gateway's private Ed25519 key.
func NewAssertionMinter(privateKey ed25519.PrivateKey) *AssertionMinter {
	return &AssertionMinter{
		privateKey: privateKey,
		issuer:     AssertionIssuer,
	}
}

// MintAssertion generates an Ed25519-signed assertion valid for 15 seconds.
func (m *AssertionMinter) MintAssertion(
	principalID string,
	principalKind string,
	roles []string,
	targetService string,
	method string,
	canonicalPath string,
	requestID string,
	snapshotVer int64,
) (string, error) {
	if len(m.privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("invalid or uninitialized ed25519 private key")
	}
	if principalID == "" {
		return "", errors.New("principalID cannot be empty")
	}
	if targetService == "" {
		return "", errors.New("targetService cannot be empty")
	}
	if method == "" {
		return "", errors.New("method cannot be empty")
	}
	if canonicalPath == "" {
		return "", errors.New("canonicalPath cannot be empty")
	}
	if requestID == "" {
		return "", errors.New("requestID cannot be empty")
	}

	now := time.Now()
	claims := AssertionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   principalID,
			Audience:  jwt.ClaimStrings{targetService},
			ExpiresAt: jwt.NewNumericDate(now.Add(AssertionLifetime)),
			NotBefore: jwt.NewNumericDate(now.Add(-ClockSkewLeeway)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
		Method:        method,
		Path:          canonicalPath,
		RequestID:     requestID,
		PrincipalKind: principalKind,
		Roles:         roles,
		SnapshotVer:   snapshotVer,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(m.privateKey)
}

// AssertionVerifier validates assertions on private backend microservices.
type AssertionVerifier struct {
	publicKey      ed25519.PublicKey
	expectedIssuer string
	serviceID      string
}

// NewAssertionVerifier creates a verifier pinned to the backend service ID.
func NewAssertionVerifier(publicKey ed25519.PublicKey, serviceID string) *AssertionVerifier {
	return &AssertionVerifier{
		publicKey:      publicKey,
		expectedIssuer: AssertionIssuer,
		serviceID:      serviceID,
	}
}

// VerifyAssertion validates signature, audience, method, and canonical path.
func (v *AssertionVerifier) VerifyAssertion(tokenStr, expectedMethod, expectedPath string) (*AssertionClaims, error) {
	if tokenStr == "" {
		return nil, ErrMissingAssertionToken
	}

	claims := &AssertionClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != "EdDSA" {
			return nil, fmt.Errorf("unexpected algorithm: %s", token.Method.Alg())
		}
		return v.publicKey, nil
	},
		jwt.WithIssuer(v.expectedIssuer),
		jwt.WithAudience(v.serviceID),
		jwt.WithLeeway(ClockSkewLeeway),
	)

	if err != nil {
		return nil, fmt.Errorf("assertion validation failed: %w", err)
	}

	if !token.Valid {
		return nil, ErrInvalidAssertionToken
	}

	if claims.Method != expectedMethod {
		return nil, fmt.Errorf("assertion method mismatch: expected %q, got %q", expectedMethod, claims.Method)
	}

	if claims.Path != expectedPath {
		return nil, fmt.Errorf("assertion path mismatch: expected %q, got %q", expectedPath, claims.Path)
	}

	return claims, nil
}
