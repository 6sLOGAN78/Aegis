package failure

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"aegis/internal/audit"
	"aegis/internal/identity"
	"aegis/internal/policy"
	"aegis/internal/proxy"
	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpoolSaturation(t *testing.T) {
	// 1. Mock Upstream Backend
	var backendCallCount atomic.Int64
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"order_processed"}`))
	}))
	defer backendServer.Close()

	// 2. Setup Spool Directory with bounded volume quota
	tmpSpoolDir := t.TempDir()
	quotaBytes := int64(30 * 1024) // 30 KiB quota; 90% threshold is 27 KiB

	spool, err := audit.NewDiskSpool(audit.DiskSpoolConfig{
		SpoolDir:         tmpSpoolDir,
		MaxSegmentBytes:  10 * 1024,
		VolumeQuotaBytes: quotaBytes,
	})
	require.NoError(t, err)
	defer spool.Close()

	// 3. Identity and JWT Validation Setup
	issuerPub, issuerPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	issuer := "aegis-issuer"
	audience := "aegis-gateway"
	validator := identity.NewTokenValidator(issuer, audience, issuerPub)

	assertionPriv := ed25519.NewKeyFromSeed([]byte("spool-sat-assertion-seed-32bytes"))
	assertionMinter := identity.NewAssertionMinter(assertionPriv)

	mintToken := func(sub string) string {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   sub,
				ID:        uuid.NewString(),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
			Roles: []string{"user"},
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		signed, signErr := tok.SignedString(issuerPriv)
		require.NoError(t, signErr)
		return signed
	}

	// 4. Control Plane Snapshot Setup
	cpPub, cpPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer := snapshot.NewSigner(cpPriv, "cp-key-1")
	verifier := snapshot.NewVerifier(cpPub)
	manager := snapshot.NewManager(verifier)

	payload := &snapshotv1.SnapshotPayload{
		Version: 1,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:      "orders.create",
				ServiceId:    "orders",
				HttpMethod:   "POST",
				PathTemplate: "/api/orders",
				UpstreamUrl:  backendServer.URL,
			},
		},
		PolicyModules: []*snapshotv1.PolicyModule{
			{
				PackageName: "aegis.authz",
				ModuleName:  "rules.rego",
				SourceRego: `package aegis.authz
