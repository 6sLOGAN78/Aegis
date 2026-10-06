package failure

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"aegis/internal/audit"
	"aegis/internal/identity"
	"aegis/internal/policy"
	"aegis/internal/proxy"
	"aegis/internal/ratelimit"
	"aegis/internal/revocation"
	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisOutage(t *testing.T) {
	// 1. Mock Upstream Backend
	var backendCallCount atomic.Int64
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backendServer.Close()

	// 2. In-memory Redis via miniredis
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() {
		_ = rdb.Close()
	}()

	revStore := revocation.NewStore(rdb)
	rateLimiter := ratelimit.NewRateLimiter(rdb)

	// 3. Identity and JWT Validation Setup
	issuerPub, issuerPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	issuer := "aegis-issuer"
	audience := "aegis-gateway"
	validator := identity.NewTokenValidator(issuer, audience, issuerPub)

	assertionPriv := ed25519.NewKeyFromSeed([]byte("assertion-seed-for-test-32bytes!"))
	assertionMinter := identity.NewAssertionMinter(assertionPriv)

	mintToken := func(sub, jti string) string {
		claims := identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  jwt.ClaimStrings{audience},
				Subject:   sub,
				ID:        jti,
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

	// 4. Control Plane and Snapshot Setup
	cpPub, cpPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer := snapshot.NewSigner(cpPriv, "cp-key-1")
	verifier := snapshot.NewVerifier(cpPub)
	manager := snapshot.NewManager(verifier)

	payload := &snapshotv1.SnapshotPayload{
		Version: 1,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:      "orders.list",
				ServiceId:    "orders",
				HttpMethod:   "GET",
				PathTemplate: "/api/orders",
				UpstreamUrl:  backendServer.URL,
				RateLimit: &snapshotv1.RateLimitPolicy{
					RequestsPerSecond: 2,
					Burst:             5,
				},
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
	env, err := signer.SignSnapshot(payload)
	require.NoError(t, err)

	err = manager.ValidateAndActivate(context.Background(), env)
	require.NoError(t, err)

	// 5. Gateway Ingress Pipeline matching cmd/gateway/main.go
	auditLogger := audit.NewLogger(nil)
	gatewayHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = "test-" + uuid.NewString()[:8]
			r.Header.Set("X-Request-ID", reqID)
			w.Header().Set("X-Request-ID", reqID)
		}
		ac := audit.FromContext(r.Context())

		// Step 1: Lease freshness
		if manager.IsLeaseExpired(60 * time.Second) {
			if ac != nil {
				ac.SetDecision("deny", "POLICY_LEASE_EXPIRED")
				ac.SetErrorCode("POLICY_LEASE_EXPIRED")
			}
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Policy freshness lease expired (>60s)", "https://aegis.local/errors/policy-lease-expired", reqID)
			return
		}

		// Step 2: Active snapshot
		state := manager.Active()
		if state == nil {
			if ac != nil {
				ac.SetDecision("deny", "UNINITIALIZED")
				ac.SetErrorCode("UNINITIALIZED")
			}
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Gateway configuration uninitialized", "https://aegis.local/errors/uninitialized", reqID)
			return
		}

		if ac != nil {
			ac.SetSnapshotVersion(state.Version)
		}

		// Path validation
		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			if ac != nil {
				ac.SetDecision("deny", "BAD_REQUEST_INVALID_PATH")
				ac.SetErrorCode("INVALID_PATH")
			}
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		// Token authentication
		authHeader := r.Header.Get("Authorization")
		claims, err := validator.ValidateBearerToken(authHeader)
		if err != nil {
			if ac != nil {
				ac.SetCanonicalPath(canonicalPath)
				ac.SetDecision("deny", "UNAUTHORIZED")
				ac.SetErrorCode("UNAUTHORIZED")
			}
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
				"https://aegis.local/errors/unauthorized", reqID)
			return
		}

		if ac != nil {
			ac.SetPrincipal(claims.Subject, "user", claims.Roles)
			ac.SetCanonicalPath(canonicalPath)
		}

		// Ephemeral Redis Revocation Check (REV-03, REV-04, Invariant 1)
		if revStore != nil {
			revoked, reason, err := revStore.CheckRevocation(r.Context(), claims.Subject, claims.ID)
			if err != nil {
				if ac != nil {
					ac.SetDecision("deny", "DEPENDENCY_OUTAGE_REDIS")
					ac.SetErrorCode("DEPENDENCY_OUTAGE_REDIS")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Authorization dependency check unavailable; failing closed.",
					"https://aegis.local/errors/dependency-unavailable",
					reqID,
				)
				return
			}
			if revoked {
				if ac != nil {
					ac.SetDecision("deny", reason)
					ac.SetErrorCode(reason)
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusForbidden,
					"Forbidden",
					reason,
					"https://aegis.local/errors/forbidden",
					reqID,
				)
				return
			}
		}

		// Route matching
		route, err := state.Router.Match(r.Method, canonicalPath)
		if err != nil {
			if ac != nil {
				ac.SetDecision("deny", "ROUTE_NOT_FOUND")
				ac.SetErrorCode("NOT_FOUND")
			}
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching upstream route found",
				"https://aegis.local/errors/not-found", reqID)
			return
		}

		if ac != nil {
			ac.SetRoute(route.RouteID, route.ServiceID)
		}

		// Distributed GCRA Rate Limiting (REV-01, ADR-0005)
		if rateLimiter != nil {
			var rps, burst int
			if route.RateLimit != nil {
				rps = int(route.RateLimit.RequestsPerSecond)
				burst = int(route.RateLimit.Burst)
			}
			res, err := rateLimiter.Allow(r.Context(), claims.Subject, route.RouteID, rps, burst)
			if err != nil {
				if ac != nil {
					ac.SetDecision("deny", "DEPENDENCY_OUTAGE_REDIS")
					ac.SetErrorCode("DEPENDENCY_OUTAGE_REDIS")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Authorization dependency check unavailable; failing closed.",
					"https://aegis.local/errors/dependency-unavailable",
					reqID,
				)
				return
			}
			if !res.Allowed {
				retrySec := int(math.Ceil(res.RetryAfter.Seconds()))
				if retrySec <= 0 {
					retrySec = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySec))
				if ac != nil {
					ac.SetDecision("deny", "RATE_LIMIT_EXCEEDED")
					ac.SetErrorCode("RATE_LIMIT_EXCEEDED")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusTooManyRequests,
					"Too Many Requests",
					"Rate limit exceeded",
					"https://aegis.local/errors/rate-limit-exceeded",
					reqID,
				)
				return
			}
		}

		// Policy Evaluation
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
			reason := "DENIED_DEFAULT"
			if decision.ReasonCode != "" {
				reason = decision.ReasonCode
			}
			if ac != nil {
				ac.SetDecision("deny", reason)
				ac.SetErrorCode("FORBIDDEN")
			}
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", reason,
				"https://aegis.local/errors/forbidden", reqID)
			return
		}

		if ac != nil {
			ac.SetDecision("allow", "ALLOWED")
		}

		assertionToken, err := assertionMinter.MintAssertion(
			claims.Subject, "user", claims.Roles, route.ServiceID, r.Method, canonicalPath, reqID, state.Version,
		)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusInternalServerError, "Internal Server Error",
				"Failed to mint assertion", "https://aegis.local/errors/internal-error", reqID)
			return
		}

		rp := proxy.NewReverseProxyWithMTLS(route.ParsedURL(), canonicalPath, reqID, assertionToken, nil)
		rp.ServeHTTP(w, r)
	})

	auditedHandler := audit.AuditMiddleware(auditLogger, 0)(gatewayHandler)
	gwServer := httptest.NewServer(auditedHandler)
	defer gwServer.Close()
	client := gwServer.Client()

	// Step 1: Normal traffic succeeds (HTTP 200)
	tokenAlice := mintToken("user-alice", "jti-alice-1")
	req, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	req.Header.Set("Authorization", "Bearer "+tokenAlice)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), backendCallCount.Load(), "backend must receive normal traffic")

	// Verify unmatched route returns 404
	req404, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/unknown-route", nil)
	req404.Header.Set("Authorization", "Bearer "+tokenAlice)
	resp404, err := client.Do(req404)
	require.NoError(t, err)
	defer resp404.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp404.StatusCode)

	// Step 2: Quarantines principal; verifies immediate HTTP 403 response
	err = revStore.QuarantinePrincipal(context.Background(), "user-alice", "SECURITY_INCIDENT_001", time.Hour)
	require.NoError(t, err)

	reqQuar, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	reqQuar.Header.Set("Authorization", "Bearer "+tokenAlice)
	respQuar, err := client.Do(reqQuar)
	require.NoError(t, err)
	defer respQuar.Body.Close()

	assert.Equal(t, http.StatusForbidden, respQuar.StatusCode)
	var probQuar proxy.RFC7807Problem
	err = json.NewDecoder(respQuar.Body).Decode(&probQuar)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/forbidden", probQuar.Type)
	assert.Equal(t, "PRINCIPAL_QUARANTINED", probQuar.Detail)
	assert.Equal(t, int64(1), backendCallCount.Load(), "backend must NOT receive requests from quarantined principal")

	// Lift quarantine and assert access restored
	err = revStore.RemoveQuarantine(context.Background(), "user-alice")
	require.NoError(t, err)

	reqUnquar, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	reqUnquar.Header.Set("Authorization", "Bearer "+tokenAlice)
	respUnquar, err := client.Do(reqUnquar)
	require.NoError(t, err)
	defer respUnquar.Body.Close()
	assert.Equal(t, http.StatusOK, respUnquar.StatusCode)
	assert.Equal(t, int64(2), backendCallCount.Load())

	// Step 3: Revokes token JTI; verifies immediate HTTP 403 response
	tokenAliceStolen := mintToken("user-alice", "jti-alice-stolen-token")
	err = revStore.RevokeJTI(context.Background(), "jti-alice-stolen-token", time.Hour)
	require.NoError(t, err)

	reqRev, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	reqRev.Header.Set("Authorization", "Bearer "+tokenAliceStolen)
	respRev, err := client.Do(reqRev)
	require.NoError(t, err)
	defer respRev.Body.Close()

	assert.Equal(t, http.StatusForbidden, respRev.StatusCode)
	var probRev proxy.RFC7807Problem
	err = json.NewDecoder(respRev.Body).Decode(&probRev)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/forbidden", probRev.Type)
	assert.Equal(t, "TOKEN_REVOKED", probRev.Detail)
	assert.Equal(t, int64(2), backendCallCount.Load(), "backend must NOT receive requests with revoked JTI")

	// Step 4: Floods rate limit; verifies HTTP 429 response with Retry-After header
	tokenBob := mintToken("user-bob", "jti-bob-1")
	// Burst capacity is 5
	for i := 0; i < 5; i++ {
		reqBurst, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
		reqBurst.Header.Set("Authorization", "Bearer "+tokenBob)
		respBurst, err := client.Do(reqBurst)
		require.NoError(t, err)
		defer respBurst.Body.Close()
		assert.Equal(t, http.StatusOK, respBurst.StatusCode, "burst request %d should succeed", i+1)
	}
	assert.Equal(t, int64(7), backendCallCount.Load())

	// 6th request exceeds burst capacity
	reqThrottled, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	reqThrottled.Header.Set("Authorization", "Bearer "+tokenBob)
	respThrottled, err := client.Do(reqThrottled)
	require.NoError(t, err)
	defer respThrottled.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, respThrottled.StatusCode)
	retryAfter := respThrottled.Header.Get("Retry-After")
	assert.NotEmpty(t, retryAfter, "Retry-After header must be set on 429 response")

	var probThrottled proxy.RFC7807Problem
	err = json.NewDecoder(respThrottled.Body).Decode(&probThrottled)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/rate-limit-exceeded", probThrottled.Type)
	assert.Equal(t, int64(7), backendCallCount.Load(), "throttled request must NOT be dispatched to backend")

	// Step 5: Closes / partitions miniredis to simulate Redis outage
	mr.Close()

	// Issues subsequent request with valid, unrevoked JWT
	tokenCarol := mintToken("user-carol", "jti-carol-fresh")
	reqOutage, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	reqOutage.Header.Set("Authorization", "Bearer "+tokenCarol)
	respOutage, err := client.Do(reqOutage)
	require.NoError(t, err)
	defer respOutage.Body.Close()

	// Must fail closed with HTTP 503 Service Unavailable (REV-04, Invariant 1, ADR-0005)
	assert.Equal(t, http.StatusServiceUnavailable, respOutage.StatusCode)

	var probOutage proxy.RFC7807Problem
	err = json.NewDecoder(respOutage.Body).Decode(&probOutage)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/dependency-unavailable", probOutage.Type)
	assert.Equal(t, "Authorization dependency check unavailable; failing closed.", probOutage.Detail)

	// ZERO requests reach upstream backends during outage!
	assert.Equal(t, int64(7), backendCallCount.Load(), "mock backend must receive ZERO requests during Redis outage")
}
