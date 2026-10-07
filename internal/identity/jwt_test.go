package identity

import (
	"crypto"
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

func TestTokenValidator_MultiKeyRotation(t *testing.T) {
	pubKeyA, privKeyA, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pubKeyB, privKeyB, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	issuer := "https://auth.aegis.local"
	audience := "aegis-gateway"

	// Validator initialized with Key A only
	validator := NewTokenValidator(issuer, audience, pubKeyA)

	mintToken := func(priv ed25519.PrivateKey, sub string) string {
		claims := UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   sub,
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
			Roles: []string{"developer"},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, err := token.SignedString(priv)
		require.NoError(t, err)
		return signed
	}

	tokenA := mintToken(privKeyA, "user-A")
	tokenB := mintToken(privKeyB, "user-B")

	// Phase 1: Key A succeeds, Key B fails
	claimsA, err := validator.ValidateBearerToken("Bearer " + tokenA)
	require.NoError(t, err)
	require.Equal(t, "user-A", claimsA.Subject)

	_, err = validator.ValidateBearerToken("Bearer " + tokenB)
	require.Error(t, err)

	// Phase 2: Overlap - add Key B
	validator.AddPublicKey(pubKeyB)

	claimsA, err = validator.ValidateBearerToken("Bearer " + tokenA)
	require.NoError(t, err)
	require.Equal(t, "user-A", claimsA.Subject)

	claimsB, err := validator.ValidateBearerToken("Bearer " + tokenB)
	require.NoError(t, err)
	require.Equal(t, "user-B", claimsB.Subject)

	// Phase 3: Retirement - set public keys to Key B only
	validator.SetPublicKeys([]crypto.PublicKey{pubKeyB})

	claimsB, err = validator.ValidateBearerToken("Bearer " + tokenB)
	require.NoError(t, err)
	require.Equal(t, "user-B", claimsB.Subject)

	_, err = validator.ValidateBearerToken("Bearer " + tokenA)
	require.Error(t, err, "Key A must be rejected after retirement")

	// Verify alg: none rejection is preserved
	noneClaims := UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			Subject:   "user-none",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
		},
	}
	noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, noneClaims)
	noneSigned, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = validator.ValidateBearerToken("Bearer " + noneSigned)
	require.ErrorIs(t, err, ErrInvalidAlgorithm)
}

