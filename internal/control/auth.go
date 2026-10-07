package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type operatorCredentials struct {
	password string
	role     string
}

// Seeded demo operators according to OPS-04 and Task 1 requirements
var demoOperators = map[string]operatorCredentials{
	"admin":         {password: "admin-secret", role: "sec-ops"},
	"sec-auditor":   {password: "auditor-secret", role: "auditor"},
	"operator-view": {password: "view-secret", role: "viewer"},
}

// LoginRequest defines credentials for operator login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse defines session response metadata returned upon login.
type LoginResponse struct {
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AuthHandler handles operator session creation, status inspection, and revocation.
type AuthHandler struct {
	sessionMgr *SessionManager
}

// NewAuthHandler constructs an AuthHandler.
func NewAuthHandler(sessionMgr *SessionManager) *AuthHandler {
	return &AuthHandler{sessionMgr: sessionMgr}
}

// HandleLogin authenticates an operator, establishes a session, and sets an HttpOnly cookie.
func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	// Invariant 11: Inbound data plane credentials rejected
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		writeErrorResponse(w, r, http.StatusForbidden, "DATA_PLANE_CREDENTIALS_REJECTED", "Data plane credentials rejected on management endpoints")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid login request payload")
		return
	}

	creds, ok := demoOperators[req.Username]
	if !ok || creds.password != req.Password {
		writeErrorResponse(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid operator username or password")
		return
	}

	sess, err := h.sessionMgr.CreateSession(req.Username, creds.role)
	if err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create operator session")
		return
	}

	cookie := &http.Cookie{
		Name:     CookieSessionName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
		Expires:  sess.ExpiresAt,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
	}
	http.SetCookie(w, cookie)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LoginResponse{
		Username:  sess.Username,
		Role:      sess.Role,
		CSRFToken: sess.CSRFToken,
		ExpiresAt: sess.ExpiresAt,
	})
}

// HandleLogout revokes the session and clears the session cookie.
func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(CookieSessionName); err == nil && cookie.Value != "" {
		h.sessionMgr.RevokeSession(cookie.Value)
	}

	clearCookie := &http.Cookie{
		Name:     CookieSessionName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	}
	http.SetCookie(w, clearCookie)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "logged_out",
	})
}

// HandleMe returns the currently authenticated operator's session metadata.
func (h *AuthHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := GetSessionFromContext(r.Context())
	if !ok || sess == nil {
		writeErrorResponse(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Not authenticated")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LoginResponse{
		Username:  sess.Username,
		Role:      sess.Role,
		CSRFToken: sess.CSRFToken,
		ExpiresAt: sess.ExpiresAt,
	})
}
