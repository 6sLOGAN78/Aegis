package control

import (
	"encoding/json"
	"net/http"
	"strconv"

	controlv1 "aegis/pkg/api/control/v1"
)

// HandleRollbackPolicy republishes historical snapshot configuration under monotonic version N+1.
func (s *APIServer) HandleRollbackPolicy(w http.ResponseWriter, r *http.Request) {
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

	var req controlv1.PolicyRollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid rollback request payload")
		return
	}

	targetVersion, err := strconv.ParseInt(req.TargetVersionId, 10, 64)
	if err != nil || targetVersion <= 0 {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid target_version_id: must be a positive integer")
		return
	}

	username := "sec-ops"
	if sess, ok := GetSessionFromContext(r.Context()); ok && sess != nil {
		username = sess.Username
	}

	if s.rollbackEng == nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Rollback engine is unconfigured")
		return
	}

	// 2. Execute Monotonic Rollback (CTRL-06, Invariant 9)
	newEnv, err := s.rollbackEng.RollbackToVersion(r.Context(), targetVersion, username)
	if err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "ROLLBACK_FAILED", err.Error())
		return
	}

	// 3. Broadcast to fleet over gRPC stream
	if s.distServer != nil {
		s.distServer.BroadcastSnapshot(newEnv)
	}

	resp := controlv1.PolicyPublishResponse{
		PublishedAt:     newEnv.CreatedAt.AsTime(),
		SigningKeyId:    newEnv.SigningKeyId,
		SnapshotVersion: newEnv.Version,
	}

	w.Header().Set("ETag", FormatETag(newEnv.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
