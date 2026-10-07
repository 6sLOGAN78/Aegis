package control

import (
	"encoding/json"
	"net/http"

	controlv1 "aegis/pkg/api/control/v1"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/go-chi/chi/v5"
)

// HandlePublishPolicy generates, signs, persists, and broadcasts a monotonic configuration snapshot envelope.
func (s *APIServer) HandlePublishPolicy(w http.ResponseWriter, r *http.Request) {
	var activeVersion int64 = 0
	if s.distServer != nil {
		if active := s.distServer.GetActiveSnapshotInternal(); active != nil {
			activeVersion = active.Version
		}
	}

	// 1. Optimistic Concurrency Control Check (OPS-02)
	if !RequireIfMatch(w, r, activeVersion) {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Policy ID is required")
		return
	}

	draft, err := s.policyRepo.GetDraft(r.Context(), id)
	if err != nil {
		writeErrorResponse(w, r, http.StatusNotFound, "NOT_FOUND", "Policy draft not found")
		return
	}

	// 2. Load routes catalog
	records, err := s.routeRepo.ListRoutes(r.Context())
	if err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to query route catalog")
		return
	}

	routeDefs := make([]*snapshotv1.RouteDefinition, 0, len(records))
	for _, rec := range records {
		routeDefs = append(routeDefs, &snapshotv1.RouteDefinition{
			RouteId:              rec.RouteID,
			ServiceId:            rec.ServiceID,
			HttpMethod:           rec.HTTPMethod,
			PathTemplate:         rec.PathTemplate,
			UpstreamUrl:          rec.UpstreamURL,
			UpstreamSpiffeId:     rec.UpstreamSPIFFEID,
			RateLimit: &snapshotv1.RateLimitPolicy{
				RequestsPerSecond: int32(rec.RateLimitRPS),
				Burst:             int32(rec.RateLimitBurst),
			},
			Timeout: &snapshotv1.TimeoutPolicy{
				RequestTimeoutMs:  int32(rec.TimeoutMS),
				UpstreamTimeoutMs: int32(rec.TimeoutMS),
			},
			RequiresWorkloadMtls: rec.RequiresWorkloadMTLS,
		})
	}

	// 3. Assemble monotonic snapshot payload (N+1)
	nextVersion := activeVersion + 1
	payload := &snapshotv1.SnapshotPayload{
		Version: nextVersion,
		Routes:  routeDefs,
		PolicyModules: []*snapshotv1.PolicyModule{
			{
				PackageName: draft.PackageName,
				ModuleName:  draft.ModuleName,
				SourceRego:  draft.SourceRego,
			},
		},
	}

	// 4. Cryptographic signing with Ed25519
	if s.signer == nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Snapshot signer is unconfigured")
		return
	}

	newEnv, err := s.signer.SignSnapshot(payload)
	if err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to sign snapshot envelope")
		return
	}

	username := "sec-ops"
	if sess, ok := GetSessionFromContext(r.Context()); ok && sess != nil {
		username = sess.Username
	}

	// 5. Persist to PostgreSQL
	if err := s.snapshotRepo.SaveSnapshot(r.Context(), newEnv, username); err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to persist snapshot: "+err.Error())
		return
	}

	// 6. Broadcast to gateway fleet
	if s.distServer != nil {
		s.distServer.BroadcastSnapshot(newEnv)
	}

	// Mark draft as active / published
	draft.Status = "published"
	_ = s.policyRepo.UpdateDraft(r.Context(), draft)

	resp := controlv1.PolicyPublishResponse{
		PublishedAt:     newEnv.CreatedAt.AsTime(),
		SigningKeyId:    newEnv.SigningKeyId,
		SnapshotVersion: newEnv.Version,
	}

	w.Header().Set("ETag", FormatETag(newEnv.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
