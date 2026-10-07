package proxy

import (
	"net/http"
	"sync/atomic"
	"time"
)

// DrainingState tracks graceful shutdown lifecycle for zero-trust gateway replicas.
type DrainingState struct {
	isDraining atomic.Bool
}

// NewDrainingState returns an initialized DrainingState with draining set to false.
func NewDrainingState() *DrainingState {
	return &DrainingState{}
}

// SetDraining marks the replica as draining, causing readiness probes to immediately fail.
func (s *DrainingState) SetDraining() {
	s.isDraining.Store(true)
}

// IsDraining reports whether the gateway replica is currently in a draining state.
func (s *DrainingState) IsDraining() bool {
	return s.isDraining.Load()
}

// CreateProbeHandler creates unauthenticated health and readiness endpoints (/livez, /readyz).
// When /livez or /healthz is requested, it immediately responds with 200 OK.
// When /readyz or /healthz/ready is requested, it verifies:
// 1. Drain state is not set (fails with 503 DRAINING).
// 2. Freshness lease is not expired (>60s) (fails with 503 LEASE_EXPIRED).
// 3. Active snapshot configuration is loaded (fails with 503 UNINITIALIZED).
// 4. Audit disk spool is not saturated (>=90%) (fails with 503 SPOOL_SATURATED).
// If all conditions pass, it returns 200 READY.
// All other request paths fall through to nextHandler.
func CreateProbeHandler(
	draining *DrainingState,
	isLeaseExpired func(time.Duration) bool,
	isSnapshotActive func() bool,
	isSpoolSaturated func() bool,
	nextHandler http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/livez", "/healthz":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK\n"))
			return

		case "/readyz", "/healthz/ready":
			if draining != nil && draining.IsDraining() {
				http.Error(w, "DRAINING", http.StatusServiceUnavailable)
				return
			}
			if isLeaseExpired != nil && isLeaseExpired(60*time.Second) {
				http.Error(w, "LEASE_EXPIRED", http.StatusServiceUnavailable)
				return
			}
			if isSnapshotActive != nil && !isSnapshotActive() {
				http.Error(w, "UNINITIALIZED", http.StatusServiceUnavailable)
				return
			}
			if isSpoolSaturated != nil && isSpoolSaturated() {
				http.Error(w, "SPOOL_SATURATED", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("READY\n"))
			return

		default:
			if nextHandler != nil {
				nextHandler.ServeHTTP(w, r)
			}
		}
	})
}
