package security

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"aegis/internal/proxy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZeroRepairPathValidation(t *testing.T) {
	var downstreamInvocations int64

	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&downstreamInvocations, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	handler := proxy.PathValidationMiddleware(innerHandler)

	testCases := []struct {
		name         string
		rawPath      string
		expectedCode int
	}{
		{"dot dot traversal", "/api/orders/../admin", http.StatusBadRequest},
		{"percent encoded dot dot", "/api/orders/%2e%2e/admin", http.StatusBadRequest},
		{"dot dot suffix", "/api/orders/..", http.StatusBadRequest},
		{"percent encoded dot dot suffix", "/api/orders/%2e%2e", http.StatusBadRequest},
		{"single dot segment", "/api/orders/./test", http.StatusBadRequest},
		{"encoded slash", "/api/orders%2fadmin", http.StatusBadRequest},
		{"encoded backslash", "/api/orders%5cadmin", http.StatusBadRequest},
		{"literal backslash", "/api/orders\\admin", http.StatusBadRequest},
		{"duplicate slashes", "//api/orders", http.StatusBadRequest},
		{"nul byte raw", "/api/orders\x00/test", http.StatusBadRequest},
		{"nul byte encoded", "/api/orders%00/test", http.StatusBadRequest},
		{"no leading slash", "api/orders", http.StatusBadRequest},
		{"valid path", "/api/orders", http.StatusOK},
	}

	t.Run("pipeline validation on raw wire paths", func(t *testing.T) {
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				invocationsBefore := atomic.LoadInt64(&downstreamInvocations)

				req := httptest.NewRequest(http.MethodGet, "http://aegis.local", nil)
				req.RequestURI = tc.rawPath
				req.Header.Set("X-Request-ID", "sec-test-req-id")
				rec := httptest.NewRecorder()

				handler.ServeHTTP(rec, req)

				assert.Equal(t, tc.expectedCode, rec.Code)

				if tc.expectedCode == http.StatusBadRequest {
					assert.Equal(t, invocationsBefore, atomic.LoadInt64(&downstreamInvocations), "Downstream must never be called on invalid paths")
					assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

					var problem proxy.RFC7807Problem
					err := json.Unmarshal(rec.Body.Bytes(), &problem)
					require.NoError(t, err)
					assert.Equal(t, "https://aegis.local/errors/invalid-path", problem.Type)
					assert.Equal(t, "Bad Request", problem.Title)
					assert.Equal(t, http.StatusBadRequest, problem.Status)
					assert.NotEmpty(t, problem.Detail)
					assert.Equal(t, "sec-test-req-id", problem.Instance)
				} else {
					assert.Equal(t, invocationsBefore+1, atomic.LoadInt64(&downstreamInvocations), "Downstream must be called on valid path")
				}
			})
		}
	})

	t.Run("http test server wire transport", func(t *testing.T) {
		server := httptest.NewServer(handler)
		defer server.Close()

		serverURL, err := url.Parse(server.URL)
		require.NoError(t, err)

		wireCases := []struct {
			name         string
			rawPath      string
			expectedCode int
		}{
			{"dot dot traversal", "/api/orders/../admin", http.StatusBadRequest},
			{"percent encoded dot dot", "/api/orders/%2e%2e/admin", http.StatusBadRequest},
			{"dot dot suffix", "/api/orders/..", http.StatusBadRequest},
			{"percent encoded dot dot suffix", "/api/orders/%2e%2e", http.StatusBadRequest},
			{"single dot segment", "/api/orders/./test", http.StatusBadRequest},
			{"encoded slash", "/api/orders%2fadmin", http.StatusBadRequest},
			{"encoded backslash", "/api/orders%5cadmin", http.StatusBadRequest},
			{"duplicate slashes", "//api/orders", http.StatusBadRequest},
			{"nul byte encoded", "/api/orders%00/test", http.StatusBadRequest},
			{"valid path", "/api/orders", http.StatusOK},
		}

		for _, tc := range wireCases {
			t.Run(tc.name, func(t *testing.T) {
				conn, err := net.Dial("tcp", serverURL.Host)
				require.NoError(t, err)
				defer conn.Close()

				reqText := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", tc.rawPath, serverURL.Host)
				_, err = conn.Write([]byte(reqText))
				require.NoError(t, err)

				resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
				require.NoError(t, err)
				defer resp.Body.Close()

				assert.Equal(t, tc.expectedCode, resp.StatusCode)
				if tc.expectedCode == http.StatusBadRequest {
					assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
				}
			})
		}

		// Verify RoundTrip with DefaultTransport
		reqValid, err := http.NewRequest(http.MethodGet, server.URL+"/api/orders", nil)
		require.NoError(t, err)
		respValid, err := http.DefaultTransport.RoundTrip(reqValid)
		require.NoError(t, err)
		defer respValid.Body.Close()
		assert.Equal(t, http.StatusOK, respValid.StatusCode)

		reqTraversal, err := http.NewRequest(http.MethodGet, server.URL+"/placeholder", nil)
		require.NoError(t, err)
		reqTraversal.URL.Opaque = "/api/orders/../admin"
		respTraversal, err := http.DefaultTransport.RoundTrip(reqTraversal)
		require.NoError(t, err)
		defer respTraversal.Body.Close()
		assert.Equal(t, http.StatusBadRequest, respTraversal.StatusCode)
		assert.Equal(t, "application/problem+json", respTraversal.Header.Get("Content-Type"))
	})
}
