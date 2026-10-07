package control

import (
	"encoding/json"
	"net/http"
	"time"

	"aegis/internal/storage"
	controlv1 "aegis/pkg/api/control/v1"
	snapshotv1 "aegis/pkg/api/snapshot/v1"
)

// HandleListRoutes returns all configured ingress routes.
func (s *APIServer) HandleListRoutes(w http.ResponseWriter, r *http.Request) {
	records, err := s.routeRepo.ListRoutes(r.Context())
	if err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to query route catalog")
		return
	}

	routes := make([]controlv1.Route, 0, len(records))
	for _, rec := range records {
		ca := rec.CreatedAt
		ua := rec.UpdatedAt
		spiffe := rec.UpstreamSPIFFEID
		rps := rec.RateLimitRPS
		burst := rec.RateLimitBurst
		timeout := rec.TimeoutMS
		mtls := rec.RequiresWorkloadMTLS

		routes = append(routes, controlv1.Route{
			RouteId:              rec.RouteID,
			ServiceId:            rec.ServiceID,
			HttpMethod:           controlv1.RouteHttpMethod(rec.HTTPMethod),
			PathTemplate:         rec.PathTemplate,
			UpstreamUrl:          rec.UpstreamURL,
			UpstreamSpiffeId:     &spiffe,
			RateLimitRps:         &rps,
			RateLimitBurst:       &burst,
			TimeoutMs:            &timeout,
			RequiresWorkloadMtls: &mtls,
			CreatedAt:            &ca,
			UpdatedAt:            &ua,
		})
	}

	resp := controlv1.RouteListResponse{
		Routes: routes,
	}

	raw, _ := json.Marshal(resp)
	etag := FormatResourceETag(raw)
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleCreateRoute registers or updates a route definition.
func (s *APIServer) HandleCreateRoute(w http.ResponseWriter, r *http.Request) {
	var req controlv1.RouteCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON route payload")
		return
	}

	rps := 100
	if req.RateLimitRps != nil && *req.RateLimitRps > 0 {
		rps = *req.RateLimitRps
	}
	burst := 200
	if req.RateLimitBurst != nil && *req.RateLimitBurst > 0 {
		burst = *req.RateLimitBurst
	}
	timeout := 5000
	if req.TimeoutMs != nil && *req.TimeoutMs > 0 {
		timeout = *req.TimeoutMs
	}
	spiffe := ""
	if req.UpstreamSpiffeId != nil {
		spiffe = *req.UpstreamSpiffeId
	}
	mtls := false
	if req.RequiresWorkloadMtls != nil {
		mtls = *req.RequiresWorkloadMtls
	}

	// 1. Syntactic and security validation
	routeDef := &snapshotv1.RouteDefinition{
		RouteId:          req.RouteId,
		ServiceId:        req.ServiceId,
		HttpMethod:       string(req.HttpMethod),
		PathTemplate:     req.PathTemplate,
		UpstreamUrl:      req.UpstreamUrl,
		UpstreamSpiffeId: spiffe,
		RateLimit: &snapshotv1.RateLimitPolicy{
			RequestsPerSecond: int32(rps),
			Burst:             int32(burst),
		},
		Timeout: &snapshotv1.TimeoutPolicy{
			RequestTimeoutMs:  int32(timeout),
			UpstreamTimeoutMs: int32(timeout),
		},
		RequiresWorkloadMtls: mtls,
	}

	if err := s.validator.ValidateRoute(routeDef); err != nil {
		writeErrorResponse(w, r, http.StatusBadRequest, "INVALID_ROUTE", err.Error())
		return
	}

	// Ensure service exists before inserting route with foreign key
	_ = s.routeRepo.UpsertService(r.Context(), storage.ServiceRecord{
		ID:          req.ServiceId,
		Environment: "production",
		Enabled:     true,
	})

	// 2. Persist to database
	now := time.Now().UTC()
	record := storage.RouteRecord{
		RouteID:              req.RouteId,
		ServiceID:            req.ServiceId,
		HTTPMethod:           string(req.HttpMethod),
		PathTemplate:         req.PathTemplate,
		UpstreamURL:          req.UpstreamUrl,
		UpstreamSPIFFEID:     spiffe,
		RateLimitRPS:         rps,
		RateLimitBurst:       burst,
		TimeoutMS:            timeout,
		RequiresWorkloadMTLS: mtls,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	if err := s.routeRepo.UpsertRoute(r.Context(), record); err != nil {
		writeErrorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to persist route")
		return
	}

	createdRoute := controlv1.Route{
		RouteId:              req.RouteId,
		ServiceId:            req.ServiceId,
		HttpMethod:           controlv1.RouteHttpMethod(req.HttpMethod),
		PathTemplate:         req.PathTemplate,
		UpstreamUrl:          req.UpstreamUrl,
		UpstreamSpiffeId:     &spiffe,
		RateLimitRps:         &rps,
		RateLimitBurst:       &burst,
		TimeoutMs:            &timeout,
		RequiresWorkloadMtls: &mtls,
		CreatedAt:            &now,
		UpdatedAt:            &now,
	}

	raw, _ := json.Marshal(createdRoute)
	w.Header().Set("ETag", FormatResourceETag(raw))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createdRoute)
}
