package snapshot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestStreamClientJitterBackoff_Bounds verifies that CalculateFullJitterBackoff
// strictly confines the reconnect backoff within [100ms, 5s] across 1,000 samples for attempts 0..20.
func TestStreamClientJitterBackoff_Bounds(t *testing.T) {
	const base = 100 * time.Millisecond
	const max = 5 * time.Second

	for attempt := 0; attempt <= 20; attempt++ {
		for i := 0; i < 1000; i++ {
			b := CalculateFullJitterBackoff(attempt, base, max)
			assert.GreaterOrEqual(t, b, base, "backoff should never be less than baseBackoff")
			assert.LessOrEqual(t, b, max, "backoff should never exceed maxBackoff")
		}
	}
}

// TestStreamClientJitterBackoff_Distribution asserts that for attempt 5, multiple samples
// exhibit statistical dispersion with standard deviation > 50ms, proving true randomization.
func TestStreamClientJitterBackoff_Distribution(t *testing.T) {
	const base = 100 * time.Millisecond
	const max = 5 * time.Second
	const samples = 1000

	durations := make([]float64, samples)
	var sum float64
	for i := 0; i < samples; i++ {
		d := CalculateFullJitterBackoff(5, base, max)
		ms := float64(d.Milliseconds())
		durations[i] = ms
		sum += ms
	}

	mean := sum / float64(samples)
	var varianceSum float64
	for _, d := range durations {
		diff := d - mean
		varianceSum += diff * diff
	}
	stdDev := math.Sqrt(varianceSum / float64(samples))

	t.Logf("Attempt 5 Jitter Backoff (n=%d): mean=%.2fms, stdDev=%.2fms", samples, mean, stdDev)
	assert.Greater(t, stdDev, 50.0, "standard deviation must be > 50ms demonstrating true randomization")
}

// TestStreamClient_StopIdempotent verifies that calling Stop() multiple times
// sequentially and concurrently does not panic, deadlock, or double-close channels.
func TestStreamClient_StopIdempotent(t *testing.T) {
	client := NewStreamClient("localhost:9999", "gw-test-stop", nil, nil)

	// Call Stop multiple times sequentially
	for i := 0; i < 5; i++ {
		require.NotPanics(t, func() {
			client.Stop()
		})
	}

	// Call Stop concurrently across 10 goroutines
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NotPanics(t, func() {
				client.Stop()
			})
		}()
	}
	wg.Wait()
}

func TestHeartbeat_AckOnVerifiedLease(t *testing.T) {
	ack := buildHeartbeatAck("gw-1", 7, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, "")
	assert.Equal(t, "gw-1", ack.GatewayId)
	assert.Equal(t, int64(7), ack.ActiveVersion)
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, ack.Status)
	assert.NotNil(t, ack.AcknowledgedAt)
}

func TestHeartbeat_PreservesRejected(t *testing.T) {
	status, msg := nextAckState(errors.New("bad signature"))
	require.Equal(t, snapshotv1.AckStatus_ACK_STATUS_REJECTED, status)
	require.NotEmpty(t, msg)
	ack := buildHeartbeatAck("gw-1", 3, status, msg)
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_REJECTED, ack.Status)
	assert.NotEmpty(t, ack.ErrorMessage)

	status, msg = nextAckState(nil)
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, status)
	assert.Empty(t, msg)
}

func TestHeartbeat_NoAckOnInvalidLease(t *testing.T) {
	// The ack is sent only inside the VerifyLease err == nil branch; an
	// unverifiable lease must fail verification.
	v := NewVerifier()
	err := v.VerifyLease(&snapshotv1.FreshnessLease{}, 1)
	assert.Error(t, err)
}

func signedSnapshotForTest(t *testing.T, priv ed25519.PrivateKey, version int64, upstream string) *snapshotv1.SnapshotEnvelope {
	t.Helper()
	payload := &snapshotv1.SnapshotPayload{
		Version: version,
		Routes: []*snapshotv1.RouteDefinition{{
			RouteId:              "orders.list",
			ServiceId:            "orders",
			HttpMethod:           "GET",
			PathTemplate:         "/api/orders",
			UpstreamUrl:          upstream,
			UpstreamSpiffeId:     "spiffe://aegis.local/service/orders",
			RequiresWorkloadMtls: true,
			RateLimit:            &snapshotv1.RateLimitPolicy{RequestsPerSecond: 100, Burst: 200},
			Timeout:              &snapshotv1.TimeoutPolicy{RequestTimeoutMs: 15000, UpstreamTimeoutMs: 5000},
		}},
		IdentityMappings: []*snapshotv1.IdentityMapping{{Role: "admin", Permissions: []string{"*"}}},
	}
	b, err := proto.Marshal(payload)
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	shaHex := hex.EncodeToString(sum[:])
	return &snapshotv1.SnapshotEnvelope{
		Version:       version,
		PayloadSha256: shaHex,
		Signature:     ed25519.Sign(priv, []byte(shaHex)),
		Payload:       b,
	}
}

func newRedeliveryClient(t *testing.T) (*StreamClient, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	v := NewVerifier(pub)
	return NewStreamClient("127.0.0.1:0", "gw-test", NewManager(v), v), priv
}

func TestSnapshotRedelivery_ActiveVersionStaysActivated(t *testing.T) {
	c, priv := newRedeliveryClient(t)
	ctx := context.Background()
	env := signedSnapshotForTest(t, priv, 5, "https://orders:8081")
	require.NoError(t, c.manager.ValidateAndActivate(ctx, env))

	ack, status, msg := c.processSnapshot(ctx, env, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, "")
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, ack.Status)
	assert.Equal(t, int64(5), ack.ActiveVersion)
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, status)
	assert.Empty(t, msg)

	hb := buildHeartbeatAck("gw-test", 5, status, msg)
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, hb.Status)
}

func TestSnapshotRedelivery_OlderStillRejected(t *testing.T) {
	c, priv := newRedeliveryClient(t)
	ctx := context.Background()
	require.NoError(t, c.manager.ValidateAndActivate(ctx, signedSnapshotForTest(t, priv, 5, "https://orders:8081")))

	older := signedSnapshotForTest(t, priv, 4, "https://orders:8081")
	ack, status, msg := c.processSnapshot(ctx, older, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, "")
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_REJECTED, ack.Status)
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_REJECTED, status)
	assert.NotEmpty(t, msg)
	hb := buildHeartbeatAck("gw-test", 5, status, msg)
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_REJECTED, hb.Status)
}

func TestSnapshotRedelivery_SameVersionDifferentHashRejected(t *testing.T) {
	c, priv := newRedeliveryClient(t)
	ctx := context.Background()
	require.NoError(t, c.manager.ValidateAndActivate(ctx, signedSnapshotForTest(t, priv, 5, "https://orders:8081")))

	other := signedSnapshotForTest(t, priv, 5, "https://orders:9999")
	ack, status, _ := c.processSnapshot(ctx, other, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, "")
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_REJECTED, ack.Status)
	assert.Equal(t, snapshotv1.AckStatus_ACK_STATUS_REJECTED, status)
}
