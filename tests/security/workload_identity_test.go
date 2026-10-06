package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aegis/internal/audit"
	"aegis/internal/config"
	"aegis/internal/identity"
	"aegis/internal/pki"
	"aegis/internal/policy"
	"aegis/internal/proxy"
	"aegis/services/middleware"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCluster struct {
	ca             *pki.CA
	serverCert     tls.Certificate
	workloadCert   tls.Certificate
	evilDomainCert tls.Certificate
	gatewayCert    tls.Certificate
	assertionPriv  ed25519.PrivateKey
	assertionPub   ed25519.PublicKey
	userPrivKey    ed25519.PrivateKey
	userPubKey     ed25519.PublicKey
	userURL        string
	workloadURL    string
	paymentsServer *httptest.Server
	adminServer    *httptest.Server
	ordersServer   *httptest.Server
	dualServer     *proxy.DualServer
}

func mintUserTestToken(t *testing.T, privKey ed25519.PrivateKey, subject string, roles []string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, &identity.UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "aegis-issuer",
			Audience:  jwt.ClaimStrings{"aegis-gateway"},
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Roles: roles,
	})
	tokenStr, err := token.SignedString(privKey)
	require.NoError(t, err)
	return tokenStr
}

func setupTestCluster(t *testing.T) *testCluster {
	t.Helper()

	// 1. In-memory PKI Setup
	ca, err := pki.NewCA("Aegis Workload Test CA")
	require.NoError(t, err)

	serverCert, err := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	workloadCert, err := ca.IssueWorkloadCert("spiffe://aegis.local/workload/orders")
	require.NoError(t, err)

	evilDomainCert, err := ca.IssueWorkloadCert("spiffe://evil.com/workload/orders")
	require.NoError(t, err)

	gatewaySPIFFE := "spiffe://aegis.local/ns/gateway/sa/aegis-gateway"
	gatewayCert, err := ca.IssueWorkloadCert(gatewaySPIFFE)
	require.NoError(t, err)

	// 2. Cryptographic Keys
	assertionPub, assertionPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	userPub, userPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	assertionMinter := identity.NewAssertionMinter(assertionPriv)
	tokenValidator := identity.NewTokenValidator("aegis-issuer", "aegis-gateway", userPub)

	// 3. Mock Upstream Microservices with HTTPS mTLS and BackendAuthMiddleware
	backendTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.CertPool,
		MinVersion:   tls.VersionTLS13,
	}

	// Payments Mock
	paymentsMux := http.NewServeMux()
	paymentsMux.HandleFunc("/api/payments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":     "processed",
				"payment_id": "pay_201",
			})
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "pay_201", "amount": 99.99},
			})
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	paymentsMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	paymentsHandler := middleware.BackendAuthMiddleware(gatewaySPIFFE, "payments", assertionPub, "/health")(paymentsMux)
	paymentsServer := httptest.NewUnstartedServer(paymentsHandler)
	paymentsServer.TLS = backendTLSConfig.Clone()
	paymentsServer.StartTLS()

	// Admin Mock
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "usr_admin_01", "role": "application-admin"},
			})
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "success",
				"message": "user created",
			})
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	adminMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	adminHandler := middleware.BackendAuthMiddleware(gatewaySPIFFE, "admin", assertionPub, "/health")(adminMux)
	adminServer := httptest.NewUnstartedServer(adminHandler)
	adminServer.TLS = backendTLSConfig.Clone()
	adminServer.StartTLS()

	// Orders Mock
	ordersMux := http.NewServeMux()
	ordersMux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "ord_101", "item": "Cloud Scanner"},
			})
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	ordersMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	ordersHandler := middleware.BackendAuthMiddleware(gatewaySPIFFE, "orders", assertionPub, "/health")(ordersMux)
	ordersServer := httptest.NewUnstartedServer(ordersHandler)
	ordersServer.TLS = backendTLSConfig.Clone()
	ordersServer.StartTLS()

	// 4. Upstream Router and Transport
	routes := []proxy.Route{
		{
			RouteID:      "payments.create",
			ServiceID:    "payments",
			HTTPMethod:   "POST",
			PathTemplate: "/api/payments",
			UpstreamURL:  paymentsServer.URL,
		},
		{
			RouteID:      "payments.get",
			ServiceID:    "payments",
			HTTPMethod:   "GET",
			PathTemplate: "/api/payments",
			UpstreamURL:  paymentsServer.URL,
		},
		{
			RouteID:      "admin.users.list",
			ServiceID:    "admin",
			HTTPMethod:   "GET",
			PathTemplate: "/api/admin/users",
			UpstreamURL:  adminServer.URL,
		},
		{
			RouteID:      "admin.users.manage",
			ServiceID:    "admin",
			HTTPMethod:   "POST",
			PathTemplate: "/api/admin/users",
			UpstreamURL:  adminServer.URL,
		},
		{
			RouteID:      "orders.list",
			ServiceID:    "orders",
			HTTPMethod:   "GET",
			PathTemplate: "/api/orders",
			UpstreamURL:  ordersServer.URL,
		},
		{
			RouteID:      "unknown.test",
			ServiceID:    "unknown",
			HTTPMethod:   "GET",
			PathTemplate: "/api/unknown",
			UpstreamURL:  paymentsServer.URL,
		},
	}
	router, err := proxy.NewRouter(routes)
	require.NoError(t, err)

	upstreamTransport := proxy.CreateUpstreamTransport(gatewayCert, ca.CertPool)

	// 5. OPA Policy Engine
	policyPath := filepath.Join("..", "..", "policies", "rego", "authz.rego")
	regoBytes, err := os.ReadFile(policyPath)
	require.NoError(t, err, "failed to read policies/rego/authz.rego")

	policyEngine, err := policy.NewEngine(context.Background(), string(regoBytes))
	require.NoError(t, err)

	auditLogger := audit.NewLogger(nil)
	snapshotVersion := int64(1)

	// 6. Gateway User Ingress Handler (:8080)
	userHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		claims, err := tokenValidator.ValidateBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
				"https://aegis.local/errors/unauthorized", reqID)
			return
		}

		route, err := router.Match(r.Method, canonicalPath)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching route found",
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
			Context:         policy.ContextInput{RiskScore: 0, RiskState: "available"},
			SnapshotVersion: snapshotVersion,
		}

		decision, err := policyEngine.Evaluate(r.Context(), input)
		if err != nil || !decision.Allow {
			reason := "DENIED_DEFAULT"
			if decision.ReasonCode != "" {
				reason = decision.ReasonCode
			}
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", reason,
				"https://aegis.local/errors/forbidden", reqID)
			return
		}

		assertionToken, err := assertionMinter.MintAssertion(
			claims.Subject,
			"user",
			claims.Roles,
			route.ServiceID,
			r.Method,
			canonicalPath,
			reqID,
			snapshotVersion,
		)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusInternalServerError, "Internal Server Error", "Failed to mint assertion",
				"https://aegis.local/errors/internal-error", reqID)
			return
		}

		rp := proxy.NewReverseProxyWithMTLS(route.ParsedURL(), canonicalPath, reqID, assertionToken, upstreamTransport)
		rp.ServeHTTP(w, r)
	})

	// 7. Gateway Workload Ingress Handler (:9443)
	workloadHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", "Client certificate required",
				"https://aegis.local/errors/unauthorized", reqID)
			return
		}

		spiffeID, err := identity.ExtractSPIFFEID(r.TLS.PeerCertificates[0], "aegis.local")
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
				"https://aegis.local/errors/invalid-workload-identity", reqID)
			return
		}

		route, err := router.Match(r.Method, canonicalPath)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching route found",
				"https://aegis.local/errors/not-found", reqID)
			return
		}

		input := policy.PolicyInput{
			Principal: policy.PrincipalInput{
				ID:    spiffeID,
				Kind:  "workload",
				Roles: []string{"workload"},
			},
			Resource: policy.ResourceInput{
				Service: route.ServiceID,
				Route:   route.RouteID,
			},
			Request: policy.RequestInput{
				Method: r.Method,
				Path:   canonicalPath,
			},
			Context:         policy.ContextInput{RiskScore: 0, RiskState: "available"},
			SnapshotVersion: snapshotVersion,
		}

		decision, err := policyEngine.Evaluate(r.Context(), input)
		if err != nil || !decision.Allow {
			reason := "DENIED_WORKLOAD_FORBIDDEN"
			if decision.ReasonCode != "" {
				reason = decision.ReasonCode
			}
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", reason,
				"https://aegis.local/errors/forbidden", reqID)
			return
		}

		assertionToken, err := assertionMinter.MintAssertion(
			spiffeID,
			"workload",
			[]string{"workload"},
			route.ServiceID,
			r.Method,
			canonicalPath,
			reqID,
			snapshotVersion,
		)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusInternalServerError, "Internal Server Error", "Failed to mint assertion",
				"https://aegis.local/errors/internal-error", reqID)
			return
		}

		rp := proxy.NewReverseProxyWithMTLS(route.ParsedURL(), canonicalPath, reqID, assertionToken, upstreamTransport)
		rp.ServeHTTP(w, r)
	})

	userLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	workloadLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	userPort := userLn.Addr().(*net.TCPAddr).Port
	workloadPort := workloadLn.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		Port:                  userPort,
		WorkloadPort:          workloadPort,
		MaxHeaderBytes:        16384,
		MaxBodyBytes:          1048576,
		ReadHeaderTimeout:     5 * time.Second,
		WriteTimeout:          5 * time.Second,
		MaxConcurrentRequests: 100,
	}

	auditedUserHandler := audit.AuditMiddleware(auditLogger, snapshotVersion)(userHandler)
	auditedWorkloadHandler := audit.AuditMiddleware(auditLogger, snapshotVersion)(workloadHandler)

	workloadTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    ca.CertPool,
		MinVersion:   tls.VersionTLS13,
	}

	ds := proxy.NewDualServer(cfg, auditedUserHandler, auditedWorkloadHandler, workloadTLSConfig)

	go func() {
		_ = ds.Serve(userLn, workloadLn)
	}()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = ds.Shutdown(ctx)
		paymentsServer.Close()
		adminServer.Close()
		ordersServer.Close()
		_ = userLn.Close()
		_ = workloadLn.Close()
	})

	return &testCluster{
		ca:             ca,
		serverCert:     serverCert,
		workloadCert:   workloadCert,
		evilDomainCert: evilDomainCert,
		gatewayCert:    gatewayCert,
		assertionPriv:  assertionPriv,
		assertionPub:   assertionPub,
		userPrivKey:    userPriv,
		userPubKey:     userPub,
		userURL:        "http://" + userLn.Addr().String(),
		workloadURL:    "https://" + workloadLn.Addr().String(),
		paymentsServer: paymentsServer,
		adminServer:    adminServer,
		ordersServer:   ordersServer,
		dualServer:     ds,
	}
}

