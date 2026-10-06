package identity

import (
	"crypto"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	// ErrMissingAuthHeader indicates missing or malformed Authorization header.
	ErrMissingAuthHeader = errors.New("missing or malformed authorization header")
	// ErrInvalidAlgorithm indicates an untrusted or unpinned algorithm was used.
	ErrInvalidAlgorithm = errors.New("unsupported or insecure token signing algorithm")
	// ErrTokenExpired indicates token is past expiration and outside leeway window.
	ErrTokenExpired = errors.New("token is expired")
	// ErrInvalidClaims indicates missing required claims such as subject.
	ErrInvalidClaims = errors.New("token claims invalid or missing required fields")
)

// UserClaims models identity claims extracted from an ingress Bearer token.
type UserClaims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles"`
}

// TokenValidator enforces RFC 8725 JWT validation with a pinned algorithm allowlist.
type TokenValidator struct {
	issuer       string
	audience     string
	publicKey    crypto.PublicKey
	allowedAlgos []string
}

// NewTokenValidator initializes a TokenValidator with pinned asymmetric algorithms.
func NewTokenValidator(issuer, audience string, publicKey crypto.PublicKey) *TokenValidator {
	return &TokenValidator{
		issuer:       issuer,
		audience:     audience,
		publicKey:    publicKey,
		allowedAlgos: []string{"EdDSA", "RS256", "ES256"},
	}
}

// ValidateBearerToken validates an incoming Authorization header and returns parsed claims.
func (v *TokenValidator) ValidateBearerToken(authHeader string) (*UserClaims, error) {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, ErrMissingAuthHeader
	}
	tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if tokenStr == "" {
		return nil, ErrMissingAuthHeader
	}

	claims := &UserClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		validMethod := false
		for _, algo := range v.allowedAlgos {
			if token.Method.Alg() == algo {
				validMethod = true
				break
			}
		}
		if !validMethod {
			return nil, fmt.Errorf("%w: %s", ErrInvalidAlgorithm, token.Method.Alg())
		}
		return v.publicKey, nil
	},
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithLeeway(30*time.Second),
	)

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("%w: %w", ErrTokenExpired, err)
		}
		if errors.Is(err, ErrInvalidAlgorithm) {
			return nil, ErrInvalidAlgorithm
		}
		return nil, fmt.Errorf("%w: %w", ErrInvalidClaims, err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("%w: token not valid", ErrInvalidClaims)
	}

	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: missing subject claim", ErrInvalidClaims)
	}

	return claims, nil
}
