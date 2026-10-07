package control

import (
	"encoding/json"
	"net/http"
	"time"

	controlv1 "aegis/pkg/api/control/v1"

	"github.com/go-chi/chi/v5"
)

type RevokeTokenRequest struct {
	JTI        string `json:"jti"`
	Reason     string `json:"reason,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

// HandleQuarantinePrincipal pushes an immediate principal block to Redis (<5s propagation).
func (s *APIServer) HandleQuarantinePrincipal(w http.ResponseWriter, r *http.Request) {
	principalID := chi.URLParam(r, "id")
	if principalID == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Principal ID is required")
		return
	}

	var req controlv1.QuarantineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid quarantine request payload")
		return
	}

	if req.Reason == "" {
		req.Reason = "ADMINISTRATIVE_QUARANTINE"
	}

	ttl := 24 * time.Hour
	if req.ExpiresAt != nil && req.ExpiresAt.After(time.Now()) {
		ttl = time.Until(*req.ExpiresAt)
	}

	actor := "sec-ops"
	if sess, ok := GetSessionFromContext(r.Context()); ok && sess != nil {
		actor = sess.Username
	}

	if s.revStore != nil {
		if err := s.revStore.QuarantinePrincipal(r.Context(), principalID, req.Reason, ttl); err != nil {
			writeErrorResponse(w, r, http.StatusServiceUnavailable, "DEPENDENCY_FAILURE", "Failed to propagate quarantine to Redis: "+err.Error())
			return
		}
	}

	now := time.Now().UTC()
	exp := now.Add(ttl)
	record := controlv1.QuarantineRecord{
		PrincipalId:   principalID,
		Reason:        req.Reason,
		Actor:         &actor,
		QuarantinedAt: now,
		Status:        controlv1.Active,
		ExpiresAt:     &exp,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}

// HandleUnquarantinePrincipal removes an active quarantine block from Redis.
func (s *APIServer) HandleUnquarantinePrincipal(w http.ResponseWriter, r *http.Request) {
	principalID := chi.URLParam(r, "id")
	if principalID == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Principal ID is required")
		return
	}

	if s.revStore != nil {
		if err := s.revStore.RemoveQuarantine(r.Context(), principalID); err != nil {
			writeErrorResponse(w, r, http.StatusServiceUnavailable, "DEPENDENCY_FAILURE", "Failed to remove quarantine from Redis: "+err.Error())
			return
		}
	}

	resp := controlv1.UnquarantineResponse{
		PrincipalId:     principalID,
		UnquarantinedAt: time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleRevokeToken writes a token JTI to the Redis revocation blocklist.
func (s *APIServer) HandleRevokeToken(w http.ResponseWriter, r *http.Request) {
	var req RevokeTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid revocation request payload")
		return
	}

	if req.JTI == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "JTI is required")
		return
	}

	ttl := 24 * time.Hour
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}

	if s.revStore != nil {
		if err := s.revStore.RevokeJTI(r.Context(), req.JTI, ttl); err != nil {
			writeErrorResponse(w, r, http.StatusServiceUnavailable, "DEPENDENCY_FAILURE", "Failed to record revocation in Redis: "+err.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "revoked",
		"jti":        req.JTI,
		"revoked_at": time.Now().UTC(),
	})
}
