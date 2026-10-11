package control

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wtEnv struct {
	h      *testHarness
	repo   *fakeSecRepo
	do     func(method, path, body string) *httptest.ResponseRecorder
	redisK func(k string) bool
}

func newWTEnv(t *testing.T) *wtEnv {
	h := setupTestHarness(t)
	t.Cleanup(func() { h.mockDB.Close() })
	repo := newFakeSecRepo()
	h.server.SetSecurityRepo(repo)
	handler := h.server.Handler()
	cookie, csrf := loginOperator(t, handler, "admin", "admin-secret")
	return &wtEnv{
		h: h, repo: repo,
		do: func(method, path, body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
			req.Header.Set("Idempotency-Key", uuid.NewString())
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			return rec
		},
		redisK: func(k string) bool { return h.mr.Exists(k) },
	}
}

func TestWriteThroughQuarantinePGFailureWritesNoRedis(t *testing.T) {
	e := newWTEnv(t)
	e.repo.err = errors.New("pg down")
	rec := e.do(http.MethodPost, "/control/v1/principals/evil/quarantine", `{"reason":"x"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.False(t, e.redisK("quarantine:principal:evil"))
}

func TestWriteThroughQuarantineRedisFailureKeepsPGRowAndRetries(t *testing.T) {
	e := newWTEnv(t)
	e.h.mr.SetError("boom")
	rec := e.do(http.MethodPost, "/control/v1/principals/evil/quarantine", `{"reason":"x"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, e.repo.q, "evil")
	assert.Equal(t, "admin", e.repo.q["evil"].CreatedBy)

	e.h.mr.SetError("")
	rec = e.do(http.MethodPost, "/control/v1/principals/evil/quarantine", `{"reason":"x"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, e.redisK("quarantine:principal:evil"))
	assert.Contains(t, e.repo.q, "evil")
}

func TestWriteThroughQuarantineSuccessWritesBoth(t *testing.T) {
	e := newWTEnv(t)
	rec := e.do(http.MethodPost, "/control/v1/principals/evil/quarantine", `{"reason":"leak"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "leak", e.repo.q["evil"].Reason)
	assert.True(t, e.redisK("quarantine:principal:evil"))
}

func TestWriteThroughUnquarantinePGFailureKeepsRedis(t *testing.T) {
	e := newWTEnv(t)
	require.Equal(t, http.StatusOK, e.do(http.MethodPost, "/control/v1/principals/evil/quarantine", `{"reason":"x"}`).Code)

	e.repo.err = errors.New("pg down")
	rec := e.do(http.MethodDelete, "/control/v1/principals/evil/quarantine", "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.True(t, e.redisK("quarantine:principal:evil"))

	e.repo.err = nil
	rec = e.do(http.MethodDelete, "/control/v1/principals/evil/quarantine", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, e.redisK("quarantine:principal:evil"))
	assert.NotContains(t, e.repo.q, "evil")
	// idempotent on a missing principal
	assert.Equal(t, http.StatusOK, e.do(http.MethodDelete, "/control/v1/principals/nobody/quarantine", "").Code)
}

func TestWriteThroughRevokePGFirst(t *testing.T) {
	e := newWTEnv(t)
	e.repo.err = errors.New("pg down")
	rec := e.do(http.MethodPost, "/control/v1/revocations", `{"jti":"abc","ttl_seconds":600}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.False(t, e.redisK("revocation:jti:abc"))

	e.repo.err = nil
	e.h.mr.SetError("boom")
	rec = e.do(http.MethodPost, "/control/v1/revocations", `{"jti":"abc","ttl_seconds":600}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, e.repo.t, "abc")

	e.h.mr.SetError("")
	rec = e.do(http.MethodPost, "/control/v1/revocations", `{"jti":"abc","ttl_seconds":600}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, e.redisK("revocation:jti:abc"))
}
