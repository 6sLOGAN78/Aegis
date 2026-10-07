package control

import (
	"bytes"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CachedResponse captures the HTTP response for idempotent replays.
type CachedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	CachedAt   time.Time
}

// IdempotencyStore manages concurrency locks and 24-hour response caching for idempotent operations.
type IdempotencyStore struct {
	mu        sync.RWMutex
	inFlight  map[string]time.Time
	responses map[string]*CachedResponse
}

// NewIdempotencyStore instantiates an IdempotencyStore.
func NewIdempotencyStore() *IdempotencyStore {
	return &IdempotencyStore{
		inFlight:  make(map[string]time.Time),
		responses: make(map[string]*CachedResponse),
	}
}

// LockOrReplay checks if an idempotency key is cached or currently in flight.
// Returns (cachedResponse, lockAcquired, err).
// If key is empty, returns (nil, true, nil) to allow requests without idempotency keys.
func (s *IdempotencyStore) LockOrReplay(key string) (*CachedResponse, bool, error) {
	if key == "" {
		return nil, true, nil
	}

	if _, err := uuid.Parse(key); err != nil {
		return nil, false, errors.New("invalid idempotency key format: must be valid UUID")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Check if completed response is cached within 24h retention
	if resp, ok := s.responses[key]; ok {
		if time.Since(resp.CachedAt) < 24*time.Hour {
			return resp, false, nil
		}
		delete(s.responses, key)
	}

	// 2. Check if a request with this key is already executing
	if _, locked := s.inFlight[key]; locked {
		return nil, false, nil // Conflict: locked is false, meaning lock not acquired
	}

	// 3. Acquire lock
	s.inFlight[key] = time.Now()
	return nil, true, nil
}

// SaveResponse stores the completed response for the idempotency key.
func (s *IdempotencyStore) SaveResponse(key string, statusCode int, header http.Header, body []byte, ttl time.Duration) {
	if key == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.inFlight, key)
	s.responses[key] = &CachedResponse{
		StatusCode: statusCode,
		Header:     header.Clone(),
		Body:       bytes.Clone(body),
		CachedAt:   time.Now(),
	}
}

// Complete saves response with default 24h retention.
func (s *IdempotencyStore) Complete(key string, statusCode int, header http.Header, body []byte) {
	s.SaveResponse(key, statusCode, header, body, 24*time.Hour)
}

// Release removes an in-flight lock without saving a response (e.g. on execution error).
func (s *IdempotencyStore) Release(key string) {
	if key == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.inFlight, key)
}

// responseBuffer intercepts response writes for caching.
type responseBuffer struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

func (r *responseBuffer) WriteHeader(code int) {
	r.statusCode = code
}

func (r *responseBuffer) Write(b []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	return r.body.Write(b)
}

// IdempotencyMiddleware wraps an http.Handler with idempotency locking and response caching.
func (s *IdempotencyStore) IdempotencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}

		cached, locked, err := s.LockOrReplay(key)
		if err != nil {
			writeErrorResponse(w, r, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", err.Error())
			return
		}

		if cached != nil {
			// Replay cached response
			for k, values := range cached.Header {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(cached.StatusCode)
			_, _ = w.Write(cached.Body)
			return
		}

		if !locked {
			// In-flight concurrency conflict (HTTP 409)
			writeErrorResponse(w, r, http.StatusConflict, "CONCURRENT_REQUEST_IN_PROGRESS", "A concurrent request with this Idempotency-Key is currently in progress")
			return
		}

		// Lock acquired: execute handler with response buffer
		buf := &responseBuffer{
			ResponseWriter: w,
			body:           &bytes.Buffer{},
		}

		completed := false
		defer func() {
			if !completed {
				s.Release(key)
			}
		}()

		next.ServeHTTP(buf, r)

		statusCode := buf.statusCode
		if statusCode == 0 {
			statusCode = http.StatusOK
		}

		// Cache successful or expected client error responses
		s.Complete(key, statusCode, buf.Header(), buf.body.Bytes())
		completed = true

		// Write to actual client
		for k, values := range buf.Header() {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(statusCode)
		_, _ = w.Write(buf.body.Bytes())
	})
}
