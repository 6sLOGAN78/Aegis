package control

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	controlv1 "aegis/pkg/api/control/v1"

	"github.com/google/uuid"
)

type contextKey string

const (
	SessionContextKey contextKey = "aegis.session"
	CookieSessionName            = "aegis_session"
)

// Session represents an authenticated operator session.
type Session struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"` // sec-ops, auditor, viewer
	CSRFToken string    `json:"csrf_token"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionManager manages thread-safe operator sessions in memory.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager constructs a SessionManager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// CreateSession generates 256-bit cryptographically secure random session IDs and CSRF tokens.
func (sm *SessionManager) CreateSession(username, role string) (*Session, error) {
	sBytes := make([]byte, 32)
	cBytes := make([]byte, 32)
	if _, err := rand.Read(sBytes); err != nil {
		return nil, err
	}
	if _, err := rand.Read(cBytes); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	sess := &Session{
		ID:        hex.EncodeToString(sBytes),
		Username:  username,
		Role:      role,
		CSRFToken: hex.EncodeToString(cBytes),
		CreatedAt: now,
		ExpiresAt: now.Add(8 * time.Hour), // 8h absolute maximum lifetime
	}

	sm.mu.Lock()
	sm.sessions[sess.ID] = sess
	sm.mu.Unlock()

	return sess, nil
}

// GetSession retrieves an active session by ID, returning false if missing or expired.
func (sm *SessionManager) GetSession(id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sess, ok := sm.sessions[id]
	if !ok || time.Now().UTC().After(sess.ExpiresAt) {
		return nil, false
	}
	return sess, true
}

// RevokeSession invalidates an operator session.
func (sm *SessionManager) RevokeSession(id string) {
	sm.mu.Lock()
	delete(sm.sessions, id)
	sm.mu.Unlock()
}

// GetSessionFromContext retrieves the authenticated Session from request context.
func GetSessionFromContext(ctx context.Context) (*Session, bool) {
	sess, ok := ctx.Value(SessionContextKey).(*Session)
	return sess, ok && sess != nil
}

// SessionMiddleware enforces session cookie authentication, Invariant 11 data-plane token rejection,
// and double-submit CSRF token validation on mutating methods.
func (sm *SessionManager) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Invariant 11: Data-plane credentials presented to /control/v1 must be rejected immediately
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			writeErrorResponse(w, r, http.StatusForbidden, "DATA_PLANE_CREDENTIALS_REJECTED", "Data plane credentials rejected on management endpoints")
			return
		}

		cookie, err := r.Cookie(CookieSessionName)
		if err != nil || cookie.Value == "" {
			writeErrorResponse(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid session cookie")
			return
		}

		sess, ok := sm.GetSession(cookie.Value)
		if !ok {
			writeErrorResponse(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Session expired or revoked")
			return
		}

		// CSRF Validation on Mutating Methods (OPS-04)
		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			csrfHeader := r.Header.Get("X-CSRF-Token")
			if csrfHeader == "" || subtle.ConstantTimeCompare([]byte(csrfHeader), []byte(sess.CSRFToken)) != 1 {
				writeErrorResponse(w, r, http.StatusForbidden, "CSRF_TOKEN_INVALID", "CSRF token validation failed")
				return
			}
		}

		ctx := context.WithValue(r.Context(), SessionContextKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeErrorResponse writes a JSON RFC 7807-style error response matching controlv1.ErrorResponse.
func writeErrorResponse(w http.ResponseWriter, r *http.Request, statusCode int, code, message string) {
	reqID := uuid.New()
	if r != nil {
		if idStr := r.Header.Get("X-Request-ID"); idStr != "" {
			if parsed, err := uuid.Parse(idStr); err == nil {
				reqID = parsed
			}
		}
	}

	errResp := controlv1.ErrorResponse{
		Code:      code,
		Message:   message,
		RequestId: reqID,
		Timestamp: time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(errResp)
}
