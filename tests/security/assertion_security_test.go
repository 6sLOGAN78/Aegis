package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"aegis/internal/identity"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

// TestAssertionSecurity_SignatureForgery verifies that assertions signed by an untrusted
// or rogue key are unconditionally rejected by backends (STRIDE Spoofing mitigation).
func TestAssertionSecurity_SignatureForgery(t *testing.T) {
	gwPub, _ := generateKeyPair(t)
	_, roguePriv := generateKeyPair(t)

	// Attacker tries to forge gateway assertion using their own private key
	rogueMinter := identity.NewAssertionMinter(roguePriv)
	forgedToken, err := rogueMinter.MintAssertion(
		"spiffe://aegis.local/workload/rogue",
		"workload",
		[]string{"admin"},
		"orders",
		"POST",
		"/api/orders",
		"req-forge-001",
		1,
	)
	require.NoError(t, err)

	// Backend verifier trusts genuine gateway public key
	backendVerifier := identity.NewAssertionVerifier(gwPub, "orders")

	claims, err := backendVerifier.VerifyAssertion(forgedToken, "POST", "/api/orders")
	assert.Error(t, err, "Forged assertion signature must be rejected")
	assert.Nil(t, claims)
	assert.Contains(t, err.Error(), "validation failed")
}

// TestAssertionSecurity_AlgorithmConfusion tests that algorithms other than EdDSA
// (specifically 'none' and symmetric HMAC) are strictly rejected (STRIDE Spoofing mitigation).
func TestAssertionSecurity_AlgorithmConfusion(t *testing.T) {
	gwPub, _ := generateKeyPair(t)
	backendVerifier := identity.NewAssertionVerifier(gwPub, "orders")

	t.Run("alg none attack rejected", func(t *testing.T) {
		claims := identity.AssertionClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    identity.AssertionIssuer,
				Subject:   "admin",
				Audience:  jwt.ClaimStrings{"orders"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(identity.AssertionLifetime)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				ID:        uuid.NewString(),
			},
			Method:        "POST",
			Path:          "/api/orders",
			RequestID:     "req-none-001",
			PrincipalKind: "user",
		}

		token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		tokenStr, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		verifiedClaims, err := backendVerifier.VerifyAssertion(tokenStr, "POST", "/api/orders")
		assert.Error(t, err, "alg:none token must be rejected")
		assert.Nil(t, verifiedClaims)
		assert.Contains(t, err.Error(), "unexpected algorithm")
	})

	t.Run("symmetric HMAC algorithm confusion attack rejected", func(t *testing.T) {
		claims := identity.AssertionClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    identity.AssertionIssuer,
				Subject:   "admin",
				Audience:  jwt.ClaimStrings{"orders"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(identity.AssertionLifetime)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				ID:        uuid.NewString(),
			},
			Method:        "POST",
			Path:          "/api/orders",
			RequestID:     "req-hmac-001",
			PrincipalKind: "user",
		}

		// Attacker uses gateway public key bytes as HMAC secret key
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenStr, err := token.SignedString([]byte(gwPub))
		require.NoError(t, err)

		verifiedClaims, err := backendVerifier.VerifyAssertion(tokenStr, "POST", "/api/orders")
		assert.Error(t, err, "HMAC token must be rejected by Ed25519 verifier")
		assert.Nil(t, verifiedClaims)
		assert.Contains(t, err.Error(), "unexpected algorithm")
	})
}

// TestAssertionSecurity_AudienceSubstitution verifies that assertion tokens minted for one
// microservice cannot be replayed against other microservices (STRIDE Tampering/Elevation mitigation).
func TestAssertionSecurity_AudienceSubstitution(t *testing.T) {
	gwPub, gwPriv := generateKeyPair(t)
	minter := identity.NewAssertionMinter(gwPriv)

	// Legitimate assertion minted specifically for 'orders' service
	ordersToken, err := minter.MintAssertion(
		"user-developer-1",
		"user",
		[]string{"developer"},
		"orders",
		"GET",
		"/api/orders",
		"req-aud-001",
		1,
	)
	require.NoError(t, err)

	// Attacker replays 'orders' token against 'payments' service
	paymentsVerifier := identity.NewAssertionVerifier(gwPub, "payments")
	claimsPayments, err := paymentsVerifier.VerifyAssertion(ordersToken, "GET", "/api/orders")
	assert.Error(t, err, "Payments service must reject token minted for orders")
	assert.Nil(t, claimsPayments)
	assert.Contains(t, err.Error(), "audience")

	// Attacker replays 'orders' token against 'admin' service
	adminVerifier := identity.NewAssertionVerifier(gwPub, "admin")
	claimsAdmin, err := adminVerifier.VerifyAssertion(ordersToken, "GET", "/api/orders")
	assert.Error(t, err, "Admin service must reject token minted for orders")
	assert.Nil(t, claimsAdmin)
	assert.Contains(t, err.Error(), "audience")
}

