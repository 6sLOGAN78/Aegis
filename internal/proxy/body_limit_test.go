package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"aegis/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bodyLimitFixture wires a DualServer whose handlers reverse-proxy to a backend
// that replies 200 without ever reading the body (the GW-01 repro shape).
type bodyLimitFixture struct {
	rec      *fakeRecorder
	hits     *int32
	handlers map[string]http.Handler
}

func newBodyLimitFixture(t *testing.T) *bodyLimitFixture {
	return newBodyLimitFixtureWith(t, nil)
}

// newBodyLimitFixtureWith lets a test add backend response headers (for
// example Connection: close, which makes the backend reply at once instead of
// first discarding up to 256 KiB of an unread request body).
func newBodyLimitFixtureWith(t *testing.T, hdr http.Header) *bodyLimitFixture {
	t.Helper()
	var hits int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		for k, v := range hdr {
			w.Header()[k] = v
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	backendURL, err := url.Parse(backend.URL)
	require.NoError(t, err)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp := NewReverseProxyWithMTLS(backendURL, "/api/payments", "", "", nil)
		rp.ServeHTTP(w, r)
	})

	cfg := &config.Config{
		Port:                  8080,
		WorkloadPort:          9443,
		MaxHeaderBytes:        16384,
		MaxBodyBytes:          1048576,
		MaxConcurrentRequests: 100,
	}
	ds := NewDualServer(cfg, inner, inner, nil)
	rec := &fakeRecorder{}
	ds.SetRejectionRecorder(rec)

	return &bodyLimitFixture{
		rec:  rec,
		hits: &hits,
		handlers: map[string]http.Handler{
			"user":     ds.UserHandler(),
			"workload": ds.WorkloadHandler(),
		},
	}
}

func TestBodyLimit_ContentLengthOverLimitRejected(t *testing.T) {
	for _, name := range []string{"user", "workload"} {
		t.Run(name, func(t *testing.T) {
			f := newBodyLimitFixture(t)
			req := httptest.NewRequest(http.MethodPost, "/api/payments", bytes.NewReader(make([]byte, 2000000)))
			require.Equal(t, int64(2000000), req.ContentLength)
			rec := httptest.NewRecorder()
			f.handlers[name].ServeHTTP(rec, req)

			assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			var p RFC7807Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, "https://aegis.local/errors/body-too-large", p.Type)
			assert.Equal(t, "Payload Too Large", p.Title)
			assert.Equal(t, rec.Header().Get("X-Request-ID"), p.Instance)
			assert.EqualValues(t, 0, atomic.LoadInt32(f.hits), "backend must not be reached")
			assert.Equal(t, []string{"body_too_large"}, f.rec.got())
		})
	}
}

func TestBodyLimit_ChunkedOverLimitNeverSucceeds(t *testing.T) {
	for _, name := range []string{"user", "workload"} {
		t.Run(name, func(t *testing.T) {
			f := newBodyLimitFixture(t)
			srv := httptest.NewServer(f.handlers[name])
			defer srv.Close()

			// io.MultiReader hides the length, forcing chunked transfer encoding.
			body := io.MultiReader(bytes.NewReader(make([]byte, 2000000)))
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/payments", body)
			require.NoError(t, err)
			req.ContentLength = -1

			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
			}
			// A transport error (connection reset after an early reply) is also
			// acceptable: the hard requirement is that no 2xx is observed.
		})
	}
}

func TestBodyLimit_ExactlyAtLimitForwarded(t *testing.T) {
	for _, name := range []string{"user", "workload"} {
		t.Run(name, func(t *testing.T) {
			f := newBodyLimitFixture(t)
			req := httptest.NewRequest(http.MethodPost, "/api/payments", bytes.NewReader(make([]byte, 1048576)))
			rec := httptest.NewRecorder()
			f.handlers[name].ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.EqualValues(t, 1, atomic.LoadInt32(f.hits))
			assert.Empty(t, f.rec.got())
		})
	}
}

// A chunked client that stalls after an early upstream reply must not hold the
// handler past bodyDrainTimeout; the gateway fails closed with 408.
func TestBodyLimit_StalledChunkedBodyTimesOut(t *testing.T) {
	old := bodyDrainTimeout
	bodyDrainTimeout = 200 * time.Millisecond
	t.Cleanup(func() { bodyDrainTimeout = old })

	for _, name := range []string{"user", "workload"} {
		t.Run(name, func(t *testing.T) {
			f := newBodyLimitFixtureWith(t, http.Header{"Connection": {"close"}})
			srv := httptest.NewServer(f.handlers[name])
			defer srv.Close()

			pr, pw := io.Pipe()
			defer pw.Close()
			go func() { _, _ = pw.Write(make([]byte, 1024)) }() // then stall

			req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/payments", pr)
			require.NoError(t, err)
			req.ContentLength = -1

			start := time.Now()
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusRequestTimeout, resp.StatusCode)
			assert.Less(t, time.Since(start), 3*time.Second)
		})
	}
}
