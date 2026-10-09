package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aegis/internal/config"
	"aegis/internal/pki"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDualServer_StartupAndShutdown(t *testing.T) {
	ca, err := pki.NewCA("Test DualServer CA")
	require.NoError(t, err)

	serverCert, err := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	userLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer userLn.Close()

	workloadLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer workloadLn.Close()

	userPort := userLn.Addr().(*net.TCPAddr).Port
	workloadPort := workloadLn.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		Port:                  userPort,
		WorkloadPort:          workloadPort,
		MaxHeaderBytes:        16384,
		MaxBodyBytes:          1048576,
		ReadHeaderTimeout:     5 * time.Second,
		WriteTimeout:          5 * time.Second,
		MaxConcurrentRequests: 100,
	}

	userHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("user-ok"))
	})

	workloadHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("workload-ok"))
	})

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    ca.CertPool,
	}

	ds := NewDualServer(cfg, userHandler, workloadHandler, tlsCfg)
	require.NotNil(t, ds)
	assert.Equal(t, tls.RequireAndVerifyClientCert, ds.WorkloadServer().TLSConfig.ClientAuth)

	errCh := make(chan error, 1)
	go func() {
		errCh <- ds.Serve(userLn, workloadLn)
	}()

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = ds.Shutdown(ctx)
	assert.NoError(t, err)

	serveErr := <-errCh
	assert.ErrorIs(t, serveErr, http.ErrServerClosed)
}

func TestDualServer_AmbiguousCredentialsRejection(t *testing.T) {
	cfg := &config.Config{
		Port:                  8080,
		WorkloadPort:          9443,
		MaxHeaderBytes:        16384,
		MaxBodyBytes:          1048576,
		MaxConcurrentRequests: 100,
	}

	userHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	workloadHandlerCalled := false
	workloadHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workloadHandlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	ds := NewDualServer(cfg, userHandler, workloadHandler, nil)

	// Workload listener receives a request with Authorization header
	req := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:9443/api/orders", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.dummy.sig")

	rec := httptest.NewRecorder()
	ds.WorkloadHandler().ServeHTTP(rec, req)

	assert.False(t, workloadHandlerCalled, "workload handler should not have been reached")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

	var problem RFC7807Problem
	err := json.Unmarshal(rec.Body.Bytes(), &problem)
	require.NoError(t, err)

	assert.Equal(t, "https://aegis.local/errors/ambiguous-credentials", problem.Type)
	assert.Equal(t, "Unauthorized", problem.Title)
	assert.Equal(t, http.StatusUnauthorized, problem.Status)
	assert.Equal(t, "Ambiguous credentials: Bearer tokens are prohibited on the workload listener", problem.Detail)
	assert.Equal(t, rec.Header().Get("X-Request-ID"), problem.Instance)
}

func TestDualServer_HeaderByteLimits(t *testing.T) {
	cfg := &config.Config{
		Port:                  8080,
		WorkloadPort:          9443,
		MaxHeaderBytes:        256, // small limit to easily trigger
		MaxBodyBytes:          1048576,
		MaxConcurrentRequests: 100,
	}

	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ds := NewDualServer(cfg, mockHandler, mockHandler, nil)

	t.Run("user handler exceeds header bytes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/orders", nil)
		req.Header.Set("X-Large-Header", strings.Repeat("A", 300))

		rec := httptest.NewRecorder()
		ds.UserHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusRequestHeaderFieldsTooLarge, rec.Code)
		assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

		var problem RFC7807Problem
		err := json.Unmarshal(rec.Body.Bytes(), &problem)
		require.NoError(t, err)
		assert.Equal(t, "https://aegis.local/errors/header-too-large", problem.Type)
	})

	t.Run("workload handler exceeds header bytes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:9443/api/orders", nil)
		req.Header.Set("X-Large-Header", strings.Repeat("B", 300))

		rec := httptest.NewRecorder()
		ds.WorkloadHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusRequestHeaderFieldsTooLarge, rec.Code)
		assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

		var problem RFC7807Problem
		err := json.Unmarshal(rec.Body.Bytes(), &problem)
		require.NoError(t, err)
		assert.Equal(t, "https://aegis.local/errors/header-too-large", problem.Type)
	})
}

