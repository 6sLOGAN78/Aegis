package control

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"aegis/internal/revocation"
	"aegis/internal/snapshot"
	"aegis/internal/storage"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// APIServer manages administrative REST management endpoints on port :8084.
type APIServer struct {
	router       chi.Router
	sessionMgr   *SessionManager
	authHandler  *AuthHandler
	idempStore   *IdempotencyStore
	distServer   *SnapshotDistributionServer
	validator    *Validator
	rollbackEng  *RollbackEngine
	routeRepo    *storage.RouteRepo
	policyRepo   *storage.PolicyRepo
	snapshotRepo *storage.SnapshotRepo
	auditRepo    *storage.AuditRepo
	revStore     *revocation.Store
	signer       *snapshot.Signer
	secRepo      SecurityStateRepository
	secState     *SecurityState

	identityMappings []*snapshotv1.IdentityMapping

	// gatewayStaleAfter is parsed once from AEGIS_GATEWAY_STALE_AFTER at
	// construction; zero means "use defaultGatewayStaleAfter".
	gatewayStaleAfter time.Duration
}

// NewAPIServer constructs and configures the control plane REST API router.
func NewAPIServer(
	sessionMgr *SessionManager,
	idempStore *IdempotencyStore,
	distServer *SnapshotDistributionServer,
	validator *Validator,
	rollbackEng *RollbackEngine,
	routeRepo *storage.RouteRepo,
	policyRepo *storage.PolicyRepo,
	snapshotRepo *storage.SnapshotRepo,
	auditRepo *storage.AuditRepo,
	revStore *revocation.Store,
	signer *snapshot.Signer,
) *APIServer {
	r := chi.NewRouter()

	// 1. Standard Chi Middlewares
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// 2. Strict Security Headers & CSP (OPS-04)
	r.Use(SecurityHeadersMiddleware)

	cookieSecure := cookieSecureFromEnv(os.Getenv)
	if !cookieSecure {
		log.Printf("WARNING: AEGIS_COOKIE_SECURE disabled; session cookies are sent without the Secure flag (demo/plain-HTTP only)")
	}

	server := &APIServer{
		router:       r,
		sessionMgr:   sessionMgr,
		authHandler:  NewAuthHandler(sessionMgr).WithSecureCookie(cookieSecure),
		idempStore:   idempStore,
		distServer:   distServer,
		validator:    validator,
		rollbackEng:  rollbackEng,
		routeRepo:    routeRepo,
		policyRepo:   policyRepo,
		snapshotRepo: snapshotRepo,
		auditRepo:    auditRepo,
		revStore:     revStore,
		signer:       signer,

		gatewayStaleAfter: gatewayStaleAfterFromEnv(os.Getenv),
	}

	server.routes()
	return server
}

// SetSecurityRepo enables Postgres-first write-through for quarantine and revocation handlers.
func (s *APIServer) SetSecurityRepo(repo SecurityStateRepository) { s.secRepo = repo }

// SetSecurityState wires the SecurityState service used by /readyz.
func (s *APIServer) SetSecurityState(ss *SecurityState) { s.secState = ss }

func (s *APIServer) handleLivez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK\n"))
}

// handleReadyz reports 200 only when Postgres is reachable and the Redis security-state marker exists.
// The body is a closed enum; error strings are never echoed.
func (s *APIServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.secState == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY\n"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.secState.Ready(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		if errors.Is(err, revocation.ErrSecurityStateMissing) {
			_, _ = w.Write([]byte("SECURITY_STATE_MISSING\n"))
		} else {
			_, _ = w.Write([]byte("DEPENDENCY_FAILURE\n"))
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY\n"))
}

// SecurityHeadersMiddleware injects defense-in-depth security headers and strict CSP.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csp := "default-src 'self'; script-src 'self' 'unsafe-eval' blob:; " +
			"style-src 'self' 'unsafe-inline'; worker-src 'self' blob:; " +
			"img-src 'self' data:; connect-src 'self'; font-src 'self' data:; " +
			"frame-ancestors 'none'; object-src 'none'; base-uri 'self';"
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// routes registers all management endpoints under /control/v1 and dashboard SPA routes.
func (s *APIServer) routes() {
	// Unauthenticated probes, registered before auth and the SPA catch-all (D-26).
	s.router.Get("/livez", s.handleLivez)
	s.router.Get("/readyz", s.handleReadyz)

	RegisterSPARoutes(s.router)

	s.router.Route("/control/v1", func(r chi.Router) {
		// Public Authentication endpoint
		r.Post("/auth/login", s.authHandler.HandleLogin)

		// Protected Subrouter requiring active session cookie, CSRF check, and Invariant 11 check
		r.Group(func(pr chi.Router) {
			pr.Use(s.sessionMgr.SessionMiddleware)
			pr.Use(s.idempStore.IdempotencyMiddleware)

			// Operator session lifecycle
			pr.Post("/auth/logout", s.authHandler.HandleLogout)
			pr.Get("/auth/me", s.authHandler.HandleMe)

			// Route catalog management
			pr.With(RequireRole("sec-ops", "auditor", "viewer")).Get("/routes", s.HandleListRoutes)
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Post("/routes", s.HandleCreateRoute)

			// Policy drafts & version inspection
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Post("/policies", s.HandleCreatePolicyDraft)
			pr.With(RequireRole("sec-ops", "auditor", "viewer")).Get("/policies/{id}/versions", s.HandleGetPolicyVersions)
			pr.With(RequireRole("sec-ops", "auditor", "viewer")).Post("/policies/{id}/validate", s.HandleValidatePolicy)

			// Policy simulation (candidate dry-run)
			pr.With(RequireRole("sec-ops", "auditor", "viewer")).Post("/policies/simulate", s.HandleSimulatePolicy)
			pr.With(RequireRole("sec-ops", "auditor", "viewer")).Post("/policies/{id}/simulate", s.HandleSimulatePolicy)

			// Monotonic publishing and rollback (sec-ops only)
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Post("/policies/{id}/publish", s.HandlePublishPolicy)
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Post("/policies/{id}/rollback", s.HandleRollbackPolicy)
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Post("/snapshots/rollback", s.HandleRollbackPolicy)

			// Gateway convergence topology
			pr.With(RequireRole("sec-ops", "auditor", "viewer")).Get("/gateways", s.HandleListGateways)

			// Emergency incident response (quarantine & token revocation)
			pr.With(RequireRole("sec-ops", "auditor", "viewer")).Get("/principals/quarantine", s.HandleListQuarantines)
			pr.With(RequireRole("sec-ops", "auditor", "viewer")).Get("/quarantine", s.HandleListQuarantines)
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Post("/principals/{id}/quarantine", s.HandleQuarantinePrincipal)
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Delete("/principals/{id}/quarantine", s.HandleUnquarantinePrincipal)
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Post("/quarantine", s.HandleQuarantinePrincipal)
			pr.With(RequireRole("sec-ops"), RequireIdempotencyKey).Post("/revocations", s.HandleRevokeToken)

			// Durable audit inspection (sec-ops and auditor)
			pr.With(RequireRole("sec-ops", "auditor")).Get("/audit-events", s.HandleListAuditEvents)
			pr.With(RequireRole("sec-ops", "auditor")).Get("/audit/events", s.HandleListAuditEvents)
			pr.With(RequireRole("sec-ops", "auditor")).Get("/audit-events/{id}", s.HandleGetAuditEvent)
		})
	})
}

// Handler returns the root HTTP handler for serving or testing.
func (s *APIServer) Handler() http.Handler {
	return s.router
}
