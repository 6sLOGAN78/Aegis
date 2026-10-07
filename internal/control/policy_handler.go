package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aegis/internal/storage"
	controlv1 "aegis/pkg/api/control/v1"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"
)

// HandleCreatePolicyDraft saves a candidate Rego policy draft.
func (s *APIServer) HandleCreatePolicyDraft(w http.ResponseWriter, r *http.Request) {
	var req controlv1.PolicyCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid policy payload")
		return
	}

	username := "admin"
	if sess, ok := GetSessionFromContext(r.Context()); ok && sess != nil {
		username = sess.Username
	}

	now := time.Now().UTC()
	draftRecord := &storage.PolicyDraftRecord{
		DraftID:     req.PolicyId,
		PackageName: "aegis.authz",
		ModuleName:  req.Name,
		SourceRego:  req.SourceRego,
		Status:      "draft",
		CreatedBy:   username,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.policyRepo.CreateDraft(r.Context(), draftRecord); err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to persist policy draft")
		return
	}

	resp := controlv1.PolicyDraft{
		PolicyId:   req.PolicyId,
		Name:       req.Name,
		SourceRego: req.SourceRego,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleGetPolicyVersions returns historical versions for a policy.
func (s *APIServer) HandleGetPolicyVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Policy ID is required")
		return
	}

	versions := make([]controlv1.PolicyVersion, 0)
	if s.snapshotRepo != nil {
		envelopes, err := s.snapshotRepo.ListSnapshots(r.Context())
		if err == nil {
			for _, env := range envelopes {
				var payload snapshotv1.SnapshotPayload
				if err := proto.Unmarshal(env.Payload, &payload); err == nil {
					sourceRego := ""
					for _, mod := range payload.PolicyModules {
						sourceRego = mod.SourceRego
						break
					}

					versions = append(versions, controlv1.PolicyVersion{
						PolicyId:        id,
						VersionId:       fmt.Sprintf("%d", env.Version),
						SnapshotVersion: env.Version,
						PublishedAt:     env.CreatedAt.AsTime(),
						PublishedBy:     "control-plane",
						SourceRego:      sourceRego,
					})
				}
			}
		}
	}

	resp := controlv1.PolicyVersionListResponse{
		Versions: versions,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleValidatePolicy validates candidate Rego syntax and evaluates embedded unit tests.
func (s *APIServer) HandleValidatePolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req controlv1.PolicyValidationRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	regoSource := ""
	if req.CandidateRego != nil && strings.TrimSpace(*req.CandidateRego) != "" {
		regoSource = *req.CandidateRego
	} else if id != "" && s.policyRepo != nil {
		draft, err := s.policyRepo.GetDraft(r.Context(), id)
		if err == nil && draft != nil {
			regoSource = draft.SourceRego
		}
	}

	if strings.TrimSpace(regoSource) == "" {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Candidate policy source is required for validation")
		return
	}

	// 1. Syntactic validation
	if err := s.validator.ValidatePolicyDraft(regoSource); err != nil {
		errs := []string{err.Error()}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlv1.PolicyValidationResponse{
			Valid:       false,
			FailedCount: 1,
			PassedCount: 0,
			TestCount:   1,
			Errors:      &errs,
		})
		return
	}

	// 2. Embedded unit test execution if test_ rules exist
	if strings.Contains(regoSource, "test_") {
		summary, testErr := s.validator.RunRegoTests(r.Context(), regoSource, regoSource)
		if summary == nil {
			errs := []string{testErr.Error()}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlv1.PolicyValidationResponse{
				Valid:       false,
				TestCount:   1,
				FailedCount: 1,
				Errors:      &errs,
			})
			return
		}
		valid := testErr == nil && summary.Failed == 0
		var errs *[]string
		if len(summary.Failures) > 0 {
			errs = &summary.Failures
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlv1.PolicyValidationResponse{
			Valid:       valid,
			TestCount:   summary.Total,
			PassedCount: summary.Passed,
			FailedCount: summary.Failed,
			Errors:      errs,
		})
		return
	}

	// Syntactically valid with no unit tests
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(controlv1.PolicyValidationResponse{
		Valid:       true,
		TestCount:   0,
		PassedCount: 0,
		FailedCount: 0,
	})
}
