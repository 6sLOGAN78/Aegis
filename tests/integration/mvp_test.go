package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loginToken authenticates against the demo issuer and returns a signed Ed25519 Bearer token.
func loginToken(t *testing.T, client *http.Client, role string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"role": role})
	require.NoError(t, err)

	resp, err := client.Post("http://localhost:8085/login", "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "Login for role %s should succeed", role)

	var res map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&res)
	require.NoError(t, err)

	token, ok := res["access_token"].(string)
	require.True(t, ok, "access_token must be a string")
	require.NotEmpty(t, token, "access_token must not be empty")
	return token
}

func assertValidRequestID(t *testing.T, header http.Header) string {
	t.Helper()
	reqID := header.Get("X-Request-Id")
	if reqID == "" {
		reqID = header.Get("X-Request-ID")
	}
	require.NotEmpty(t, reqID, "X-Request-ID header must be present on response")
	parsed, err := uuid.Parse(reqID)
	require.NoError(t, err, "X-Request-ID %q must be a valid UUID v4", reqID)
	return parsed.String()
}

func sendRawHTTP(t *testing.T, rawPath, token string, extraHeaders map[string]string) *http.Response {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8080", 2*time.Second)
	require.NoError(t, err, "Connection to gateway :8080 must succeed")
	defer conn.Close()

	var reqBuf bytes.Buffer
	fmt.Fprintf(&reqBuf, "GET %s HTTP/1.1\r\n", rawPath)
	fmt.Fprintf(&reqBuf, "Host: localhost:8080\r\n")
	if token != "" {
		fmt.Fprintf(&reqBuf, "Authorization: Bearer %s\r\n", token)
	}
	for k, v := range extraHeaders {
		fmt.Fprintf(&reqBuf, "%s: %s\r\n", k, v)
	}
	fmt.Fprintf(&reqBuf, "Connection: close\r\n\r\n")

	_, err = conn.Write(reqBuf.Bytes())
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	return resp
}

