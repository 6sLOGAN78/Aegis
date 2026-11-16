package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"aegis/internal/proxy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwoPhaseDrainSequence(t *testing.T) {
	state := proxy.NewDrainingState()
	h := proxy.CreateProbeHandler(state, nil, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	delay := 200 * time.Millisecond
	var shutdownAt atomic.Int64
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- twoPhaseDrain(state, delay, time.Second, func(ctx context.Context) error {
			shutdownAt.Store(int64(time.Since(start)))
			return nil
		}, make(chan os.Signal, 1))
	}()

	require.Eventually(t, state.IsDraining, time.Second, 5*time.Millisecond)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/other", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Zero(t, shutdownAt.Load(), "shutdown must not run before the delay elapses")

	require.NoError(t, <-done)
	assert.GreaterOrEqual(t, time.Duration(shutdownAt.Load()), delay)
}

func TestTwoPhaseDrainZeroDelaySkipsWait(t *testing.T) {
	state := proxy.NewDrainingState()
	called := false
	start := time.Now()
	require.NoError(t, twoPhaseDrain(state, 0, time.Second, func(context.Context) error {
		called = true
		return nil
	}, make(chan os.Signal, 1)))
	assert.True(t, called)
	assert.True(t, state.IsDraining())
	assert.Less(t, time.Since(start), 100*time.Millisecond)
}

func TestTwoPhaseDrainInterruptSkipsDelay(t *testing.T) {
	state := proxy.NewDrainingState()
	sig := make(chan os.Signal, 1)
	sig <- os.Interrupt
	start := time.Now()
	require.NoError(t, twoPhaseDrain(state, 10*time.Second, time.Second, func(context.Context) error { return nil }, sig))
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestTwoPhaseDrainInFlightCompletes(t *testing.T) {
	state := proxy.NewDrainingState()
	release := make(chan struct{})
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := make(chan int, 1)
	go func() {
		resp, err := http.Get(srv.URL)
		if err != nil {
			res <- 0
			return
		}
		_ = resp.Body.Close()
		res <- resp.StatusCode
	}()
	<-started

	done := make(chan error, 1)
	go func() {
		done <- twoPhaseDrain(state, 0, 5*time.Second, srv.Config.Shutdown, make(chan os.Signal, 1))
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)

	assert.Equal(t, http.StatusOK, <-res)
	require.NoError(t, <-done)
}

func TestWorkloadDrainConnectionClose(t *testing.T) {
	state := proxy.NewDrainingState()
	h := drainAwareHandler(state, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Empty(t, rr.Header().Get("Connection"))

	state.SetDraining()
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, "close", rr.Header().Get("Connection"))
}

func TestParseDrainPropagationDelay(t *testing.T) {
	ok := map[string]time.Duration{
		"":     0,
		"5s":   5 * time.Second,
		"10":   10 * time.Second, // bare integer = seconds (WR-04)
		" 10 ": 10 * time.Second,
		"0":    0,
		"60s":  60 * time.Second,
		"60":   60 * time.Second,
	}
	for in, want := range ok {
		d, warn, err := parseDrainPropagationDelay(in)
		assert.NoError(t, err, in)
		assert.Empty(t, warn, in)
		assert.Equal(t, want, d, in)
	}

	// WR-04: unparsable or negative values fail closed instead of silently becoming 0.
	for _, in := range []string{"garbage", "10 s", "-1s", "-5", "1.5"} {
		_, _, err := parseDrainPropagationDelay(in)
		assert.Error(t, err, in)
	}

	// WR-04: values above the documented maximum are capped with a warning.
	for _, in := range []string{"61s", "5m", "3600", "99999999999999999"} {
		d, warn, err := parseDrainPropagationDelay(in)
		assert.NoError(t, err, in)
		assert.Equal(t, maxDrainPropagationDelay, d, in)
		assert.Contains(t, warn, "capped", in)
	}
}
