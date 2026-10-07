package control

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"aegis/internal/revocation"
	"aegis/internal/snapshot"
	"aegis/internal/storage"
	controlv1 "aegis/pkg/api/control/v1"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type testHarness struct {
	server       *APIServer
	mockDB       pgxmock.PgxPoolIface
	mr           *miniredis.Miniredis
	rdb          *redis.Client
	revStore     *revocation.Store
	signer       *snapshot.Signer
	sessionMgr   *SessionManager
	routeRepo    *storage.RouteRepo
	policyRepo   *storage.PolicyRepo
	snapshotRepo *storage.SnapshotRepo
	auditRepo    *storage.AuditRepo
	distServer   *SnapshotDistributionServer
}

func setupTestHarness(t *testing.T) *testHarness {
	mockDB, err := pgxmock.NewPool()
	require.NoError(t, err)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	revStore := revocation.NewStore(rdb)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer := snapshot.NewSigner(priv, "test-key-1")

	sessionMgr := NewSessionManager()
	idempStore := NewIdempotencyStore()
	validator := NewValidator()

	routeRepo := storage.NewRouteRepo(mockDB)
	policyRepo := storage.NewPolicyRepo(mockDB)
	snapshotRepo := storage.NewSnapshotRepo(mockDB)
	auditRepo := storage.NewAuditRepo(mockDB)

	rollbackEng := NewRollbackEngine(snapshotRepo, signer)
	distServer := NewSnapshotDistributionServer(nil, NewAckTracker(snapshotRepo))

	apiServer := NewAPIServer(
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

	return &testHarness{
		server:       apiServer,
		mockDB:       mockDB,
		mr:           mr,
		rdb:          rdb,
		revStore:     revStore,
		signer:       signer,
		sessionMgr:   sessionMgr,
		routeRepo:    routeRepo,
		policyRepo:   policyRepo,
		snapshotRepo: snapshotRepo,
		auditRepo:    auditRepo,
		distServer:   distServer,
	}
}

func loginOperator(t *testing.T, handler http.Handler, username, password string) (*http.Cookie, string) {
	loginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/control/v1/auth/login", bytes.NewBufferString(loginBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var loginResp LoginResponse
	err := json.Unmarshal(rec.Body.Bytes(), &loginResp)
	require.NoError(t, err)
	require.NotEmpty(t, loginResp.CSRFToken)

	cookies := rec.Result().Cookies()
	require.NotEmpty(t, cookies)
	return cookies[0], loginResp.CSRFToken
}

func TestControlAPI(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()

	handler := h.server.Handler()

	// 1. Operator Login
	cookie, csrfToken := loginOperator(t, handler, "admin", "admin-secret")

	t.Run("Security Headers & Invariant 11 Data Plane Token Rejection", func(t *testing.T) {
		// Invariant 11 rejection
		req := httptest.NewRequest(http.MethodGet, "/control/v1/routes", nil)
		req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiIs...")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Contains(t, rec.Body.String(), "DATA_PLANE_CREDENTIALS_REJECTED")

		// Verify CSP and security headers
		assert.NotEmpty(t, rec.Header().Get("Content-Security-Policy"))
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	})

	t.Run("List Routes and Create Route", func(t *testing.T) {
		now := time.Now().Truncate(time.Second)

		// Mock RouteRepo.ListRoutes
		routeRows := h.mockDB.NewRows([]string{
			"route_id", "service_id", "http_method", "path_template", "upstream_url", "upstream_spiffe_id",
			"rate_limit_rps", "rate_limit_burst", "timeout_ms", "requires_workload_mtls", "created_at", "updated_at",
		}).AddRow("route-orders", "orders", "GET", "/api/orders", "https://orders:8081",
			"spiffe://aegis.local/ns/default/sa/orders", 100, 200, 5000, true, now, now)

		h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT route_id, service_id, http_method, path_template")).
			WillReturnRows(routeRows)

		getReq := httptest.NewRequest(http.MethodGet, "/control/v1/routes", nil)
		getReq.AddCookie(cookie)
		getRec := httptest.NewRecorder()
		handler.ServeHTTP(getRec, getReq)

		assert.Equal(t, http.StatusOK, getRec.Code)
		assert.NotEmpty(t, getRec.Header().Get("ETag"))

		var listResp controlv1.RouteListResponse
		err := json.Unmarshal(getRec.Body.Bytes(), &listResp)
		require.NoError(t, err)
		require.Len(t, listResp.Routes, 1)
		assert.Equal(t, "route-orders", listResp.Routes[0].RouteId)

		// Create Route
		h.mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO services")).
			WithArgs("payments", "production", true, pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		h.mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO routes")).
			WithArgs("route-payments", "payments", "POST", "/api/payments", "https://payments:8082",
				"spiffe://aegis.local/ns/default/sa/payments", 50, 100, 3000, true).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		createBody := `{
			"route_id": "route-payments",
			"service_id": "payments",
			"http_method": "POST",
			"path_template": "/api/payments",
			"upstream_url": "https://payments:8082",
			"upstream_spiffe_id": "spiffe://aegis.local/ns/default/sa/payments",
			"rate_limit_rps": 50,
			"rate_limit_burst": 100,
			"timeout_ms": 3000,
			"requires_workload_mtls": true
		}`
		postReq := httptest.NewRequest(http.MethodPost, "/control/v1/routes", bytes.NewBufferString(createBody))
		postReq.AddCookie(cookie)
		postReq.Header.Set("X-CSRF-Token", csrfToken)
		postRec := httptest.NewRecorder()
		handler.ServeHTTP(postRec, postReq)

		assert.Equal(t, http.StatusCreated, postRec.Code)
		assert.NotEmpty(t, postRec.Header().Get("ETag"))
	})

	t.Run("Principal Emergency Quarantine and Unquarantine", func(t *testing.T) {
		// Quarantine principal
		qBody := `{"reason":"Credential leaked on pastebin"}`
		qReq := httptest.NewRequest(http.MethodPost, "/control/v1/principals/evil-user/quarantine", bytes.NewBufferString(qBody))
		qReq.AddCookie(cookie)
		qReq.Header.Set("X-CSRF-Token", csrfToken)
		qRec := httptest.NewRecorder()
		handler.ServeHTTP(qRec, qReq)

		assert.Equal(t, http.StatusOK, qRec.Code)

		var qRecord controlv1.QuarantineRecord
		err := json.Unmarshal(qRec.Body.Bytes(), &qRecord)
		require.NoError(t, err)
		assert.Equal(t, "evil-user", qRecord.PrincipalId)
		assert.Equal(t, "Credential leaked on pastebin", qRecord.Reason)
		assert.Equal(t, controlv1.Active, qRecord.Status)

		// Verify directly in Redis
		val, err := h.rdb.Get(t.Context(), "quarantine:principal:evil-user").Result()
		require.NoError(t, err)
		assert.Equal(t, "Credential leaked on pastebin", val)

		// Unquarantine
		unqReq := httptest.NewRequest(http.MethodDelete, "/control/v1/principals/evil-user/quarantine", nil)
		unqReq.AddCookie(cookie)
		unqReq.Header.Set("X-CSRF-Token", csrfToken)
		unqRec := httptest.NewRecorder()
		handler.ServeHTTP(unqRec, unqReq)

		assert.Equal(t, http.StatusOK, unqRec.Code)

		// Verify deleted from Redis
		exists, err := h.rdb.Exists(t.Context(), "quarantine:principal:evil-user").Result()
		require.NoError(t, err)
		assert.Equal(t, int64(0), exists)
	})

	t.Run("Token JTI Revocation", func(t *testing.T) {
		revBody := `{"jti":"compromised-jti-999","reason":"Compromised bearer token","ttl_seconds":3600}`
		revReq := httptest.NewRequest(http.MethodPost, "/control/v1/revocations", bytes.NewBufferString(revBody))
		revReq.AddCookie(cookie)
		revReq.Header.Set("X-CSRF-Token", csrfToken)
		revRec := httptest.NewRecorder()
		handler.ServeHTTP(revRec, revReq)

		assert.Equal(t, http.StatusOK, revRec.Code)

		// Verify in Redis
		val, err := h.rdb.Get(t.Context(), "revocation:jti:compromised-jti-999").Result()
		require.NoError(t, err)
		assert.Equal(t, "1", val)
	})

	t.Run("Gateway Replica Convergence Topology", func(t *testing.T) {
		now := time.Now()
		ackRows := h.mockDB.NewRows([]string{
			"gateway_id", "active_version", "status", "error_message", "lease_expires_at", "last_heartbeat_at", "connected_at",
		}).AddRow("gw-east-1", int64(1), "healthy", nil, &now, now, now)

		h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT gateway_id, active_version, status, error_message")).
			WillReturnRows(ackRows)

		gwReq := httptest.NewRequest(http.MethodGet, "/control/v1/gateways", nil)
		gwReq.AddCookie(cookie)
		gwRec := httptest.NewRecorder()
		handler.ServeHTTP(gwRec, gwReq)

		assert.Equal(t, http.StatusOK, gwRec.Code)

		var gwResp controlv1.GatewayListResponse
		err := json.Unmarshal(gwRec.Body.Bytes(), &gwResp)
		require.NoError(t, err)
		require.Len(t, gwResp.Gateways, 1)
		assert.Equal(t, "gw-east-1", gwResp.Gateways[0].GatewayId)
		assert.Equal(t, controlv1.Healthy, gwResp.Gateways[0].Status)
	})
}

func TestPolicyAndSimulationAPI(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()

	handler := h.server.Handler()
	cookie, csrfToken := loginOperator(t, handler, "admin", "admin-secret")

	candidateRego := `package aegis.authz

default decision := {"allow": false, "reason_code": "DENIED_DEFAULT"}

decision := {"allow": true, "reason_code": "RULE_ALLOW"} if {
    input.identity.roles[_] == "admin"
}

test_admin_allowed if {
    d := decision with input as {"identity": {"roles": ["admin"]}}
    d.allow == true
}

test_guest_denied if {
    d := decision with input as {"identity": {"roles": ["guest"]}}
    d.allow == false
}
`

	t.Run("Create Policy Draft", func(t *testing.T) {
		h.mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO policy_drafts")).
			WithArgs("policy-authz", "aegis.authz", "authz.rego", candidateRego, "draft", "admin").
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		draftBody := fmt.Sprintf(`{
			"policy_id": "policy-authz",
			"name": "authz.rego",
			"source_rego": %q
		}`, candidateRego)

		draftReq := httptest.NewRequest(http.MethodPost, "/control/v1/policies", bytes.NewBufferString(draftBody))
		draftReq.AddCookie(cookie)
		draftReq.Header.Set("X-CSRF-Token", csrfToken)
		draftRec := httptest.NewRecorder()
		handler.ServeHTTP(draftRec, draftReq)

		assert.Equal(t, http.StatusCreated, draftRec.Code)

		var draftResp controlv1.PolicyDraft
		err := json.Unmarshal(draftRec.Body.Bytes(), &draftResp)
		require.NoError(t, err)
		assert.Equal(t, "policy-authz", draftResp.PolicyId)
	})

	t.Run("Validate Policy with Embedded Unit Tests", func(t *testing.T) {
		valBody := fmt.Sprintf(`{"candidate_rego": %q}`, candidateRego)
		valReq := httptest.NewRequest(http.MethodPost, "/control/v1/policies/policy-authz/validate", bytes.NewBufferString(valBody))
		valReq.AddCookie(cookie)
		valReq.Header.Set("X-CSRF-Token", csrfToken)
		valRec := httptest.NewRecorder()
		handler.ServeHTTP(valRec, valReq)

		assert.Equal(t, http.StatusOK, valRec.Code)

		var valResp controlv1.PolicyValidationResponse
		err := json.Unmarshal(valRec.Body.Bytes(), &valResp)
		require.NoError(t, err)
		assert.True(t, valResp.Valid)
		assert.Equal(t, 2, valResp.TestCount)
		assert.Equal(t, 2, valResp.PassedCount)
		assert.Equal(t, 0, valResp.FailedCount)
	})

	t.Run("Dry-Run Simulation API Allow and Deny", func(t *testing.T) {
		// 1. Simulation matching rule (Allow)
		allowSimBody := fmt.Sprintf(`{
			"candidate_rego": %q,
			"input_context": {
				"identity": {"roles": ["admin"]}
			}
		}`, candidateRego)

		simReq := httptest.NewRequest(http.MethodPost, "/control/v1/policies/simulate", bytes.NewBufferString(allowSimBody))
		simReq.AddCookie(cookie)
		simReq.Header.Set("X-CSRF-Token", csrfToken)
		simRec := httptest.NewRecorder()
		handler.ServeHTTP(simRec, simReq)

		assert.Equal(t, http.StatusOK, simRec.Code)

		var simResp controlv1.PolicySimulationResponse
		err := json.Unmarshal(simRec.Body.Bytes(), &simResp)
		require.NoError(t, err)
		assert.True(t, simResp.Allow)
		assert.Equal(t, "RULE_ALLOW", simResp.ReasonCode)
		assert.Greater(t, simResp.DurationUs, 0)

		// 2. Simulation failing rule (Deny)
		denySimBody := fmt.Sprintf(`{
			"candidate_rego": %q,
			"input_context": {
				"identity": {"roles": ["guest"]}
			}
		}`, candidateRego)

		simDenyReq := httptest.NewRequest(http.MethodPost, "/control/v1/policies/simulate", bytes.NewBufferString(denySimBody))
		simDenyReq.AddCookie(cookie)
		simDenyReq.Header.Set("X-CSRF-Token", csrfToken)
		simDenyRec := httptest.NewRecorder()
		handler.ServeHTTP(simDenyRec, simDenyReq)

		assert.Equal(t, http.StatusOK, simDenyRec.Code)

		var simDenyResp controlv1.PolicySimulationResponse
		err = json.Unmarshal(simDenyRec.Body.Bytes(), &simDenyResp)
		require.NoError(t, err)
		assert.False(t, simDenyResp.Allow)
		assert.Equal(t, "DENIED_DEFAULT", simDenyResp.ReasonCode)
	})

	t.Run("Publish Policy with Optimistic Concurrency", func(t *testing.T) {
		now := time.Now().Truncate(time.Second)

		// 1. Missing If-Match -> 412
		pubReqNoIfMatch := httptest.NewRequest(http.MethodPost, "/control/v1/policies/policy-authz/publish", bytes.NewBufferString(`{}`))
		pubReqNoIfMatch.AddCookie(cookie)
		pubReqNoIfMatch.Header.Set("X-CSRF-Token", csrfToken)
		pubRecNoIfMatch := httptest.NewRecorder()
		handler.ServeHTTP(pubRecNoIfMatch, pubReqNoIfMatch)
		assert.Equal(t, http.StatusPreconditionFailed, pubRecNoIfMatch.Code)

		// 2. Mismatched If-Match -> 412
		pubReqMismatch := httptest.NewRequest(http.MethodPost, "/control/v1/policies/policy-authz/publish", bytes.NewBufferString(`{}`))
		pubReqMismatch.AddCookie(cookie)
		pubReqMismatch.Header.Set("X-CSRF-Token", csrfToken)
		pubReqMismatch.Header.Set("If-Match", `"99"`)
		pubRecMismatch := httptest.NewRecorder()
		handler.ServeHTTP(pubRecMismatch, pubReqMismatch)
		assert.Equal(t, http.StatusPreconditionFailed, pubRecMismatch.Code)
		assert.Equal(t, `"0"`, pubRecMismatch.Header().Get("ETag"))

		// 3. Valid If-Match ("0" because no snapshot exists yet) -> 200 OK
		draftRows := h.mockDB.NewRows([]string{
			"draft_id", "package_name", "module_name", "source_rego", "status", "created_by", "created_at", "updated_at",
		}).AddRow("policy-authz", "aegis.authz", "authz.rego", candidateRego, "draft", "admin", now, now)

		h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT draft_id, package_name, module_name, source_rego, status, created_by, created_at, updated_at FROM policy_drafts WHERE draft_id = $1")).
			WithArgs("policy-authz").
			WillReturnRows(draftRows)

		// RouteRepo.ListRoutes returns empty
		h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT route_id, service_id, http_method, path_template")).
			WillReturnRows(h.mockDB.NewRows([]string{
				"route_id", "service_id", "http_method", "path_template", "upstream_url", "upstream_spiffe_id",
				"rate_limit_rps", "rate_limit_burst", "timeout_ms", "requires_workload_mtls", "created_at", "updated_at",
			}))

		// SnapshotRepo.SaveSnapshot
		h.mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO snapshots")).
			WithArgs(int64(1), int32(1), pgxmock.AnyArg(), pgxmock.AnyArg(), "test-key-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "admin").
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		// PolicyRepo.UpdateDraft
		h.mockDB.ExpectExec(regexp.QuoteMeta("UPDATE policy_drafts")).
			WithArgs("policy-authz", candidateRego, "authz.rego", "aegis.authz", "published").
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		pubReq := httptest.NewRequest(http.MethodPost, "/control/v1/policies/policy-authz/publish", bytes.NewBufferString(`{}`))
		pubReq.AddCookie(cookie)
		pubReq.Header.Set("X-CSRF-Token", csrfToken)
		pubReq.Header.Set("If-Match", `"0"`)
		pubReq.Header.Set("Idempotency-Key", uuid.NewString())
		pubRec := httptest.NewRecorder()
		handler.ServeHTTP(pubRec, pubReq)

		assert.Equal(t, http.StatusOK, pubRec.Code)
		assert.Equal(t, `"1"`, pubRec.Header().Get("ETag"))

		var pubResp controlv1.PolicyPublishResponse
		err := json.Unmarshal(pubRec.Body.Bytes(), &pubResp)
		require.NoError(t, err)
		assert.Equal(t, int64(1), pubResp.SnapshotVersion)
		assert.Equal(t, "test-key-1", pubResp.SigningKeyId)
	})

	t.Run("Monotonic Rollback to Version 1 (Published as Version 2)", func(t *testing.T) {
		// Mock historical snapshot v1 payload
		v1Payload := &snapshotv1.SnapshotPayload{
			Version: 1,
			PolicyModules: []*snapshotv1.PolicyModule{
				{
					PackageName: "aegis.authz",
					ModuleName:  "authz.rego",
					SourceRego:  candidateRego,
				},
			},
		}
		v1PayloadBytes, err := proto.Marshal(v1Payload)
		require.NoError(t, err)

		now := time.Now().Truncate(time.Second)

		// 1. GetSnapshotByVersion(1)
		v1Rows := h.mockDB.NewRows([]string{
			"version", "schema_version", "payload_sha256", "payload_bytes", "signing_key_id", "signature", "created_at", "expires_at",
		}).AddRow(int64(1), 1, "sha-1", v1PayloadBytes, "test-key-1", []byte("sig-1"), now, now)

		h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at FROM snapshots WHERE version = $1")).
			WithArgs(int64(1)).
			WillReturnRows(v1Rows)

		// 2. GetLatestSnapshot() -> version 1
		latestRows := h.mockDB.NewRows([]string{
			"version", "schema_version", "payload_sha256", "payload_bytes", "signing_key_id", "signature", "created_at", "expires_at",
		}).AddRow(int64(1), 1, "sha-1", v1PayloadBytes, "test-key-1", []byte("sig-1"), now, now)

		h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at FROM snapshots ORDER BY version DESC LIMIT 1")).
			WillReturnRows(latestRows)

		// 3. SaveSnapshot(2)
		h.mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO snapshots")).
			WithArgs(int64(2), int32(1), pgxmock.AnyArg(), pgxmock.AnyArg(), "test-key-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "admin").
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		rbBody := `{"target_version_id":"1","comment":"Rollback due to regression"}`
		rbReq := httptest.NewRequest(http.MethodPost, "/control/v1/policies/policy-authz/rollback", bytes.NewBufferString(rbBody))
		rbReq.AddCookie(cookie)
		rbReq.Header.Set("X-CSRF-Token", csrfToken)
		rbReq.Header.Set("If-Match", `"1"`)
		rbReq.Header.Set("Idempotency-Key", uuid.NewString())
		rbRec := httptest.NewRecorder()
		handler.ServeHTTP(rbRec, rbReq)

		assert.Equal(t, http.StatusOK, rbRec.Code)
		assert.Equal(t, `"2"`, rbRec.Header().Get("ETag"))

		var rbResp controlv1.PolicyPublishResponse
		err = json.Unmarshal(rbRec.Body.Bytes(), &rbResp)
		require.NoError(t, err)
		assert.Equal(t, int64(2), rbResp.SnapshotVersion)
	})
}
