package control

// SecurityState keeps Redis security state (principal quarantines and revoked
// JTIs) consistent with Postgres, which is the source of truth (REV-03).
//
// Consistency model (documented for the DR runbook): handlers write Postgres
// first and then Redis. After a Redis wipe, Reconstruct restores every
// unexpired row with its remaining TTL and only then writes the marker that
// gateways require. Reconcile runs every reconcile interval (30s default) and
// re-asserts missing rows and prunes stray quarantine keys. The resurrection
// race (a Reconcile that read a row just before an unquarantine deleted it)
// and the partial-restore window (old marker, lost keys) are therefore both
// bounded by the reconcile interval.
//
// The opposite, fail-open race (WR-01) is closed within one process: Reconcile
// snapshots Redis, lists Postgres and then prunes Redis keys without a row, so
// an unquarantine + re-quarantine landing between the list and the prune would
// have its fresh Redis key deleted. The HTTP write-through handlers therefore
// run their Postgres+Redis writes via SerialiseWrite, under the same mutex as
// Reconstruct and Reconcile, so no write can interleave with a reconcile pass.
//
// Remaining gap for multi-replica control planes: the mutex is per process.
// With several control-plane replicas, a write handled by replica A can still
// interleave with a Reconcile on replica B and lose a fresh quarantine key
// until the next reconcile (bounded by the reconcile interval). Closing that
// needs a cross-replica guard, for example a Postgres advisory lock held by
// both the write-through path and Reconcile, or a per-write generation stored
// as the Redis value and pruned only with a compare-and-delete.

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"aegis/internal/revocation"
	"aegis/internal/storage"
	"aegis/internal/telemetry"
)

// SecurityStateRepository is the durable store of security state.
type SecurityStateRepository interface {
	UpsertQuarantine(ctx context.Context, q storage.QuarantineRecord) error
	DeleteQuarantine(ctx context.Context, principalID string) error
	ListUnexpiredQuarantines(ctx context.Context) ([]storage.QuarantineRecord, error)
	UpsertRevokedToken(ctx context.Context, t storage.RevokedTokenRecord) error
	ListUnexpiredRevokedTokens(ctx context.Context) ([]storage.RevokedTokenRecord, error)
	DeleteExpired(ctx context.Context) (int64, error)
}

const (
	defaultReconcileInterval   = 30 * time.Second
	defaultMarkerCheckInterval = 2 * time.Second
)

// SecurityState reconstructs, reconciles and reports on Redis security state.
type SecurityState struct {
	repo  SecurityStateRepository
	store *revocation.Store

	pinger         func(context.Context) error
	reconcileEvery time.Duration
	markerEvery    time.Duration
	clock          func() time.Time
	metrics        *telemetry.Metrics

	mu           sync.Mutex // serialises Reconstruct / Reconcile / SerialiseWrite
	backfillDone atomic.Bool
}

// SecurityStateOption configures a SecurityState.
type SecurityStateOption func(*SecurityState)

// WithReconcileInterval sets the reconcile period.
func WithReconcileInterval(d time.Duration) SecurityStateOption {
	return func(s *SecurityState) { s.reconcileEvery = d }
}

// WithMarkerCheckInterval sets the marker loss detection period.
func WithMarkerCheckInterval(d time.Duration) SecurityStateOption {
	return func(s *SecurityState) { s.markerEvery = d }
}

// WithClock overrides the clock used to compute remaining TTLs.
func WithClock(f func() time.Time) SecurityStateOption {
	return func(s *SecurityState) { s.clock = f }
}

// WithPinger sets the Postgres reachability check used by Ready.
func WithPinger(f func(context.Context) error) SecurityStateOption {
	return func(s *SecurityState) { s.pinger = f }
}

// WithSecurityStateMetrics attaches telemetry (optional).
func WithSecurityStateMetrics(m *telemetry.Metrics) SecurityStateOption {
	return func(s *SecurityState) { s.metrics = m }
}

