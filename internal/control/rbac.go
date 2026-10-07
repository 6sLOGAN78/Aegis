package control

import (
	"net/http"
	"strings"
)

// RBACMiddleware returns an http middleware that verifies the authenticated session has at least
// one of the allowed roles, and enforces Invariant 11 rejecting data-plane credentials.
func RBACMiddleware(allowedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Invariant 11: Management plane access separately authorized.
			// Data-plane user or workload tokens presented to /control/v1 MUST return HTTP 403.
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				writeErrorResponse(w, r, http.StatusForbidden, "DATA_PLANE_CREDENTIALS_REJECTED", "Data plane credentials rejected on management endpoints")
				return
			}

			sess, ok := GetSessionFromContext(r.Context())
			if !ok || sess == nil {
				writeErrorResponse(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
				return
			}

			permitted := false
			for _, role := range allowedRoles {
				if sess.Role == role || sess.Role == "admin" {
					permitted = true
					break
				}
			}

			if !permitted {
				writeErrorResponse(w, r, http.StatusForbidden, "FORBIDDEN", "Insufficient role permissions for this operation")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole is an alias for RBACMiddleware.
func RequireRole(allowedRoles ...string) func(http.Handler) http.Handler {
	return RBACMiddleware(allowedRoles...)
}

// RequireRoles is an alias for RBACMiddleware.
func RequireRoles(allowedRoles ...string) func(http.Handler) http.Handler {
	return RBACMiddleware(allowedRoles...)
}
