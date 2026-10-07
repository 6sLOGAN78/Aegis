package chaos

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aegis/internal/proxy"

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

// TestChaosDependencies validates strict fail-closed security across 4 cascading failure scenarios:
// 1. Control Plane partition past the 60s lease boundary.
// 2. Redis network partition with rigid 200ms timeout failing closed.
// 3. PostgreSQL database outage with hot-path traffic continuing unaffected.
// 4. WAL spool saturation halting permitted admissions while preserving default-deny.
func TestChaosDependencies(t *testing.T) {
	t.Run("ControlPlaneSeveredAndRecovered", func(t *testing.T) {
		cluster := NewChaosCluster(t, 3)
		defer cluster.Close()

		validToken := cluster.MintValidToken("user-cp", "developer")

		// Baseline: 200 OK on /readyz and protected route
		reqBase, _ := http.NewRequest(http.MethodGet, cluster.LBURL+"/api/orders", nil)
		reqBase.Header.Set("Authorization", "Bearer "+validToken)
		respBase, err := cluster.Client.Do(reqBase)
		require.NoError(t, err)
		defer respBase.Body.Close()
		assert.Equal(t, http.StatusOK, respBase.StatusCode)

		// Sever Control Plane
		cluster.SeverControlPlane()

		// Transient window (<60s): simulate lease age = 30s
		for _, n := range cluster.Nodes {
			n.Manager.SetLastLeaseRenewedAt(time.Now().Add(-30 * time.Second))
		}
		for _, n := range cluster.Nodes {
			assert.False(t, n.Manager.IsLeaseExpired(60*time.Second))
			readyResp, err := http.Get(n.Server.URL + "/readyz")
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, readyResp.StatusCode)
			_ = readyResp.Body.Close()
		}

		reqTransient, _ := http.NewRequest(http.MethodGet, cluster.LBURL+"/api/orders", nil)
		reqTransient.Header.Set("Authorization", "Bearer "+validToken)
		respTransient, err := cluster.Client.Do(reqTransient)
		require.NoError(t, err)
		defer respTransient.Body.Close()
		assert.Equal(t, http.StatusOK, respTransient.StatusCode, "Traffic authorized during transient window <60s")

		// Hard boundary (>60s): simulate lease age = 65s
		for _, n := range cluster.Nodes {
			n.Manager.SetLastLeaseRenewedAt(time.Now().Add(-65 * time.Second))
		}
		for _, n := range cluster.Nodes {
			assert.True(t, n.Manager.IsLeaseExpired(60*time.Second))
			readyResp, err := http.Get(n.Server.URL + "/readyz")
			require.NoError(t, err)
			assert.Equal(t, http.StatusServiceUnavailable, readyResp.StatusCode)
			_ = readyResp.Body.Close()
		}

		backendCallsBeforeExpired := cluster.BackendCallCount.Load()
		reqExpired, _ := http.NewRequest(http.MethodGet, cluster.LBURL+"/api/orders", nil)
		reqExpired.Header.Set("Authorization", "Bearer "+validToken)
		respExpired, err := cluster.Client.Do(reqExpired)
		require.NoError(t, err)
		defer respExpired.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, respExpired.StatusCode)
		var prob proxy.RFC7807Problem
		err = json.NewDecoder(respExpired.Body).Decode(&prob)
		require.NoError(t, err)
		assert.Equal(t, "https://aegis.local/errors/policy-lease-expired", prob.Type)
		assert.Equal(t, backendCallsBeforeExpired, cluster.BackendCallCount.Load(), "Zero requests reach backend when lease expired")

		// Reconnect control plane
		cluster.RestoreControlPlane()

		// Restores HTTP 200 within 5 seconds
		require.Eventually(t, func() bool {
			reqRec, _ := http.NewRequest(http.MethodGet, cluster.LBURL+"/api/orders", nil)
			reqRec.Header.Set("Authorization", "Bearer "+validToken)
			respRec, err := cluster.Client.Do(reqRec)
			if err != nil {
				return false
			}
			defer respRec.Body.Close()
			return respRec.StatusCode == http.StatusOK
		}, 5*time.Second, 50*time.Millisecond, "Reconnecting control plane must restore HTTP 200 within 5s")
	})

	t.Run("RedisPartitionFailClosed", func(t *testing.T) {
		cluster := NewChaosCluster(t, 3)
		defer cluster.Close()

		validToken := cluster.MintValidToken("user-redis", "developer")

		// Baseline: 200 OK
		req, _ := http.NewRequest(http.MethodGet, cluster.LBURL+"/api/orders", nil)
		req.Header.Set("Authorization", "Bearer "+validToken)
		resp, err := cluster.Client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		backendCallsBeforeOutage := cluster.BackendCallCount.Load()

		// Partition Redis
		cluster.PartitionRedis()

		// Verify fail-closed returning HTTP 503
		for i := 0; i < 10; i++ {
			reqOutage, _ := http.NewRequest(http.MethodGet, cluster.LBURL+"/api/orders", nil)
			reqOutage.Header.Set("Authorization", "Bearer "+validToken)
			respOutage, err := cluster.Client.Do(reqOutage)
			require.NoError(t, err)
			defer respOutage.Body.Close()

			assert.Equal(t, http.StatusServiceUnavailable, respOutage.StatusCode)
			var prob proxy.RFC7807Problem
			_ = json.NewDecoder(respOutage.Body).Decode(&prob)
			assert.Equal(t, "https://aegis.local/errors/dependency-unavailable", prob.Type)
		}

		// Zero requests reach backend during outage
		assert.Equal(t, backendCallsBeforeOutage, cluster.BackendCallCount.Load(), "Zero requests reach upstream backend during Redis outage")

		// Restore Redis
		cluster.RestoreRedis()

		// Resumes normal authorization
		reqRec, _ := http.NewRequest(http.MethodGet, cluster.LBURL+"/api/orders", nil)
		reqRec.Header.Set("Authorization", "Bearer "+validToken)
		respRec, err := cluster.Client.Do(reqRec)
		require.NoError(t, err)
		defer respRec.Body.Close()

		assert.Equal(t, http.StatusOK, respRec.StatusCode)
		assert.Equal(t, backendCallsBeforeOutage+1, cluster.BackendCallCount.Load())
	})

	t.Run("PostgreSQLOutageHotPathUnaffected", func(t *testing.T) {
		cluster := NewChaosCluster(t, 3)
		defer cluster.Close()

		validToken := cluster.MintValidToken("user-postgres", "developer")

		// Sever PostgreSQL
		cluster.SeverPostgres()

		backendCallsBefore := cluster.BackendCallCount.Load()

		// Hot path continues serving requests with HTTP 200
		const requestCount = 10
		for i := 0; i < requestCount; i++ {
			req, _ := http.NewRequest(http.MethodGet, cluster.LBURL+"/api/orders", nil)
			req.Header.Set("Authorization", "Bearer "+validToken)
			resp, err := cluster.Client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
		}

		assert.Equal(t, backendCallsBefore+int64(requestCount), cluster.BackendCallCount.Load())

		// Verify local disk WAL files exist and contain records appended with fsync
		for _, node := range cluster.Nodes {
			entries, err := os.ReadDir(node.SpoolDir)
			require.NoError(t, err)
			var hasWal bool
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".log" {
					info, err := e.Info()
					require.NoError(t, err)
					if info.Size() > 0 {
						hasWal = true
						break
					}
				}
			}
			assert.True(t, hasWal, "Local disk WAL must contain records written with fsync despite DB outage")
		}

		// Restore PostgreSQL
		cluster.RestorePostgres()
	})

	t.Run("SpoolSaturationHaltsAdmission", func(t *testing.T) {
		cluster := NewChaosCluster(t, 1)
		defer cluster.Close()

		node := cluster.Nodes[0]
		validToken := cluster.MintValidToken("user-spool", "developer")

		// Baseline: 200 OK
		req, _ := http.NewRequest(http.MethodGet, node.Server.URL+"/api/orders", nil)
		req.Header.Set("Authorization", "Bearer "+validToken)
		resp, err := cluster.Client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Saturate spool to >=90%
		cluster.SaturateSpool(0)
		sat, err := node.Spool.CheckSaturation()
		require.NoError(t, err)
		assert.True(t, sat, "Spool must be saturated")

		// Readiness probe drops to 503 SPOOL_SATURATED
		readyResp, err := cluster.Client.Get(node.Server.URL + "/readyz")
		require.NoError(t, err)
		defer readyResp.Body.Close()
		assert.Equal(t, http.StatusServiceUnavailable, readyResp.StatusCode)

		backendCallsBefore := cluster.BackendCallCount.Load()

		// Permitted request halts admission with HTTP 503
		reqSat, _ := http.NewRequest(http.MethodGet, node.Server.URL+"/api/orders", nil)
		reqSat.Header.Set("Authorization", "Bearer "+validToken)
		respSat, err := cluster.Client.Do(reqSat)
		require.NoError(t, err)
		defer respSat.Body.Close()
		assert.Equal(t, http.StatusServiceUnavailable, respSat.StatusCode)
		assert.Equal(t, backendCallsBefore, cluster.BackendCallCount.Load(), "Zero requests reach backend when spool is saturated")

		// Unauthenticated request still returns 401 (default-deny preserved)
		cluster.RequireAuth = true
		reqUnauth, _ := http.NewRequest(http.MethodGet, node.Server.URL+"/api/orders", nil)
		respUnauth, err := cluster.Client.Do(reqUnauth)
		require.NoError(t, err)
		defer respUnauth.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, respUnauth.StatusCode)

		// Unauthorized role returns 403 (default-deny preserved)
		unauthRoleToken := cluster.MintValidToken("unauth-user", "guest")
		reqForbidden, _ := http.NewRequest(http.MethodGet, node.Server.URL+"/api/orders", nil)
		reqForbidden.Header.Set("Authorization", "Bearer "+unauthRoleToken)
		respForbidden, err := cluster.Client.Do(reqForbidden)
		require.NoError(t, err)
		defer respForbidden.Body.Close()
		assert.Equal(t, http.StatusForbidden, respForbidden.StatusCode)

		// Drain spool
		cluster.DrainSpool(0)
		satAfter, err := node.Spool.CheckSaturation()
		require.NoError(t, err)
		assert.False(t, satAfter)

		// Readiness returns 200 OK
		readyDrain, err := cluster.Client.Get(node.Server.URL + "/readyz")
		require.NoError(t, err)
		defer readyDrain.Body.Close()
		assert.Equal(t, http.StatusOK, readyDrain.StatusCode)

		// Permitted admission resumes with HTTP 200 OK
		reqDrain, _ := http.NewRequest(http.MethodGet, node.Server.URL+"/api/orders", nil)
		reqDrain.Header.Set("Authorization", "Bearer "+validToken)
		respDrain, err := cluster.Client.Do(reqDrain)
		require.NoError(t, err)
		defer respDrain.Body.Close()
		assert.Equal(t, http.StatusOK, respDrain.StatusCode)
	})
}

