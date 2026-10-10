package control

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const validPublishRego = `package aegis.authz

default decision := {"allow": false, "reason_code": "DENIED_DEFAULT"}

decision := {"allow": true, "reason_code": "RULE_ALLOW"} if {
    input.identity.roles[_] == "admin"
}

test_admin_allowed if {
    d := decision with input as {"identity": {"roles": ["admin"]}}
    d.allow == true
}
`

const failingTestRego = `package aegis.authz

default decision := {"allow": false, "reason_code": "DENIED_DEFAULT"}

test_always_fails if {
    d := decision with input as {"identity": {"roles": ["guest"]}}
    d.allow == true
}
`

const draftSelect = "SELECT draft_id, package_name, module_name, source_rego, status, created_by, created_at, updated_at FROM policy_drafts WHERE draft_id = $1"

var routeCols = []string{
	"route_id", "service_id", "http_method", "path_template", "upstream_url", "upstream_spiffe_id",
	"rate_limit_rps", "rate_limit_burst", "timeout_ms", "requires_workload_mtls", "created_at", "updated_at",
}

func expectDraft(h *testHarness, id, src string) {
	now := time.Now().Truncate(time.Second)
	rows := h.mockDB.NewRows([]string{
		"draft_id", "package_name", "module_name", "source_rego", "status", "created_by", "created_at", "updated_at",
	}).AddRow(id, "aegis.authz", "authz.rego", src, "draft", "admin", now, now)
	h.mockDB.ExpectQuery(regexp.QuoteMeta(draftSelect)).WithArgs(id).WillReturnRows(rows)
}

