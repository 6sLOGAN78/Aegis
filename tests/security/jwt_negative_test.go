package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"aegis/internal/identity"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestJWTNegativeSecurity(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	issuer := "https://auth.aegis.local"
	audience := "aegis-gateway"
	validator := identity.NewTokenValidator(issuer, audience, pubKey)

	t.Run("Rejects token with alg none", func(t *testing.T) {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   "attacker",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			},
			Roles: []string{"application-admin"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		_, err = validator.ValidateBearerToken("Bearer " + signed)
		require.Error(t, err)
		require.ErrorIs(t, err, identity.ErrInvalidAlgorithm)
	})

	t.Run("Rejects token signed with HMAC using public key bytes (Key Confusion)", func(t *testing.T) {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   "attacker",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			},
			Roles: []string{"application-admin"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, err := token.SignedString([]byte(pubKey))
		require.NoError(t, err)

		_, err = validator.ValidateBearerToken("Bearer " + signed)
		require.Error(t, err)
		require.ErrorIs(t, err, identity.ErrInvalidAlgorithm)
	})

	t.Run("Rejects token with forged signature bytes", func(t *testing.T) {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   "usr_dev_123",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			},
			Roles: []string{"developer"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, err := token.SignedString(privKey)
		require.NoError(t, err)

		parts := strings.Split(signed, ".")
		require.Len(t, parts, 3)

		// Tamper signature part
		tamperedSig := parts[2]
		if len(tamperedSig) > 4 {
			tamperedSig = "AAAA" + tamperedSig[4:]
		} else {
			tamperedSig = "AAAA"
		}
		forgedToken := parts[0] + "." + parts[1] + "." + tamperedSig

		_, err = validator.ValidateBearerToken("Bearer " + forgedToken)
		require.Error(t, err)
	})

	t.Run("Rejects token expired beyond 30-second leeway window", func(t *testing.T) {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   "usr_dev_123",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-60 * time.Second)),
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-120 * time.Second)),
			},
			Roles: []string{"developer"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, err := token.SignedString(privKey)
		require.NoError(t, err)

		_, err = validator.ValidateBearerToken("Bearer " + signed)
		require.Error(t, err)
		require.ErrorIs(t, err, identity.ErrTokenExpired)
	})

	t.Run("Rejects token with untrusted issuer", func(t *testing.T) {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "https://evil.aegis.local",
				Audience:  jwt.ClaimStrings{audience},
				Subject:   "usr_dev_123",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			},
			Roles: []string{"developer"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, err := token.SignedString(privKey)
		require.NoError(t, err)

		_, err = validator.ValidateBearerToken("Bearer " + signed)
		require.Error(t, err)
		require.ErrorIs(t, err, identity.ErrInvalidClaims)
	})

	t.Run("Rejects token with incorrect audience", func(t *testing.T) {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{"unauthorized-service"},
				Subject:   "usr_dev_123",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			},
			Roles: []string{"developer"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, err := token.SignedString(privKey)
		require.NoError(t, err)

		_, err = validator.ValidateBearerToken("Bearer " + signed)
		require.Error(t, err)
		require.ErrorIs(t, err, identity.ErrInvalidClaims)
	})

	t.Run("Rejects token with empty subject claim", func(t *testing.T) {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   "",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			},
			Roles: []string{"developer"},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, err := token.SignedString(privKey)
		require.NoError(t, err)

		_, err = validator.ValidateBearerToken("Bearer " + signed)
		require.Error(t, err)
		require.ErrorIs(t, err, identity.ErrInvalidClaims)
	})
}
