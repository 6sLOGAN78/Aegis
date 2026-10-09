package proxy

import (
	"net/http"
	"sync/atomic"
)

// RejectionRecorder counts requests rejected before the audit middleware
// (concurrency limit, oversized headers, ambiguous credentials). It is metrics
// only: those rejections create no audit rows (D-16). The interface is declared
// consumer-side so the proxy package does not import telemetry.
type RejectionRecorder interface {
	RecordRejection(reason string)
}

// rejectionBox lets atomic.Pointer hold an interface value (which may not be stored directly).
type rejectionBox struct{ r RejectionRecorder }

// rejectionHolder is a nil-safe, race-free slot for a RejectionRecorder that
// can be attached after construction.
type rejectionHolder struct {
	p atomic.Pointer[rejectionBox]
}

func (h *rejectionHolder) set(r RejectionRecorder) {
	h.p.Store(&rejectionBox{r: r})
}

func (h *rejectionHolder) record(reason string) {
	if b := h.p.Load(); b != nil && b.r != nil {
		b.r.RecordRejection(reason)
	}
}

// ConcurrencyLimiter bounds the number of concurrent in-flight requests using a counting semaphore.
type ConcurrencyLimiter struct {
	sem        chan struct{}
	rejections rejectionHolder
}

// SetRejectionRecorder attaches the recorder notified when a request is rejected
// by the concurrency limit. A nil recorder disables counting.
func (l *ConcurrencyLimiter) SetRejectionRecorder(r RejectionRecorder) {
	l.rejections.set(r)
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
			l.rejections.record("concurrency")
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