// -----------------------------------------------------------------------------
// Workload RBAC and Policy Enforcement Security Tests (Task 1)
// -----------------------------------------------------------------------------

func TestWorkloadPolicyEnforcement_OrdersCallsPaymentsSuccess(t *testing.T) {
	cluster := setupTestCluster(t)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cluster.workloadCert},
				RootCAs:      cluster.ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	payload := []byte(`{"amount": 199.99, "currency": "USD"}`)
	resp, err := client.Post(cluster.workloadURL+"/api/payments", "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("X-Request-Id"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(bodyBytes, &result)
	require.NoError(t, err)

	assert.Equal(t, "processed", result["status"])
	assert.Equal(t, "pay_201", result["payment_id"])
}

func TestWorkloadPolicyEnforcement_OrdersCallsAdminForbidden(t *testing.T) {
	cluster := setupTestCluster(t)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cluster.workloadCert},
				RootCAs:      cluster.ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	// 1. GET /api/admin/users -> 403 Forbidden with DENIED_WORKLOAD_ADMIN_FORBIDDEN
	respGet, err := client.Get(cluster.workloadURL + "/api/admin/users")
	require.NoError(t, err)
	defer respGet.Body.Close()

	assert.Equal(t, http.StatusForbidden, respGet.StatusCode)
	assert.NotEmpty(t, respGet.Header.Get("X-Request-Id"))

	bodyGet, err := io.ReadAll(respGet.Body)
	require.NoError(t, err)

	var problemGet proxy.RFC7807Problem
	err = json.Unmarshal(bodyGet, &problemGet)
	require.NoError(t, err)
	assert.Equal(t, "DENIED_WORKLOAD_ADMIN_FORBIDDEN", problemGet.Detail)
	assert.Equal(t, "https://aegis.local/errors/forbidden", problemGet.Type)

	// 2. POST /api/admin/users -> 403 Forbidden with DENIED_WORKLOAD_ADMIN_FORBIDDEN
	respPost, err := client.Post(cluster.workloadURL+"/api/admin/users", "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	defer respPost.Body.Close()

	assert.Equal(t, http.StatusForbidden, respPost.StatusCode)
	bodyPost, err := io.ReadAll(respPost.Body)
	require.NoError(t, err)

	var problemPost proxy.RFC7807Problem
	err = json.Unmarshal(bodyPost, &problemPost)
	require.NoError(t, err)
	assert.Equal(t, "DENIED_WORKLOAD_ADMIN_FORBIDDEN", problemPost.Detail)
}