// TestAssertionSecurity_MethodTampering verifies that assertions cryptographically bound to
// an HTTP method cannot be replayed with a different method (STRIDE Tampering mitigation).
func TestAssertionSecurity_MethodTampering(t *testing.T) {
	gwPub, gwPriv := generateKeyPair(t)
	minter := identity.NewAssertionMinter(gwPriv)
	verifier := identity.NewAssertionVerifier(gwPub, "orders")

	// Token minted for read-only GET /api/orders
	getToken, err := minter.MintAssertion(
		"user-developer-1",
		"user",
		[]string{"developer"},
		"orders",
		"GET",
		"/api/orders",
		"req-meth-001",
		1,
	)
	require.NoError(t, err)

	// Adversary attempts to use token for mutating POST request
	claims, err := verifier.VerifyAssertion(getToken, "POST", "/api/orders")
	assert.Error(t, err, "Verifier expecting POST must reject GET assertion")
	assert.Nil(t, claims)
	assert.Contains(t, err.Error(), "method mismatch")

	// Adversary attempts to use token for DELETE request
	claimsDelete, err := verifier.VerifyAssertion(getToken, "DELETE", "/api/orders")
	assert.Error(t, err, "Verifier expecting DELETE must reject GET assertion")
	assert.Nil(t, claimsDelete)
	assert.Contains(t, err.Error(), "method mismatch")
}

// TestAssertionSecurity_PathTampering verifies that assertions cryptographically bound to
// a canonical path cannot be replayed for other endpoints (STRIDE Tampering mitigation).
func TestAssertionSecurity_PathTampering(t *testing.T) {
	gwPub, gwPriv := generateKeyPair(t)
	minter := identity.NewAssertionMinter(gwPriv)
	verifier := identity.NewAssertionVerifier(gwPub, "admin")

	// Assertion minted for public/low-privilege path /api/orders
	token, err := minter.MintAssertion(
		"user-developer-1",
		"user",
		[]string{"developer"},
		"admin",
		"GET",
		"/api/orders",
		"req-path-001",
		1,
	)
	require.NoError(t, err)

	// Adversary attempts to access /api/admin/users
	claims, err := verifier.VerifyAssertion(token, "GET", "/api/admin/users")
	assert.Error(t, err, "Verifier must reject assertion with mismatched path")
	assert.Nil(t, claims)
	assert.Contains(t, err.Error(), "path mismatch")

	// Adversary attempts path traversal replay
	claimsTraversal, err := verifier.VerifyAssertion(token, "GET", "/api/orders/../admin/users")
	assert.Error(t, err, "Verifier must reject path traversal attempt")
	assert.Nil(t, claimsTraversal)
	assert.Contains(t, err.Error(), "path mismatch")
}

// TestAssertionSecurity_ExpiredAssertion verifies that expired assertion tokens outside
// the 5-second clock skew leeway window are rejected (STRIDE Elevation of Privilege mitigation).
func TestAssertionSecurity_ExpiredAssertion(t *testing.T) {
	gwPub, gwPriv := generateKeyPair(t)
	verifier := identity.NewAssertionVerifier(gwPub, "orders")

	// Token expired 25 seconds ago (exceeding 5-second clock skew leeway)
	past := time.Now().Add(-25 * time.Second)
	claims := identity.AssertionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    identity.AssertionIssuer,
			Subject:   "user-1",
			Audience:  jwt.ClaimStrings{"orders"},
			ExpiresAt: jwt.NewNumericDate(past),
			NotBefore: jwt.NewNumericDate(past.Add(-identity.AssertionLifetime)),
			IssuedAt:  jwt.NewNumericDate(past.Add(-identity.AssertionLifetime)),
			ID:        uuid.NewString(),
		},
		Method:        "GET",
		Path:          "/api/orders",
		RequestID:     "req-exp-001",
		PrincipalKind: "user",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tokenStr, err := token.SignedString(gwPriv)
	require.NoError(t, err)

	verifiedClaims, err := verifier.VerifyAssertion(tokenStr, "GET", "/api/orders")
	assert.Error(t, err, "Expired assertion must be rejected")
	assert.Nil(t, verifiedClaims)
	assert.Contains(t, err.Error(), "token is expired")
}

// TestAssertionSecurity_PrematureAssertion verifies that assertions with a future NotBefore (nbf)
// outside the clock leeway window are rejected (preventing pre-minted assertion abuse).
func TestAssertionSecurity_PrematureAssertion(t *testing.T) {
	gwPub, gwPriv := generateKeyPair(t)
	verifier := identity.NewAssertionVerifier(gwPub, "orders")

	// Token valid 30 seconds into the future (outside 5s clock skew leeway)
	future := time.Now().Add(30 * time.Second)
	claims := identity.AssertionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    identity.AssertionIssuer,
			Subject:   "user-1",
			Audience:  jwt.ClaimStrings{"orders"},
			ExpiresAt: jwt.NewNumericDate(future.Add(identity.AssertionLifetime)),
			NotBefore: jwt.NewNumericDate(future),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.NewString(),
		},
		Method:        "GET",
		Path:          "/api/orders",
		RequestID:     "req-premature-001",
		PrincipalKind: "user",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tokenStr, err := token.SignedString(gwPriv)
	require.NoError(t, err)

	verifiedClaims, err := verifier.VerifyAssertion(tokenStr, "GET", "/api/orders")
	assert.Error(t, err, "Premature assertion with future nbf must be rejected")
	assert.Nil(t, verifiedClaims)
	assert.Contains(t, err.Error(), "token is not valid yet")
}
