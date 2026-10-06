package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestJWTValidation(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	issuer := "https://auth.aegis.local"
	audience := "aegis-gateway"
	validator := NewTokenValidator(issuer, audience, pubKey)

	t.Run("Valid token succeeds and extracts claims", func(t *testing.T) {
		claims := UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   "usr_dev_123",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
			Roles: []string{"developer"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, err := token.SignedString(privKey)
		require.NoError(t, err)

		extracted, err := validator.ValidateBearerToken("Bearer " + signed)
		require.NoError(t, err)
		require.Equal(t, "usr_dev_123", extracted.Subject)
		require.Equal(t, []string{"developer"}, extracted.Roles)
	})

	t.Run("Token expired within 30-second leeway window is accepted", func(t *testing.T) {
		// Expired 15 seconds ago - within 30s leeway
		claims := UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   "usr_dev_123",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-15 * time.Second)),
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-60 * time.Second)),
			},
			Roles: []string{"developer"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, err := token.SignedString(privKey)
		require.NoError(t, err)

		extracted, err := validator.ValidateBearerToken("Bearer " + signed)
		require.NoError(t, err)
		require.Equal(t, "usr_dev_123", extracted.Subject)
	})

	t.Run("Missing Bearer prefix returns ErrMissingAuthHeader", func(t *testing.T) {
		_, err := validator.ValidateBearerToken("Basic dXNlcjpwYXNz")
		require.ErrorIs(t, err, ErrMissingAuthHeader)

		_, err = validator.ValidateBearerToken("")
		require.ErrorIs(t, err, ErrMissingAuthHeader)
	})
}
