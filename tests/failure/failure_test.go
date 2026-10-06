package failure

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
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

// TestFailure_RedisOutageFailClosed verifies REV-04 and Invariant 1:
// When Redis is unavailable or severed, 100% of protected requests fail closed with HTTP 503,
// zero requests reach upstream backends, and normal authorization resumes upon recovery.
func TestFailure_RedisOutageFailClosed(t *testing.T) {
	// 1. Mock Upstream Backend
	var backendCallCount atomic.Int64
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer backendServer.Close()

	// 2. Start In-Memory Redis via miniredis
	mr := miniredis.RunT(t)
	redisAddr := mr.Addr()
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
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

	assertionPriv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
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
					RequestsPerSecond: 100,
					Burst:             200,
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
	require.NoError(t, manager.ValidateAndActivate(context.Background(), env))

	// 5. Gateway Ingress Pipeline Handler
	gatewayHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = "test-" + uuid.NewString()[:8]
			r.Header.Set("X-Request-ID", reqID)
			w.Header().Set("X-Request-ID", reqID)
		}

		if manager.IsLeaseExpired(60 * time.Second) {
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Policy freshness lease expired (>60s)", "https://aegis.local/errors/policy-lease-expired", reqID)
			return
		}

		state := manager.Active()
		if state == nil {
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Gateway configuration uninitialized", "https://aegis.local/errors/uninitialized", reqID)
			return
		}

		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		claims, err := validator.ValidateBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
				"https://aegis.local/errors/unauthorized", reqID)
			return
		}

		// Ephemeral Redis Revocation Check (REV-03, REV-04, Invariant 1)
		revoked, reason, err := revStore.CheckRevocation(r.Context(), claims.Subject, claims.ID)
		if err != nil {
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
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", reason,
				"https://aegis.local/errors/forbidden", reqID)
			return
		}

		route, err := state.Router.Match(r.Method, canonicalPath)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching upstream route found",
				"https://aegis.local/errors/not-found", reqID)
			return
		}

		// Distributed Rate Limiting Check
		res, err := rateLimiter.Allow(r.Context(), claims.Subject, route.RouteID, 100, 200)
		if err != nil {
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
			proxy.WriteProblemDetails(w, http.StatusTooManyRequests, "Too Many Requests", "Rate limit exceeded",
				"https://aegis.local/errors/rate-limit-exceeded", reqID)
			return
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
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", "DENIED",
				"https://aegis.local/errors/forbidden", reqID)
			return
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

	gwServer := httptest.NewServer(gatewayHandler)
	defer gwServer.Close()
	client := gwServer.Client()

	// 6. Baseline: Authenticated request with valid JWT reaches mock upstream backend; returns HTTP 200
	token := mintToken("user-baseline", "jti-baseline-1")
	req, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), backendCallCount.Load(), "Upstream mock must receive baseline request")

	// 7. Fault Injection: Stop miniredis server
	mr.Close()

	// 8. Verification: Issue 10 concurrent requests with valid JWT
	const concurrentRequests = 10
	var wg sync.WaitGroup
	var failClosedCount atomic.Int64

	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqToken := mintToken("user-concurrent", fmt.Sprintf("jti-c-%d", idx))
			cReq, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
			cReq.Header.Set("Authorization", "Bearer "+reqToken)
			cResp, cErr := client.Do(cReq)
			if cErr != nil {
				return
			}
			defer cResp.Body.Close()

			if cResp.StatusCode == http.StatusServiceUnavailable {
				var prob proxy.RFC7807Problem
				if decErr := json.NewDecoder(cResp.Body).Decode(&prob); decErr == nil {
					if prob.Type == "https://aegis.local/errors/dependency-unavailable" && prob.Status == 503 {
						failClosedCount.Add(1)
					}
				}
			}
		}(i)
	}
	wg.Wait()

	// All 10 requests must return HTTP 503 Service Unavailable (100% fail-closed)
	assert.Equal(t, int64(concurrentRequests), failClosedCount.Load(),
		"100% of concurrent requests during Redis outage must fail closed with HTTP 503")

	// Invariant 1 check: Zero requests reached upstream backend during the outage
	assert.Equal(t, int64(1), backendCallCount.Load(),
		"Zero requests must reach upstream backend during Redis outage (no fail-open bypass)")

	// 9. Recovery: Restart miniredis on the same port
	err = mr.Restart()
	require.NoError(t, err, "miniredis must restart cleanly on same address")

	// Gateway resumes normal authorization
	tokenRecovered := mintToken("user-recovered", "jti-recovered-1")
	recReq, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	recReq.Header.Set("Authorization", "Bearer "+tokenRecovered)
	recResp, err := client.Do(recReq)
	require.NoError(t, err)
	defer recResp.Body.Close()

	assert.Equal(t, http.StatusOK, recResp.StatusCode, "Gateway must resume normal authorization after Redis recovery")
	assert.Equal(t, int64(2), backendCallCount.Load(), "Upstream mock must receive recovered request")
}

