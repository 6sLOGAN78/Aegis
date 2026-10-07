package chaos

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFleetConvergenceUnderLoad verifies DIST-01 and Invariant 4:
// A multi-replica gateway cluster (3 nodes) converges to newly broadcast policy snapshots
// within 5 seconds at p99 while actively processing concurrent HTTP traffic, with zero
// dropped requests, zero 500 errors, and zero race conditions during atomic pointer swaps.
func TestFleetConvergenceUnderLoad(t *testing.T) {
	// 1 & 2. Initialize in-memory Control Plane and 3 gateway replicas (gw-1, gw-2, gw-3)
	cluster := NewTestCluster(t, 3)
	defer cluster.Close()

	// 3. Verify all 3 replicas stream and activate initial snapshot v1, acknowledging to Control Plane
	require.Eventually(t, func() bool {
		for _, node := range cluster.Nodes {
			act := node.Manager.Active()
			if act == nil || act.Version != 1 {
				return false
			}
		}
		statuses := cluster.ControlPlane.AckTracker.GetReplicaStatuses()
		if len(statuses) < 3 {
			return false
		}
		for _, node := range cluster.Nodes {
			ack, ok := statuses[node.ID]
			if !ok || ack.ActiveVersion != 1 || ack.Status != snapshotv1.AckStatus_ACK_STATUS_ACTIVATED {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond, "All 3 replicas must activate and acknowledge snapshot v1")

	// 4. Start concurrent background HTTP traffic across all 3 gateways (50 concurrent workers)
	var totalRequests atomic.Int64
	var errorRequests atomic.Int64
	var v1Requests atomic.Int64
	var v2Requests atomic.Int64

	stopTraffic := make(chan struct{})
	var wg sync.WaitGroup

	httpClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 50,
			IdleConnTimeout:     10 * time.Second,
		},
	}

	const concurrency = 50
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopTraffic:
					return
				default:
					node := cluster.Nodes[workerID%len(cluster.Nodes)]
					reqURL := node.Server.URL + "/api/orders"
					resp, err := httpClient.Get(reqURL)
					if err != nil {
						errorRequests.Add(1)
						time.Sleep(1 * time.Millisecond)
						continue
					}

					totalRequests.Add(1)
					ver := resp.Header.Get("X-Snapshot-Version")
					switch ver {
					case "1":
						v1Requests.Add(1)
					case "2":
						v2Requests.Add(1)
					}

					if resp.StatusCode >= 500 {
						errorRequests.Add(1)
					}
					_ = resp.Body.Close()

					// Yield slightly to maintain realistic pacing
					time.Sleep(1 * time.Millisecond)
				}
			}
		}()
	}

	// Allow traffic to establish across all replicas
	time.Sleep(150 * time.Millisecond)

	// 5. Broadcast newly signed snapshot version 2 with modified policy
	v2Policy := `package aegis.authz

default allow := true
default reason_code := "ALLOWED_BY_V2"

decision := {
    "allow": allow,
    "reason_code": reason_code,
    "snapshot_version": input.snapshot_version,
}
`
	start := time.Now()
	cluster.PublishNewSnapshot(t, 2, v2Policy)

	// 6. Measure elapsed duration until all 3 gateways report active snapshot version 2
	require.Eventually(t, func() bool {
		for _, node := range cluster.Nodes {
			act := node.Manager.Active()
			if act == nil || act.Version != 2 {
				return false
			}
		}
		statuses := cluster.ControlPlane.AckTracker.GetReplicaStatuses()
		for _, node := range cluster.Nodes {
			ack, ok := statuses[node.ID]
			if !ok || ack.ActiveVersion != 2 || ack.Status != snapshotv1.AckStatus_ACK_STATUS_ACTIVATED {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "All 3 replicas must converge and acknowledge snapshot v2 within 5s")

	convergenceDuration := time.Since(start)
	t.Logf("Fleet convergence across %d replicas completed in %v", len(cluster.Nodes), convergenceDuration)

	// 7. Assert fleet convergence duration completes in < 5.0 seconds at p99
	assert.Less(t, convergenceDuration, 5*time.Second, "Fleet convergence must complete in under 5.0 seconds")

	// Allow continued traffic post-swap to verify v2 serving stability
	time.Sleep(150 * time.Millisecond)

	// Terminate traffic generator
	close(stopTraffic)
	wg.Wait()

	t.Logf("Traffic summary during swap: total=%d, v1=%d, v2=%d, errors=%d",
		totalRequests.Load(), v1Requests.Load(), v2Requests.Load(), errorRequests.Load())

	// 8. Assert zero in-flight requests encountered panics, nil dereferences, or 500 errors
	assert.Equal(t, int64(0), errorRequests.Load(), "Zero requests must experience errors during lock-free atomic pointer swaps")
	assert.GreaterOrEqual(t, totalRequests.Load(), int64(50), "Must successfully service continuous concurrent load")
	assert.Greater(t, v1Requests.Load(), int64(0), "Must have serviced requests against snapshot v1")
	assert.Greater(t, v2Requests.Load(), int64(0), "Must have serviced requests against snapshot v2")
}
