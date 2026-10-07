package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"aegis/internal/storage"
	controlv1 "aegis/pkg/api/control/v1"

	"github.com/go-chi/chi/v5"
)

// HandleListAuditEvents queries partitioned audit logs with indexed filters.
func (s *APIServer) HandleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var principalID *string
	if val := q.Get("principal_id"); val != "" {
		principalID = &val
	}

	var serviceID *string
	if val := q.Get("service_id"); val != "" {
		serviceID = &val
	}

	var decision *string
	if val := q.Get("decision"); val != "" {
		decision = &val
	}

	var fromTS *time.Time
	if val := q.Get("from_timestamp"); val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			fromTS = &t
		}
	}

	var toTS *time.Time
	if val := q.Get("to_timestamp"); val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			toTS = &t
		}
	}

	var cursor *string
	if val := q.Get("cursor"); val != "" {
		cursor = &val
	}

	var limit *int
	if val := q.Get("limit"); val != "" {
		if l, err := strconv.Atoi(val); err == nil && l > 0 {
			limit = &l
		}
	}

	params := storage.ListAuditEventsParams{
		PrincipalID:   principalID,
		ServiceID:     serviceID,
		Decision:      decision,
		FromTimestamp: fromTS,
		ToTimestamp:   toTS,
		Cursor:        cursor,
		Limit:         limit,
	}

	result, err := s.auditRepo.ListAuditEvents(r.Context(), params)
	if err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to query audit logs: "+err.Error())
		return
	}

	resp := controlv1.AuditEventListResponse{
		Events:     result.Events,
		NextCursor: result.NextCursor,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleGetAuditEvent retrieves a single audit event record by event ID.
func (s *APIServer) HandleGetAuditEvent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Event ID is required")
		return
	}

	event, err := s.auditRepo.GetAuditEvent(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeErrorResponse(w, r, http.StatusNotFound, "NOT_FOUND", "Audit event not found")
			return
		}
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve audit event: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(event)
}
