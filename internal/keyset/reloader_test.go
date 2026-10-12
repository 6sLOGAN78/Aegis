package keyset

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"aegis/internal/identity"
	"aegis/internal/snapshot"
	"aegis/internal/telemetry"

	"github.com/golang-jwt/jwt/v5"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func writeKey(t *testing.T, dir, name string, pub ed25519.PublicKey) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(base64.StdEncoding.EncodeToString(pub)), 0o600))
}

type sink struct {
	mu    sync.Mutex
	keys  []ed25519.PublicKey
	calls int
}

func (s *sink) apply(k []ed25519.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append([]ed25519.PublicKey(nil), k...)
	s.calls++
}

func (s *sink) snapshot() ([]ed25519.PublicKey, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ed25519.PublicKey(nil), s.keys...), s.calls
}

func TestLoadDirNamesAndIgnores(t *testing.T) {
	dir := t.TempDir()
	p0, _ := newKey(t)
	p1, _ := newKey(t)
	p2, _ := newKey(t)
	writeKey(t, dir, "keyset.pub.0", p0)
	writeKey(t, dir, "current.pub", p1)
	writeKey(t, dir, "new.pub", p2)
	writeKey(t, dir, "dup.pub", p0) // duplicate of keyset.pub.0
	pHidden, _ := newKey(t)
	writeKey(t, dir, ".hidden", pHidden)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "..data"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0o755))

	keys, err := LoadDir(dir)
	require.NoError(t, err)
	assert.Len(t, keys, 3)
	for _, k := range keys {
		assert.False(t, k.Equal(pHidden))
	}
}

func TestLoadDirRejects(t *testing.T) {
	_, err := LoadDir(filepath.Join(t.TempDir(), "missing"))
	assert.Error(t, err)

	empty := t.TempDir()
	_, err = LoadDir(empty)
	assert.Error(t, err)

	bad := t.TempDir()
	p, _ := newKey(t)
	writeKey(t, bad, "good.pub", p)
	require.NoError(t, os.WriteFile(filepath.Join(bad, "bad.pub"), []byte("garbage!!"), 0o600))
	_, err = LoadDir(bad)
	assert.Error(t, err)
}

func TestReloaderBadSetKeepsLastGoodAndRecovers(t *testing.T) {
	dir := t.TempDir()
	p0, _ := newKey(t)
	writeKey(t, dir, "a.pub", p0)
	s := &sink{}
	m := telemetry.NewMetrics()
	r := New("jwt", dir, time.Hour, s.apply, WithMetrics(m))
	require.NoError(t, r.Load())
	keys, calls := s.snapshot()
	require.Len(t, keys, 1)
	assert.Equal(t, 1, calls)

	// unchanged -> no apply
	require.NoError(t, r.Reload())
	_, calls = s.snapshot()
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1.0, val(m.KeysetReloadTotal.WithLabelValues("jwt", "unchanged")))

	// garbage file -> rejected
	require.NoError(t, os.WriteFile(filepath.Join(dir, "z.pub"), []byte("nope"), 0o600))
	require.Error(t, r.Reload())
	keys, calls = s.snapshot()
	assert.Len(t, keys, 1)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1.0, val(m.KeysetReloadTotal.WithLabelValues("jwt", "error")))

	// empty dir -> rejected
	require.NoError(t, os.Remove(filepath.Join(dir, "z.pub")))
	require.NoError(t, os.Remove(filepath.Join(dir, "a.pub")))
	require.Error(t, r.Reload())
	keys, _ = s.snapshot()
	assert.Len(t, keys, 1)

	// dir removed -> rejected
	require.NoError(t, os.Remove(dir))
	require.Error(t, r.Reload())
	keys, _ = s.snapshot()
	assert.Len(t, keys, 1)

	// recover
	require.NoError(t, os.Mkdir(dir, 0o755))
	p1, _ := newKey(t)
	writeKey(t, dir, "b.pub", p1)
	require.NoError(t, r.Reload())
	keys, calls = s.snapshot()
	require.Len(t, keys, 1)
	assert.True(t, keys[0].Equal(p1))
	assert.Equal(t, 2, calls)
	assert.Equal(t, 1.0, val(m.KeysetKeys.WithLabelValues("jwt")))
	assert.Greater(t, val(m.KeysetLastSuccessTimestamp.WithLabelValues("jwt")), 0.0)
}

func TestConfigMapLayoutAtomicSwap(t *testing.T) {
	root := t.TempDir()
	pA, _ := newKey(t)
	pB, _ := newKey(t)

	mk := func(ts string, pub ed25519.PublicKey) {
		d := filepath.Join(root, ts)
		require.NoError(t, os.Mkdir(d, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(d, "k.pub"), []byte(base64.StdEncoding.EncodeToString(pub)), 0o600))
	}
	swap := func(ts string) {
		tmp := filepath.Join(root, "..data_tmp")
		_ = os.Remove(tmp)
		require.NoError(t, os.Symlink(ts, tmp))
		require.NoError(t, os.Rename(tmp, filepath.Join(root, "..data")))
	}
	mk("..2024_01_01", pA)
	swap("..2024_01_01")
	require.NoError(t, os.Symlink(filepath.Join("..data", "k.pub"), filepath.Join(root, "k.pub")))

	s := &sink{}
	r := New("snapshot", root, time.Hour, s.apply)
	require.NoError(t, r.Load())
	keys, _ := s.snapshot()
	require.Len(t, keys, 1)
	assert.True(t, keys[0].Equal(pA))

	mk("..2024_01_02", pB)
	swap("..2024_01_02")
	require.NoError(t, r.Reload())
	keys, _ = s.snapshot()
	require.Len(t, keys, 1)
	assert.True(t, keys[0].Equal(pB))
}

