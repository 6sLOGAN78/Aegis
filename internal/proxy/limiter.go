package proxy

import (
	"net/http"
)

// ConcurrencyLimiter bounds the number of concurrent in-flight requests using a counting semaphore.
type ConcurrencyLimiter struct {
	sem chan struct{}
}

// NewConcurrencyLimiter creates a new ConcurrencyLimiter with the specified capacity.
func NewConcurrencyLimiter(maxConcurrent int) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		sem: make(chan struct{}, maxConcurrent),
	}
}

// Wrap returns an http.Handler that restricts concurrency to the configured limit.
func (l *ConcurrencyLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case l.sem <- struct{}{}:
			defer func() { <-l.sem }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			reqID := r.Header.Get("X-Request-ID")
			WriteProblemDetails(
				w,
				http.StatusTooManyRequests,
				"Too Many Requests",
				"Ingress concurrency capacity exceeded",
				"https://aegis.local/errors/concurrency-exceeded",
				reqID,
			)
		}
	})
}
