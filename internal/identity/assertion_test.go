package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateEd25519KeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func TestAssertion_MintAndVerifyValid(t *testing.T) {
	pub, priv := generateEd25519KeyPair(t)
	minter := NewAssertionMinter(priv)
	verifier := NewAssertionVerifier(pub, "orders")

	principalID := "spiffe://aegis.local/workload/checkout"
	principalKind := "workload"
	roles := []string{"workload"}
	targetService := "orders"
	method := "POST"
	canonicalPath := "/api/orders"
	requestID := "req-test-12345"
	snapshotVer := int64(42)

	tokenStr, err := minter.MintAssertion(
		principalID,
		principalKind,
		roles,
		targetService,
		method,
		canonicalPath,
		requestID,
		snapshotVer,
	)
	require.NoError(t, err)
	require.NotEmpty(t, tokenStr)

	claims, err := verifier.VerifyAssertion(tokenStr, method, canonicalPath)
	require.NoError(t, err)
	require.NotNil(t, claims)

	assert.Equal(t, AssertionIssuer, claims.Issuer)
	assert.Equal(t, principalID, claims.Subject)
	assert.Equal(t, jwt.ClaimStrings{targetService}, claims.Audience)
	assert.Equal(t, method, claims.Method)
	assert.Equal(t, canonicalPath, claims.Path)
	assert.Equal(t, requestID, claims.RequestID)
	assert.Equal(t, principalKind, claims.PrincipalKind)
	assert.Equal(t, roles, claims.Roles)
	assert.Equal(t, snapshotVer, claims.SnapshotVer)
	assert.NotEmpty(t, claims.ID)

	// Ensure unique JTI nonces across sequential minting calls
	tokenStr2, err := minter.MintAssertion(
		principalID,
		principalKind,
		roles,
		targetService,
		method,
		canonicalPath,
		requestID,
		snapshotVer,
	)
	require.NoError(t, err)
	claims2, err := verifier.VerifyAssertion(tokenStr2, method, canonicalPath)
	require.NoError(t, err)
	assert.NotEqual(t, claims.ID, claims2.ID, "Sequential assertions must have unique jti nonces")
}

func TestAssertion_AudienceMismatch(t *testing.T) {
	pub, priv := generateEd25519KeyPair(t)
	minter := NewAssertionMinter(priv)
	verifierPayments := NewAssertionVerifier(pub, "payments")

	tokenStr, err := minter.MintAssertion(
		"spiffe://aegis.local/workload/checkout",
		"workload",
		[]string{"workload"},
		"orders", // Minted for orders
		"POST",
		"/api/orders",
		"req-1",
		1,
	)
	require.NoError(t, err)

	// Verifier pinned to payments must reject token minted for orders
	claims, err := verifierPayments.VerifyAssertion(tokenStr, "POST", "/api/orders")
	assert.Error(t, err)
	assert.Nil(t, claims)
	assert.Contains(t, err.Error(), "audience")
}

func TestAssertion_MethodMismatch(t *testing.T) {
	pub, priv := generateEd25519KeyPair(t)
	minter := NewAssertionMinter(priv)
	verifier := NewAssertionVerifier(pub, "orders")

	tokenStr, err := minter.MintAssertion(
		"user-123",
		"user",
		[]string{"developer"},
		"orders",
		"GET", // Minted for GET
		"/api/orders",
		"req-2",
		1,
	)
	require.NoError(t, err)

	// Verifier expecting POST must reject GET assertion
	claims, err := verifier.VerifyAssertion(tokenStr, "POST", "/api/orders")
	assert.Error(t, err)
	assert.Nil(t, claims)
	assert.Contains(t, err.Error(), "method mismatch")
}

func TestAssertion_PathMismatch(t *testing.T) {
	pub, priv := generateEd25519KeyPair(t)
	minter := NewAssertionMinter(priv)
	verifier := NewAssertionVerifier(pub, "orders")

	tokenStr, err := minter.MintAssertion(
		"user-123",
		"user",
		[]string{"developer"},
		"orders",
		"GET",
		"/api/orders", // Minted for /api/orders
		"req-3",
		1,
	)
	require.NoError(t, err)

	// Verifier expecting /api/payments must reject /api/orders assertion
	claims, err := verifier.VerifyAssertion(tokenStr, "GET", "/api/payments")
	assert.Error(t, err)
	assert.Nil(t, claims)
	assert.Contains(t, err.Error(), "path mismatch")
}

func TestAssertion_Expired(t *testing.T) {
	pub, priv := generateEd25519KeyPair(t)
	verifier := NewAssertionVerifier(pub, "orders")

	// Manually construct token expired >20s in the past (outside 5s clock skew leeway)
	past := time.Now().Add(-30 * time.Second)
	claims := AssertionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    AssertionIssuer,
			Subject:   "user-123",
			Audience:  jwt.ClaimStrings{"orders"},
			ExpiresAt: jwt.NewNumericDate(past),
			NotBefore: jwt.NewNumericDate(past.Add(-AssertionLifetime)),
			IssuedAt:  jwt.NewNumericDate(past.Add(-AssertionLifetime)),
			ID:        uuid.NewString(),
		},
		Method:        "GET",
		Path:          "/api/orders",
		RequestID:     "req-4",
		PrincipalKind: "user",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tokenStr, err := token.SignedString(priv)
	require.NoError(t, err)

	verifiedClaims, err := verifier.VerifyAssertion(tokenStr, "GET", "/api/orders")
	assert.Error(t, err)
	assert.Nil(t, verifiedClaims)
	assert.Contains(t, err.Error(), "token is expired")
}