func TestDualServer_BodySizeBounding(t *testing.T) {
	cfg := &config.Config{
		Port:                  8080,
		WorkloadPort:          9443,
		MaxHeaderBytes:        16384,
		MaxBodyBytes:          64, // 64 bytes max body
		MaxConcurrentRequests: 100,
	}

	bodyReadingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "body too large: "+err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	ds := NewDualServer(cfg, bodyReadingHandler, bodyReadingHandler, nil)

	t.Run("user handler body exceeds limit", func(t *testing.T) {
		oversizedBody := bytes.Repeat([]byte("X"), 128)
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/orders", bytes.NewReader(oversizedBody))

		rec := httptest.NewRecorder()
		ds.UserHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	})

	t.Run("workload handler body exceeds limit", func(t *testing.T) {
		oversizedBody := bytes.Repeat([]byte("Y"), 128)
		req := httptest.NewRequest(http.MethodPost, "https://127.0.0.1:9443/api/orders", bytes.NewReader(oversizedBody))

		rec := httptest.NewRecorder()
		ds.WorkloadHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	})
}

func TestDualServer_RejectionRecorder(t *testing.T) {
	newCfg := func(maxConcurrent int) *config.Config {
		return &config.Config{
			Port:                  8080,
			WorkloadPort:          9443,
			MaxHeaderBytes:        256,
			MaxBodyBytes:          1048576,
			MaxConcurrentRequests: maxConcurrent,
		}
	}
	reached := false
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	t.Run("user listener oversized headers record header_too_large", func(t *testing.T) {
		ds := NewDualServer(newCfg(10), ok, ok, nil)
		rec := &fakeRecorder{}
		ds.SetRejectionRecorder(rec)

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/x", nil)
		req.Header.Set("X-Large", strings.Repeat("A", 300))
		out := httptest.NewRecorder()
		ds.UserHandler().ServeHTTP(out, req)

		assert.Equal(t, http.StatusRequestHeaderFieldsTooLarge, out.Code)
		assert.Equal(t, []string{"header_too_large"}, rec.got())
	})

	t.Run("workload listener bearer records ambiguous_credentials", func(t *testing.T) {
		ds := NewDualServer(newCfg(10), ok, ok, nil)
		rec := &fakeRecorder{}
		ds.SetRejectionRecorder(rec)

		req := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:9443/x", nil)
		req.Header.Set("Authorization", "Bearer abc")
		out := httptest.NewRecorder()
		ds.WorkloadHandler().ServeHTTP(out, req)

		assert.Equal(t, http.StatusUnauthorized, out.Code)
		assert.Equal(t, []string{"ambiguous_credentials"}, rec.got())
	})

	t.Run("workload listener oversized headers record header_too_large", func(t *testing.T) {
		ds := NewDualServer(newCfg(10), ok, ok, nil)
		rec := &fakeRecorder{}
		ds.SetRejectionRecorder(rec)

		req := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:9443/x", nil)
		req.Header.Set("X-Large", strings.Repeat("B", 300))
		out := httptest.NewRecorder()
		ds.WorkloadHandler().ServeHTTP(out, req)

		assert.Equal(t, http.StatusRequestHeaderFieldsTooLarge, out.Code)
		assert.Equal(t, []string{"header_too_large"}, rec.got())
	})

	t.Run("concurrency rejection reaches the recorder attached via DualServer", func(t *testing.T) {
		hold := make(chan struct{})
		running := make(chan struct{})
		var once sync.Once
		blocking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(running) })
			<-hold
		})
		ds := NewDualServer(newCfg(1), blocking, blocking, nil)
		rec := &fakeRecorder{}
		ds.SetRejectionRecorder(rec)

		done := make(chan struct{})
		go func() {
			defer close(done)
			ds.UserHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/hold", nil))
		}()
		<-running

		out := httptest.NewRecorder()
		ds.WorkloadHandler().ServeHTTP(out, httptest.NewRequest(http.MethodGet, "https://127.0.0.1:9443/x", nil))
		close(hold)
		<-done

		assert.Equal(t, http.StatusTooManyRequests, out.Code)
		assert.Equal(t, []string{"concurrency"}, rec.got())
	})

	t.Run("passing requests are not counted and nil recorder is safe", func(t *testing.T) {
		ds := NewDualServer(newCfg(10), ok, ok, nil)

		// No recorder attached: rejections must not panic.
		bad := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:9443/x", nil)
		bad.Header.Set("Authorization", "Bearer abc")
		assert.NotPanics(t, func() { ds.WorkloadHandler().ServeHTTP(httptest.NewRecorder(), bad) })

		rec := &fakeRecorder{}
		ds.SetRejectionRecorder(rec)
		reached = false
		out := httptest.NewRecorder()
		ds.UserHandler().ServeHTTP(out, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/x", nil))
		assert.True(t, reached)
		assert.Equal(t, http.StatusOK, out.Code)
		assert.Empty(t, rec.got())
	})
}
