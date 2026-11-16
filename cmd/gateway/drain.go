package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"aegis/internal/proxy"
)

// twoPhaseDrain performs the DIST-01 two-phase shutdown. Phase 1 marks the
// replica draining (readiness fails) and keeps serving for the propagation
// delay so load balancers can deregister it; an interrupt during the delay
// skips the remainder. Phase 2 calls shutdown with a drainTimeout context so
// in-flight requests complete.
func twoPhaseDrain(state *proxy.DrainingState, delay, drainTimeout time.Duration, shutdown func(context.Context) error, interrupt <-chan os.Signal) error {
	state.SetDraining()
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-interrupt:
			timer.Stop()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	return shutdown(ctx)
}

// drainAwareHandler sets Connection: close on responses while draining so
// keep-alive clients on the workload listener reconnect to another replica.
func drainAwareHandler(state *proxy.DrainingState, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state != nil && state.IsDraining() {
			w.Header().Set("Connection", "close")
		}
		next.ServeHTTP(w, r)
	})
}

// parseDrainPropagationDelay parses AEGIS_DRAIN_PROPAGATION_DELAY; empty,
// invalid or negative values yield 0 (no delay).
func parseDrainPropagationDelay(val string) time.Duration {
	if val == "" {
		return 0
	}
	d, err := time.ParseDuration(val)
	if err != nil || d < 0 {
		return 0
	}
	return d
}
