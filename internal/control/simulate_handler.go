package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	controlv1 "aegis/pkg/api/control/v1"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/go-chi/chi/v5"
	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
	"google.golang.org/protobuf/proto"
)

// HandleSimulatePolicy evaluates synthetic input against candidate or active Rego code in memory.
func (s *APIServer) HandleSimulatePolicy(w http.ResponseWriter, r *http.Request) {
	var req controlv1.PolicySimulationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON simulation request")
		return
	}

	id := chi.URLParam(r, "id")
	regoSource := ""
	if req.CandidateRego != nil && strings.TrimSpace(*req.CandidateRego) != "" {
		regoSource = *req.CandidateRego
	} else if id != "" && s.policyRepo != nil {
		draft, err := s.policyRepo.GetDraft(r.Context(), id)
		if err == nil && draft != nil {
			regoSource = draft.SourceRego
		}
	}

	if regoSource == "" && s.distServer != nil {
		active := s.distServer.GetActiveSnapshotInternal()
		if active != nil {
			var payload snapshotv1.SnapshotPayload
			if err := proto.Unmarshal(active.Payload, &payload); err == nil {
				for _, mod := range payload.PolicyModules {
					regoSource = mod.SourceRego
					break
				}
			}
		}
	}

	if regoSource == "" {
		regoSource = "package aegis.authz\n\ndefault decision := {\"allow\": false, \"reason_code\": \"DENIED_DEFAULT\"}\n"
	}

	start := time.Now()
	evalQuery, err := rego.New(
		rego.SetRegoVersion(ast.RegoV1),
		rego.Module("simulate.rego", regoSource),
		rego.Query("data.aegis.authz.decision"),
	).PrepareForEval(r.Context())

	if err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "COMPILATION_ERROR", err.Error())
		return
	}

	rs, err := evalQuery.Eval(r.Context(), rego.EvalInput(req.InputContext))
	durationUs := int(time.Since(start).Microseconds())
	if durationUs <= 0 {
		durationUs = 1
	}

	if err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "EVALUATION_ERROR", err.Error())
		return
	}

	allow := false
	reasonCode := "DENIED_DEFAULT"
	diagnostics := map[string]interface{}{
		"query": "data.aegis.authz.decision",
	}

	if len(rs) > 0 && len(rs[0].Expressions) > 0 {
		if val, ok := rs[0].Expressions[0].Value.(map[string]interface{}); ok {
			if a, ok := val["allow"].(bool); ok {
				allow = a
			}
			if r, ok := val["reason_code"].(string); ok {
				reasonCode = r
			}
		}
	}

	resp := controlv1.PolicySimulationResponse{
		Allow:       allow,
		ReasonCode:  reasonCode,
		DurationUs:  durationUs,
		Diagnostics: &diagnostics,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
