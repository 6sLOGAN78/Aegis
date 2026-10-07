package chaos

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChaosGatewayProcessKill verifies DIST-01 and Invariant 4:
// When 1 of 3 gateway nodes is abruptly terminated mid-traffic, the reverse proxy
// load balancer seamlessly fails over to surviving nodes with zero dropped
// requests, zero 500/502 errors, and continuous 200 OK traffic continuity.
func TestChaosGatewayProcessKill(t *testing.T) {
	cluster := NewChaosCluster(t, 3)
	defer cluster.Close()

	// Verify all 3 replicas are active and initialized
	require.Eventually(t, func() bool {
		for _, node := range cluster.Nodes {
			if node.Manager.Active() == nil {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "All 3 replicas must be initialized")

	// Start concurrent background traffic (15 concurrent workers)
	stopTraffic := cluster.StartTraffic(15)

	// Allow initial traffic to establish across all nodes
	time.Sleep(150 * time.Millisecond)

	// Abruptly kill gateway node 2 (index 1) mid-traffic
	t.Log("Killing gateway replica gw-2 (index 1)...")
	cluster.KillNode(1)

	// Continue traffic while node 1 is dead to ensure surviving nodes handle all traffic
	time.Sleep(300 * time.Millisecond)

	// Restart gateway node 2
	t.Log("Restarting gateway replica gw-2...")
	cluster.RestartNode(1)

	// Continue traffic after node rejoins rotation
	time.Sleep(200 * time.Millisecond)

	// Terminate traffic and inspect results
	stats := stopTraffic()
	t.Logf("Traffic stats during node kill & restart: Total=%d, 200 OK=%d, Errors=%d, StatusCodes=%v",
		stats.TotalRequests, stats.Success200, stats.Errors, stats.StatusCodes)

	assert.Greater(t, stats.Success200, int64(30), "Traffic generator must service sustained request load")
	assert.Equal(t, int64(0), stats.Errors, "Reverse proxy must experience ZERO dropped requests or 500/502 errors")
	assert.Equal(t, stats.TotalRequests, stats.Success200, "100% of requests must succeed with HTTP 200 OK")
}