func TestWorkloadPolicyEnforcement_OrdersCallsUnmappedRoute(t *testing.T) {
	cluster := setupTestCluster(t)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cluster.workloadCert},
				RootCAs:      cluster.ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	// 1. Unmapped policy route: /api/unknown
	resp, err := client.Get(cluster.workloadURL + "/api/unknown")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var problem proxy.RFC7807Problem
	err = json.Unmarshal(bodyBytes, &problem)
	require.NoError(t, err)
	assert.Equal(t, "DENIED_DEFAULT", problem.Detail)

	// 2. Unmapped workload method: GET /api/payments (only POST is permitted for orders workload)
	respGetPay, err := client.Get(cluster.workloadURL + "/api/payments")
	require.NoError(t, err)
	defer respGetPay.Body.Close()

	assert.Equal(t, http.StatusForbidden, respGetPay.StatusCode)
	bodyGetPay, err := io.ReadAll(respGetPay.Body)
	require.NoError(t, err)

	var problemGetPay proxy.RFC7807Problem
	err = json.Unmarshal(bodyGetPay, &problemGetPay)
	require.NoError(t, err)
	assert.Equal(t, "DENIED_DEFAULT", problemGetPay.Detail)
}

func TestWorkloadIdentity_UntrustedCAHandshakeFailure(t *testing.T) {
	cluster := setupTestCluster(t)

	rogueCA, err := pki.NewCA("Rogue Untrusted CA")
	require.NoError(t, err)

	rogueCert, err := rogueCA.IssueWorkloadCert("spiffe://aegis.local/workload/orders")
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{rogueCert},
				RootCAs:      cluster.ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	_, err = client.Post(cluster.workloadURL+"/api/payments", "application/json", bytes.NewReader([]byte("{}")))
	require.Error(t, err, "certificate signed by untrusted CA must fail TLS handshake")
}