default allow = true
decision := {"allow": allow, "reason_code": "ALLOWED", "snapshot_version": input.snapshot_version}
`,
			},
		},
	}

	envelope, err := signer.SignSnapshot(payload)
	require.NoError(t, err)
	err = manager.ValidateAndActivate(context.Background(), envelope)
	require.NoError(t, err)

	auditLogger := audit.NewLogger(nil)

	// 5. Build Gateway Pipeline Modeling cmd/gateway/main.go
	gatewayHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		ac := audit.FromContext(r.Context())

		// Step 1: Freshness lease check
		if manager.IsLeaseExpired(60 * time.Second) {
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Policy freshness lease expired (>60s)", "https://aegis.local/errors/policy-lease-expired", reqID)
			return
		}

		// Step 2: Active snapshot
		state := manager.Active()
		if state == nil {
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Gateway configuration uninitialized", "https://aegis.local/errors/uninitialized", reqID)
			return
		}
		if ac != nil {
			ac.SetSnapshotVersion(state.Version)
		}

		// Step 3: Zero-repair path validation
		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		// Step 4: JWT Authentication
		authHeader := r.Header.Get("Authorization")
		claims, err := validator.ValidateBearerToken(authHeader)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
				"https://aegis.local/errors/unauthorized", reqID)
			return
		}
		if ac != nil {
			ac.SetPrincipal(claims.Subject, "user", claims.Roles)
			ac.SetCanonicalPath(canonicalPath)
		}

		// Step 5: Route Match
		route, err := state.Router.Match(r.Method, canonicalPath)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching route found",
				"https://aegis.local/errors/not-found", reqID)
			return
		}
		if ac != nil {
			ac.SetRoute(route.RouteID, route.ServiceID)
		}

		// Step 6: OPA Policy Evaluation
		input := policy.PolicyInput{
			Principal: policy.PrincipalInput{
				ID:    claims.Subject,
				Kind:  "user",
				Roles: claims.Roles,
			},
			Resource: policy.ResourceInput{
				Service: route.ServiceID,
				Route:   route.RouteID,
			},
			Request: policy.RequestInput{
				Method: r.Method,
				Path:   canonicalPath,
			},
			SnapshotVersion: state.Version,
		}
		decision, err := state.PolicyEngine.Evaluate(r.Context(), input)
		if err != nil || !decision.Allow {
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", "Denied",
				"https://aegis.local/errors/forbidden", reqID)
			return
		}
		if ac != nil {
			ac.SetDecision("allow", "ALLOWED")
		}

		// Step 7: Pre-Forward Durable Disk WAL Append with fsync (Invariant 10, AUD-01, AUD-02)
		var clientIP string
		if host, _, splitErr := net.SplitHostPort(r.RemoteAddr); splitErr == nil && host != "" {
			clientIP = host
		} else {
			clientIP = r.RemoteAddr
		}

		var preForwardEvent *audit.CompletionEvent
		if ac != nil {
			preForwardEvent = ac.ToCompletionEvent(r.Method, clientIP, state.Version)
		} else {
			preForwardEvent = &audit.CompletionEvent{
				EventID:         uuid.NewString(),
				Timestamp:       time.Now().UTC(),
				RequestID:       reqID,
				PrincipalID:     claims.Subject,
				PrincipalKind:   "user",
				PrincipalRoles:  claims.Roles,
				ClientIP:        clientIP,
				HTTPMethod:      r.Method,
				CanonicalPath:   canonicalPath,
				RouteID:         route.RouteID,
				ServiceID:       route.ServiceID,
				Decision:        "allow",
				ReasonCode:      "ALLOWED",
				SnapshotVersion: state.Version,
			}
		}

		if appendErr := spool.AppendPreForward(preForwardEvent); appendErr != nil {
			if errors.Is(appendErr, audit.ErrSpoolSaturated) {
				if ac != nil {
					ac.SetDecision("deny", "AUDIT_SPOOL_SATURATED")
					ac.SetErrorCode("AUDIT_SPOOL_SATURATED")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Audit spool saturated; halting admission",
					"https://aegis.local/errors/spool-saturated",
					reqID,
				)
				return
			}
			proxy.WriteProblemDetails(
				w,
				http.StatusInternalServerError,
				"Internal Server Error",
				"Durable audit logging failed",
				"https://aegis.local/errors/internal-error",
				reqID,
			)
			return
		}

		// Step 8: Mint assertion and forward
		assertionToken, err := assertionMinter.MintAssertion(
			claims.Subject, "user", claims.Roles, route.ServiceID,
			r.Method, canonicalPath, reqID, state.Version,
		)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusInternalServerError, "Internal Server Error",
				"Failed to mint assertion", "https://aegis.local/errors/internal-error", reqID)
			return
		}

		rp := proxy.NewReverseProxyWithMTLS(route.ParsedURL(), canonicalPath, reqID, assertionToken, nil)
		rp.ServeHTTP(w, r)
	})

	auditedHandler := audit.AuditMiddleware(auditLogger, 1)(gatewayHandler)
	gwServer := httptest.NewServer(auditedHandler)
	defer gwServer.Close()

	client := gwServer.Client()

	// 6. Test Initial Permitted Request (Under Capacity)
	validToken := mintToken("user-alpha")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, gwServer.URL+"/api/orders", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+validToken)
	req.Header.Set("X-Request-ID", uuid.NewString())

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	assert.Equal(t, int64(1), backendCallCount.Load(), "Initial permitted request should reach upstream")

	// 7. Flood Spool Volume to reach >= 90% Saturation Threshold
	for i := 0; i < 200; i++ {
		fillEvent := &audit.CompletionEvent{
			EventID:         uuid.NewString(),
			Timestamp:       time.Now().UTC(),
			RequestID:       uuid.NewString(),
			PrincipalID:     "flood-user",
			PrincipalKind:   "user",
			PrincipalRoles:  []string{"filler"},
			ClientIP:        "127.0.0.1",
			HTTPMethod:      "POST",
			CanonicalPath:   "/api/orders",
			RouteID:         "orders.create",
			ServiceID:       "orders",
			Decision:        "allow",
			ReasonCode:      "ALLOWED",
			SnapshotVersion: 1,
		}
		if appendErr := spool.AppendPreForward(fillEvent); appendErr != nil {
			if errors.Is(appendErr, audit.ErrSpoolSaturated) {
				// Saturation reached!
				break
			}
			require.NoError(t, appendErr)
		}
	}

	sat, err := spool.CheckSaturation()
	require.NoError(t, err)
	require.True(t, sat, "Spool must be saturated after flood")

	// 8. Issue Subsequent Permitted Request While Saturated
	backendCallsBeforeSaturatedReq := backendCallCount.Load()

	satReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, gwServer.URL+"/api/orders", nil)
	require.NoError(t, err)
	satReq.Header.Set("Authorization", "Bearer "+validToken)
	satReq.Header.Set("X-Request-ID", uuid.NewString())

	satResp, err := client.Do(satReq)
	require.NoError(t, err)
	defer satResp.Body.Close()

	// 9. Assertions: Must return HTTP 503 and Zero Upstream Calls
	assert.Equal(t, http.StatusServiceUnavailable, satResp.StatusCode)

	var problem proxy.RFC7807Problem
	err = json.NewDecoder(satResp.Body).Decode(&problem)
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, problem.Status)
	assert.Equal(t, "https://aegis.local/errors/spool-saturated", problem.Type)
	assert.Equal(t, "Audit spool saturated; halting admission", problem.Detail)

	assert.Equal(t, backendCallsBeforeSaturatedReq, backendCallCount.Load(),
		"Upstream backend must receive ZERO requests after audit spool saturation triggers (Invariant 10)")
}
