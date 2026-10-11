package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"aegis/internal/revocation"
	"aegis/internal/telemetry"
)

func newSecurityStateFixture(t *testing.T) (*miniredis.Miniredis, *revocation.Store, *securityStatePoller) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := revocation.NewStore(client, revocation.WithRequireSecurityMarker())
	p := newSecurityStatePoller(store, telemetry.NewMetrics(), 50*time.Millisecond)
	return mr, store, p
}

func TestSecurityStatePoller(t *testing.T) {
	mr, store, p := newSecurityStateFixture(t)
	require.True(t, p.Missing(), "fail closed before first poll")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Marker absent: stays missing.
	time.Sleep(150 * time.Millisecond)
	require.True(t, p.Missing())

	_, err := store.MarkSecurityStateReady(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return !p.Missing() }, 2*time.Second, 20*time.Millisecond)

	mr.FlushAll()
	require.Eventually(t, func() bool { return p.Missing() }, 2*time.Second, 20*time.Millisecond)

	_, err = store.MarkSecurityStateReady(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return !p.Missing() }, 2*time.Second, 20*time.Millisecond)

	mr.Close()
	require.Eventually(t, func() bool { return p.Missing() }, 2*time.Second, 20*time.Millisecond)
}

func TestSecurityStateGateNeverAuthorizesWhileMissing(t *testing.T) {
	mr, store, _ := newSecurityStateFixture(t)
	ctx := context.Background()
	require.NoError(t, store.QuarantinePrincipal(ctx, "spiffe://x/bad", "", time.Hour))
	mr.Del(revocation.SecurityStateMarkerKey)

	revoked, _, err := store.CheckRevocation(ctx, "spiffe://x/bad", "")
	require.True(t, errors.Is(err, revocation.ErrSecurityStateMissing))
	require.False(t, revoked)
	revoked, _, err = store.CheckRevocation(ctx, "spiffe://x/good", "jti")
	require.True(t, errors.Is(err, revocation.ErrSecurityStateMissing))
	require.False(t, revoked)

	_, err = store.MarkSecurityStateReady(ctx)
	require.NoError(t, err)
	revoked, reason, err := store.CheckRevocation(ctx, "spiffe://x/bad", "")
	require.NoError(t, err)
	require.True(t, revoked)
	require.Equal(t, "PRINCIPAL_QUARANTINED", reason)
	revoked, _, err = store.CheckRevocation(ctx, "spiffe://x/good", "jti")
	require.NoError(t, err)
	require.False(t, revoked)
}

func TestSecurityStateWiring(t *testing.T) {
	b, err := os.ReadFile("main.go")
	require.NoError(t, err)
	src := string(b)
	require.Contains(t, src, "revocation.WithRequireSecurityMarker()")
	require.Contains(t, src, "revocation.ErrSecurityStateMissing")
	require.Contains(t, src, "https://aegis.local/errors/security-state-missing")
	require.Contains(t, src, "proxy.WithSecurityState(")
	require.GreaterOrEqual(t, strings.Count(src, `SetDecision("deny", "SECURITY_STATE_MISSING")`), 2)

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		c, err := os.ReadFile(f)
		require.NoError(t, err)
		require.NotContains(t, string(c), "pgx", f)
	}
}