func TestMVPEndToEnd(t *testing.T) {
	ensureComposeCluster(t)
	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Authenticate against demo issuer (:8085) for developer, finance, application-admin
	devToken := loginToken(t, client, "developer")
	financeToken := loginToken(t, client, "finance")
	adminToken := loginToken(t, client, "application-admin")

	t.Run("Developer can retrieve orders and payments (HTTP 200)", func(t *testing.T) {
		// GET /api/orders
		reqOrders, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/orders", nil)
		require.NoError(t, err)
		reqOrders.Header.Set("Authorization", "Bearer "+devToken)

		respOrders, err := client.Do(reqOrders)
		require.NoError(t, err)
		defer respOrders.Body.Close()

		assert.Equal(t, http.StatusOK, respOrders.StatusCode)
		assertValidRequestID(t, respOrders.Header)

		bodyOrders, err := io.ReadAll(respOrders.Body)
		require.NoError(t, err)
		assert.Contains(t, string(bodyOrders), "ord_101")

		// GET /api/payments
		reqPayments, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/payments", nil)
		require.NoError(t, err)
		reqPayments.Header.Set("Authorization", "Bearer "+devToken)

		respPayments, err := client.Do(reqPayments)
		require.NoError(t, err)
		defer respPayments.Body.Close()

		assert.Equal(t, http.StatusOK, respPayments.StatusCode)
		assertValidRequestID(t, respPayments.Header)

		bodyPayments, err := io.ReadAll(respPayments.Body)
		require.NoError(t, err)
		assert.Contains(t, string(bodyPayments), "pay_")
	})

	t.Run("Developer cannot create payments (HTTP 403 Forbidden)", func(t *testing.T) {
		reqPost, err := http.NewRequest(http.MethodPost, "http://localhost:8080/api/payments", bytes.NewReader([]byte(`{"amount":100}`)))
		require.NoError(t, err)
		reqPost.Header.Set("Authorization", "Bearer "+devToken)
		reqPost.Header.Set("Content-Type", "application/json")

		respPost, err := client.Do(reqPost)
		require.NoError(t, err)
		defer respPost.Body.Close()

		assert.Equal(t, http.StatusForbidden, respPost.StatusCode)
		assertValidRequestID(t, respPost.Header)
	})

	t.Run("Developer is forbidden from admin routes (HTTP 403 with reason code)", func(t *testing.T) {
		reqAdmin, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/admin/users", nil)
		require.NoError(t, err)
		reqAdmin.Header.Set("Authorization", "Bearer "+devToken)

		respAdmin, err := client.Do(reqAdmin)
		require.NoError(t, err)
		defer respAdmin.Body.Close()

		assert.Equal(t, http.StatusForbidden, respAdmin.StatusCode)
		assertValidRequestID(t, respAdmin.Header)

		var problem map[string]interface{}
		err = json.NewDecoder(respAdmin.Body).Decode(&problem)
		require.NoError(t, err)
		assert.Equal(t, "DENIED_DEVELOPER_ADMIN_FORBIDDEN", problem["detail"])
	})

	t.Run("Finance user can create payments but cannot read orders", func(t *testing.T) {
		// POST /api/payments allowed for finance
		reqPost, err := http.NewRequest(http.MethodPost, "http://localhost:8080/api/payments", bytes.NewReader([]byte(`{"amount":500}`)))
		require.NoError(t, err)
		reqPost.Header.Set("Authorization", "Bearer "+financeToken)
		reqPost.Header.Set("Content-Type", "application/json")

		respPost, err := client.Do(reqPost)
		require.NoError(t, err)
		defer respPost.Body.Close()

		assert.Equal(t, http.StatusOK, respPost.StatusCode)
		assertValidRequestID(t, respPost.Header)

		// GET /api/orders forbidden for finance
		reqOrders, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/orders", nil)
		require.NoError(t, err)
		reqOrders.Header.Set("Authorization", "Bearer "+financeToken)

		respOrders, err := client.Do(reqOrders)
		require.NoError(t, err)
		defer respOrders.Body.Close()

		assert.Equal(t, http.StatusForbidden, respOrders.StatusCode)
		assertValidRequestID(t, respOrders.Header)
	})

	t.Run("Application admin can access admin users (HTTP 200)", func(t *testing.T) {
		reqAdmin, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/admin/users", nil)
		require.NoError(t, err)
		reqAdmin.Header.Set("Authorization", "Bearer "+adminToken)

		respAdmin, err := client.Do(reqAdmin)
		require.NoError(t, err)
		defer respAdmin.Body.Close()

		assert.Equal(t, http.StatusOK, respAdmin.StatusCode)
		assertValidRequestID(t, respAdmin.Header)

		body, err := io.ReadAll(respAdmin.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "admin")
	})

	t.Run("Path traversal attempts return HTTP 400 Bad Request immediately", func(t *testing.T) {
		traversalProbes := []string{
			"/api/orders/../admin",
			"/api/orders/%2e%2e/admin",
			"/api/orders/%2fadmin",
			"/api/orders/..%2fadmin",
		}

		for _, probe := range traversalProbes {
			resp := sendRawHTTP(t, probe, devToken, nil)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Probe %s must return 400 Bad Request", probe)
			assertValidRequestID(t, resp.Header)

			var problem map[string]interface{}
			err := json.NewDecoder(resp.Body).Decode(&problem)
			require.NoError(t, err)
			assert.Equal(t, "Bad Request", problem["title"])
		}
	})

	t.Run("Client-injected X-Aegis-User header is stripped and privilege escalation is denied", func(t *testing.T) {
		// Send request attempting to escalate to admin via header injection
		resp := sendRawHTTP(t, "/api/admin/users", devToken, map[string]string{
			"X-Aegis-User": "admin",
			"X-Aegis-Role": "application-admin",
		})
		defer resp.Body.Close()

		// Developer token must still be denied regardless of client-injected headers
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assertValidRequestID(t, resp.Header)
	})

	t.Run("Direct connection from host to backend port fails (BYP-01 network isolation)", func(t *testing.T) {
		backendPorts := []int{8081, 8082, 8083}
		for _, port := range backendPorts {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1*time.Second)
			require.Error(t, err, "Direct connection from host to backend port :%d must fail", port)
			if conn != nil {
				conn.Close()
			}
		}
	})

	t.Run("X-Request-ID is present and valid UUID on all responses", func(t *testing.T) {
		// Unauthenticated request -> 401
		anonReq, err := http.NewRequest(http.MethodGet, "http://localhost:8080/api/orders", nil)
		require.NoError(t, err)

		anonResp, err := client.Do(anonReq)
		require.NoError(t, err)
		defer anonResp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, anonResp.StatusCode)
		reqID := assertValidRequestID(t, anonResp.Header)
		assert.NotEmpty(t, reqID)
	})
}