// TestChaosSecurityInvariants asserts zero fail-open bypass under combined chaos outages
// (active Redis partition and expired control plane lease).
func TestChaosSecurityInvariants(t *testing.T) {
	cluster := NewChaosCluster(t, 3)
	defer cluster.Close()
	cluster.RequireAuth = true

	initialBackendCalls := cluster.BackendCallCount.Load()

	// Sever Control Plane AND Partition Redis simultaneously
	cluster.SeverControlPlane()
	cluster.PartitionRedis()

	// Advance lease age past 60s
	for _, n := range cluster.Nodes {
		n.Manager.SetLastLeaseRenewedAt(time.Now().Add(-65 * time.Second))
	}

	testCases := []struct {
		name       string
		method     string
		path       string
		opaque     string
		headers    map[string]string
		wantStatus []int
	}{
		{
			name:   "Path traversal attempt using dot-segments",
			method: http.MethodGet,
			path:   "/api/orders/%2e%2e/%2e%2e/etc/passwd",
			headers: map[string]string{
				"Authorization": "Bearer " + cluster.MintValidToken("attacker", "developer"),
			},
			wantStatus: []int{http.StatusBadRequest},
		},
		{
			name:   "Header spoofing X-Aegis-User injection",
			method: http.MethodGet,
			path:   "/api/orders",
			headers: map[string]string{
				"X-Aegis-User": "admin",
			},
			wantStatus: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable},
		},
		{
			name:   "Invalid JWT algorithm none",
			method: http.MethodGet,
			path:   "/api/orders",
			headers: map[string]string{
				"Authorization": "Bearer " + cluster.MintAlgNoneToken("attacker", "developer"),
			},
			wantStatus: []int{http.StatusUnauthorized},
		},
		{
			name:   "Untrusted signing key certificate",
			method: http.MethodGet,
			path:   "/api/orders",
			headers: map[string]string{
				"Authorization": "Bearer " + cluster.MintUntrustedKeyToken("attacker", "developer"),
			},
			wantStatus: []int{http.StatusUnauthorized},
		},
		{
			name:   "Expired JWT token",
			method: http.MethodGet,
			path:   "/api/orders",
			headers: map[string]string{
				"Authorization": "Bearer " + cluster.MintExpiredToken("user", "developer"),
			},
			wantStatus: []int{http.StatusUnauthorized},
		},
		{
			name:   "Valid token during combined chaos outage fails closed",
			method: http.MethodGet,
			path:   "/api/orders",
			headers: map[string]string{
				"Authorization": "Bearer " + cluster.MintValidToken("user", "developer"),
			},
			wantStatus: []int{http.StatusServiceUnavailable},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, cluster.LBURL+tc.path, nil)
			require.NoError(t, err)

			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			resp, err := cluster.Client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.NotEqual(t, http.StatusOK, resp.StatusCode, "Hostile or degraded request must NEVER return 200 OK")
			assert.Contains(t, tc.wantStatus, resp.StatusCode, "Expected fail-closed response status")
		})
	}

	// 100% rejection with ZERO unauthorized backend requests
	assert.Equal(t, initialBackendCalls, cluster.BackendCallCount.Load(),
		"Zero requests must reach upstream backend during chaos security attacks")
}