func TestWorkloadIdentity_TrustDomainMismatch(t *testing.T) {
	cluster := setupTestCluster(t)

	// Client presents certificate with spiffe://evil.com/workload/orders
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cluster.evilDomainCert},
				RootCAs:      cluster.ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	resp, err := client.Post(cluster.workloadURL+"/api/payments", "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Invariant 2 & AUTH-02: Trust domain mismatch rejected at authentication layer
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var problem proxy.RFC7807Problem
	err = json.Unmarshal(bodyBytes, &problem)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/invalid-workload-identity", problem.Type)
	assert.Contains(t, problem.Detail, "trust domain mismatch")
}

func TestWorkloadIdentity_AmbiguousCredentialsRejected(t *testing.T) {
	cluster := setupTestCluster(t)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cluster.workloadCert},
				RootCAs:      cluster.ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	req, err := http.NewRequest(http.MethodPost, cluster.workloadURL+"/api/payments", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.dummy.sig")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Invariant 2 & AUTH-03: Ambiguous credentials rejected immediately with HTTP 401
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
	assert.NotEmpty(t, resp.Header.Get("X-Request-Id"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var problem proxy.RFC7807Problem
	err = json.Unmarshal(bodyBytes, &problem)
	require.NoError(t, err)

	assert.Equal(t, "https://aegis.local/errors/ambiguous-credentials", problem.Type)
	assert.Equal(t, "Unauthorized", problem.Title)
	assert.Equal(t, http.StatusUnauthorized, problem.Status)
	assert.Equal(t, "Ambiguous credentials: Bearer tokens are prohibited on the workload listener", problem.Detail)
	assert.Equal(t, resp.Header.Get("X-Request-Id"), problem.Instance)
}

func TestWorkloadIdentity_UserPortIgnoresWorkloadCert(t *testing.T) {
	cluster := setupTestCluster(t)

	// Case 1: Client presents workload certificate to user port :8080 without Bearer token
	tlsClientWithCert := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cluster.workloadCert},
				RootCAs:      cluster.ca.CertPool,
			},
		},
		Timeout: 2 * time.Second,
	}

	respNoToken, err := tlsClientWithCert.Get(cluster.userURL + "/api/orders")
	require.NoError(t, err)
	defer respNoToken.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, respNoToken.StatusCode)
	bodyNoToken, err := io.ReadAll(respNoToken.Body)
	require.NoError(t, err)

	var problem proxy.RFC7807Problem
	err = json.Unmarshal(bodyNoToken, &problem)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/unauthorized", problem.Type)

	// Case 2: Client on user port :8080 presents valid developer Bearer token
	devToken := mintUserTestToken(t, cluster.userPrivKey, "usr_dev_01", []string{"developer"})

	reqWithToken, err := http.NewRequest(http.MethodGet, cluster.userURL+"/api/orders", nil)
	require.NoError(t, err)
	reqWithToken.Header.Set("Authorization", "Bearer "+devToken)

	respWithToken, err := http.DefaultClient.Do(reqWithToken)
	require.NoError(t, err)
	defer respWithToken.Body.Close()

	assert.Equal(t, http.StatusOK, respWithToken.StatusCode)
	bodyWithToken, err := io.ReadAll(respWithToken.Body)
	require.NoError(t, err)
	assert.Contains(t, string(bodyWithToken), "Cloud Scanner")

	// Case 3: Developer attempts to access admin endpoint -> 403 Forbidden
	reqAdmin, err := http.NewRequest(http.MethodGet, cluster.userURL+"/api/admin/users", nil)
	require.NoError(t, err)
	reqAdmin.Header.Set("Authorization", "Bearer "+devToken)

	respAdmin, err := http.DefaultClient.Do(reqAdmin)
	require.NoError(t, err)
	defer respAdmin.Body.Close()

	assert.Equal(t, http.StatusForbidden, respAdmin.StatusCode)
	bodyAdmin, err := io.ReadAll(respAdmin.Body)
	require.NoError(t, err)

	var problemAdmin proxy.RFC7807Problem
	err = json.Unmarshal(bodyAdmin, &problemAdmin)
	require.NoError(t, err)
	assert.Equal(t, "DENIED_DEVELOPER_ADMIN_FORBIDDEN", problemAdmin.Detail)
}

