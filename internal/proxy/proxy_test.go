package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/internal/config"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenerLimits(t *testing.T) {
	cfg := &config.Config{
		Port:                  8080,
		MaxHeaderBytes:        16 * 1024,      // 16 KiB
		MaxBodyBytes:          1024 * 1024,    // 1 MiB
		MaxConcurrentRequests: 100,
	}

	downstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"read":%d}`, len(body))))
	})

	server := NewServer(cfg, downstreamHandler)

	ts := httptest.NewUnstartedServer(server.Handler())
	ts.Config.MaxHeaderBytes = cfg.MaxHeaderBytes
	ts.Start()
	defer ts.Close()

	client := ts.Client()

	t.Run("valid request receives UUID v4 correlation ID", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/test", nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		reqID := resp.Header.Get("X-Request-ID")
		require.NotEmpty(t, reqID)

		parsed, err := uuid.Parse(reqID)
		require.NoError(t, err)
		assert.Equal(t, uuid.Version(4), parsed.Version())
	})

	t.Run("header block exceeding 16 KiB rejected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/oversized-header", nil)
		require.NoError(t, err)

		// Create header block > 16 KiB
		largeHeaderVal := strings.Repeat("X", 18*1024)
		req.Header.Set("X-Oversized-Header", largeHeaderVal)

		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			// Should be 431 Request Header Fields Too Large or 400 Bad Request
			assert.True(t, resp.StatusCode == http.StatusRequestHeaderFieldsTooLarge || resp.StatusCode == http.StatusBadRequest,
				"Expected 431 or 400, got %d", resp.StatusCode)
		}
		// If the server terminates the connection directly without response, that also satisfies rejection
	})

	t.Run("body exceeding 1 MiB rejected", func(t *testing.T) {
		// 1.5 MiB payload
		largeBody := bytes.Repeat([]byte("A"), 1536*1024)
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/oversized-body", bytes.NewReader(largeBody))
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.True(t, resp.StatusCode == http.StatusRequestEntityTooLarge || resp.StatusCode == http.StatusBadRequest,
			"Expected 413 or 400, got %d", resp.StatusCode)
	})

	t.Run("body within 1 MiB succeeds", func(t *testing.T) {
		smallBody := bytes.Repeat([]byte("B"), 512*1024) // 512 KiB
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/normal-body", bytes.NewReader(smallBody))
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
