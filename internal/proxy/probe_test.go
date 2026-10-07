package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProbeHandler(t *testing.T) {
	nextCalled := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("NEXT_HANDLER"))
	})

	t.Run("livez and healthz return 200 OK", func(t *testing.T) {
		drainState := NewDrainingState()
		handler := CreateProbeHandler(drainState, func(time.Duration) bool { return false }, func() bool { return true }, func() bool { return false }, nextHandler)

		for _, path := range []string{"/livez", "/healthz"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "OK\n", rec.Body.String())
		}
	})

	t.Run("readyz returns 200 READY when all healthy", func(t *testing.T) {
		drainState := NewDrainingState()
		handler := CreateProbeHandler(drainState, func(time.Duration) bool { return false }, func() bool { return true }, func() bool { return false }, nextHandler)

		for _, path := range []string{"/readyz", "/healthz/ready"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "READY\n", rec.Body.String())
		}
	})

	t.Run("readyz returns 503 DRAINING when draining", func(t *testing.T) {
		drainState := NewDrainingState()
		drainState.SetDraining()
		assert.True(t, drainState.IsDraining())

		handler := CreateProbeHandler(drainState, func(time.Duration) bool { return false }, func() bool { return true }, func() bool { return false }, nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, "DRAINING\n", rec.Body.String())
	})

	t.Run("readyz returns 503 LEASE_EXPIRED when lease expired", func(t *testing.T) {
		drainState := NewDrainingState()
		handler := CreateProbeHandler(drainState, func(d time.Duration) bool {
			return d == 60*time.Second
		}, func() bool { return true }, func() bool { return false }, nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, "LEASE_EXPIRED\n", rec.Body.String())
	})

	t.Run("readyz returns 503 UNINITIALIZED when snapshot not active", func(t *testing.T) {
		drainState := NewDrainingState()
		handler := CreateProbeHandler(drainState, func(time.Duration) bool { return false }, func() bool { return false }, func() bool { return false }, nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, "UNINITIALIZED\n", rec.Body.String())
	})

	t.Run("readyz returns 503 SPOOL_SATURATED when spool saturated", func(t *testing.T) {
		drainState := NewDrainingState()
		handler := CreateProbeHandler(drainState, func(time.Duration) bool { return false }, func() bool { return true }, func() bool { return true }, nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, "SPOOL_SATURATED\n", rec.Body.String())
	})

	t.Run("passes non-probe paths through to next handler", func(t *testing.T) {
		nextCalled = false
		drainState := NewDrainingState()
		handler := CreateProbeHandler(drainState, func(time.Duration) bool { return false }, func() bool { return true }, func() bool { return false }, nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.True(t, nextCalled)
		assert.Equal(t, http.StatusTeapot, rec.Code)
		assert.Equal(t, "NEXT_HANDLER", rec.Body.String())
	})

	t.Run("nil callbacks handling gracefully", func(t *testing.T) {
		handler := CreateProbeHandler(nil, nil, nil, nil, nil)

		reqLive := httptest.NewRequest(http.MethodGet, "/livez", nil)
		recLive := httptest.NewRecorder()
		handler.ServeHTTP(recLive, reqLive)
		assert.Equal(t, http.StatusOK, recLive.Code)

		reqReady := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		recReady := httptest.NewRecorder()
		handler.ServeHTTP(recReady, reqReady)
		assert.Equal(t, http.StatusOK, recReady.Code)

		reqOther := httptest.NewRequest(http.MethodGet, "/other", nil)
		recOther := httptest.NewRecorder()
		handler.ServeHTTP(recOther, reqOther)
		assert.Equal(t, http.StatusOK, recOther.Code)
	})
}
