package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	controlv1 "aegis/pkg/api/control/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionAndCSRF(t *testing.T) {
	sm := NewSessionManager()
	auth := NewAuthHandler(sm)

	t.Run("Session Creation and Expiry", func(t *testing.T) {
		sess, err := sm.CreateSession("test-operator", "sec-ops")
		require.NoError(t, err)
		assert.NotEmpty(t, sess.ID)
		assert.NotEmpty(t, sess.CSRFToken)
		assert.Equal(t, "test-operator", sess.Username)
		assert.Equal(t, "sec-ops", sess.Role)
		assert.True(t, sess.ExpiresAt.After(time.Now().UTC().Add(7*time.Hour)))

		fetched, ok := sm.GetSession(sess.ID)
		assert.True(t, ok)
		assert.Equal(t, sess.ID, fetched.ID)

		sm.RevokeSession(sess.ID)
		_, ok = sm.GetSession(sess.ID)
		assert.False(t, ok)
	})

	t.Run("Login Success and Set-Cookie", func(t *testing.T) {
		body := `{"username":"admin","password":"admin-secret"}`
		req := httptest.NewRequest(http.MethodPost, "/control/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		auth.HandleLogin(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp LoginResponse
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, "admin", resp.Username)
		assert.Equal(t, "sec-ops", resp.Role)
		assert.NotEmpty(t, resp.CSRFToken)

		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		cookie := cookies[0]
		assert.Equal(t, CookieSessionName, cookie.Name)
		assert.NotEmpty(t, cookie.Value)
		assert.True(t, cookie.HttpOnly)
		assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
		assert.True(t, cookie.Secure)
	})

	t.Run("Login Failure with Invalid Credentials", func(t *testing.T) {
		body := `{"username":"admin","password":"wrong-password"}`
		req := httptest.NewRequest(http.MethodPost, "/control/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		auth.HandleLogin(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		var errResp controlv1.ErrorResponse
		err := json.Unmarshal(rec.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Equal(t, "INVALID_CREDENTIALS", errResp.Code)
	})

	t.Run("Logout Clears Session", func(t *testing.T) {
		sess, err := sm.CreateSession("admin", "sec-ops")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/control/v1/auth/logout", nil)
		req.AddCookie(&http.Cookie{Name: CookieSessionName, Value: sess.ID})
		rec := httptest.NewRecorder()

		auth.HandleLogout(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		// Session is revoked in store
		_, ok := sm.GetSession(sess.ID)
		assert.False(t, ok)

		// Cookie is expired
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, -1, cookies[0].MaxAge)
	})

	t.Run("CSRF Protection on Mutating and Safe Methods", func(t *testing.T) {
		sess, err := sm.CreateSession("operator", "sec-ops")
		require.NoError(t, err)

		dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})

		protected := sm.SessionMiddleware(dummyHandler)

		// 1. GET without CSRF token -> SUCCEEDS (Safe method)
		getReq := httptest.NewRequest(http.MethodGet, "/control/v1/test", nil)
		getReq.AddCookie(&http.Cookie{Name: CookieSessionName, Value: sess.ID})
		getRec := httptest.NewRecorder()
		protected.ServeHTTP(getRec, getReq)
		assert.Equal(t, http.StatusOK, getRec.Code)

		// 2. POST without CSRF token -> FAILS (403)
		postNoCsrf := httptest.NewRequest(http.MethodPost, "/control/v1/test", nil)
		postNoCsrf.AddCookie(&http.Cookie{Name: CookieSessionName, Value: sess.ID})
		postNoCsrfRec := httptest.NewRecorder()
		protected.ServeHTTP(postNoCsrfRec, postNoCsrf)
		assert.Equal(t, http.StatusForbidden, postNoCsrfRec.Code)
		assert.Contains(t, postNoCsrfRec.Body.String(), "CSRF_TOKEN_INVALID")

		// 3. POST with invalid CSRF token -> FAILS (403)
		postBadCsrf := httptest.NewRequest(http.MethodPost, "/control/v1/test", nil)
		postBadCsrf.AddCookie(&http.Cookie{Name: CookieSessionName, Value: sess.ID})
		postBadCsrf.Header.Set("X-CSRF-Token", "invalid-token-12345")
		postBadCsrfRec := httptest.NewRecorder()
		protected.ServeHTTP(postBadCsrfRec, postBadCsrf)
		assert.Equal(t, http.StatusForbidden, postBadCsrfRec.Code)
		assert.Contains(t, postBadCsrfRec.Body.String(), "CSRF_TOKEN_INVALID")

		// 4. POST with valid CSRF token -> SUCCEEDS (200)
		postValidCsrf := httptest.NewRequest(http.MethodPost, "/control/v1/test", nil)
		postValidCsrf.AddCookie(&http.Cookie{Name: CookieSessionName, Value: sess.ID})
		postValidCsrf.Header.Set("X-CSRF-Token", sess.CSRFToken)
		postValidCsrfRec := httptest.NewRecorder()
		protected.ServeHTTP(postValidCsrfRec, postValidCsrf)
		assert.Equal(t, http.StatusOK, postValidCsrfRec.Code)

		// 5. Missing Session Cookie -> FAILS (401)
		postNoCookie := httptest.NewRequest(http.MethodPost, "/control/v1/test", nil)
		postNoCookie.Header.Set("X-CSRF-Token", sess.CSRFToken)
		postNoCookieRec := httptest.NewRecorder()
		protected.ServeHTTP(postNoCookieRec, postNoCookie)
		assert.Equal(t, http.StatusUnauthorized, postNoCookieRec.Code)
	})

	t.Run("Invariant 11: Data-Plane Bearer Token Rejection", func(t *testing.T) {
		sess, err := sm.CreateSession("operator", "sec-ops")
		require.NoError(t, err)

		dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		protected := sm.SessionMiddleware(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/control/v1/test", nil)
		req.AddCookie(&http.Cookie{Name: CookieSessionName, Value: sess.ID})
		req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiIs...") // Data-plane credential
		rec := httptest.NewRecorder()

		protected.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Contains(t, rec.Body.String(), "DATA_PLANE_CREDENTIALS_REJECTED")
	})

	t.Run("RBAC Middleware Role Enforcement", func(t *testing.T) {
		dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		secOpsOnly := sm.SessionMiddleware(RequireRole("sec-ops")(dummyHandler))

		// Viewer session
		viewerSess, err := sm.CreateSession("operator-view", "viewer")
		require.NoError(t, err)

		viewReq := httptest.NewRequest(http.MethodGet, "/control/v1/mutate", nil)
		viewReq.AddCookie(&http.Cookie{Name: CookieSessionName, Value: viewerSess.ID})
		viewRec := httptest.NewRecorder()
		secOpsOnly.ServeHTTP(viewRec, viewReq)
		assert.Equal(t, http.StatusForbidden, viewRec.Code)
		assert.Contains(t, viewRec.Body.String(), "FORBIDDEN")

		// Sec-Ops session
		secOpsSess, err := sm.CreateSession("admin", "sec-ops")
		require.NoError(t, err)

		secOpsReq := httptest.NewRequest(http.MethodGet, "/control/v1/mutate", nil)
		secOpsReq.AddCookie(&http.Cookie{Name: CookieSessionName, Value: secOpsSess.ID})
		secOpsRec := httptest.NewRecorder()
		secOpsOnly.ServeHTTP(secOpsRec, secOpsReq)
		assert.Equal(t, http.StatusOK, secOpsRec.Code)
	})
}
