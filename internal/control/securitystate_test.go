package control

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"aegis/internal/revocation"
	"aegis/internal/storage"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSecRepo is an in-memory SecurityStateRepository with injectable errors.
type fakeSecRepo struct {
	mu  sync.Mutex
	q   map[string]storage.QuarantineRecord
	t   map[string]storage.RevokedTokenRecord
	err error // returned by every call when set
	now func() time.Time
}

func newFakeSecRepo() *fakeSecRepo {
	return &fakeSecRepo{q: map[string]storage.QuarantineRecord{}, t: map[string]storage.RevokedTokenRecord{}, now: time.Now}
}

func (f *fakeSecRepo) UpsertQuarantine(_ context.Context, q storage.QuarantineRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.q[q.PrincipalID] = q
	return nil
}

func (f *fakeSecRepo) DeleteQuarantine(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	delete(f.q, id)
	return nil
}

func (f *fakeSecRepo) ListUnexpiredQuarantines(context.Context) ([]storage.QuarantineRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []storage.QuarantineRecord
	for _, q := range f.q {
		if q.ExpiresAt.After(f.now()) {
			out = append(out, q)
		}
	}
	return out, nil
}

func (f *fakeSecRepo) UpsertRevokedToken(_ context.Context, t storage.RevokedTokenRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.t[t.JTI] = t
	return nil
}

func (f *fakeSecRepo) ListUnexpiredRevokedTokens(context.Context) ([]storage.RevokedTokenRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []storage.RevokedTokenRecord
	for _, t := range f.t {
		if t.ExpiresAt.After(f.now()) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeSecRepo) DeleteExpired(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	var n int64
	for k, q := range f.q {
		if !q.ExpiresAt.After(f.now()) {
			delete(f.q, k)
			n++
		}
	}
	for k, t := range f.t {
		if !t.ExpiresAt.After(f.now()) {
			delete(f.t, k)
			n++
		}
	}
	return n, nil
}

func newSecEnv(t *testing.T) (*miniredis.Miniredis, *revocation.Store, *fakeSecRepo) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, revocation.NewStore(rdb), newFakeSecRepo()
}

func TestSecurityStateReconstructRestoresWithRemainingTTL(t *testing.T) {
	mr, store, repo := newSecEnv(t)
	ctx := context.Background()
	now := time.Now()
	repo.q["p1"] = storage.QuarantineRecord{PrincipalID: "p1", Reason: "R", ExpiresAt: now.Add(time.Hour)}
	repo.q["old"] = storage.QuarantineRecord{PrincipalID: "old", Reason: "R", ExpiresAt: now.Add(-time.Minute)}
	repo.t["j1"] = storage.RevokedTokenRecord{JTI: "j1", ExpiresAt: now.Add(10 * time.Minute)}
	ss := NewSecurityState(repo, store)

	require.NoError(t, ss.Reconstruct(ctx))

	ttl := mr.TTL("quarantine:principal:p1")
	assert.Greater(t, ttl, 59*time.Minute)
	assert.LessOrEqual(t, ttl, time.Hour)
	assert.False(t, mr.Exists("quarantine:principal:old"))
	jttl := mr.TTL("revocation:jti:j1")
	assert.Greater(t, jttl, 9*time.Minute)
	assert.LessOrEqual(t, jttl, 10*time.Minute)
	assert.True(t, mr.Exists(revocation.SecurityStateMarkerKey))
}

func TestSecurityStateReconstructErrorSkipsMarker(t *testing.T) {
	mr, store, repo := newSecEnv(t)
	repo.err = errors.New("pg down")
	ss := NewSecurityState(repo, store)
	require.Error(t, ss.Reconstruct(context.Background()))
	assert.False(t, mr.Exists(revocation.SecurityStateMarkerKey))
}

func TestSecurityStateReconstructConcurrent(t *testing.T) {
	_, store, repo := newSecEnv(t)
	repo.q["p1"] = storage.QuarantineRecord{PrincipalID: "p1", ExpiresAt: time.Now().Add(time.Hour)}
	ss := NewSecurityState(repo, store)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); assert.NoError(t, ss.Reconstruct(context.Background())) }()
	}
	wg.Wait()
	revoked, _, err := store.CheckRevocation(context.Background(), "p1", "")
	require.NoError(t, err)
	assert.True(t, revoked)
}

