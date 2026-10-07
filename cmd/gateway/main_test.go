package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"aegis/internal/proxy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayGracefulDrain(t *testing.T) {
	drainingState := proxy.NewDrainingState()

	inFlightStarted := make(chan struct{})
	canFinishInFlight := make(chan struct{})

	// Business handler that simulates an active proxy request completing during drain
	businessHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inFlightStarted)
		<-canFinishInFlight
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	probeHandler := proxy.CreateProbeHandler(
		drainingState,
		func(time.Duration) bool { return false },
		func() bool { return true },
		func() bool { return false },
		businessHandler,
	)

	server := httptest.NewServer(probeHandler)
	defer server.Close()

	// Initial probe check: readyz should return 200 OK
	readyResp, err := http.Get(server.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, readyResp.StatusCode)
	readyBody, _ := io.ReadAll(readyResp.Body)
	_ = readyResp.Body.Close()
	assert.Equal(t, "READY\n", string(readyBody))

	// Start in-flight business request
	var inFlightResp *http.Response
	var inFlightErr error
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		inFlightResp, inFlightErr = http.Get(server.URL + "/api/orders")
	}()

	// Wait until business request is actively in-flight
	<-inFlightStarted

	// Trigger graceful draining phase
	drainingState.SetDraining()
	assert.True(t, drainingState.IsDraining())

	// Concurrent /readyz probe MUST immediately return 503 DRAINING
	drainProbeResp, err := http.Get(server.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, drainProbeResp.StatusCode)
	drainProbeBody, _ := io.ReadAll(drainProbeResp.Body)
	_ = drainProbeResp.Body.Close()
	assert.Equal(t, "DRAINING\n", string(drainProbeBody))

	// Release in-flight business request so it finishes during the drain window
	close(canFinishInFlight)
	wg.Wait()

	// Assert in-flight request completed successfully with 200 OK
	require.NoError(t, inFlightErr)
	require.NotNil(t, inFlightResp)
	assert.Equal(t, http.StatusOK, inFlightResp.StatusCode)
	body, _ := io.ReadAll(inFlightResp.Body)
	_ = inFlightResp.Body.Close()
	assert.Equal(t, `{"status":"success"}`, string(body))
}

func TestGatewayProbes(t *testing.T) {
	drainingState := proxy.NewDrainingState()
	var leaseExpired bool
	var snapshotActive bool = true
	var spoolSaturated bool

	probeHandler := proxy.CreateProbeHandler(
		drainingState,
		func(time.Duration) bool { return leaseExpired },
		func() bool { return snapshotActive },
		func() bool { return spoolSaturated },
		http.NotFoundHandler(),
	)

	server := httptest.NewServer(probeHandler)
	defer server.Close()

	// 1. Livez
	resp, err := http.Get(server.URL + "/livez")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	// 2. Readyz - healthy
	resp, err = http.Get(server.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	// 3. Readyz - lease expired
	leaseExpired = true
	resp, err = http.Get(server.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	_ = resp.Body.Close()
	leaseExpired = false

	// 4. Readyz - uninitialized
	snapshotActive = false
	resp, err = http.Get(server.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	_ = resp.Body.Close()
	snapshotActive = true

	// 5. Readyz - spool saturated
	spoolSaturated = true
	resp, err = http.Get(server.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	_ = resp.Body.Close()
	spoolSaturated = false

	// 6. Readyz - draining
	drainingState.SetDraining()
	resp, err = http.Get(server.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	_ = resp.Body.Close()
}