// -----------------------------------------------------------------------------
// Backwards Compatible Aliases for Phase 2 Workload Listener Tests
// -----------------------------------------------------------------------------

func TestWorkloadIdentityListener_RequireClientCert(t *testing.T) {
	cluster := setupTestCluster(t)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    cluster.ca.CertPool,
				MinVersion: tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	_, err := client.Post(cluster.workloadURL+"/api/payments", "application/json", bytes.NewReader([]byte("{}")))
	require.Error(t, err, "client without certificate must fail TLS handshake on workload listener (:9443)")
}

func TestWorkloadIdentityListener_RogueCACertRejected(t *testing.T) {
	TestWorkloadIdentity_UntrustedCAHandshakeFailure(t)
}

func TestWorkloadIdentityListener_ValidCertSuccess(t *testing.T) {
	TestWorkloadPolicyEnforcement_OrdersCallsPaymentsSuccess(t)
}

func TestWorkloadIdentityListener_AmbiguousCredentialsRejected(t *testing.T) {
	TestWorkloadIdentity_AmbiguousCredentialsRejected(t)
}

func TestWorkloadIdentityListener_UserPortPermitsBearer(t *testing.T) {
	cluster := setupTestCluster(t)

	devToken := mintUserTestToken(t, cluster.userPrivKey, "usr_dev_01", []string{"developer"})

	req, err := http.NewRequest(http.MethodGet, cluster.userURL+"/api/orders", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+devToken)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("X-Request-Id"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(bodyBytes), "Cloud Scanner")
}