func TestRunTicksAndSIGHUP(t *testing.T) {
	dir := t.TempDir()
	p0, _ := newKey(t)
	writeKey(t, dir, "a.pub", p0)
	s := &sink{}
	r := New("jwt", dir, time.Hour, s.apply)
	require.NoError(t, r.Load())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond) // let Notify register

	p1, _ := newKey(t)
	writeKey(t, dir, "b.pub", p1)
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))
	require.Eventually(t, func() bool {
		k, _ := s.snapshot()
		return len(k) == 2
	}, 2*time.Second, 10*time.Millisecond)
	cancel()
	<-done

	// ticker path
	dir2 := t.TempDir()
	writeKey(t, dir2, "a.pub", p0)
	s2 := &sink{}
	r2 := New("jwt", dir2, 20*time.Millisecond, s2.apply)
	require.NoError(t, r2.Load())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go r2.Run(ctx2)
	writeKey(t, dir2, "b.pub", p1)
	require.Eventually(t, func() bool {
		k, _ := s2.snapshot()
		return len(k) == 2
	}, 2*time.Second, 10*time.Millisecond)
}

func TestParseInterval(t *testing.T) {
	def := 10 * time.Second
	assert.Equal(t, def, ParseInterval("", def))
	assert.Equal(t, def, ParseInterval("abc", def))
	assert.Equal(t, time.Second, ParseInterval("1ms", def))
	assert.Equal(t, 10*time.Minute, ParseInterval("2h", def))
	assert.Equal(t, 30*time.Second, ParseInterval("30s", def))
}

func TestConcurrentVerifyDuringSwaps(t *testing.T) {
	dir := t.TempDir()
	pA, privA := newKey(t)
	pB, privB := newKey(t)
	writeKey(t, dir, "a.pub", pA)

	ver := snapshot.NewVerifier(pA)
	val := identity.NewTokenValidator("iss", "aud", pA)
	apply := func(k []ed25519.PublicKey) {
		ver.SetTrustedKeys(k)
		cks := make([]crypto.PublicKey, len(k))
		for i := range k {
			cks[i] = k[i]
		}
		val.SetPublicKeys(cks)
	}
	r := New("jwt", dir, 5*time.Millisecond, apply)
	require.NoError(t, r.Load())
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	mint := func(priv ed25519.PrivateKey) string {
		claims := identity.UserClaims{RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "iss", Audience: jwt.ClaimStrings{"aud"}, Subject: "u",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		}, Roles: []string{"developer"}}
		s, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(priv)
		require.NoError(t, err)
		return s
	}
	tokA := mint(privA) // key A stays in every set below

	var failures atomic.Int64
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := val.ValidateBearerToken("Bearer " + tokA); err != nil {
					failures.Add(1)
				}
			}
		}()
	}
	_ = privB
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			writeKey(t, dir, "b.pub", pB)
		} else {
			_ = os.Remove(filepath.Join(dir, "b.pub"))
		}
		time.Sleep(3 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	cancel()
	assert.Zero(t, failures.Load(), "key A is always present; verification must never fail or see an empty set")
}

type writer interface{ Write(*dto.Metric) error }

func val(w writer) float64 {
	var m dto.Metric
	_ = w.Write(&m)
	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return m.Gauge.GetValue()
}

// WR-05: a prune shipped together with an invalid file is rejected as a whole
// (no partial sets), so the removed key stays trusted. The condition must be
// loud: the error names the offending file, every attempt is counted, and
// aegis_keyset_reload_failing stays at 1 until a reload succeeds.
func TestReloaderRejectedPruneIsReportedUntilFixed(t *testing.T) {
	dir := t.TempDir()
	compromised, _ := newKey(t)
	good, _ := newKey(t)
	writeKey(t, dir, "keyset.pub.0", compromised)
	writeKey(t, dir, "keyset.pub.1", good)
	s := &sink{}
	m := telemetry.NewMetrics()
	r := New("jwt", dir, time.Hour, s.apply, WithMetrics(m))
	require.NoError(t, r.Load())
	failing, n, _ := r.Failing()
	assert.False(t, failing)
	assert.Equal(t, 0, n)
	assert.Equal(t, 0.0, val(m.KeysetReloadFailing.WithLabelValues("jwt")))

	// Operator prunes the compromised key, but an editor backup lands too.
	require.NoError(t, os.Remove(filepath.Join(dir, "keyset.pub.0")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keyset.pub.1~"), []byte("garbage"), 0o600))

	for i := 1; i <= 3; i++ {
		err := r.Reload()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keyset.pub.1~", "error must name the offending file")
		failing, n, lastErr := r.Failing()
		assert.True(t, failing)
		assert.Equal(t, i, n)
		assert.Equal(t, err, lastErr)
		assert.Equal(t, 1.0, val(m.KeysetReloadFailing.WithLabelValues("jwt")))
		assert.Equal(t, float64(i), val(m.KeysetReloadTotal.WithLabelValues("jwt", "error")))
	}
	keys, _ := s.snapshot()
	require.Len(t, keys, 2, "last good set (fail-safe) is kept, never a partial set")

	// Fixing the directory applies the prune and clears the signal.
	require.NoError(t, os.Remove(filepath.Join(dir, "keyset.pub.1~")))
	require.NoError(t, r.Reload())
	keys, _ = s.snapshot()
	require.Len(t, keys, 1)
	assert.True(t, keys[0].Equal(good))
	failing, n, _ = r.Failing()
	assert.False(t, failing)
	assert.Equal(t, 0, n)
	assert.Equal(t, 0.0, val(m.KeysetReloadFailing.WithLabelValues("jwt")))
}
