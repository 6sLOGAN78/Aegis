package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProbeHandlerSecurityState(t *testing.T) {
	build := func(opts ...ProbeOption) http.Handler {
		return CreateProbeHandler(NewDrainingState(), func(time.Duration) bool { return false }, func() bool { return true }, func() bool { return false }, nil, opts...)
	}
	get := func(h http.Handler, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	t.Run("missing returns 503 with exact body", func(t *testing.T) {
		rec := get(build(WithSecurityState(func() bool { return true })), "/readyz")
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, "SECURITY_STATE_MISSING\n", rec.Body.String())
	})
	t.Run("present returns 200", func(t *testing.T) {
		rec := get(build(WithSecurityState(func() bool { return false })), "/readyz")
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("no option unchanged", func(t *testing.T) {
		rec := get(build(), "/readyz")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "READY\n", rec.Body.String())
	})
	t.Run("livez unaffected", func(t *testing.T) {
		rec := get(build(WithSecurityState(func() bool { return true })), "/livez")
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("drain takes precedence", func(t *testing.T) {
		d := NewDrainingState()
		d.SetDraining()
		h := CreateProbeHandler(d, nil, nil, nil, nil, WithSecurityState(func() bool { return true }))
		assert.Equal(t, "DRAINING\n", get(h, "/readyz").Body.String())
	})
}
