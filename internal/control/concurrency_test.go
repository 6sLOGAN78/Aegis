package control

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestConcurrencyAndIdempotency(t *testing.T) {
	t.Run("ETag Formatting and If-Match Verification", func(t *testing.T) {
		assert.Equal(t, `"42"`, FormatETag(42))
		resourceTag := FormatResourceETag([]byte("policy data"))
		assert.NotEmpty(t, resourceTag)
		assert.True(t, resourceTag[0] == '"' && resourceTag[len(resourceTag)-1] == '"')

		// Direct verification helper
		assert.True(t, VerifyIfMatch(`"42"`, 42))
		assert.True(t, VerifyIfMatch("42", 42))
		assert.True(t, VerifyIfMatch("*", 42))
		assert.False(t, VerifyIfMatch(`"41"`, 42))
		assert.False(t, VerifyIfMatch("41", 42))

		// RequireIfMatch helper
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !RequireIfMatch(w, r, 42) {
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("updated"))
		})

		// 1. Missing If-Match -> 412
		reqNoIfMatch := httptest.NewRequest(http.MethodPost, "/test", nil)
		recNoIfMatch := httptest.NewRecorder()
		h.ServeHTTP(recNoIfMatch, reqNoIfMatch)
		assert.Equal(t, http.StatusPreconditionFailed, recNoIfMatch.Code)
		assert.Contains(t, recNoIfMatch.Body.String(), "PRECONDITION_FAILED")

		// 2. Mismatched If-Match -> 412 with active ETag
		reqMismatch := httptest.NewRequest(http.MethodPost, "/test", nil)
		reqMismatch.Header.Set("If-Match", `"40"`)
		recMismatch := httptest.NewRecorder()
		h.ServeHTTP(recMismatch, reqMismatch)
		assert.Equal(t, http.StatusPreconditionFailed, recMismatch.Code)
		assert.Equal(t, `"42"`, recMismatch.Header().Get("ETag"))

		// 3. Matching If-Match -> 200
		reqMatch := httptest.NewRequest(http.MethodPost, "/test", nil)
		reqMatch.Header.Set("If-Match", `"42"`)
		recMatch := httptest.NewRecorder()
		h.ServeHTTP(recMatch, reqMatch)
		assert.Equal(t, http.StatusOK, recMatch.Code)
		assert.Equal(t, "updated", recMatch.Body.String())
	})

	t.Run("Idempotency Key Validation and In-Flight 409 Conflict", func(t *testing.T) {
		store := NewIdempotencyStore()

		var execCount int64
		slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&execCount, 1)
			time.Sleep(100 * time.Millisecond) // Simulate slow execution
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"status":"created"}`))
		})

		mw := store.IdempotencyMiddleware(slowHandler)

		// 1. Invalid UUID in Idempotency-Key -> 400
		reqBadKey := httptest.NewRequest(http.MethodPost, "/test", nil)
		reqBadKey.Header.Set("Idempotency-Key", "not-a-valid-uuid")
		recBadKey := httptest.NewRecorder()
		mw.ServeHTTP(recBadKey, reqBadKey)
		assert.Equal(t, http.StatusBadRequest, recBadKey.Code)
		assert.Contains(t, recBadKey.Body.String(), "INVALID_IDEMPOTENCY_KEY")

		// 2. Concurrent executions with identical Idempotency-Key -> One 201, One 409
		key := uuid.NewString()
		var wg sync.WaitGroup
		wg.Add(2)

		var code1, code2 int
		var body1, body2 string

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.Header.Set("Idempotency-Key", key)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			code1 = rec.Code
			body1 = rec.Body.String()
		}()

		time.Sleep(10 * time.Millisecond) // Ensure first goroutine claims lock

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.Header.Set("Idempotency-Key", key)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			code2 = rec.Code
			body2 = rec.Body.String()
		}()

		wg.Wait()

		// Verify one succeeded (201 Created) and one was blocked with 409 Conflict
		if code1 == http.StatusCreated {
			assert.Equal(t, http.StatusConflict, code2)
			assert.Contains(t, body2, "CONCURRENT_REQUEST_IN_PROGRESS")
		} else {
			assert.Equal(t, http.StatusConflict, code1)
			assert.Equal(t, http.StatusCreated, code2)
			assert.Contains(t, body1, "CONCURRENT_REQUEST_IN_PROGRESS")
		}
		assert.Equal(t, int64(1), atomic.LoadInt64(&execCount))

		// 3. Sequential Replay -> Replays cached response with Idempotent-Replay header
		reqReplay := httptest.NewRequest(http.MethodPost, "/test", nil)
		reqReplay.Header.Set("Idempotency-Key", key)
		recReplay := httptest.NewRecorder()
		mw.ServeHTTP(recReplay, reqReplay)

		assert.Equal(t, http.StatusCreated, recReplay.Code)
		assert.Equal(t, "true", recReplay.Header().Get("Idempotent-Replay"))
		assert.Equal(t, `{"status":"created"}`, recReplay.Body.String())
		// Handler was not re-executed
		assert.Equal(t, int64(1), atomic.LoadInt64(&execCount))
	})

	t.Run("Requests Without Idempotency-Key Pass Normally", func(t *testing.T) {
		store := NewIdempotencyStore()
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		mw := store.IdempotencyMiddleware(handler)

		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "ok", rec.Body.String())
		assert.Empty(t, rec.Header().Get("Idempotent-Replay"))
	})
}
