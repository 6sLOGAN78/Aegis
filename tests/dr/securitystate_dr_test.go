package dr

import (
	"context"
	"sync"
	"time"

	"aegis/internal/storage"
)

// memSecRepo is an in-memory control.SecurityStateRepository for hermetic DR drills.
type memSecRepo struct {
	mu sync.Mutex
	q  map[string]storage.QuarantineRecord
	t  map[string]storage.RevokedTokenRecord
}

func newMemSecRepo() *memSecRepo {
	return &memSecRepo{q: map[string]storage.QuarantineRecord{}, t: map[string]storage.RevokedTokenRecord{}}
}

func (m *memSecRepo) UpsertQuarantine(_ context.Context, q storage.QuarantineRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.q[q.PrincipalID] = q
	return nil
}

func (m *memSecRepo) DeleteQuarantine(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.q, id)
	return nil
}

func (m *memSecRepo) ListUnexpiredQuarantines(context.Context) ([]storage.QuarantineRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.QuarantineRecord
	for _, q := range m.q {
		if q.ExpiresAt.After(time.Now()) {
			out = append(out, q)
		}
	}
	return out, nil
}

func (m *memSecRepo) UpsertRevokedToken(_ context.Context, t storage.RevokedTokenRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t[t.JTI] = t
	return nil
}

func (m *memSecRepo) ListUnexpiredRevokedTokens(context.Context) ([]storage.RevokedTokenRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.RevokedTokenRecord
	for _, t := range m.t {
		if t.ExpiresAt.After(time.Now()) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *memSecRepo) DeleteExpired(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k, q := range m.q {
		if !q.ExpiresAt.After(time.Now()) {
			delete(m.q, k)
			n++
		}
	}
	for k, t := range m.t {
		if !t.ExpiresAt.After(time.Now()) {
			delete(m.t, k)
			n++
		}
	}
	return n, nil
}
