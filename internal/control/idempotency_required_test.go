package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRequireIdempotencyKey(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	handler := h.server.Handler()
	cookie, csrfToken := loginOperator(t, handler, "admin", "admin-secret")

	do := func(method, path string, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(`{}`))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrfToken)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	gated := []struct{ method, path string }{
		{http.MethodPost, "/control/v1/routes"},
		{http.MethodPost, "/control/v1/policies"},
		{http.MethodPost, "/control/v1/policies/x/publish"},
		{http.MethodPost, "/control/v1/policies/x/rollback"},
		{http.MethodPost, "/control/v1/snapshots/rollback"},
		{http.MethodPost, "/control/v1/principals/x/quarantine"},
		{http.MethodDelete, "/control/v1/principals/x/quarantine"},
		{http.MethodPost, "/control/v1/quarantine"},
		{http.MethodPost, "/control/v1/revocations"},
	}
	for _, g := range gated {
		t.Run(g.method+" "+g.path, func(t *testing.T) {
			rec := do(g.method, g.path, "")
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "IDEMPOTENCY_KEY_REQUIRED")

			rec = do(g.method, g.path, uuid.NewString())
			require.NotContains(t, rec.Body.String(), "IDEMPOTENCY_KEY_REQUIRED")
		})
	}

	t.Run("not gated", func(t *testing.T) {
		for _, p := range []struct{ method, path string }{
			{http.MethodGet, "/control/v1/gateways"},
			{http.MethodPost, "/control/v1/policies/x/validate"},
			{http.MethodPost, "/control/v1/policies/simulate"},
			{http.MethodPost, "/control/v1/auth/logout"},
		} {
			rec := do(p.method, p.path, "")
			require.NotContains(t, rec.Body.String(), "IDEMPOTENCY_KEY_REQUIRED", p.path)
		}
	})

	t.Run("same key replays", func(t *testing.T) {
		cookie, csrfToken = loginOperator(t, handler, "admin", "admin-secret")
		key := uuid.NewString()
		first := do(http.MethodPost, "/control/v1/policies/x/publish", key)
		second := do(http.MethodPost, "/control/v1/policies/x/publish", key)
		require.Equal(t, first.Code, second.Code)
		require.Equal(t, "true", second.Header().Get("Idempotent-Replay"))
	})
}
