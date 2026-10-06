package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestLimiter(t *testing.T) (*RateLimiter, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})
	return NewRateLimiter(rdb), mr
}

func TestRateLimiter_WithinLimit(t *testing.T) {
	limiter, _ := setupTestLimiter(t)
	ctx := context.Background()

	// Rate 100 rps, burst 200
	for i := 0; i < 10; i++ {
		res, err := limiter.Allow(ctx, "user-alice", "orders.create", 100, 200)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.GreaterOrEqual(t, res.Remaining, 0)
	}
}

func TestRateLimiter_ExceedBurst(t *testing.T) {
	limiter, _ := setupTestLimiter(t)
	ctx := context.Background()

	// Rate 1 rps, Burst 5
	for i := 0; i < 5; i++ {
		res, err := limiter.Allow(ctx, "user-bob", "payments.charge", 1, 5)
		require.NoError(t, err)
		assert.True(t, res.Allowed, "request %d should be allowed", i+1)
	}

	// 6th request immediately exceeds burst limit
	res, err := limiter.Allow(ctx, "user-bob", "payments.charge", 1, 5)
	require.NoError(t, err)
	assert.False(t, res.Allowed, "6th request must be throttled")
	assert.Equal(t, 0, res.Remaining)
	assert.Greater(t, res.RetryAfter, time.Duration(0), "RetryAfter must be positive on throttle")
}

func TestRateLimiter_PrincipalIsolation(t *testing.T) {
	limiter, _ := setupTestLimiter(t)
	ctx := context.Background()

	// Exhaust principal A (Burst 2)
	res1, err := limiter.Allow(ctx, "user-charlie", "orders.read", 1, 2)
	require.NoError(t, err)
	assert.True(t, res1.Allowed)

	res2, err := limiter.Allow(ctx, "user-charlie", "orders.read", 1, 2)
	require.NoError(t, err)
	assert.True(t, res2.Allowed)

	res3, err := limiter.Allow(ctx, "user-charlie", "orders.read", 1, 2)
	require.NoError(t, err)
	assert.False(t, res3.Allowed)

	// Principal B on the same route must still be permitted
	resOther, err := limiter.Allow(ctx, "user-david", "orders.read", 1, 2)
	require.NoError(t, err)
	assert.True(t, resOther.Allowed, "principal B should not be affected by principal A exhaustion")

	// Principal A on a different route must also still be permitted
	resOtherRoute, err := limiter.Allow(ctx, "user-charlie", "reports.read", 1, 2)
	require.NoError(t, err)
	assert.True(t, resOtherRoute.Allowed, "different route should have separate rate limit bucket")
}

func TestRateLimiter_DefaultLimits(t *testing.T) {
	limiter, _ := setupTestLimiter(t)
	ctx := context.Background()

	// Non-positive rps and burst default to 100 and 200
	res, err := limiter.Allow(ctx, "user-eve", "orders.list", 0, 0)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
	assert.Greater(t, res.Remaining, 0)
}

func TestRateLimiter_TimeoutAndDependencyFailure(t *testing.T) {
	limiter, mr := setupTestLimiter(t)

	// Close miniredis to simulate dependency failure / outage
	mr.Close()

	ctx := context.Background()
	res, err := limiter.Allow(ctx, "user-frank", "orders.read", 10, 10)
	assert.Error(t, err)
	assert.Nil(t, res)
	assert.True(t, errors.Is(err, ErrRateLimitDependency), "error must wrap ErrRateLimitDependency")

	// Verify canceled context returns error wrapping ErrRateLimitDependency
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	resCancel, errCancel := limiter.Allow(canceledCtx, "user-frank", "orders.read", 10, 10)
	assert.Error(t, errCancel)
	assert.Nil(t, resCancel)
	assert.True(t, errors.Is(errCancel, ErrRateLimitDependency))
}
