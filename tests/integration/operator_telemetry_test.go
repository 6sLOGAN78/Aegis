package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"aegis/internal/control"
	"aegis/internal/revocation"
	"aegis/internal/snapshot"
	"aegis/internal/storage"
	"aegis/internal/telemetry"
	controlv1 "aegis/pkg/api/control/v1"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestOperatorAndTelemetryIntegration(t *testing.T) {
	// =========================================================================
	// Step 1: Bootstrap in-memory / mock Control Plane and Gateway instances
	// =========================================================================
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	revStore := revocation.NewStore(rdb)

	mockDB, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mockDB.Close()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer := snapshot.NewSigner(priv, "test-signing-key-1")
	verifier := snapshot.NewVerifier(pub)
	snapManager := snapshot.NewManager(verifier)

	snapshotRepo := storage.NewSnapshotRepo(mockDB)
	routeRepo := storage.NewRouteRepo(mockDB)
	policyRepo := storage.NewPolicyRepo(mockDB)
	auditRepo := storage.NewAuditRepo(mockDB)

	// Create initial snapshot v1
	v1Payload := &snapshotv1.SnapshotPayload{
		Version: 1,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:      "orders.list",
				ServiceId:    "orders",
				HttpMethod:   "GET",
				PathTemplate: "/api/orders",
				UpstreamUrl:  "https://orders:8081",
			},
		},
		PolicyModules: []*snapshotv1.PolicyModule{
			{
				PackageName: "aegis.authz",
				ModuleName:  "rules.rego",
				SourceRego: `package aegis.authz
default decision := {"allow": false, "reason_code": "DENIED_DEFAULT"}
decision := {"allow": true, "reason_code": "RULE_AUTHORIZED"} if {
    input.request.path == "/api/orders"
}
`,
			},
		},
	}
	v1Env, err := signer.SignSnapshot(v1Payload)
	require.NoError(t, err)

	// Use in-memory tracker (nil repo) to prevent async DB calls from gRPC stream
	ackTracker := control.NewAckTracker(nil)
	distServer := control.NewSnapshotDistributionServer(v1Env, ackTracker)

	// Start in-memory gRPC distribution server via bufconn
	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	snapshotv1.RegisterSnapshotDistributionServiceServer(grpcServer, distServer)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer func() {
		grpcServer.Stop()
		_ = lis.Close()
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	// Connect Gateway StreamClient to Control Plane
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	gatewayID := "gw-replica-alpha"
	streamClient := snapshot.NewStreamClient(
		"bufnet",
		gatewayID,
		snapManager,
		verifier,
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	go func() {
		_ = streamClient.Start(streamCtx)
	}()
	defer streamClient.Stop()

	// Wait for gateway stream client to apply initial snapshot v1 and report ack
	require.Eventually(t, func() bool {
		statuses := ackTracker.GetReplicaStatuses()
		ack, ok := statuses[gatewayID]
		return ok && ack.ActiveVersion == 1
	}, 3*time.Second, 50*time.Millisecond, "Gateway replica failed to acknowledge snapshot v1")

	// Instantiate Telemetry Collectors
	cpMetrics := telemetry.NewMetrics()
	gwMetrics := telemetry.NewMetrics()

	cpMetricsServer := telemetry.NewServer(":0", cpMetrics)
	cpMetricsTS := httptest.NewServer(cpMetricsServer.Handler())
	defer cpMetricsTS.Close()

	gwMetricsServer := telemetry.NewServer(":0", gwMetrics)
	gwMetricsTS := httptest.NewServer(gwMetricsServer.Handler())
	defer gwMetricsTS.Close()

	// Control Plane REST Management API Server
	sessionMgr := control.NewSessionManager()
	idempStore := control.NewIdempotencyStore()
	validator := control.NewValidator()
	rollbackEng := control.NewRollbackEngine(snapshotRepo, signer)

	apiServer := control.NewAPIServer(
		sessionMgr,
		idempStore,
		distServer,
		validator,
		rollbackEng,
		routeRepo,
		policyRepo,
		snapshotRepo,
		auditRepo,
		revStore,
		signer,
	)

	cpHttpServer := httptest.NewServer(apiServer.Handler())
	defer cpHttpServer.Close()

	// Simulated Gateway Ingress Pipeline with revocation check and telemetry middleware
	gatewayCoreHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principalID := r.Header.Get("X-Principal-ID")
		jti := r.Header.Get("X-Token-JTI")

		// Revocation and Quarantine check (<200ms Redis SLA)
		revoked, reason, checkErr := revStore.CheckRevocation(r.Context(), principalID, jti)
		if checkErr != nil {
			http.Error(w, `{"code":"DEPENDENCY_FAILURE"}`, http.StatusServiceUnavailable)
			return
		}
		if revoked {
			gwMetrics.RecordPolicyDecision("deny", reason)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"` + reason + `"}`))
			return
		}

		gwMetrics.RecordPolicyDecision("allow", "RULE_AUTHORIZED")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"authorized"}`))
	})

	gwHttpHandler := telemetry.MetricsMiddleware(gwMetrics)(gatewayCoreHandler)
	gwHttpServer := httptest.NewServer(gwHttpHandler)
	defer gwHttpServer.Close()

	// =========================================================================
	// Step 2: Authenticate operator via POST /control/v1/auth/login
	// =========================================================================
	loginPayload := `{"username":"admin","password":"admin-secret"}`
	loginResp, err := http.Post(cpHttpServer.URL+"/control/v1/auth/login", "application/json", bytes.NewBufferString(loginPayload))
	require.NoError(t, err)
	defer loginResp.Body.Close()

	require.Equal(t, http.StatusOK, loginResp.StatusCode)

	var loginData struct {
		Username  string `json:"username"`
		Role      string `json:"role"`
		CSRFToken string `json:"csrf_token"`
	}
	err = json.NewDecoder(loginResp.Body).Decode(&loginData)
	require.NoError(t, err)
	require.Equal(t, "admin", loginData.Username)
	require.Equal(t, "sec-ops", loginData.Role)
	require.NotEmpty(t, loginData.CSRFToken)

	// Extract HttpOnly session cookie
	var sessionCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "aegis_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "HttpOnly aegis_session cookie must be set upon login")
	assert.True(t, sessionCookie.HttpOnly)

	// Helper for authenticated requests to control plane
	doAuthRequest := func(method, path string, body []byte, headers map[string]string) *http.Response {
		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		req, reqErr := http.NewRequest(method, cpHttpServer.URL+path, bodyReader)
		require.NoError(t, reqErr)
		req.AddCookie(sessionCookie)
		if headers != nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
		if method != http.MethodGet && method != http.MethodHead {
			req.Header.Set("X-CSRF-Token", loginData.CSRFToken)
			if req.Header.Get("Content-Type") == "" {
				req.Header.Set("Content-Type", "application/json")
			}
		}
		resp, doErr := http.DefaultClient.Do(req)
		require.NoError(t, doErr)
		return resp
	}

	// =========================================================================
	// Step 3: Execute dry-run candidate Rego simulation via POST /control/v1/policies/simulate
	// =========================================================================
	candidateRego := `package aegis.authz

default decision := {"allow": false, "reason_code": "DENIED_DEFAULT"}

decision := {"allow": true, "reason_code": "RULE_AUTHORIZED"} if {
    input.request.path == "/api/payments"
}
`
	simPayload := map[string]interface{}{
		"candidate_rego": candidateRego,
		"input_context": map[string]interface{}{
			"request": map[string]interface{}{
				"path": "/api/payments",
			},
		},
	}
	simBytes, err := json.Marshal(simPayload)
	require.NoError(t, err)

	simResp := doAuthRequest(http.MethodPost, "/control/v1/policies/simulate", simBytes, nil)
	defer simResp.Body.Close()
	require.Equal(t, http.StatusOK, simResp.StatusCode)

	var simData controlv1.PolicySimulationResponse
	err = json.NewDecoder(simResp.Body).Decode(&simData)
	require.NoError(t, err)
	assert.True(t, simData.Allow, "Simulation on /api/payments should evaluate to ALLOW")
	assert.Greater(t, simData.DurationUs, 0, "Simulation duration must be recorded in microseconds")

	// =========================================================================
	// Step 4: Publish new policy snapshot via POST /control/v1/policies/{id}/publish with If-Match
	// =========================================================================
	policyID := "policy-v2-test"
	now := time.Now().Truncate(time.Second)

	// Mock DB expectations for publishing snapshot
	draftRows := mockDB.NewRows([]string{
		"draft_id", "package_name", "module_name", "source_rego", "status", "created_by", "created_at", "updated_at",
	}).AddRow(policyID, "aegis.authz", "rules.rego", candidateRego, "draft", "admin", now, now)

	mockDB.ExpectQuery(regexp.QuoteMeta("SELECT draft_id, package_name, module_name, source_rego, status, created_by, created_at, updated_at")).
		WithArgs(policyID).
		WillReturnRows(draftRows)

	routeRows := mockDB.NewRows([]string{
		"route_id", "service_id", "http_method", "path_template", "upstream_url", "upstream_spiffe_id",
		"rate_limit_rps", "rate_limit_burst", "timeout_ms", "requires_workload_mtls", "created_at", "updated_at",
	}).AddRow("payments.create", "payments", "POST", "/api/payments", "https://payments:8082", nil, 100, 200, 5000, false, now, now)

	mockDB.ExpectQuery(regexp.QuoteMeta("SELECT route_id, service_id, http_method, path_template")).
		WillReturnRows(routeRows)

	mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO snapshots")).
		WithArgs(int64(2), int32(1), pgxmock.AnyArg(), pgxmock.AnyArg(), "test-signing-key-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "admin").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	mockDB.ExpectExec(regexp.QuoteMeta("UPDATE policy_drafts")).
		WithArgs(policyID, candidateRego, "rules.rego", "aegis.authz", "published").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	pubHeaders := map[string]string{
		"If-Match":        `"1"`, // Current active snapshot version is 1
		"Idempotency-Key": uuid.NewString(),
	}
	pubResp := doAuthRequest(http.MethodPost, "/control/v1/policies/"+policyID+"/publish", []byte(`{}`), pubHeaders)
	defer pubResp.Body.Close()
	require.Equal(t, http.StatusOK, pubResp.StatusCode)

	var pubData controlv1.PolicyPublishResponse
	err = json.NewDecoder(pubResp.Body).Decode(&pubData)
	require.NoError(t, err)
	assert.Equal(t, int64(2), pubData.SnapshotVersion, "Snapshot version must monotonically increment to 2")
	assert.Equal(t, `"2"`, pubResp.Header.Get("ETag"))

	cpMetrics.RecordSnapshotPublish()

	// Wait for Gateway to receive snapshot v2 over gRPC stream, verify, apply, and ack
	require.Eventually(t, func() bool {
		statuses := ackTracker.GetReplicaStatuses()
		ack, ok := statuses[gatewayID]
		return ok && ack.ActiveVersion == 2
	}, 3*time.Second, 50*time.Millisecond, "Gateway replica failed to converge to snapshot v2")

	// =========================================================================
	// Step 5: Verify /control/v1/gateways reports replica active version and healthy status
	// =========================================================================
	mockDB.ExpectQuery(regexp.QuoteMeta("SELECT gateway_id, active_version, status, error_message")).
		WillReturnRows(mockDB.NewRows([]string{
			"gateway_id", "active_version", "status", "error_message", "lease_expires_at", "last_heartbeat_at", "connected_at",
		}))

	gwListResp := doAuthRequest(http.MethodGet, "/control/v1/gateways", nil, nil)
	defer gwListResp.Body.Close()
	require.Equal(t, http.StatusOK, gwListResp.StatusCode)

	var gwListData controlv1.GatewayListResponse
	err = json.NewDecoder(gwListResp.Body).Decode(&gwListData)
	require.NoError(t, err)
	require.NotEmpty(t, gwListData.Gateways)

	replica := gwListData.Gateways[0]
	assert.Equal(t, gatewayID, replica.GatewayId)
	assert.Equal(t, int64(2), replica.ActiveVersion)
	assert.Equal(t, controlv1.Healthy, replica.Status)

	// =========================================================================
	// Step 6: Submit emergency quarantine via POST /control/v1/principals/{id}/quarantine
	// =========================================================================
	quarantineSubject := "user_malicious_attacker_99"
	quarantineBody := `{"reason":"Brute-force credential stuffing attack detected"}`
	quarResp := doAuthRequest(
		http.MethodPost,
		"/control/v1/principals/"+quarantineSubject+"/quarantine",
		[]byte(quarantineBody),
		map[string]string{"Idempotency-Key": uuid.NewString()},
	)
	defer quarResp.Body.Close()
	require.Equal(t, http.StatusOK, quarResp.StatusCode)

	var quarRecord controlv1.QuarantineRecord
	err = json.NewDecoder(quarResp.Body).Decode(&quarRecord)
	require.NoError(t, err)
	assert.Equal(t, quarantineSubject, quarRecord.PrincipalId)
	assert.Equal(t, controlv1.Active, quarRecord.Status)

	// Verify quarantine key written directly to Redis
	redisVal, err := rdb.Get(context.Background(), "quarantine:principal:"+quarantineSubject).Result()
	require.NoError(t, err)
	assert.Equal(t, "Brute-force credential stuffing attack detected", redisVal)

	// Gateway Ingress request for quarantined subject must immediately return HTTP 403
	gwReq, err := http.NewRequest(http.MethodGet, gwHttpServer.URL+"/api/orders", nil)
	require.NoError(t, err)
	gwReq.Header.Set("X-Principal-ID", quarantineSubject)

	gwDeniedResp, err := http.DefaultClient.Do(gwReq)
	require.NoError(t, err)
	defer gwDeniedResp.Body.Close()

	assert.Equal(t, http.StatusForbidden, gwDeniedResp.StatusCode)
	deniedBody, err := io.ReadAll(gwDeniedResp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(deniedBody), "PRINCIPAL_QUARANTINED")

	// Regular non-quarantined subject succeeds through gateway
	gwReqOk, err := http.NewRequest(http.MethodGet, gwHttpServer.URL+"/api/orders", nil)
	require.NoError(t, err)
	gwReqOk.Header.Set("X-Principal-ID", "user_legitimate_42")

	gwOkResp, err := http.DefaultClient.Do(gwReqOk)
	require.NoError(t, err)
	defer gwOkResp.Body.Close()
	assert.Equal(t, http.StatusOK, gwOkResp.StatusCode)

	// =========================================================================
	// Step 7: Query /control/v1/audit-events
	// =========================================================================
	eventID := uuid.New()
	reqUUID := uuid.New()
	method := "GET"
	path := "/api/orders"
	durMs := 1.25
	httpStatus := 200
	kind := "user"
	auditRows := mockDB.NewRows([]string{
		"event_id", "event_type", "request_id", "timestamp", "principal_id", "principal_kind",
		"roles", "service_id", "route_id", "http_method", "request_path", "decision", "reason_code",
		"snapshot_version", "http_status", "duration_ms",
	}).AddRow(
		eventID, "decision", reqUUID, now, "user_legitimate_42", &kind,
		[]byte(`["orders.read"]`), "orders", "orders.list", &method, &path, "allow", "RULE_AUTHORIZED",
		int64(2), &httpStatus, &durMs,
	)

	mockDB.ExpectQuery(regexp.QuoteMeta("SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind")).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), 51).
		WillReturnRows(auditRows)

	auditResp := doAuthRequest(http.MethodGet, "/control/v1/audit-events", nil, nil)
	defer auditResp.Body.Close()
	require.Equal(t, http.StatusOK, auditResp.StatusCode)

	var auditListData controlv1.AuditEventListResponse
	err = json.NewDecoder(auditResp.Body).Decode(&auditListData)
	require.NoError(t, err)
	require.Len(t, auditListData.Events, 1)
	assert.Equal(t, eventID, auditListData.Events[0].EventId)
	assert.Equal(t, "user_legitimate_42", auditListData.Events[0].PrincipalId)

	// =========================================================================
	// Step 8: Scrape /metrics on private listeners (:9091 / :9092)
	// =========================================================================
	// Scrape Control Plane metrics
	cpScrapeResp, err := http.Get(cpMetricsTS.URL + "/metrics")
	require.NoError(t, err)
	defer cpScrapeResp.Body.Close()
	require.Equal(t, http.StatusOK, cpScrapeResp.StatusCode)

	cpScrapeBytes, err := io.ReadAll(cpScrapeResp.Body)
	require.NoError(t, err)
	cpScrapeOutput := string(cpScrapeBytes)

	assert.Contains(t, cpScrapeOutput, "aegis_control_plane_snapshot_publish_total")

	// Scrape Gateway metrics
	gwScrapeResp, err := http.Get(gwMetricsTS.URL + "/metrics")
	require.NoError(t, err)
	defer gwScrapeResp.Body.Close()
	require.Equal(t, http.StatusOK, gwScrapeResp.StatusCode)

	gwScrapeBytes, err := io.ReadAll(gwScrapeResp.Body)
	require.NoError(t, err)
	gwScrapeOutput := string(gwScrapeBytes)

	assert.Contains(t, gwScrapeOutput, "aegis_http_requests_total")
	assert.Contains(t, gwScrapeOutput, "aegis_policy_decisions_total")

	// Negative label security assertion: zero forbidden PII or raw parameters
	forbiddenLabels := []string{"principal_id", "client_ip", "user_id", quarantineSubject}
	for _, forbidden := range forbiddenLabels {
		assert.NotContains(t, cpScrapeOutput, forbidden, "Control plane metrics must not contain PII or unparameterized tokens")
		assert.NotContains(t, gwScrapeOutput, forbidden, "Gateway metrics must not contain PII or unparameterized tokens")
	}
}