// TestFailure_SnapshotLeaseExpiryFailClosed verifies CTRL-04, Invariant 1, and Invariant 9:
// When control plane lease renewal stops, gateway authorizes during transient window (<=60s),
// but drops readiness and fails closed with HTTP 503 when lease age > 60s.
func TestFailure_SnapshotLeaseExpiryFailClosed(t *testing.T) {
	// 1. Mock Upstream Backend
	var backendCallCount atomic.Int64
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"orders_retrieved"}`))
	}))
	defer backendServer.Close()

	// 2. Cryptographic Keys and Snapshot Setup
	cpPub, cpPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer := snapshot.NewSigner(cpPriv, "cp-key-1")
	verifier := snapshot.NewVerifier(cpPub)
	manager := snapshot.NewManager(verifier)

	issuerPub, issuerPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	issuer := "aegis-issuer"
	audience := "aegis-gateway"
	validator := identity.NewTokenValidator(issuer, audience, issuerPub)

	assertionPriv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
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

	// 3. Build Gateway Multiplexer modeling readiness and protected routes
	mux := http.NewServeMux()

	// Readiness endpoint (CTRL-04, Invariant 1)
	mux.HandleFunc("/healthz/ready", func(w http.ResponseWriter, r *http.Request) {
		if manager.IsLeaseExpired(60 * time.Second) {
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Gateway unready: freshness lease expired (>60s)",
				"https://aegis.local/errors/policy-lease-expired",
				"health-check",
			)
			return
		}
		if manager.Active() == nil {
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Gateway unready: active state uninitialized",
				"https://aegis.local/errors/uninitialized",
				"health-check",
			)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	// Protected route pipeline
	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = "test-" + uuid.NewString()[:8]
		}

		// Hard boundary fail-closed check: lease age > 60s
		if manager.IsLeaseExpired(60 * time.Second) {
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Policy freshness lease expired (>60s)",
				"https://aegis.local/errors/policy-lease-expired",
				reqID,
			)
			return
		}

		state := manager.Active()
		if state == nil {
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Gateway configuration uninitialized",
				"https://aegis.local/errors/uninitialized",
				reqID,
			)
			return
		}

		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		claims, err := validator.ValidateBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
				"https://aegis.local/errors/unauthorized", reqID)
			return
		}

		route, err := state.Router.Match(r.Method, canonicalPath)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "Route not found",
				"https://aegis.local/errors/not-found", reqID)
			return
		}

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
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", "DENIED",
				"https://aegis.local/errors/forbidden", reqID)
			return
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

	gwServer := httptest.NewServer(mux)
	defer gwServer.Close()
	client := gwServer.Client()

	// 4. Activate Initial Snapshot v1 and Record Fresh Lease
	payload := &snapshotv1.SnapshotPayload{
		Version: 1,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:      "orders.list",
				ServiceId:    "orders",
				HttpMethod:   "GET",
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
	env, err := signer.SignSnapshot(payload)
	require.NoError(t, err)
	require.NoError(t, manager.ValidateAndActivate(context.Background(), env))
	manager.RecordLeaseRenewal()

	// Baseline: Gateway authorizes requests normally and readiness returns 200 OK
	readyResp, err := client.Get(gwServer.URL + "/healthz/ready")
	require.NoError(t, err)
	defer readyResp.Body.Close()
	assert.Equal(t, http.StatusOK, readyResp.StatusCode)

	validToken := mintToken("user-alpha")
	req, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), backendCallCount.Load(), "Baseline request reaches upstream")

	// 5. Fault Injection: Sever lease updates; simulate transient window (<= 60s)
	// Lease age is 30 seconds (within the 60s grace threshold)
	manager.SetLastLeaseRenewedAt(time.Now().Add(-30 * time.Second))
	assert.False(t, manager.IsLeaseExpired(60*time.Second))

	// Transient window verification: Traffic is still authorized against cached snapshot
	reqTransient, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	reqTransient.Header.Set("Authorization", "Bearer "+validToken)
	respTransient, err := client.Do(reqTransient)
	require.NoError(t, err)
	defer respTransient.Body.Close()
	assert.Equal(t, http.StatusOK, respTransient.StatusCode, "Traffic authorized during transient window <=60s")
	assert.Equal(t, int64(2), backendCallCount.Load(), "Upstream receives request during transient window")

	// 6. Hard Boundary Verification (> 60s): Lease age is 65 seconds
	manager.SetLastLeaseRenewedAt(time.Now().Add(-65 * time.Second))
	assert.True(t, manager.IsLeaseExpired(60*time.Second))

	// Readiness probe drops to HTTP 503
	readyExpired, err := client.Get(gwServer.URL + "/healthz/ready")
	require.NoError(t, err)
	defer readyExpired.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, readyExpired.StatusCode)

	// Protected route returns HTTP 503 Service Unavailable (POLICY_LEASE_EXPIRED)
	reqExpired, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	reqExpired.Header.Set("Authorization", "Bearer "+validToken)
	respExpired, err := client.Do(reqExpired)
	require.NoError(t, err)
	defer respExpired.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, respExpired.StatusCode)
	var probExpired proxy.RFC7807Problem
	err = json.NewDecoder(respExpired.Body).Decode(&probExpired)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/policy-lease-expired", probExpired.Type)
	assert.Equal(t, "Policy freshness lease expired (>60s)", probExpired.Detail)

	// Invariant 1 check: Upstream backend received zero additional requests after lease expired
	assert.Equal(t, int64(2), backendCallCount.Load(), "Mock backend must receive ZERO requests after lease expiration")

	// 7. Recovery: Control Plane reconnects and issues fresh lease
	manager.RecordLeaseRenewal()
	assert.False(t, manager.IsLeaseExpired(60*time.Second))

	// Readiness probe restores to 200 OK
	readyRecovered, err := client.Get(gwServer.URL + "/healthz/ready")
	require.NoError(t, err)
	defer readyRecovered.Body.Close()
	assert.Equal(t, http.StatusOK, readyRecovered.StatusCode)

	// Protected routes immediately resume normal authorization
	reqRecovered, _ := http.NewRequest(http.MethodGet, gwServer.URL+"/api/orders", nil)
	reqRecovered.Header.Set("Authorization", "Bearer "+validToken)
	respRecovered, err := client.Do(reqRecovered)
	require.NoError(t, err)
	defer respRecovered.Body.Close()

	assert.Equal(t, http.StatusOK, respRecovered.StatusCode)
	assert.Equal(t, int64(3), backendCallCount.Load(), "Mock backend must receive request after recovery")
}

// TestFailure_SpoolSaturationFailClosed verifies AUD-02, Invariant 1, and Invariant 10:
// When local disk WAL spool reaches >=90% capacity, gateway halts admissions with HTTP 503,
// upstream backends receive zero requests, and admission resumes once spool is drained.
func TestFailure_SpoolSaturationFailClosed(t *testing.T) {
	// 1. Mock Upstream Backend
	var backendCallCount atomic.Int64
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"order_completed"}`))
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

	assertionPriv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
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
	env, err := signer.SignSnapshot(payload)
	require.NoError(t, err)
	require.NoError(t, manager.ValidateAndActivate(context.Background(), env))

	// 5. Build Gateway Pipeline with Pre-Forward Spool Append & Saturation Gate
	auditLogger := audit.NewLogger(nil)
	gatewayHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = "test-" + uuid.NewString()[:8]
			r.Header.Set("X-Request-ID", reqID)
			w.Header().Set("X-Request-ID", reqID)
		}
		ac := audit.FromContext(r.Context())

		state := manager.Active()
		if state == nil {
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Gateway configuration uninitialized", "https://aegis.local/errors/uninitialized", reqID)
			return
		}

		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		claims, err := validator.ValidateBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
				"https://aegis.local/errors/unauthorized", reqID)
			return
		}

		if ac != nil {
			ac.SetPrincipal(claims.Subject, "user", claims.Roles)
			ac.SetCanonicalPath(canonicalPath)
			ac.SetSnapshotVersion(state.Version)
		}

		route, err := state.Router.Match(r.Method, canonicalPath)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching route",
				"https://aegis.local/errors/not-found", reqID)
			return
		}

		if ac != nil {
			ac.SetRoute(route.RouteID, route.ServiceID)
		}

		// Policy evaluation
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
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", "DENIED",
				"https://aegis.local/errors/forbidden", reqID)
			return
		}

		if ac != nil {
			ac.SetDecision("allow", "ALLOWED")
		}

		// Pre-Forward Spool Saturation Safety Gate (AUD-02, Invariant 10)
		saturated, err := spool.CheckSaturation()
		if err != nil || saturated {
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

		// Pre-forward synchronous fsync WAL append
		if ac != nil {
			preForwardEvent := ac.ToCompletionEvent(r.Method, "127.0.0.1", state.Version)
			if appendErr := spool.AppendPreForward(preForwardEvent); appendErr != nil {
				if errors.Is(appendErr, audit.ErrSpoolSaturated) {
					ac.SetDecision("deny", "AUDIT_SPOOL_SATURATED")
					ac.SetErrorCode("AUDIT_SPOOL_SATURATED")
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
				proxy.WriteProblemDetails(w, http.StatusInternalServerError, "Internal Server Error",
					"Audit append failure", "https://aegis.local/errors/internal-error", reqID)
				return
			}
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

	auditedHandler := audit.AuditMiddleware(auditLogger, 1)(gatewayHandler)
	gwServer := httptest.NewServer(auditedHandler)
	defer gwServer.Close()
	client := gwServer.Client()

	// 6. Baseline: Permitted request under capacity succeeds with HTTP 200
	validToken := mintToken("user-spool-alpha")
	req, _ := http.NewRequest(http.MethodPost, gwServer.URL+"/api/orders", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), backendCallCount.Load(), "Baseline permitted request reaches upstream")

	// 7. Fault Injection: Flood spool directory until >= 90% saturation
	for i := 0; i < 200; i++ {
		fillEvent := &audit.CompletionEvent{
			EventID:         uuid.NewString(),
			Timestamp:       time.Now().UTC(),
			RequestID:       uuid.NewString(),
			PrincipalID:     "filler-principal",
			PrincipalKind:   "user",
			PrincipalRoles:  []string{"worker"},
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
				break
			}
			require.NoError(t, appendErr)
		}
	}

	sat, err := spool.CheckSaturation()
	require.NoError(t, err)
	require.True(t, sat, "Spool must be saturated after flood")

	// 8. Verification: Issue subsequent request while saturated
	backendCallsBeforeSaturated := backendCallCount.Load()
	satReq, _ := http.NewRequest(http.MethodPost, gwServer.URL+"/api/orders", nil)
	satReq.Header.Set("Authorization", "Bearer "+validToken)
	satResp, err := client.Do(satReq)
	require.NoError(t, err)
	defer satResp.Body.Close()

	// Must fail closed with HTTP 503 Service Unavailable (AUDIT_SPOOL_SATURATED)
	assert.Equal(t, http.StatusServiceUnavailable, satResp.StatusCode)
	var probSat proxy.RFC7807Problem
	err = json.NewDecoder(satResp.Body).Decode(&probSat)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/spool-saturated", probSat.Type)
	assert.Equal(t, "Audit spool saturated; halting admission", probSat.Detail)

	// Invariant 10 check: Zero requests reached upstream backend when saturated
	assert.Equal(t, backendCallsBeforeSaturated, backendCallCount.Load(),
		"Zero requests must reach upstream backend during spool saturation (Invariant 10)")

	// 9. Recovery: Simulate audit worker draining & pruning completed segments
	// Prune closed segment files to bring capacity below 90%
	entries, err := os.ReadDir(tmpSpoolDir)
	require.NoError(t, err)
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".log" {
			_ = os.Remove(filepath.Join(tmpSpoolDir, entry.Name()))
		}
	}

	satAfterPrune, err := spool.CheckSaturation()
	require.NoError(t, err)
	assert.False(t, satAfterPrune, "Spool must no longer be saturated after worker pruning")

	// Gateway resumes admitting traffic
	recReq, _ := http.NewRequest(http.MethodPost, gwServer.URL+"/api/orders", nil)
	recReq.Header.Set("Authorization", "Bearer "+validToken)
	recResp, err := client.Do(recReq)
	require.NoError(t, err)
	defer recResp.Body.Close()

	assert.Equal(t, http.StatusOK, recResp.StatusCode, "Gateway must resume admissions after spool pruning")
	assert.Equal(t, int64(2), backendCallCount.Load(), "Upstream backend must receive permitted request after recovery")
}
