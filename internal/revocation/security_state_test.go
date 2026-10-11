package revocation

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMarkerStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewStore(rdb, WithRequireSecurityMarker()), mr
}

func TestSecurityState_MarkerGateFailsClosedAfterFlush(t *testing.T) {
	store, mr := setupMarkerStore(t)
	ctx := context.Background()

	_, _, err := store.CheckRevocation(ctx, "u", "j")
	require.ErrorIs(t, err, ErrSecurityStateMissing)
	// Empty ids must not bypass the marker check.
	_, _, err = store.CheckRevocation(ctx, "", "")
	require.ErrorIs(t, err, ErrSecurityStateMissing)

	gen, err := store.MarkSecurityStateReady(ctx)
	require.NoError(t, err)
	assert.Contains(t, gen, "/")
	ready, err := store.SecurityStateReady(ctx)
	require.NoError(t, err)
	assert.True(t, ready)
	assert.Equal(t, time.Duration(0), mr.TTL(SecurityStateMarkerKey))

	revoked, _, err := store.CheckRevocation(ctx, "u", "j")
	require.NoError(t, err)
	assert.False(t, revoked)

	mr.FlushAll()
	ready, err = store.SecurityStateReady(ctx)
	require.NoError(t, err)
	assert.False(t, ready)
	_, _, err = store.CheckRevocation(ctx, "u", "j")
	require.True(t, errors.Is(err, ErrSecurityStateMissing))
}

func TestSecurityState_DefaultStoreIgnoresMarker(t *testing.T) {
	store, _ := setupTestStore(t)
	revoked, _, err := store.CheckRevocation(context.Background(), "u", "j")
	require.NoError(t, err)
	assert.False(t, revoked)
}

func TestSecurityState_ConcurrentCheckWhileMarking(t *testing.T) {
	store, _ := setupMarkerStore(t)
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_, _, err := store.CheckRevocation(ctx, "u", "j")
			if err != nil && !errors.Is(err, ErrSecurityStateMissing) {
				t.Errorf("unexpected error: %v", err)
				return
			}
		}
	}()
	_, err := store.MarkSecurityStateReady(ctx)
	require.NoError(t, err)
	<-done
}

func TestSecurityState_ReconstructRevocationsAndList(t *testing.T) {
	store, mr := setupMarkerStore(t)
	ctx := context.Background()

	require.NoError(t, store.ReconstructRevocations(ctx, nil))
	require.NoError(t, store.ReconstructRevocations(ctx, []RevokedJTI{
		{JTI: "a", TTL: 90 * time.Second},
		{JTI: "b", TTL: 0},
	}))
	assert.Equal(t, 90*time.Second, mr.TTL("revocation:jti:a"))
	assert.Equal(t, time.Second, mr.TTL("revocation:jti:b"))

	list, err := store.ListRevokedJTIs(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestSecurityState_ChunkedRestore(t *testing.T) {
	store, mr := setupMarkerStore(t)
	ctx := context.Background()
	n := 2500
	jtis := make([]RevokedJTI, n)
	qs := make([]QuarantinedPrincipal, n)
	for i := 0; i < n; i++ {
		jtis[i] = RevokedJTI{JTI: fmt.Sprintf("j%d", i), TTL: time.Minute}
		qs[i] = QuarantinedPrincipal{PrincipalID: fmt.Sprintf("p%d", i), TTL: time.Minute}
	}
	require.NoError(t, store.ReconstructRevocations(ctx, jtis))
	require.NoError(t, store.ReconstructQuarantines(ctx, qs))
	assert.True(t, mr.Exists("revocation:jti:j2499"))
	assert.True(t, mr.Exists("quarantine:principal:p2499"))
	v, _ := mr.Get("quarantine:principal:p0")
	assert.Equal(t, "DISASTER_RECOVERY_RECONSTRUCTED", v)
}

func TestSecurityState_ListRevokedJTIsReportsRemainingTTL(t *testing.T) {
	store, _ := setupMarkerStore(t)
	ctx := context.Background()

	require.NoError(t, store.RevokeJTI(ctx, "keep", 5*time.Minute))
	list, err := store.ListRevokedJTIs(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "keep", list[0].JTI)
	assert.Equal(t, 5*time.Minute, list[0].TTL)
}
