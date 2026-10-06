package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConcurrencyLimiter(t *testing.T) {
	t.Run("allows requests within capacity", func(t *testing.T) {
		limiter := NewConcurrencyLimiter(2)
		nextCalled := false
		handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.True(t, nextCalled)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("rejects request with 429 when capacity is saturated", func(t *testing.T) {
		limiter := NewConcurrencyLimiter(1)

		holdChan := make(chan struct{})
		handlerRunning := make(chan struct{})

		var once sync.Once
		handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() {
				close(handlerRunning)
			})
			<-holdChan
			w.WriteHeader(http.StatusOK)
		}))

		var wg sync.WaitGroup
		wg.Add(1)

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/in-flight", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()

		// Wait until first request has acquired the slot
		<-handlerRunning

		// Second request should be rejected immediately
		reqRejected := httptest.NewRequest(http.MethodGet, "/overflow", nil)
		reqRejected.Header.Set("X-Request-ID", "test-uuid-429")
		recRejected := httptest.NewRecorder()

		handler.ServeHTTP(recRejected, reqRejected)

		assert.Equal(t, http.StatusTooManyRequests, recRejected.Code)
		assert.Equal(t, "1", recRejected.Header().Get("Retry-After"))
		assert.Equal(t, "application/problem+json", recRejected.Header().Get("Content-Type"))

		var problem RFC7807Problem
		err := json.Unmarshal(recRejected.Body.Bytes(), &problem)
		require.NoError(t, err)
		assert.Equal(t, "https://aegis.local/errors/concurrency-exceeded", problem.Type)
		assert.Equal(t, "Too Many Requests", problem.Title)
		assert.Equal(t, http.StatusTooManyRequests, problem.Status)
		assert.Equal(t, "Ingress concurrency capacity exceeded", problem.Detail)
		assert.Equal(t, "test-uuid-429", problem.Instance)

		// Unblock first request
		close(holdChan)
		wg.Wait()

		// Verify semaphore released: new request now succeeds
		reqSubsequent := httptest.NewRequest(http.MethodGet, "/after-release", nil)
		recSubsequent := httptest.NewRecorder()
		handler.ServeHTTP(recSubsequent, reqSubsequent)
		assert.Equal(t, http.StatusOK, recSubsequent.Code)
	})

	t.Run("concurrent goroutines properly release semaphore under load", func(t *testing.T) {
		const capacity = 10
		const totalRequests = 100

		limiter := NewConcurrencyLimiter(capacity)
		handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))

		var wg sync.WaitGroup
		wg.Add(totalRequests)

		var successCount int64
		var rejectedCount int64
		var countMu sync.Mutex

		for i := 0; i < totalRequests; i++ {
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "/load", nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				countMu.Lock()
				if rec.Code == http.StatusOK {
					successCount++
				} else if rec.Code == http.StatusTooManyRequests {
					rejectedCount++
				}
				countMu.Unlock()
			}()
		}

		wg.Wait()

		countMu.Lock()
		defer countMu.Unlock()
		assert.Equal(t, int64(totalRequests), successCount+rejectedCount)
		assert.Greater(t, successCount, int64(0))

		// Once all finished, channel must be fully empty (all capacity recovered)
		assert.Equal(t, 0, len(limiter.sem))
	})
}