func expectPublishTail(h *testHarness, id, src string, version int64) {
	h.mockDB.ExpectQuery(regexp.QuoteMeta("SELECT route_id, service_id, http_method, path_template")).
		WillReturnRows(h.mockDB.NewRows(routeCols))
	h.mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO snapshots")).
		WithArgs(version, int32(1), pgxmock.AnyArg(), pgxmock.AnyArg(), "test-key-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "admin").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	h.mockDB.ExpectExec(regexp.QuoteMeta("UPDATE policy_drafts")).
		WithArgs(id, src, "authz.rego", "aegis.authz", "published").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
}

func doPublish(t *testing.T, h *testHarness, id, ifMatch string) *httptest.ResponseRecorder {
	handler := h.server.Handler()
	cookie, csrf := loginOperator(t, handler, "admin", "admin-secret")
	req := httptest.NewRequest(http.MethodPost, "/control/v1/policies/"+id+"/publish", bytes.NewBufferString(`{}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("If-Match", ifMatch)
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func activePayload(t *testing.T, h *testHarness) *snapshotv1.SnapshotPayload {
	active := h.distServer.GetActiveSnapshotInternal()
	require.NotNil(t, active)
	var p snapshotv1.SnapshotPayload
	require.NoError(t, proto.Unmarshal(active.Payload, &p))
	return &p
}

func TestPublish_UnknownDraft404(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	h.mockDB.ExpectQuery(regexp.QuoteMeta(draftSelect)).WithArgs("nope").WillReturnError(pgx.ErrNoRows)

	rec := doPublish(t, h, "nope", `"0"`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	require.NoError(t, h.mockDB.ExpectationsWereMet())
}

func TestPublish_SaveDraftThenPublish200(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	handler := h.server.Handler()
	cookie, csrf := loginOperator(t, handler, "admin", "admin-secret")

	h.mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO policy_drafts")).
		WithArgs("mvp-authz", "aegis.authz", "authz.rego", validPublishRego, "draft", "admin").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	body := fmt.Sprintf(`{"policy_id":"mvp-authz","name":"authz.rego","source_rego":%q}`, validPublishRego)
	req := httptest.NewRequest(http.MethodPost, "/control/v1/policies", bytes.NewBufferString(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	expectDraft(h, "mvp-authz", validPublishRego)
	expectPublishTail(h, "mvp-authz", validPublishRego, 1)
	pub := doPublish(t, h, "mvp-authz", `"0"`)
	assert.Equal(t, http.StatusOK, pub.Code)
	assert.Equal(t, `"1"`, pub.Header().Get("ETag"))
	require.NoError(t, h.mockDB.ExpectationsWereMet())
}

func TestPublishValidation_InvalidRego422(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	expectDraft(h, "bad", "this is not rego {{{")

	rec := doPublish(t, h, "bad", `"0"`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Nil(t, h.distServer.GetActiveSnapshotInternal())
	require.NoError(t, h.mockDB.ExpectationsWereMet())
}

func TestPublishValidation_FailingTestRule422(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	expectDraft(h, "failing", failingTestRego)

	rec := doPublish(t, h, "failing", `"0"`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Nil(t, h.distServer.GetActiveSnapshotInternal())
	require.NoError(t, h.mockDB.ExpectationsWereMet())
}

func TestPublish_IdentityMappings_Configured(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	h.server.SetIdentityMappings([]*snapshotv1.IdentityMapping{
		{Role: "zeta", Permissions: []string{"z.read"}},
		{Role: "alpha", Permissions: []string{"a.read", "a.write"}},
	})
	expectDraft(h, "p1", validPublishRego)
	expectPublishTail(h, "p1", validPublishRego, 1)

	rec := doPublish(t, h, "p1", `"0"`)
	require.Equal(t, http.StatusOK, rec.Code)
	p := activePayload(t, h)
	require.Len(t, p.IdentityMappings, 2)
	assert.Equal(t, "alpha", p.IdentityMappings[0].Role)
	assert.Equal(t, "zeta", p.IdentityMappings[1].Role)
	assert.Equal(t, []string{"a.read", "a.write"}, p.IdentityMappings[0].Permissions)
}

func TestPublish_IdentityMappings_CarryForward(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	h.server.SetIdentityMappings([]*snapshotv1.IdentityMapping{{Role: "dev", Permissions: []string{"x"}}})
	expectDraft(h, "p1", validPublishRego)
	expectPublishTail(h, "p1", validPublishRego, 1)
	require.Equal(t, http.StatusOK, doPublish(t, h, "p1", `"0"`).Code)

	// Clear configured set: second publish must carry forward from the active snapshot.
	h.server.SetIdentityMappings(nil)
	expectDraft(h, "p1", validPublishRego)
	expectPublishTail(h, "p1", validPublishRego, 2)
	require.Equal(t, http.StatusOK, doPublish(t, h, "p1", `"1"`).Code)

	p := activePayload(t, h)
	assert.Equal(t, int64(2), p.Version)
	require.Len(t, p.IdentityMappings, 1)
	assert.Equal(t, "dev", p.IdentityMappings[0].Role)
}

func TestPublish_IdentityMappings_Rollback(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	handler := h.server.Handler()
	cookie, csrf := loginOperator(t, handler, "admin", "admin-secret")

	v1 := &snapshotv1.SnapshotPayload{
		Version:          1,
		IdentityMappings: []*snapshotv1.IdentityMapping{{Role: "dev", Permissions: []string{"x"}}},
	}
	v1Bytes, err := proto.Marshal(v1)
	require.NoError(t, err)
	// Seed the active snapshot at v1 so If-Match "1" matches.
	env1, err := h.signer.SignSnapshot(v1)
	require.NoError(t, err)
	h.distServer.BroadcastSnapshot(env1)
	now := time.Now().Truncate(time.Second)
	cols := []string{"version", "schema_version", "payload_sha256", "payload_bytes", "signing_key_id", "signature", "created_at", "expires_at"}
	h.mockDB.ExpectQuery(regexp.QuoteMeta("FROM snapshots WHERE version = $1")).WithArgs(int64(1)).
		WillReturnRows(h.mockDB.NewRows(cols).AddRow(int64(1), 1, "sha", v1Bytes, "test-key-1", []byte("sig"), now, now))
	h.mockDB.ExpectQuery(regexp.QuoteMeta("FROM snapshots ORDER BY version DESC LIMIT 1")).
		WillReturnRows(h.mockDB.NewRows(cols).AddRow(int64(1), 1, "sha", v1Bytes, "test-key-1", []byte("sig"), now, now))
	h.mockDB.ExpectExec(regexp.QuoteMeta("INSERT INTO snapshots")).
		WithArgs(int64(2), int32(1), pgxmock.AnyArg(), pgxmock.AnyArg(), "test-key-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "admin").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	req := httptest.NewRequest(http.MethodPost, "/control/v1/policies/p1/rollback",
		bytes.NewBufferString(`{"target_version_id":"1","comment":"rb"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("If-Match", `"1"`)
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	p := activePayload(t, h)
	assert.Equal(t, int64(2), p.Version)
	require.Len(t, p.IdentityMappings, 1)
	assert.Equal(t, "dev", p.IdentityMappings[0].Role)
}

func TestPublish_NilRepos503(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	h.server.policyRepo = nil

	rec := doPublish(t, h, "p1", `"0"`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestCreatePolicy_InvalidID400(t *testing.T) {
	h := setupTestHarness(t)
	defer h.mockDB.Close()
	handler := h.server.Handler()
	cookie, csrf := loginOperator(t, handler, "admin", "admin-secret")

	for _, id := range []string{"", "bad id/../x"} {
		body := fmt.Sprintf(`{"policy_id":%q,"name":"a.rego","source_rego":"package x"}`, id)
		req := httptest.NewRequest(http.MethodPost, "/control/v1/policies", bytes.NewBufferString(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Idempotency-Key", uuid.NewString())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "id=%q", id)
	}
}
