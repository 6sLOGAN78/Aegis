package failure

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aegis/internal/proxy"
	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLeaseExpiry(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer := snapshot.NewSigner(priv, "cp-key-1")
	verifier := snapshot.NewVerifier(pub)
	manager := snapshot.NewManager(verifier)

	// Build Gateway HTTP request multiplexer modeling main.go behavior
	mux := http.NewServeMux()

	// Readiness endpoint (CTRL-04, Invariant 1)
	mux.HandleFunc("/healthz/ready", func(w http.ResponseWriter, r *http.Request) {
		if manager.IsLeaseExpired(60 * time.Second) {
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Gateway unready: freshness lease expired (>60s)",
				"https://aegis.local/errors/policy-lease-expired",
				"health-check",
			)
			return
		}
		if manager.Active() == nil {
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Gateway unready: active state uninitialized",
				"https://aegis.local/errors/uninitialized",
				"health-check",
			)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	// Core proxy handler modeling gatewayHandler
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		reqID := "test-req-id"

		// Fail closed check 1: Lease freshness boundary (>60s)
		if manager.IsLeaseExpired(60 * time.Second) {
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Policy freshness lease expired (>60s)",
				"https://aegis.local/errors/policy-lease-expired",
				reqID,
			)
			return
		}

		// Fail closed check 2: Initialization state
		state := manager.Active()
		if state == nil {
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Gateway configuration uninitialized",
				"https://aegis.local/errors/uninitialized",
				reqID,
			)
			return
		}

		// Route match
		route, err := state.Router.Match(r.Method, r.URL.Path)
		if err != nil {
			proxy.WriteProblemDetails(
				w,
				http.StatusNotFound,
				"Not Found",
				"No matching upstream route found",
				"https://aegis.local/errors/not-found",
				reqID,
			)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"route_id":"` + route.RouteID + `"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()

	// 1. Initial State: Uninitialized snapshot -> 503 UNINITIALIZED
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/orders", nil)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	var prob proxy.RFC7807Problem
	err = json.NewDecoder(resp.Body).Decode(&prob)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/uninitialized", prob.Type)

	// Readiness returns 503 when uninitialized
	readyResp, err := client.Get(server.URL + "/healthz/ready")
	require.NoError(t, err)
	defer readyResp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, readyResp.StatusCode)

	// 2. Activate snapshot v1 with valid lease
	payload := &snapshotv1.SnapshotPayload{
		Version: 1,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:      "orders.list",
				ServiceId:    "orders",
				HttpMethod:   "GET",
				PathTemplate: "/api/orders",
				UpstreamUrl:  "https://orders:8081",
			},
		},
		PolicyModules: []*snapshotv1.PolicyModule{
			{
				PackageName: "aegis.authz",
				ModuleName:  "rules.rego",
				SourceRego:  "package aegis.authz\ndefault allow = true",
			},
		},
	}
	env, err := signer.SignSnapshot(payload)
	require.NoError(t, err)

	err = manager.ValidateAndActivate(context.Background(), env)
	require.NoError(t, err)

	// Now readiness returns 200 OK
	readyResp2, err := client.Get(server.URL + "/healthz/ready")
	require.NoError(t, err)
	defer readyResp2.Body.Close()
	assert.Equal(t, http.StatusOK, readyResp2.StatusCode)

	// Requests succeed with 200 OK while lease is fresh
	req2, _ := http.NewRequest(http.MethodGet, server.URL+"/api/orders", nil)
	resp2, err := client.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	// 3. Simulate control plane disconnection: lease age exceeds 60 seconds
	// Rewind lease renewal time by 61 seconds
	manager.SetLastLeaseRenewedAt(time.Now().Add(-61 * time.Second))
	assert.True(t, manager.IsLeaseExpired(60*time.Second))

	// Requests must now FAIL CLOSED with HTTP 503 POLICY_LEASE_EXPIRED
	reqExpired, _ := http.NewRequest(http.MethodGet, server.URL+"/api/orders", nil)
	respExpired, err := client.Do(reqExpired)
	require.NoError(t, err)
	defer respExpired.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, respExpired.StatusCode)
	var probExpired proxy.RFC7807Problem
	err = json.NewDecoder(respExpired.Body).Decode(&probExpired)
	require.NoError(t, err)
	assert.Equal(t, "https://aegis.local/errors/policy-lease-expired", probExpired.Type)
	assert.Equal(t, "Policy freshness lease expired (>60s)", probExpired.Detail)

	// Readiness drops to 503
	readyRespExpired, err := client.Get(server.URL + "/healthz/ready")
	require.NoError(t, err)
	defer readyRespExpired.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, readyRespExpired.StatusCode)

	// 4. Recovery: Control Plane reconnects and issues new lease
	manager.RecordLeaseRenewal()
	assert.False(t, manager.IsLeaseExpired(60*time.Second))

	reqRecovered, _ := http.NewRequest(http.MethodGet, server.URL+"/api/orders", nil)
	respRecovered, err := client.Do(reqRecovered)
	require.NoError(t, err)
	defer respRecovered.Body.Close()
	assert.Equal(t, http.StatusOK, respRecovered.StatusCode)
}
