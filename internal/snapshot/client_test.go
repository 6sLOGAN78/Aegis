package snapshot

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
