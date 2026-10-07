package revocation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrRevocationDependency indicates a Redis failure or timeout during revocation check.
var ErrRevocationDependency = errors.New("revocation dependency check failed")

// Store manages ephemeral JTI revocation blocklists and principal quarantine states in Redis (REV-03, REV-04).
type Store struct {
	client *redis.Client
}

// NewStore creates a new Store backed by the provided Redis client.
func NewStore(client *redis.Client) *Store {
	return &Store{
		client: client,
	}
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

	if principalID != "" {
		quarCmd = pipe.Exists(ctx, "quarantine:principal:"+principalID)
	}
	if jti != "" {
		jtiCmd = pipe.Exists(ctx, "revocation:jti:"+jti)
	}

	if quarCmd == nil && jtiCmd == nil {
		return false, "", nil
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return false, "", fmt.Errorf("%w: %w", ErrRevocationDependency, err)
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
