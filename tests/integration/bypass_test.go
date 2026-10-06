package integration

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const composeFilePath = "../../deployments/compose/docker-compose.mvp.yml"

// ensureComposeCluster checks if the MVP compose cluster is reachable, starting it if necessary.
func ensureComposeCluster(t *testing.T) {
	t.Helper()

	// 1. Ensure development certificates exist before launching cluster
	certPath := filepath.Join("../../deployments/certs", "root-ca.crt")
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Log("Certificates not found; generating PKI assets...")
		cmd := exec.Command("go", "run", "../../scripts/certificates/main.go")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "failed to generate certificates: %s", string(out))
	}

	// 2. Check if gateway and demo-issuer are already responding
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, errGateway := client.Get("http://localhost:8080/health")
	_, errIssuer := client.Get("http://localhost:8085/health")

	if errGateway != nil || errIssuer != nil {
		t.Log("Starting Docker Compose MVP cluster...")
		cmd := exec.Command("docker", "compose", "-f", composeFilePath, "up", "-d")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "Failed to start docker compose: %s", string(output))

		// Wait up to 30 seconds for gateway and demo issuer readiness
		deadline := time.Now().Add(30 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			resp, err := client.Get("http://localhost:8085/public-key")
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				conn8080, err1 := net.DialTimeout("tcp", "127.0.0.1:8080", 500*time.Millisecond)
				conn9443, err2 := net.DialTimeout("tcp", "127.0.0.1:9443", 500*time.Millisecond)
				if err1 == nil && err2 == nil {
					conn8080.Close()
					conn9443.Close()
					ready = true
					break
				}
				if conn8080 != nil {
					conn8080.Close()
				}
				if conn9443 != nil {
					conn9443.Close()
				}
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

	// Subtest 1: Verify orders (8081), payments (8082), and admin (8083) have zero published host ports
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

		// Verify gateway ports 8080 and 9443 ARE published
		gwCmd8080 := exec.Command("docker", "compose", "-f", composeFilePath, "port", "gateway", "8080")
		gwOut8080, err := gwCmd8080.Output()
		require.NoError(t, err)
		require.Contains(t, string(gwOut8080), "8080")

		gwCmd9443 := exec.Command("docker", "compose", "-f", composeFilePath, "port", "gateway", "9443")
		gwOut9443, err := gwCmd9443.Output()
		require.NoError(t, err)
		require.Contains(t, string(gwOut9443), "9443")
	})

	// Subtest 2: Direct TCP connection attempts from host fail
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

	// Subtest 3: Direct HTTP/HTTPS requests from host fail
	t.Run("Direct HTTP and HTTPS requests to backend endpoints fail", func(t *testing.T) {
		urls := []string{
			"http://localhost:8081/api/orders",
			"https://localhost:8081/api/orders",
			"http://localhost:8082/api/payments",
			"https://localhost:8082/api/payments",
			"http://localhost:8083/api/admin/users",
			"https://localhost:8083/api/admin/users",
		}

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			Timeout: 1 * time.Second,
		}
		for _, u := range urls {
			resp, err := client.Get(u)
			require.Error(t, err, "Direct HTTP/HTTPS request to %s must fail", u)
			if resp != nil {
				resp.Body.Close()
			}
		}
	})

	// Subtest 4: Lateral direct bypass elimination without client certificates fails TLS handshake
	t.Run("Lateral direct container call without client certificate fails at TLS handshake", func(t *testing.T) {
		cmd := exec.Command(
			"docker", "compose", "-f", composeFilePath,
			"exec", "-T", "orders",
			"curl", "-k", "-s", "-S", "https://payments:8082/api/payments",
		)
		output, err := cmd.CombinedOutput()
		require.Error(t, err, "Lateral connection to payments:8082 without client certificate must fail")
		outStr := string(output)
		require.True(
			t,
			strings.Contains(outStr, "certificate required") ||
				strings.Contains(outStr, "handshake failure") ||
				strings.Contains(outStr, "SSL") ||
				strings.Contains(outStr, "alert"),
			"Expected TLS client certificate requirement error, got: %s",
			outStr,
		)
	})

	// Subtest 5: Lateral peer workload cert bypass directly to backend fails with HTTP 403 Forbidden
	t.Run("Lateral direct container call with workload certificate fails authorization", func(t *testing.T) {
		cmd := exec.Command(
			"docker", "compose", "-f", composeFilePath,
			"exec", "-T", "orders",
			"curl", "-i", "-s", "-k",
			"--cert", "/certs/workload-orders.crt",
			"--key", "/certs/workload-orders.key",
			"--cacert", "/certs/root-ca.crt",
			"-X", "POST",
			"https://payments:8082/api/payments",
		)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "Curl execution must succeed and return HTTP response")
		outStr := string(output)

		require.Contains(t, outStr, "403")
		require.Contains(t, outStr, "Forbidden")
		require.Contains(t, outStr, "unauthorized client identity: peer is not the Aegis Gateway")
	})

	// Subtest 6: Gateway workload mediation succeeds on port 9443
	t.Run("Gateway workload mediation succeeds on port 9443", func(t *testing.T) {
		certPath := filepath.Join("../../deployments/certs", "workload-orders.crt")
		keyPath := filepath.Join("../../deployments/certs", "workload-orders.key")
		caPath := filepath.Join("../../deployments/certs", "root-ca.crt")

		tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
		require.NoError(t, err)

		caPEM, err := os.ReadFile(caPath)
		require.NoError(t, err)

		caPool := x509.NewCertPool()
		require.True(t, caPool.AppendCertsFromPEM(caPEM))

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					Certificates: []tls.Certificate{tlsCert},
					RootCAs:      caPool,
				},
			},
			Timeout: 5 * time.Second,
		}

		// 1. Permitted workload action: orders calls POST /api/payments through Gateway
		reqBody := bytes.NewReader([]byte(`{"amount": 100.0, "currency": "USD"}`))
		resp, err := client.Post("https://localhost:9443/api/payments", "application/json", reqBody)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotEmpty(t, resp.Header.Get("X-Request-Id"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "processed")
		require.Contains(t, string(body), "pay_201")

		// 2. Denied workload action: orders calls GET /api/admin/users through Gateway (Rule 5 / Invariant 3)
		adminResp, err := client.Get("https://localhost:9443/api/admin/users")
		require.NoError(t, err)
		defer adminResp.Body.Close()

		require.Equal(t, http.StatusForbidden, adminResp.StatusCode)
		adminBody, err := io.ReadAll(adminResp.Body)
		require.NoError(t, err)
		require.Contains(t, string(adminBody), "DENIED_WORKLOAD_ADMIN_FORBIDDEN")
	})

	// Subtest 7: Gateway user mediation succeeds on port 8080
	t.Run("Gateway user mediation succeeds on port 8080", func(t *testing.T) {
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
		adminBody, err := io.ReadAll(adminResp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(adminBody), "DENIED_DEVELOPER_ADMIN_FORBIDDEN")

		// 5. Verify unauthenticated call fails closed with 401
		anonReq, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/orders", nil)
		require.NoError(t, err)

		anonResp, err := client.Do(anonReq)
		require.NoError(t, err)
		defer anonResp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, anonResp.StatusCode)
	})
}
