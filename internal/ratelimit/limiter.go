package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
)

// ErrRateLimitDependency indicates a Redis failure or timeout during rate limit evaluation.
var ErrRateLimitDependency = errors.New("rate limit dependency check failed")

// Result contains the outcome of a rate limit check.
type Result struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
	ResetAfter time.Duration
}

// RateLimiter evaluates atomic Generic Cell Rate Algorithm (GCRA) token bucket limits in Redis.
type RateLimiter struct {
	client  *redis.Client
	limiter *redis_rate.Limiter
}

// NewRateLimiter creates a RateLimiter backed by the provided Redis client.
func NewRateLimiter(client *redis.Client) *RateLimiter {
	return &RateLimiter{
		client:  client,
		limiter: redis_rate.NewLimiter(client),
	}
}

// Allow evaluates whether a request identified by principalID and routeID is permitted under the GCRA limit.
// All Redis calls are strictly bounded by a 200ms context deadline.
func (l *RateLimiter) Allow(ctx context.Context, principalID string, routeID string, rps int, burst int) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	if rps <= 0 {
		rps = 100
	}
	if burst <= 0 {
		burst = 200
	}

	key := fmt.Sprintf("ratelimit:%s:%s", principalID, routeID)
	res, err := l.limiter.Allow(ctx, key, redis_rate.Limit{
		Rate:   rps,
		Burst:  burst,
		Period: time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRateLimitDependency, err)
	}

	allowed := res.Allowed > 0
	remaining := res.Remaining
	if !allowed {
		remaining = 0
	}

	return &Result{
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: res.RetryAfter,
		ResetAfter: res.ResetAfter,
	}, nil
}
