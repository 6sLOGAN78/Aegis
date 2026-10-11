package revocation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ErrRevocationDependency indicates a Redis failure or timeout during revocation check.
var ErrRevocationDependency = errors.New("revocation dependency check failed")

// SecurityStateMarkerKey is the Redis key whose presence proves security state was restored from Postgres.
const SecurityStateMarkerKey = "aegis:security-state:generation"

// ErrSecurityStateMissing indicates the Redis security-state marker is absent (e.g. Redis was wiped
// and revocations have not been reconstructed), so revocation results cannot be trusted.
var ErrSecurityStateMissing = errors.New("security state marker missing")

// restoreChunkSize is the maximum number of keys written per pipeline during restore.
const restoreChunkSize = 1000

// Store manages ephemeral JTI revocation blocklists and principal quarantine states in Redis (REV-03, REV-04).
type Store struct {
	client        *redis.Client
	requireMarker bool
}

// Option configures a Store.
type Option func(*Store)

// WithRequireSecurityMarker makes CheckRevocation fail closed with ErrSecurityStateMissing
// whenever the security-state marker is absent from Redis.
func WithRequireSecurityMarker() Option {
	return func(s *Store) { s.requireMarker = true }
}

// NewStore creates a new Store backed by the provided Redis client.
func NewStore(client *redis.Client, opts ...Option) *Store {
	s := &Store{
		client: client,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// MarkSecurityStateReady writes the security-state marker (no TTL) and returns its generation value.
func (s *Store) MarkSecurityStateReady(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	gen := time.Now().UTC().Format(time.RFC3339) + "/" + uuid.NewString()
	if err := s.client.Set(ctx, SecurityStateMarkerKey, gen, 0).Err(); err != nil {
		return "", fmt.Errorf("failed to write security state marker: %w", err)
	}
	return gen, nil
}

// SecurityStateReady reports whether the security-state marker exists.
func (s *Store) SecurityStateReady(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	n, err := s.client.Exists(ctx, SecurityStateMarkerKey).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check security state marker: %w", err)
	}
	return n > 0, nil
}

// RevokeJTI records a token JTI as revoked with the specified TTL.
// Defaults to 24 hours if ttl <= 0.
func (s *Store) RevokeJTI(ctx context.Context, jti string, ttl time.Duration) error {
	if jti == "" {
		return errors.New("cannot revoke empty jti")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	key := "revocation:jti:" + jti
	return s.client.Set(ctx, key, "1", ttl).Err()
}

// QuarantinePrincipal records an administrative quarantine on a principal with an optional reason and TTL.
// Defaults to 24 hours if ttl <= 0.
func (s *Store) QuarantinePrincipal(ctx context.Context, principalID string, reason string, ttl time.Duration) error {
	if principalID == "" {
		return errors.New("cannot quarantine empty principal ID")
	}
	if reason == "" {
		reason = "ADMINISTRATIVE_QUARANTINE"
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	key := "quarantine:principal:" + principalID
	return s.client.Set(ctx, key, reason, ttl).Err()
}

// RemoveQuarantine removes an active quarantine from a principal.
func (s *Store) RemoveQuarantine(ctx context.Context, principalID string) error {
	if principalID == "" {
		return errors.New("cannot un-quarantine empty principal ID")
	}
	key := "quarantine:principal:" + principalID
	return s.client.Del(ctx, key).Err()
}

// CheckRevocation performs a pipelined check for principal quarantine and JTI revocation in a single network roundtrip.
// All Redis calls are strictly bounded by a 200ms context deadline.
// Returns (revoked, reason, err).
func (s *Store) CheckRevocation(ctx context.Context, principalID string, jti string) (bool, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	pipe := s.client.Pipeline()
	var quarCmd *redis.IntCmd
	var jtiCmd *redis.IntCmd
	var markerCmd *redis.IntCmd

	if s.requireMarker {
		markerCmd = pipe.Exists(ctx, SecurityStateMarkerKey)
	}
	if principalID != "" {
		quarCmd = pipe.Exists(ctx, "quarantine:principal:"+principalID)
	}
	if jti != "" {
		jtiCmd = pipe.Exists(ctx, "revocation:jti:"+jti)
	}

	if markerCmd == nil && quarCmd == nil && jtiCmd == nil {
		return false, "", nil
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return false, "", fmt.Errorf("%w: %w", ErrRevocationDependency, err)
	}

	if markerCmd != nil && markerCmd.Val() == 0 {
		return false, "", ErrSecurityStateMissing
	}

	if quarCmd != nil && quarCmd.Val() > 0 {
		return true, "PRINCIPAL_QUARANTINED", nil
	}

	if jtiCmd != nil && jtiCmd.Val() > 0 {
		return true, "TOKEN_REVOKED", nil
	}

	return false, "", nil
}

// QuarantinedPrincipal represents an actively quarantined principal in Redis.
type QuarantinedPrincipal struct {
	PrincipalID string        `json:"principal_id"`
	Reason      string        `json:"reason"`
	TTL         time.Duration `json:"ttl"`
}

// ListQuarantined retrieves all actively quarantined principals from Redis.
func (s *Store) ListQuarantined(ctx context.Context) ([]QuarantinedPrincipal, error) {
	if s.client == nil {
		return []QuarantinedPrincipal{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	var cursor uint64
	var keys []string
	for {
		var k []string
		var err error
		k, cursor, err = s.client.Scan(ctx, cursor, "quarantine:principal:*", 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, k...)
		if cursor == 0 {
			break
		}
	}

	if len(keys) == 0 {
		return []QuarantinedPrincipal{}, nil
	}

	results := make([]QuarantinedPrincipal, 0, len(keys))
	pipe := s.client.Pipeline()
	getCmds := make([]*redis.StringCmd, len(keys))
	ttlCmds := make([]*redis.DurationCmd, len(keys))
	for i, key := range keys {
		getCmds[i] = pipe.Get(ctx, key)
		ttlCmds[i] = pipe.TTL(ctx, key)
	}
	_, _ = pipe.Exec(ctx)

	for i, key := range keys {
		principalID := strings.TrimPrefix(key, "quarantine:principal:")
		reason := getCmds[i].Val()
		ttl := ttlCmds[i].Val()
		results = append(results, QuarantinedPrincipal{
			PrincipalID: principalID,
			Reason:      reason,
			TTL:         ttl,
		})
	}
	return results, nil
}

// chunkTimeout scales the restore timeout at 2s per chunk.
func chunkTimeout() time.Duration { return 2 * time.Second }

// restoreInChunks runs n pipelined writes in chunks of restoreChunkSize, each with its own timeout.
func (s *Store) restoreInChunks(ctx context.Context, n int, what string, add func(pipe redis.Pipeliner, ctx context.Context, i int)) error {
	for start := 0; start < n; start += restoreChunkSize {
		end := start + restoreChunkSize
		if end > n {
			end = n
		}
		cctx, cancel := context.WithTimeout(ctx, chunkTimeout())
		pipe := s.client.Pipeline()
		for i := start; i < end; i++ {
			add(pipe, cctx, i)
		}
		_, err := pipe.Exec(cctx)
		cancel()
		if err != nil {
			return fmt.Errorf("failed to reconstruct %s state in Redis: %w", what, err)
		}
	}
	return nil
}

// RevokedJTI represents an actively revoked token JTI in Redis with its remaining TTL.
type RevokedJTI struct {
	JTI string        `json:"jti"`
	TTL time.Duration `json:"ttl"`
}

// ListRevokedJTIs retrieves all revoked JTIs from Redis with their remaining TTLs.
func (s *Store) ListRevokedJTIs(ctx context.Context) ([]RevokedJTI, error) {
	if s.client == nil {
		return []RevokedJTI{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	const prefix = "revocation:jti:"
	var cursor uint64
	var keys []string
	for {
		k, next, err := s.client.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, k...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return []RevokedJTI{}, nil
	}
	pipe := s.client.Pipeline()
	ttlCmds := make([]*redis.DurationCmd, len(keys))
	for i, key := range keys {
		ttlCmds[i] = pipe.TTL(ctx, key)
	}
	// A failed TTL read must not surface as TTL 0: restore floors that to 1s and would silently un-revoke.
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("failed to read revoked jti TTLs: %w", err)
	}
	out := make([]RevokedJTI, 0, len(keys))
	for i, key := range keys {
		ttl := ttlCmds[i].Val()
		if ttl == -2 {
			continue // key expired between SCAN and TTL
		}
		out = append(out, RevokedJTI{JTI: strings.TrimPrefix(key, prefix), TTL: ttl})
	}
	return out, nil
}

// ReconstructRevocations pipeline-inserts revoked JTIs with their remaining TTLs (floored at 1s).
func (s *Store) ReconstructRevocations(ctx context.Context, jtis []RevokedJTI) error {
	if len(jtis) == 0 {
		return nil
	}
	return s.restoreInChunks(ctx, len(jtis), "revocation", func(pipe redis.Pipeliner, cctx context.Context, i int) {
		ttl := jtis[i].TTL
		if ttl <= 0 {
			ttl = time.Second
		}
		pipe.Set(cctx, "revocation:jti:"+jtis[i].JTI, "1", ttl)
	})
}

// ReconstructQuarantines pipeline-inserts an entire slice of quarantined principals into Redis in chunks.
// Used during Disaster Recovery to restore security state before marking gateway readiness healthy.
func (s *Store) ReconstructQuarantines(ctx context.Context, principals []QuarantinedPrincipal) error {
	if len(principals) == 0 {
		return nil
	}
	return s.restoreInChunks(ctx, len(principals), "quarantine", func(pipe redis.Pipeliner, cctx context.Context, i int) {
		p := principals[i]
		ttl := p.TTL
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		reason := p.Reason
		if reason == "" {
			reason = "DISASTER_RECOVERY_RECONSTRUCTED"
		}
		pipe.Set(cctx, "quarantine:principal:"+p.PrincipalID, reason, ttl)
	})
}
