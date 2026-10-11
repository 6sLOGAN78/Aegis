package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func probe(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestLivezUnauthenticated(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	rec := probe(h.server.Handler(), "/livez")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OK\n", rec.Body.String())
}

func TestReadyzStates(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	handler := h.server.Handler()

	// legacy: no SecurityState set
	assert.Equal(t, http.StatusOK, probe(handler, "/readyz").Code)

	var pingErr error
	ss := NewSecurityState(newFakeSecRepo(), h.revStore, WithPinger(func(context.Context) error { return pingErr }))
	h.server.SetSecurityState(ss)

	rec := probe(handler, "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "SECURITY_STATE_MISSING", strings.TrimSpace(rec.Body.String()))

	require.NoError(t, ss.Reconstruct(context.Background()))
	assert.Equal(t, http.StatusOK, probe(handler, "/readyz").Code)

	pingErr = errors.New("secret connection string")
	rec = probe(handler, "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "DEPENDENCY_FAILURE", strings.TrimSpace(rec.Body.String()))
	assert.NotContains(t, rec.Body.String(), "secret")
}