func TestAssertion_InvalidSignature(t *testing.T) {
	_, privA := generateEd25519KeyPair(t)
	pubB, _ := generateEd25519KeyPair(t)

	minterA := NewAssertionMinter(privA)
	verifierB := NewAssertionVerifier(pubB, "orders")

	tokenStr, err := minterA.MintAssertion(
		"user-123",
		"user",
		[]string{"developer"},
		"orders",
		"GET",
		"/api/orders",
		"req-5",
		1,
	)
	require.NoError(t, err)

	claims, err := verifierB.VerifyAssertion(tokenStr, "GET", "/api/orders")
	assert.Error(t, err)
	assert.Nil(t, claims)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestAssertion_ClockSkewLeeway(t *testing.T) {
	pub, priv := generateEd25519KeyPair(t)
	verifier := NewAssertionVerifier(pub, "orders")

	// Token expired 2 seconds ago (within 5-second leeway window)
	now := time.Now()
	expiredWithinLeeway := now.Add(-2 * time.Second)
	claims := AssertionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    AssertionIssuer,
			Subject:   "user-123",
			Audience:  jwt.ClaimStrings{"orders"},
			ExpiresAt: jwt.NewNumericDate(expiredWithinLeeway),
			NotBefore: jwt.NewNumericDate(now.Add(-15 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now.Add(-15 * time.Second)),
			ID:        uuid.NewString(),
		},
		Method:        "GET",
		Path:          "/api/orders",
		RequestID:     "req-6",
		PrincipalKind: "user",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tokenStr, err := token.SignedString(priv)
	require.NoError(t, err)

	verifiedClaims, err := verifier.VerifyAssertion(tokenStr, "GET", "/api/orders")
	require.NoError(t, err)
	assert.NotNil(t, verifiedClaims)
}

func TestAssertion_InsecureAlgorithmsRejected(t *testing.T) {
	pub, _ := generateEd25519KeyPair(t)
	verifier := NewAssertionVerifier(pub, "orders")

	t.Run("alg none rejected", func(t *testing.T) {
		claims := AssertionClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    AssertionIssuer,
				Subject:   "user-123",
				Audience:  jwt.ClaimStrings{"orders"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(AssertionLifetime)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				ID:        uuid.NewString(),
			},
			Method: "GET",
			Path:   "/api/orders",
		}
		token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		tokenStr, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		verifiedClaims, err := verifier.VerifyAssertion(tokenStr, "GET", "/api/orders")
		assert.Error(t, err)
		assert.Nil(t, verifiedClaims)
		assert.Contains(t, err.Error(), "unexpected algorithm")
	})

	t.Run("HMAC rejected", func(t *testing.T) {
		claims := AssertionClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    AssertionIssuer,
				Subject:   "user-123",
				Audience:  jwt.ClaimStrings{"orders"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(AssertionLifetime)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				ID:        uuid.NewString(),
			},
			Method: "GET",
			Path:   "/api/orders",
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenStr, err := token.SignedString([]byte("symmetric-secret-key-cannot-work"))
		require.NoError(t, err)

		verifiedClaims, err := verifier.VerifyAssertion(tokenStr, "GET", "/api/orders")
		assert.Error(t, err)
		assert.Nil(t, verifiedClaims)
		assert.Contains(t, err.Error(), "unexpected algorithm")
	})
}

func TestAssertion_ValidationErrors(t *testing.T) {
	_, priv := generateEd25519KeyPair(t)
	minter := NewAssertionMinter(priv)

	t.Run("missing required fields in minting", func(t *testing.T) {
		_, err := minter.MintAssertion("", "user", nil, "orders", "GET", "/api/orders", "req-1", 1)
		assert.Error(t, err)

		_, err = minter.MintAssertion("user-1", "user", nil, "", "GET", "/api/orders", "req-1", 1)
		assert.Error(t, err)

		_, err = minter.MintAssertion("user-1", "user", nil, "orders", "", "/api/orders", "req-1", 1)
		assert.Error(t, err)

		_, err = minter.MintAssertion("user-1", "user", nil, "orders", "GET", "", "req-1", 1)
		assert.Error(t, err)

		_, err = minter.MintAssertion("user-1", "user", nil, "orders", "GET", "/api/orders", "", 1)
		assert.Error(t, err)
	})

	t.Run("empty token string in verification", func(t *testing.T) {
		pub, _ := generateEd25519KeyPair(t)
		verifier := NewAssertionVerifier(pub, "orders")
		claims, err := verifier.VerifyAssertion("", "GET", "/api/orders")
		assert.Error(t, err)
		assert.Nil(t, claims)
		assert.ErrorIs(t, err, ErrMissingAssertionToken)
	})
}
