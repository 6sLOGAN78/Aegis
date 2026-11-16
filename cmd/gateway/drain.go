package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
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

// maxDrainPropagationDelay caps AEGIS_DRAIN_PROPAGATION_DELAY. The K8s grace budget
// (preStop 5s + delay + drain 30s + audit flush) and the compose stop_grace_period must
// stay above the sum, so a larger delay would let the orchestrator SIGKILL the process
// before the audit pipeline flushes.
const maxDrainPropagationDelay = 60 * time.Second

// parseDrainPropagationDelay parses AEGIS_DRAIN_PROPAGATION_DELAY. Empty means 0 (no
// delay). A Go duration ("10s") or a bare non-negative integer (seconds, "10") is
// accepted. Anything else, or a negative value, is an error: startup fails closed
// rather than silently disabling the delay. Values above maxDrainPropagationDelay are
// capped and reported through warning.
func parseDrainPropagationDelay(val string) (d time.Duration, warning string, err error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0, "", nil
	}
	if secs, perr := strconv.ParseInt(val, 10, 64); perr == nil {
		if secs < 0 {
			return 0, "", fmt.Errorf("AEGIS_DRAIN_PROPAGATION_DELAY must not be negative, got %q", val)
		}
		if secs > int64(maxDrainPropagationDelay/time.Second) {
			d = maxDrainPropagationDelay + time.Second // forces the cap below
		} else {
			d = time.Duration(secs) * time.Second
		}
	} else {
		d, perr = time.ParseDuration(val)
		if perr != nil {
			return 0, "", fmt.Errorf("AEGIS_DRAIN_PROPAGATION_DELAY %q is not a duration (e.g. \"10s\") or whole seconds (e.g. \"10\")", val)
		}
		if d < 0 {
			return 0, "", fmt.Errorf("AEGIS_DRAIN_PROPAGATION_DELAY must not be negative, got %q", val)
		}
	}
	if d > maxDrainPropagationDelay {
		return maxDrainPropagationDelay, fmt.Sprintf("AEGIS_DRAIN_PROPAGATION_DELAY %q exceeds the %v maximum; capped to %v", val, maxDrainPropagationDelay, maxDrainPropagationDelay), nil
	}
	return d, "", nil
}
