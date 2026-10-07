package identity

import (
	"crypto"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// TokenValidator enforces RFC 8725 JWT validation with a pinned algorithm allowlist
// and a multi-key trusted keyset for zero-downtime key rotation (AUTH-01).
type TokenValidator struct {
	issuer       string
	audience     string
	publicKeys   []crypto.PublicKey
	allowedAlgos []string
	mu           sync.RWMutex
}

// NewTokenValidator initializes a TokenValidator with pinned asymmetric algorithms
// and one or more trusted public keys.
func NewTokenValidator(issuer, audience string, publicKeys ...crypto.PublicKey) *TokenValidator {
	keys := make([]crypto.PublicKey, len(publicKeys))
	copy(keys, publicKeys)
	return &TokenValidator{
		issuer:       issuer,
		audience:     audience,
		publicKeys:   keys,
		allowedAlgos: []string{"EdDSA", "RS256", "ES256"},
	}
}

// AddPublicKey adds a newly issued public key to the trusted keyset (Phase 1 of rotation).
func (v *TokenValidator) AddPublicKey(pubKey crypto.PublicKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.publicKeys = append(v.publicKeys, pubKey)
}

// SetPublicKeys atomically replaces the trusted keyset (Phase 3 of rotation: retirement).
func (v *TokenValidator) SetPublicKeys(keys []crypto.PublicKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	copied := make([]crypto.PublicKey, len(keys))
	copy(copied, keys)
	v.publicKeys = copied
}

// ValidateBearerToken validates an incoming Authorization header and returns parsed claims.
// It evaluates the token across all trusted public keys in the keyset.
func (v *TokenValidator) ValidateBearerToken(authHeader string) (*UserClaims, error) {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, ErrMissingAuthHeader
	}
	tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if tokenStr == "" {
		return nil, ErrMissingAuthHeader
	}

	v.mu.RLock()
	keys := make([]crypto.PublicKey, len(v.publicKeys))
	copy(keys, v.publicKeys)
	v.mu.RUnlock()

	if len(keys) == 0 {
		return nil, ErrInvalidClaims
	}

	var hasInvalidAlgorithm bool
	var hasExpired bool
	var lastExpiredErr error
	var lastErr error

	for _, key := range keys {
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
			return key, nil
		},
			jwt.WithIssuer(v.issuer),
			jwt.WithAudience(v.audience),
			jwt.WithLeeway(30*time.Second),
		)

		if err == nil && token.Valid && claims.Subject != "" {
			return claims, nil
		}

		if err != nil {
			lastErr = err
			if errors.Is(err, ErrInvalidAlgorithm) {
				hasInvalidAlgorithm = true
			}
			if errors.Is(err, jwt.ErrTokenExpired) {
				hasExpired = true
				lastExpiredErr = err
			}
		} else if !token.Valid || claims.Subject == "" {
			lastErr = ErrInvalidClaims
		}
	}

	if hasInvalidAlgorithm {
		return nil, ErrInvalidAlgorithm
	}
	if hasExpired {
		return nil, fmt.Errorf("%w: %w", ErrTokenExpired, lastExpiredErr)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidClaims, lastErr)
	}
	return nil, ErrInvalidClaims
}
