package control

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"aegis/internal/storage"

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

// hookedListRepo lets a test run code in the middle of the prune-side
// ListUnexpiredQuarantines call of Reconcile (the second call per pass).
type hookedListRepo struct {
	*fakeSecRepo
	calls int
	hook  func(list func() []storage.QuarantineRecord) []storage.QuarantineRecord
}

func (h *hookedListRepo) ListUnexpiredQuarantines(ctx context.Context) ([]storage.QuarantineRecord, error) {
	h.calls++
	if h.hook != nil && h.calls == 2 {
		hook := h.hook
		h.hook = nil
		return hook(func() []storage.QuarantineRecord {
			out, _ := h.fakeSecRepo.ListUnexpiredQuarantines(ctx)
			return out
		}), nil
	}
	return h.fakeSecRepo.ListUnexpiredQuarantines(ctx)
}

// TestWriteThroughRequarantineSurvivesConcurrentReconcilePrune reproduces
// WR-01: Reconcile snapshots Redis (K present), an operator unquarantines K,
// Reconcile lists Postgres (K absent), the operator re-quarantines K (200),
// and the prune then deletes the fresh Redis key. With the write-through
// serialised against Reconcile, the operator writes wait for the pass to
// finish and K stays quarantined.
func TestWriteThroughRequarantineSurvivesConcurrentReconcilePrune(t *testing.T) {
	e := newWTEnv(t)
	repo := &hookedListRepo{fakeSecRepo: e.repo}
	ss := NewSecurityState(repo, e.h.server.revStore)
	e.h.server.SetSecurityState(ss)
	ctx := context.Background()
	require.NoError(t, ss.Reconstruct(ctx))

	const key = "quarantine:principal:evil"
	require.Equal(t, http.StatusOK, e.do(http.MethodPost, "/control/v1/principals/evil/quarantine", `{"reason":"x"}`).Code)
	require.True(t, e.redisK(key))

	unqDone := make(chan struct{})
	listed := make(chan struct{})
	opsDone := make(chan [2]int, 1)
	repo.calls = 0
	repo.hook = func(list func() []storage.QuarantineRecord) []storage.QuarantineRecord {
		go func() {
			c1 := e.do(http.MethodDelete, "/control/v1/principals/evil/quarantine", "").Code
			close(unqDone)
			<-listed
			c2 := e.do(http.MethodPost, "/control/v1/principals/evil/quarantine", `{"reason":"again"}`).Code
			opsDone <- [2]int{c1, c2}
		}()
		// Unserialised, the unquarantine completes here; serialised, it
		// blocks on the SecurityState mutex until Reconcile returns.
		select {
		case <-unqDone:
		case <-time.After(300 * time.Millisecond):
		}
		rows := list()
		close(listed)
		select {
		case codes := <-opsDone:
			opsDone <- codes
		case <-time.After(300 * time.Millisecond):
		}
		return rows
	}

	require.NoError(t, ss.Reconcile(ctx))

	var codes [2]int
	select {
	case codes = <-opsDone:
	case <-time.After(5 * time.Second):
		t.Fatal("operator writes did not complete")
	}
	assert.Equal(t, [2]int{http.StatusOK, http.StatusOK}, codes)
	assert.True(t, e.redisK(key), "fresh re-quarantine must not be pruned by a concurrent reconcile")
	assert.Contains(t, e.repo.q, "evil")
	assert.Equal(t, "again", e.repo.q["evil"].Reason)
}

// WR-03: inputs that cannot fit the security-state schema, or whose TTL
// would overflow, are rejected with 400 before any Postgres or Redis write.
func TestWriteThroughRevokeRejectsOutOfBoundsInput(t *testing.T) {
	e := newWTEnv(t)
	long := strings.Repeat("j", maxJTILen+1)
	rec := e.do(http.MethodPost, "/control/v1/revocations", `{"jti":"`+long+`","ttl_seconds":600}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, e.repo.t)
	assert.False(t, e.redisK("revocation:jti:"+long))

	for _, ttl := range []string{"2592001", "9300000000", "9223372036854775807"} {
		rec = e.do(http.MethodPost, "/control/v1/revocations", `{"jti":"abc","ttl_seconds":`+ttl+`}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "ttl_seconds=%s", ttl)
	}
	assert.Empty(t, e.repo.t)
	assert.False(t, e.redisK("revocation:jti:abc"))

	exact := strings.Repeat("j", maxJTILen)
	rec = e.do(http.MethodPost, "/control/v1/revocations", `{"jti":"`+exact+`","ttl_seconds":2592000}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, e.repo.t, exact)
	assert.True(t, e.repo.t[exact].ExpiresAt.After(time.Now().Add(29*24*time.Hour)))
}

func TestOperatorActorTruncatedToColumnWidth(t *testing.T) {
	mk := func(name string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		return r.WithContext(context.WithValue(r.Context(), SessionContextKey, &Session{Username: name}))
	}
	assert.Equal(t, "sec-ops", operatorActor(httptest.NewRequest(http.MethodPost, "/", nil)))
	assert.Equal(t, "admin", operatorActor(mk("admin")))
	got := operatorActor(mk(strings.Repeat("a", 100)))
	assert.Len(t, got, maxActorLen)
	// Multi-byte runes are never split.
	got = operatorActor(mk(strings.Repeat("a", 63) + "éé"))
	assert.Equal(t, strings.Repeat("a", 63), got)
	assert.True(t, utf8.ValidString(got))
}