// NewSecurityState builds the service.
func NewSecurityState(repo SecurityStateRepository, store *revocation.Store, opts ...SecurityStateOption) *SecurityState {
	s := &SecurityState{
		repo:           repo,
		store:          store,
		reconcileEvery: defaultReconcileInterval,
		markerEvery:    defaultMarkerCheckInterval,
		clock:          time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *SecurityState) record(err error) {
	if s.metrics == nil {
		return
	}
	if err != nil {
		s.metrics.SecurityStateReconstructTotal.WithLabelValues("error").Inc()
		return
	}
	s.metrics.SecurityStateReconstructTotal.WithLabelValues("ok").Inc()
	s.metrics.SecurityStateLastReconstructTimestamp.Set(float64(s.clock().Unix()))
}

// Reconstruct restores all unexpired Postgres rows into Redis and then writes
// the marker. The marker is written last: if restoring fails it is not written.
func (s *SecurityState) Reconstruct(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.reconstructLocked(ctx)
	s.record(err)
	return err
}

func (s *SecurityState) reconstructLocked(ctx context.Context) error {
	ready, err := s.store.SecurityStateReady(ctx)
	if err != nil {
		return err
	}
	if !ready && !s.backfillDone.Load() {
		if err := s.backfillFromRedis(ctx); err != nil {
			return fmt.Errorf("security state backfill: %w", err)
		}
		s.backfillDone.Store(true)
	}
	if err := s.restoreLocked(ctx); err != nil {
		return err
	}
	if _, err := s.store.MarkSecurityStateReady(ctx); err != nil {
		return err
	}
	return nil
}

// restoreLocked pushes every unexpired repo row into Redis with remaining TTL.
func (s *SecurityState) restoreLocked(ctx context.Context) error {
	now := s.clock()
	qs, err := s.repo.ListUnexpiredQuarantines(ctx)
	if err != nil {
		return fmt.Errorf("list quarantines: %w", err)
	}
	toks, err := s.repo.ListUnexpiredRevokedTokens(ctx)
	if err != nil {
		return fmt.Errorf("list revoked tokens: %w", err)
	}
	var rq []revocation.QuarantinedPrincipal
	for _, q := range qs {
		rem := q.ExpiresAt.Sub(now)
		if rem <= 0 {
			continue
		}
		if rem < time.Second {
			rem = time.Second
		}
		rq = append(rq, revocation.QuarantinedPrincipal{PrincipalID: q.PrincipalID, Reason: q.Reason, TTL: rem})
	}
	var rj []revocation.RevokedJTI
	for _, t := range toks {
		rem := t.ExpiresAt.Sub(now)
		if rem <= 0 {
			continue
		}
		if rem < time.Second {
			rem = time.Second
		}
		rj = append(rj, revocation.RevokedJTI{JTI: t.JTI, TTL: rem})
	}
	if err := s.store.ReconstructQuarantines(ctx, rq); err != nil {
		return err
	}
	return s.store.ReconstructRevocations(ctx, rj)
}

// backfillFromRedis imports pre-Phase-11 Redis-only state into the repo.
// It is a no-op when Redis is empty (for example after FLUSHALL). Errors from
// reading Redis (including TTL reads) are returned, never swallowed, so a
// failed import never writes rows with a zero or short TTL.
func (s *SecurityState) backfillFromRedis(ctx context.Context) error {
	now := s.clock()
	qs, err := s.store.ListQuarantined(ctx)
	if err != nil {
		return err
	}
	for _, q := range qs {
		ttl := q.TTL
		if ttl == -2 {
			continue // expired between SCAN and TTL
		}
		if ttl <= 0 {
			ttl = 24 * time.Hour // persistent key: use the store default horizon
		}
		if err := s.repo.UpsertQuarantine(ctx, storage.QuarantineRecord{
			PrincipalID: q.PrincipalID, Reason: q.Reason, CreatedBy: "backfill",
			CreatedAt: now, ExpiresAt: now.Add(ttl),
		}); err != nil {
			return err
		}
	}
	jtis, err := s.store.ListRevokedJTIs(ctx)
	if err != nil {
		return err
	}
	for _, j := range jtis {
		ttl := j.TTL
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		if err := s.repo.UpsertRevokedToken(ctx, storage.RevokedTokenRecord{
			JTI: j.JTI, Reason: "backfill", CreatedBy: "backfill",
			CreatedAt: now, ExpiresAt: now.Add(ttl),
		}); err != nil {
			return err
		}
	}
	return nil
}

// SerialiseWrite runs fn while holding the mutex that Reconstruct and
// Reconcile hold, so an operator write-through (Postgres then Redis) can
// never interleave with a reconcile pass and have its fresh Redis key pruned
// (WR-01). fn must not call Reconstruct or Reconcile.
func (s *SecurityState) SerialiseWrite(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

// Reconcile heals Redis from Postgres: deletes expired rows, re-asserts all
// unexpired rows, and (once backfill has completed) removes quarantine keys
// that have no Postgres row.
func (s *SecurityState) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ready, err := s.store.SecurityStateReady(ctx); err == nil && ready {
		// A marker proves a Phase-11 reconstruct (including backfill) already ran.
		s.backfillDone.Store(true)
	}

	// Snapshot Redis BEFORE the repo: a key in the Redis snapshot was written
	// after its PG row, so the later PG list is guaranteed to contain it.
	var redisQ []revocation.QuarantinedPrincipal
	prune := s.backfillDone.Load()
	if prune {
		var err error
		if redisQ, err = s.store.ListQuarantined(ctx); err != nil {
			return err
		}
	}
	if _, err := s.repo.DeleteExpired(ctx); err != nil {
		return fmt.Errorf("delete expired: %w", err)
	}
	if err := s.restoreLocked(ctx); err != nil {
		return err
	}
	if prune {
		rows, err := s.repo.ListUnexpiredQuarantines(ctx)
		if err != nil {
			return err
		}
		have := make(map[string]struct{}, len(rows))
		for _, r := range rows {
			have[r.PrincipalID] = struct{}{}
		}
		for _, q := range redisQ {
			if _, ok := have[q.PrincipalID]; !ok {
				if err := s.store.RemoveQuarantine(ctx, q.PrincipalID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Run blocks until ctx is done, detecting marker loss and reconciling.
// Errors are logged and counted, never fatal.
func (s *SecurityState) Run(ctx context.Context) {
	s.checkMarker(ctx)
	markerT := time.NewTicker(s.markerEvery)
	defer markerT.Stop()
	reconT := time.NewTicker(s.reconcileEvery)
	defer reconT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-markerT.C:
			s.checkMarker(ctx)
		case <-reconT.C:
			if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
				log.Printf("security state reconcile failed: %v", err)
				s.record(err)
			}
		}
	}
}

func (s *SecurityState) checkMarker(ctx context.Context) {
	ready, err := s.store.SecurityStateReady(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("security state marker check failed: %v", err)
		}
		return
	}
	if ready {
		return
	}
	if err := s.Reconstruct(ctx); err != nil && ctx.Err() == nil {
		log.Printf("security state reconstruct failed: %v", err)
	}
}

// Ready returns nil iff Postgres is reachable and the marker is present.
func (s *SecurityState) Ready(ctx context.Context) error {
	if s.pinger != nil {
		if err := s.pinger(ctx); err != nil {
			return fmt.Errorf("postgres unreachable: %w", err)
		}
	}
	ok, err := s.store.SecurityStateReady(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w", revocation.ErrSecurityStateMissing)
	}
	return nil
}
