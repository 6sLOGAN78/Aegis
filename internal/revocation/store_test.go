package revocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})
	return NewStore(rdb), mr
}

func TestRevocationStore_CleanPass(t *testing.T) {
	store, _ := setupTestStore(t)
	ctx := context.Background()

	revoked, reason, err := store.CheckRevocation(ctx, "user-alice", "jti-12345")
	require.NoError(t, err)
	assert.False(t, revoked)
	assert.Empty(t, reason)
}

func TestRevocationStore_JTIRevocation(t *testing.T) {
	store, _ := setupTestStore(t)
	ctx := context.Background()

	err := store.RevokeJTI(ctx, "jti-stolen-token", 10*time.Minute)
	require.NoError(t, err)

	revoked, reason, err := store.CheckRevocation(ctx, "user-alice", "jti-stolen-token")
	require.NoError(t, err)
	assert.True(t, revoked)
	assert.Equal(t, "TOKEN_REVOKED", reason)

	// Another JTI for the same user should still pass
	revokedOther, reasonOther, errOther := store.CheckRevocation(ctx, "user-alice", "jti-valid-token")
	require.NoError(t, errOther)
	assert.False(t, revokedOther)
	assert.Empty(t, reasonOther)
}

func TestRevocationStore_QuarantinePrincipal(t *testing.T) {
	store, _ := setupTestStore(t)
	ctx := context.Background()

	err := store.QuarantinePrincipal(ctx, "compromised-user", "SECURITY_INCIDENT_123", 1*time.Hour)
	require.NoError(t, err)

	// Any token (even a brand new JTI) from this principal must be blocked
	revoked, reason, err := store.CheckRevocation(ctx, "compromised-user", "jti-fresh-token")
	require.NoError(t, err)
	assert.True(t, revoked)
	assert.Equal(t, "PRINCIPAL_QUARANTINED", reason)

	// Another principal is not blocked
	revokedOther, reasonOther, errOther := store.CheckRevocation(ctx, "honest-user", "jti-fresh-token")
	require.NoError(t, errOther)
	assert.False(t, revokedOther)
	assert.Empty(t, reasonOther)
}

func TestRevocationStore_RemoveQuarantine(t *testing.T) {
	store, _ := setupTestStore(t)
	ctx := context.Background()

	err := store.QuarantinePrincipal(ctx, "user-under-investigation", "AUDIT_LOCK", 1*time.Hour)
	require.NoError(t, err)

	revoked, reason, err := store.CheckRevocation(ctx, "user-under-investigation", "jti-100")
	require.NoError(t, err)
	assert.True(t, revoked)
	assert.Equal(t, "PRINCIPAL_QUARANTINED", reason)

	// Lift quarantine
	err = store.RemoveQuarantine(ctx, "user-under-investigation")
	require.NoError(t, err)

	// Access should immediately be restored
	revokedRestored, reasonRestored, errRestored := store.CheckRevocation(ctx, "user-under-investigation", "jti-100")
	require.NoError(t, errRestored)
	assert.False(t, revokedRestored)
	assert.Empty(t, reasonRestored)
}

func TestRevocationStore_Expiration(t *testing.T) {
	store, mr := setupTestStore(t)
	ctx := context.Background()

	// Revoke JTI with short TTL (10 seconds)
	err := store.RevokeJTI(ctx, "jti-short-lived", 10*time.Second)
	require.NoError(t, err)

	// Must be blocked initially
	revoked, _, err := store.CheckRevocation(ctx, "user-bob", "jti-short-lived")
	require.NoError(t, err)
	assert.True(t, revoked)

	// Fast-forward miniredis clock by 11 seconds
	mr.FastForward(11 * time.Second)

	// Key should have expired; access restored automatically
	revokedExpired, _, errExpired := store.CheckRevocation(ctx, "user-bob", "jti-short-lived")
	require.NoError(t, errExpired)
	assert.False(t, revokedExpired)
}

func TestRevocationStore_DependencyFailureAndTimeout(t *testing.T) {
	store, mr := setupTestStore(t)

	// Close miniredis to simulate server down
	mr.Close()

	ctx := context.Background()
	revoked, reason, err := store.CheckRevocation(ctx, "user-alice", "jti-test")
	assert.Error(t, err)
	assert.False(t, revoked)
	assert.Empty(t, reason)
	assert.True(t, errors.Is(err, ErrRevocationDependency), "error must wrap ErrRevocationDependency")

	// Verify canceled context also fails closed with ErrRevocationDependency
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	revokedCancel, reasonCancel, errCancel := store.CheckRevocation(canceledCtx, "user-alice", "jti-test")
	assert.Error(t, errCancel)
	assert.False(t, revokedCancel)
	assert.Empty(t, reasonCancel)
	assert.True(t, errors.Is(errCancel, ErrRevocationDependency))
}

func TestReconstructQuarantines(t *testing.T) {
	store, _ := setupTestStore(t)
	ctx := context.Background()

	// Empty slice returns nil immediately
	err := store.ReconstructQuarantines(ctx, nil)
	require.NoError(t, err)

	principals := []QuarantinedPrincipal{
		{
			PrincipalID: "bad-actor-1",
			Reason:      "COMPROMISED_CREDENTIALS",
			TTL:         10 * time.Minute,
		},
		{
			PrincipalID: "bad-actor-2",
			Reason:      "", // Should default to DISASTER_RECOVERY_RECONSTRUCTED
			TTL:         0,  // Should default to 24h
		},
	}

	err = store.ReconstructQuarantines(ctx, principals)
	require.NoError(t, err)

	// Verify bad-actor-1 is quarantined with expected reason
	revoked1, reason1, err1 := store.CheckRevocation(ctx, "bad-actor-1", "")
	require.NoError(t, err1)
	assert.True(t, revoked1)
	assert.Equal(t, "PRINCIPAL_QUARANTINED", reason1)

	// Verify bad-actor-2 is quarantined
	revoked2, reason2, err2 := store.CheckRevocation(ctx, "bad-actor-2", "")
	require.NoError(t, err2)
	assert.True(t, revoked2)
	assert.Equal(t, "PRINCIPAL_QUARANTINED", reason2)

	// Unlisted principal is not quarantined
	revokedOther, _, errOther := store.CheckRevocation(ctx, "honest-user", "")
	require.NoError(t, errOther)
	assert.False(t, revokedOther)
}

