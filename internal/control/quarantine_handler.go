package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aegis/internal/identity"
	controlv1 "aegis/pkg/api/control/v1"

	"github.com/go-chi/chi/v5"
)

type RevokeTokenRequest struct {
	JTI        string `json:"jti"`
	Reason     string `json:"reason,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

const (
	quarantineTrustDomain = "aegis.local"
	maxPrincipalIDLen     = 128
)

// canonicalPrincipalID decodes (once) and validates an operator-supplied
// principal id so the Redis key matches what the gateway checks. SPIFFE ids
// are strictly parsed and canonicalized; user ids pass through unchanged.
func canonicalPrincipalID(raw string) (string, error) {
	id, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("invalid principal id encoding")
	}
	if id == "" || len(id) > maxPrincipalIDLen {
		return "", fmt.Errorf("principal id must be 1-%d bytes", maxPrincipalIDLen)
	}
	for _, c := range id {
		if c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("principal id contains control characters")
		}
	}
	if strings.Contains(id, "%") {
		return "", fmt.Errorf("principal id contains invalid percent-encoding")
	}
	if strings.HasPrefix(id, "spiffe://") {
		canon, err := identity.ParseSPIFFEID(id, quarantineTrustDomain)
		if err != nil {
			return "", fmt.Errorf("invalid SPIFFE principal id: %w", err)
		}
		return canon, nil
	}
	return id, nil
}

// pathPrincipalID returns the still-encoded {id} path parameter. chi returns
// the escaped form only when the request has a RawPath; otherwise it has
// already been decoded once, so re-escape it to keep exactly one decode.
func pathPrincipalID(r *http.Request) string {
	id := chi.URLParam(r, "id")
	if id != "" && r.URL.RawPath == "" {
		return url.PathEscape(id)
	}
	return id
}

// HandleQuarantinePrincipal pushes an immediate principal block to Redis (<5s propagation).
// It serves both POST /principals/{id}/quarantine and the body form POST /quarantine.
func (s *APIServer) HandleQuarantinePrincipal(w http.ResponseWriter, r *http.Request) {
	var req controlv1.QuarantineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid quarantine request payload")
		return
	}

	pathID := pathPrincipalID(r)
	bodyID := ""
	if req.PrincipalId != nil {
		bodyID = *req.PrincipalId
	}
	if pathID == "" && bodyID == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Principal ID is required")
		return
	}

	var principalID string
	if pathID != "" {
		id, err := canonicalPrincipalID(pathID)
		if err != nil {
			writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		principalID = id
	}
	if bodyID != "" {
		id, err := canonicalPrincipalID(url.PathEscape(bodyID))
		if err != nil {
			writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		if principalID != "" && principalID != id {
			writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Path principal ID and body principal_id differ")
			return
		}
		principalID = id
	}

	s.quarantineCore(w, r, principalID, req)
}

// quarantineCore performs the Redis write and response for a validated principal id.
func (s *APIServer) quarantineCore(w http.ResponseWriter, r *http.Request, principalID string, req controlv1.QuarantineRequest) {
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
	rawID := pathPrincipalID(r)
	if rawID == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Principal ID is required")
		return
	}
	principalID, err := canonicalPrincipalID(rawID)
	if err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
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

// HandleListQuarantines returns all currently active principal quarantines from Redis.
func (s *APIServer) HandleListQuarantines(w http.ResponseWriter, r *http.Request) {
	items := make([]controlv1.QuarantineRecord, 0)
	if s.revStore != nil {
		quarantines, err := s.revStore.ListQuarantined(r.Context())
		if err != nil {
			writeErrorResponse(w, r, http.StatusServiceUnavailable, "DEPENDENCY_FAILURE", "Failed to query quarantines from Redis: "+err.Error())
			return
		}
		now := time.Now().UTC()
		actor := "sec-ops"
		for _, q := range quarantines {
			exp := now.Add(q.TTL)
			items = append(items, controlv1.QuarantineRecord{
				PrincipalId:   q.PrincipalID,
				Reason:        q.Reason,
				Status:        controlv1.Active,
				QuarantinedAt: now,
				ExpiresAt:     &exp,
				Actor:         &actor,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"quarantines": items,
	})
}
