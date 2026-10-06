package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const composeFilePath = "../../deployments/compose/docker-compose.mvp.yml"

// ensureComposeCluster checks if the MVP compose cluster is reachable, starting it if necessary.
func ensureComposeCluster(t *testing.T) {
	t.Helper()

	// Check if gateway and demo-issuer are already responding
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, errGateway := client.Get("http://localhost:8080/health")
	_, errIssuer := client.Get("http://localhost:8085/health")

	if errGateway != nil || errIssuer != nil {
		t.Log("Starting Docker Compose MVP cluster...")
		cmd := exec.Command("docker", "compose", "-f", composeFilePath, "up", "-d")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "Failed to start docker compose: %s", string(output))

		// Wait up to 15 seconds for gateway and demo issuer readiness
		deadline := time.Now().Add(15 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			resp, err := client.Get("http://localhost:8085/public-key")
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				ready = true
				break
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(500 * time.Millisecond)
		}
		require.True(t, ready, "Docker Compose services did not become ready in time")
	}
}

func TestBackendBypassPrevention(t *testing.T) {
	ensureComposeCluster(t)

	t.Run("Backend microservices have 0 published host ports", func(t *testing.T) {
		backends := []struct {
			service string
			port    string
		}{
			{"orders", "8081"},
			{"payments", "8082"},
			{"admin", "8083"},
		}

		for _, b := range backends {
			cmd := exec.Command("docker", "compose", "-f", composeFilePath, "port", b.service, b.port)
			out, err := cmd.Output()
			// Port is unmapped if command returns non-zero, empty string, or ":0"
			mapped := strings.TrimSpace(string(out))
			require.True(
				t,
				err != nil || mapped == "" || mapped == ":0",
				"Backend %s port %s must not be mapped to host, got: %s",
				b.service,
				b.port,
				mapped,
			)
		}

		// Verify gateway port 8080 IS published
		gwCmd := exec.Command("docker", "compose", "-f", composeFilePath, "port", "gateway", "8080")
		gwOut, err := gwCmd.Output()
		require.NoError(t, err)
		require.Contains(t, string(gwOut), "8080")
	})

	t.Run("Direct TCP connection attempts to backend ports fail", func(t *testing.T) {
		ports := []int{8081, 8082, 8083}

		for _, port := range ports {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
			require.Error(t, err, "Direct TCP connection to localhost:%d must fail", port)
			if conn != nil {
				conn.Close()
			}
		}
	})

	t.Run("Direct HTTP requests to backend endpoints fail", func(t *testing.T) {
		urls := []string{
			"http://localhost:8081/api/orders",
			"http://localhost:8082/api/payments",
			"http://localhost:8083/api/admin/users",
		}

		client := &http.Client{Timeout: 1 * time.Second}
		for _, u := range urls {
			resp, err := client.Get(u)
			require.Error(t, err, "Direct HTTP GET to %s must fail", u)
			if resp != nil {
				resp.Body.Close()
			}
		}
	})

	t.Run("Gateway-mediated routing succeeds through gateway port 8080", func(t *testing.T) {
		client := &http.Client{Timeout: 5 * time.Second}

		// 1. Authenticate with demo issuer to get developer token
		loginPayload, _ := json.Marshal(map[string]string{"role": "developer"})
		loginResp, err := client.Post("http://localhost:8085/login", "application/json", bytes.NewReader(loginPayload))
		require.NoError(t, err)
		defer loginResp.Body.Close()
		require.Equal(t, http.StatusOK, loginResp.StatusCode)

		var loginData map[string]interface{}
		err = json.NewDecoder(loginResp.Body).Decode(&loginData)
		require.NoError(t, err)

		token, ok := loginData["access_token"].(string)
		require.True(t, ok)
		require.NotEmpty(t, token)

		// 2. Access /api/orders via Gateway with Bearer token
		req, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/orders", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotEmpty(t, resp.Header.Get("X-Request-Id"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "Cloud Scanner")
		require.Contains(t, string(body), "ord_101")

		// 3. Verify developer can read payments via Gateway
		payReq, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/payments", nil)
		require.NoError(t, err)
		payReq.Header.Set("Authorization", "Bearer "+token)

		payResp, err := client.Do(payReq)
		require.NoError(t, err)
		defer payResp.Body.Close()
		require.Equal(t, http.StatusOK, payResp.StatusCode)

		// 4. Verify developer is forbidden from admin routes via Gateway (RBAC)
		adminReq, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/admin/users", nil)
		require.NoError(t, err)
		adminReq.Header.Set("Authorization", "Bearer "+token)

		adminResp, err := client.Do(adminReq)
		require.NoError(t, err)
		defer adminResp.Body.Close()
		require.Equal(t, http.StatusForbidden, adminResp.StatusCode)

		// 5. Verify unauthenticated call fails closed with 401
		anonReq, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/orders", nil)
		require.NoError(t, err)

		anonResp, err := client.Do(anonReq)
		require.NoError(t, err)
		defer anonResp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, anonResp.StatusCode)
	})
}