func TestSecurityStateBackfillImportsLegacyRedisState(t *testing.T) {
	mr, store, repo := newSecEnv(t)
	ctx := context.Background()
	require.NoError(t, store.QuarantinePrincipal(ctx, "legacy", "OLD", time.Hour))
	require.NoError(t, store.RevokeJTI(ctx, "legacyjti", time.Hour))
	ss := NewSecurityState(repo, store)

	require.NoError(t, ss.Reconstruct(ctx))

	assert.Contains(t, repo.q, "legacy")
	assert.Equal(t, "backfill", repo.q["legacy"].CreatedBy)
	assert.Contains(t, repo.t, "legacyjti")
	require.NoError(t, ss.Reconcile(ctx))
	assert.True(t, mr.Exists("quarantine:principal:legacy"))
	assert.True(t, mr.Exists("revocation:jti:legacyjti"))
}

func TestSecurityStateBackfillNoopAfterFlush(t *testing.T) {
	mr, store, repo := newSecEnv(t)
	mr.FlushAll()
	ss := NewSecurityState(repo, store)
	require.NoError(t, ss.Reconstruct(context.Background()))
	assert.Empty(t, repo.q)
	assert.Empty(t, repo.t)
}

func TestSecurityStateBackfillFailureBlocksMarker(t *testing.T) {
	mr, store, repo := newSecEnv(t)
	ctx := context.Background()
	require.NoError(t, store.QuarantinePrincipal(ctx, "legacy", "OLD", time.Hour))
	repo.err = errors.New("pg down")
	ss := NewSecurityState(repo, store)
	require.Error(t, ss.Reconstruct(ctx))
	assert.False(t, mr.Exists(revocation.SecurityStateMarkerKey))
	assert.True(t, mr.Exists("quarantine:principal:legacy"), "legacy key must not be pruned")
}

func TestSecurityStateMarkerLossTriggersReconstruct(t *testing.T) {
	mr, store, repo := newSecEnv(t)
	repo.q["p1"] = storage.QuarantineRecord{PrincipalID: "p1", Reason: "R", ExpiresAt: time.Now().Add(time.Hour)}
	ss := NewSecurityState(repo, store, WithMarkerCheckInterval(20*time.Millisecond), WithReconcileInterval(time.Hour))
	require.NoError(t, ss.Reconstruct(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ss.Run(ctx); close(done) }()

	mr.FlushAll()
	require.Eventually(t, func() bool {
		return mr.Exists(revocation.SecurityStateMarkerKey) && mr.Exists("quarantine:principal:p1")
	}, 2*time.Second, 10*time.Millisecond)
	cancel()
	<-done
}

func TestSecurityStateReconcileHealsPartialRestore(t *testing.T) {
	mr, store, repo := newSecEnv(t)
	ctx := context.Background()
	repo.q["p1"] = storage.QuarantineRecord{PrincipalID: "p1", Reason: "R", ExpiresAt: time.Now().Add(time.Hour)}
	ss := NewSecurityState(repo, store)
	require.NoError(t, ss.Reconstruct(ctx))

	mr.Del("quarantine:principal:p1") // old marker stays
	require.True(t, mr.Exists(revocation.SecurityStateMarkerKey))
	require.NoError(t, ss.Reconcile(ctx))
	assert.True(t, mr.Exists("quarantine:principal:p1"))
}

func TestSecurityStateReconcilePrunesRowlessAndExpired(t *testing.T) {
	mr, store, repo := newSecEnv(t)
	ctx := context.Background()
	ss := NewSecurityState(repo, store)
	require.NoError(t, ss.Reconstruct(ctx))

	require.NoError(t, store.QuarantinePrincipal(ctx, "stray", "X", time.Hour)) // no PG row
	repo.q["exp"] = storage.QuarantineRecord{PrincipalID: "exp", ExpiresAt: time.Now().Add(-time.Second)}
	require.NoError(t, ss.Reconcile(ctx))
	assert.False(t, mr.Exists("quarantine:principal:stray"))
	assert.NotContains(t, repo.q, "exp")
}

func TestSecurityStateReadyReflectsPingerAndMarker(t *testing.T) {
	_, store, repo := newSecEnv(t)
	ctx := context.Background()
	var pingErr error
	ss := NewSecurityState(repo, store, WithPinger(func(context.Context) error { return pingErr }))

	err := ss.Ready(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, revocation.ErrSecurityStateMissing))

	require.NoError(t, ss.Reconstruct(ctx))
	assert.NoError(t, ss.Ready(ctx))

	pingErr = errors.New("down")
	err = ss.Ready(ctx)
	require.Error(t, err)
	assert.False(t, errors.Is(err, revocation.ErrSecurityStateMissing))
}
