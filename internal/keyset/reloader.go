// Package keyset loads a directory of Ed25519 public keys and keeps a
// consumer's trusted set in sync with it (polling plus SIGHUP). Every regular
// non-dot file in the directory is a key. A bad set never replaces a good one.
package keyset

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"aegis/internal/keyfile"
	"aegis/internal/telemetry"
)

// Option configures a Reloader.
type Option func(*Reloader)

// WithMetrics enables keyset metrics.
func WithMetrics(m *telemetry.Metrics) Option {
	return func(r *Reloader) { r.metrics = m }
}

// Reloader polls a key directory and applies changes to a consumer.
type Reloader struct {
	family   string
	dir      string
	interval time.Duration
	apply    func([]ed25519.PublicKey)
	metrics  *telemetry.Metrics

	mu       sync.Mutex
	lastHash [sha256.Size]byte
	loaded   bool
}

// New creates a Reloader. family is "jwt" or "snapshot".
func New(family, dir string, interval time.Duration, apply func([]ed25519.PublicKey), opts ...Option) *Reloader {
	r := &Reloader{family: family, dir: dir, interval: interval, apply: apply}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ParseInterval parses a duration, returning def for empty or invalid input
// and clamping to [1s, 10m].
func ParseInterval(s string, def time.Duration) time.Duration {
	d := def
	if s != "" {
		if p, err := time.ParseDuration(s); err == nil && p > 0 {
			d = p
		}
	}
	if d < time.Second {
		d = time.Second
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

type entry struct {
	name string
	key  ed25519.PublicKey
}

func readDir(dir string) ([]entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot read keyset dir: %w", err)
	}
	var out []entry
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		p := filepath.Join(dir, name)
		st, err := os.Stat(p) // follows symlinks
		if err != nil {
			return nil, fmt.Errorf("cannot stat key file %q: %w", name, err)
		}
		if !st.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("cannot read key file %q: %w", name, err)
		}
		k, err := keyfile.ParseEd25519Public(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid key file %q: %w", name, err)
		}
		out = append(out, entry{name: name, key: k})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	ded := out[:0]
	for _, e := range out {
		dup := false
		for _, d := range ded {
			if bytes.Equal(d.key, e.key) {
				dup = true
				break
			}
		}
		if !dup {
			ded = append(ded, e)
		}
	}
	if len(ded) == 0 {
		return nil, errors.New("keyset dir contains no keys")
	}
	return ded, nil
}

// LoadDir reads every regular non-dot file in dir as a public key. Any
// failure, or an empty result, is an error (no partial sets).
func LoadDir(dir string) ([]ed25519.PublicKey, error) {
	es, err := readDir(dir)
	if err != nil {
		return nil, err
	}
	keys := make([]ed25519.PublicKey, len(es))
	for i, e := range es {
		keys[i] = e.key
	}
	return keys, nil
}

func (r *Reloader) count(result string) {
	if r.metrics != nil && r.metrics.KeysetReloadTotal != nil {
		r.metrics.KeysetReloadTotal.WithLabelValues(r.family, result).Inc()
	}
}

// Reload re-reads the directory and applies it when changed. On error the
// previously applied set is retained.
func (r *Reloader) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	es, err := readDir(r.dir)
	if err != nil {
		log.Printf("keyset[%s]: reload rejected, keeping last good set: %v", r.family, err)
		r.count("error")
		return err
	}
	h := sha256.New()
	keys := make([]ed25519.PublicKey, len(es))
	for i, e := range es {
		h.Write(e.key)
		keys[i] = e.key
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	if r.loaded && sum == r.lastHash {
		r.count("unchanged")
		return nil
	}
	r.apply(keys)
	r.lastHash, r.loaded = sum, true
	r.count("ok")
	if r.metrics != nil {
		if r.metrics.KeysetKeys != nil {
			r.metrics.KeysetKeys.WithLabelValues(r.family).Set(float64(len(keys)))
		}
		if r.metrics.KeysetLastSuccessTimestamp != nil {
			r.metrics.KeysetLastSuccessTimestamp.WithLabelValues(r.family).Set(float64(time.Now().Unix()))
		}
	}
	log.Printf("keyset[%s]: loaded %d key(s)", r.family, len(keys))
	return nil
}

// Load performs the synchronous initial load.
func (r *Reloader) Load() error { return r.Reload() }

// Run reloads on every tick and on SIGHUP until ctx is done.
func (r *Reloader) Run(ctx context.Context) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = r.Reload()
		case <-hup:
			_ = r.Reload()
		}
	}
}
