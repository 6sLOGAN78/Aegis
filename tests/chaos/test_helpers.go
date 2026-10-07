package chaos

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aegis/internal/audit"
	"aegis/internal/control"
	"aegis/internal/identity"
	"aegis/internal/policy"
	"aegis/internal/proxy"
	"aegis/internal/ratelimit"
	"aegis/internal/revocation"
	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// TestGatewayNode encapsulates an isolated gateway replica for chaos and resilience testing.
type TestGatewayNode struct {
	ID            string
	Manager       *snapshot.Manager
	StreamClient  *snapshot.StreamClient
	DrainingState *proxy.DrainingState
	Handler       http.Handler
	Server        *httptest.Server
	cancel        context.CancelFunc
	Spool         *audit.DiskSpool
	SpoolDir      string
	isKilled      atomic.Bool
	closed        atomic.Bool
}

// Close gracefully terminates the gateway node's HTTP server and gRPC stream client.
func (n *TestGatewayNode) Close() {
	if n.closed.CompareAndSwap(false, true) {
		if n.cancel != nil {
			n.cancel()
		}
		if n.StreamClient != nil {
			n.StreamClient.Stop()
		}
		if n.Server != nil {
			n.Server.Close()
		}
		if n.Spool != nil {
			_ = n.Spool.Close()
		}
		if n.SpoolDir != "" {
			_ = os.RemoveAll(n.SpoolDir)
		}
	}
}

// TestControlPlane manages an in-memory gRPC control plane for snapshot streaming.
type TestControlPlane struct {
	DistServer *control.SnapshotDistributionServer
	AckTracker *control.AckTracker
	GRPCServer *grpc.Server
	Listener   *bufconn.Listener
	Signer     *snapshot.Signer
	Verifier   *snapshot.Verifier
	Dialer     func(context.Context, string) (net.Conn, error)
	severed    atomic.Bool
}

// Close stops the gRPC server and buffer listener.
func (cp *TestControlPlane) Close() {
	if cp.GRPCServer != nil {
		cp.GRPCServer.Stop()
	}
	if cp.Listener != nil {
		_ = cp.Listener.Close()
	}
}

// ChaosCluster coordinates the in-memory Control Plane, N gateway replica nodes,
// and a simulated round-robin reverse proxy load balancer with retry/failover.
type ChaosCluster struct {
	t            *testing.T
	ControlPlane *TestControlPlane
	Nodes        []*TestGatewayNode
	LBServer     *httptest.Server
	LBURL        string
	Client       *http.Client
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.RWMutex
	rrIndex      atomic.Uint64

	// Shared dependencies for fault injection
	RedisServer *miniredis.Miniredis
	RedisClient *redis.Client
	RevStore    *revocation.Store
	RateLimiter *ratelimit.RateLimiter

	IssuerPub    ed25519.PublicKey
	IssuerPriv   ed25519.PrivateKey
	Issuer       string
	Audience     string
	Validator    *identity.TokenValidator
	AssertionMin *identity.AssertionMinter

	BackendServer    *httptest.Server
	BackendCallCount atomic.Int64

	RequireAuth  bool
	redisSevered atomic.Bool
	dbSevered    atomic.Bool
}

// TestCluster is an alias for ChaosCluster for backwards compatibility.
type TestCluster = ChaosCluster

// TrafficStats captures request outcomes from the concurrent traffic generator.
type TrafficStats struct {
	TotalRequests int64
	Success200    int64
	Errors        int64
	StatusCodes   map[int]int64
}

// RoundRobinLB simulates an edge reverse proxy (like HAProxy) distributing requests
// across healthy gateway replicas with automatic failover upon connection failure.
type RoundRobinLB struct {
	cluster *ChaosCluster
	client  *http.Client
}

func (lb *RoundRobinLB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
	}

	lb.cluster.mu.RLock()
	numNodes := len(lb.cluster.Nodes)
	nodes := make([]*TestGatewayNode, numNodes)
	copy(nodes, lb.cluster.Nodes)
	lb.cluster.mu.RUnlock()

	if numNodes == 0 {
		http.Error(w, "No gateway nodes available", http.StatusServiceUnavailable)
		return
	}

	startIdx := lb.cluster.rrIndex.Add(1) - 1
	var lastErr error

	// Try up to numNodes active nodes before failing
	for attempt := 0; attempt < numNodes; attempt++ {
		node := nodes[(int(startIdx)+attempt)%numNodes]
		if node == nil || node.isKilled.Load() {
			continue
		}

		targetURL := node.Server.URL + r.URL.RequestURI()
		var bodyReader io.Reader
		if len(bodyBytes) > 0 {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bodyReader)
		if err != nil {
			lastErr = err
			continue
		}

		// Copy incoming headers
		for k, vv := range r.Header {
			for _, v := range vv {
				req.Header.Add(k, v)
			}
		}

		resp, err := lb.client.Do(req)
		if err != nil {
			// Connection error: replica killed or terminating; redispatch to next healthy replica
			lastErr = err
			continue
		}

		// Copy response headers and body
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		_ = resp.Body.Close()
		return
	}

	if lastErr != nil {
		http.Error(w, fmt.Sprintf("All gateway nodes failed: %v", lastErr), http.StatusBadGateway)
	} else {
		http.Error(w, "All gateway nodes unavailable", http.StatusServiceUnavailable)
	}
}

// Close terminates all cluster gateway nodes, reverse proxy, and dependencies.
func (c *ChaosCluster) Close() {
	c.cancel()
	if c.LBServer != nil {
		c.LBServer.Close()
	}
	c.mu.Lock()
	for _, n := range c.Nodes {
		n.Close()
	}
	c.mu.Unlock()
	if c.ControlPlane != nil {
		c.ControlPlane.Close()
	}
	if c.RedisClient != nil {
		_ = c.RedisClient.Close()
	}
	if c.RedisServer != nil {
		c.RedisServer.Close()
	}
	if c.BackendServer != nil {
		c.BackendServer.Close()
	}
}

// KillNode simulates abruptly terminating a gateway replica node.
func (c *ChaosCluster) KillNode(index int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if index < 0 || index >= len(c.Nodes) {
		return
	}
	node := c.Nodes[index]
	if node != nil && !node.isKilled.Load() {
		node.isKilled.Store(true)
		node.Close()
	}
}

// RestartNode respawns a killed gateway replica and rejoins it to the cluster.
func (c *ChaosCluster) RestartNode(index int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if index < 0 || index >= len(c.Nodes) {
		return
	}
	gwID := fmt.Sprintf("gw-%d", index+1)
	newNode := c.spawnNode(c.ctx, gwID)

	// Wait up to 2 seconds for node to stream and activate snapshot
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if newNode.Manager.Active() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Nodes[index] = newNode
}

// StartTraffic starts concurrent background HTTP requests against the cluster load balancer.
func (c *ChaosCluster) StartTraffic(concurrency int) func() TrafficStats {
	if concurrency <= 0 {
		concurrency = 10
	}
	var totalReqs atomic.Int64
	var success200 atomic.Int64
	var errorReqs atomic.Int64
	var statusCounts sync.Map

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 50,
			IdleConnTimeout:     10 * time.Second,
		},
	}

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					reqURL := c.LBURL + "/api/orders"
					req, err := http.NewRequest(http.MethodGet, reqURL, nil)
					if err != nil {
						errorReqs.Add(1)
						continue
					}

					token := c.MintValidToken("traffic-user", "developer")
					req.Header.Set("Authorization", "Bearer "+token)

					resp, err := client.Do(req)
					if err != nil {
						errorReqs.Add(1)
						time.Sleep(2 * time.Millisecond)
						continue
					}

					totalReqs.Add(1)
					code := resp.StatusCode
					val, _ := statusCounts.LoadOrStore(code, new(atomic.Int64))
					val.(*atomic.Int64).Add(1)

					if code == http.StatusOK {
						success200.Add(1)
					} else if code >= 500 {
						errorReqs.Add(1)
					}
					_ = resp.Body.Close()
					time.Sleep(2 * time.Millisecond)
				}
			}
		}()
	}

	return func() TrafficStats {
		close(stopCh)
		wg.Wait()

		resMap := make(map[int]int64)
		statusCounts.Range(func(key, val interface{}) bool {
			resMap[key.(int)] = val.(*atomic.Int64).Load()
			return true
		})

		return TrafficStats{
			TotalRequests: totalReqs.Load(),
			Success200:    success200.Load(),
			Errors:        errorReqs.Load(),
			StatusCodes:   resMap,
		}
	}
}

// MintValidToken generates a cryptographically signed Ed25519 JWT for the test issuer.
func (c *ChaosCluster) MintValidToken(sub, role string) string {
	claims := identity.UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    c.Issuer,
			Audience:  jwt.ClaimStrings{c.Audience},
			Subject:   sub,
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Roles: []string{role},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(c.IssuerPriv)
	if err != nil {
		panic(err)
	}
	return signed
}

// MintExpiredToken generates an expired Ed25519 JWT.
func (c *ChaosCluster) MintExpiredToken(sub, role string) string {
	claims := identity.UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    c.Issuer,
			Audience:  jwt.ClaimStrings{c.Audience},
			Subject:   sub,
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-10 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-20 * time.Minute)),
		},
		Roles: []string{role},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(c.IssuerPriv)
	if err != nil {
		panic(err)
	}
	return signed
}

// MintAlgNoneToken crafts an insecure JWT specifying "alg": "none".
func (c *ChaosCluster) MintAlgNoneToken(sub, role string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := fmt.Sprintf(`{"iss":"%s","aud":["%s"],"sub":"%s","roles":["%s"],"exp":%d}`,
		c.Issuer, c.Audience, sub, role, time.Now().Add(15*time.Minute).Unix())
	payload := base64.RawURLEncoding.EncodeToString([]byte(claims))
	return header + "." + payload + "."
}

// MintUntrustedKeyToken generates a JWT signed by an unauthorized key pair.
func (c *ChaosCluster) MintUntrustedKeyToken(sub, role string) string {
	_, unPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	claims := identity.UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    c.Issuer,
			Audience:  jwt.ClaimStrings{c.Audience},
			Subject:   sub,
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Roles: []string{role},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(unPriv)
	if err != nil {
		panic(err)
	}
	return signed
}

// SeverControlPlane simulates disconnecting the control plane.
func (c *ChaosCluster) SeverControlPlane() {
	c.ControlPlane.severed.Store(true)
}

// RestoreControlPlane reconnects the control plane and issues a fresh lease.
func (c *ChaosCluster) RestoreControlPlane() {
	c.ControlPlane.severed.Store(false)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.Nodes {
		if n != nil && !n.isKilled.Load() {
			n.Manager.RecordLeaseRenewal()
		}
	}
}

// PartitionRedis severs access to Redis.
func (c *ChaosCluster) PartitionRedis() {
	c.redisSevered.Store(true)
	if c.RedisServer != nil {
		c.RedisServer.Close()
	}
}

// RestoreRedis restores Redis connectivity.
func (c *ChaosCluster) RestoreRedis() {
	c.redisSevered.Store(false)
	if c.RedisServer != nil {
		_ = c.RedisServer.Restart()
	}
}

// SeverPostgres simulates an outage of the audit PostgreSQL database.
func (c *ChaosCluster) SeverPostgres() {
	c.dbSevered.Store(true)
}

// RestorePostgres restores the PostgreSQL database.
func (c *ChaosCluster) RestorePostgres() {
	c.dbSevered.Store(false)
}

// SaturateSpool floods the node's disk spool to >=90% capacity.
func (c *ChaosCluster) SaturateSpool(index int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if index < 0 || index >= len(c.Nodes) {
		return
	}
	node := c.Nodes[index]
	if node == nil {
		return
	}

	_ = node.Spool.Close()
	smallSpool, err := audit.NewDiskSpool(audit.DiskSpoolConfig{
		SpoolDir:         node.SpoolDir,
		MaxSegmentBytes:  10 * 1024,
		VolumeQuotaBytes: 20 * 1024,
	})
	if err != nil {
		panic(err)
	}
	node.Spool = smallSpool

	for i := 0; i < 150; i++ {
		fillEvent := &audit.CompletionEvent{
			EventID:         uuid.NewString(),
			Timestamp:       time.Now().UTC(),
			RequestID:       uuid.NewString(),
			PrincipalID:     "filler-principal",
			PrincipalKind:   "user",
			PrincipalRoles:  []string{"worker"},
			ClientIP:        "127.0.0.1",
			HTTPMethod:      "GET",
			CanonicalPath:   "/api/orders",
			RouteID:         "orders.list",
			ServiceID:       "orders",
			Decision:        "allow",
			ReasonCode:      "ALLOWED",
			SnapshotVersion: 1,
		}
		if appendErr := node.Spool.AppendPreForward(fillEvent); appendErr != nil {
			if errors.Is(appendErr, audit.ErrSpoolSaturated) {
				break
			}
		}
	}
}

// DrainSpool clears old segment files from the node's spool, restoring capacity.
func (c *ChaosCluster) DrainSpool(index int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if index < 0 || index >= len(c.Nodes) {
		return
	}
	node := c.Nodes[index]
	if node == nil || node.SpoolDir == "" {
		return
	}

	entries, err := os.ReadDir(node.SpoolDir)
	if err == nil {
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) == ".log" {
				_ = os.Remove(filepath.Join(node.SpoolDir, entry.Name()))
			}
		}
	}
	_ = node.Spool.Close()
	newSpool, err := audit.NewDiskSpool(audit.DiskSpoolConfig{
		SpoolDir:         node.SpoolDir,
		MaxSegmentBytes:  10 * 1024 * 1024,
		VolumeQuotaBytes: 100 * 1024 * 1024,
	})
	if err == nil {
		node.Spool = newSpool
	}
}

// CreateSignedTestSnapshot creates and cryptographically signs a SnapshotPayload.
func CreateSignedTestSnapshot(signer *snapshot.Signer, version int64, regoPolicy string) (*snapshotv1.SnapshotEnvelope, error) {
	if regoPolicy == "" {
		regoPolicy = `package aegis.authz

default allow := true
default reason_code := "ALLOWED"

decision := {
    "allow": allow,
    "reason_code": reason_code,
    "snapshot_version": input.snapshot_version,
}
`
	}

	payload := &snapshotv1.SnapshotPayload{
		Version: version,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:              "orders.list",
				ServiceId:            "orders",
				HttpMethod:           "GET",
				PathTemplate:         "/api/orders",
				UpstreamUrl:          "https://orders:8081",
				UpstreamSpiffeId:     "spiffe://aegis.local/service/orders",
				RequiresWorkloadMtls: false,
				RateLimit:            &snapshotv1.RateLimitPolicy{RequestsPerSecond: 1000, Burst: 2000},
				Timeout:              &snapshotv1.TimeoutPolicy{RequestTimeoutMs: 5000, UpstreamTimeoutMs: 2000},
			},
			{
				RouteId:              "orders.create",
				ServiceId:            "orders",
				HttpMethod:           "POST",
				PathTemplate:         "/api/orders",
				UpstreamUrl:          "https://orders:8081",
				UpstreamSpiffeId:     "spiffe://aegis.local/service/orders",
				RequiresWorkloadMtls: false,
				RateLimit:            &snapshotv1.RateLimitPolicy{RequestsPerSecond: 1000, Burst: 2000},
				Timeout:              &snapshotv1.TimeoutPolicy{RequestTimeoutMs: 5000, UpstreamTimeoutMs: 2000},
			},
		},
		PolicyModules: []*snapshotv1.PolicyModule{
			{
				PackageName: "aegis.authz",
				ModuleName:  "rules.rego",
				SourceRego:  regoPolicy,
			},
		},
		IdentityMappings: []*snapshotv1.IdentityMapping{
			{
				Role:        "developer",
				Permissions: []string{"orders:read", "orders:create"},
			},
			{
				Role:        "user",
				Permissions: []string{"orders:read", "orders:create"},
			},
		},
	}

	return signer.SignSnapshot(payload)
}

// StartTestControlPlane initializes an in-memory gRPC control plane over bufconn.
func StartTestControlPlane(t *testing.T, initialVersion int64) *TestControlPlane {
	t.Helper()
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer := snapshot.NewSigner(privKey, "test-cp-key-1")
	verifier := snapshot.NewVerifier(pubKey)

	var initEnv *snapshotv1.SnapshotEnvelope
	if initialVersion > 0 {
		var err error
		initEnv, err = CreateSignedTestSnapshot(signer, initialVersion, "")
		require.NoError(t, err)
	}

	ackTracker := control.NewAckTracker(nil)
	distServer := control.NewSnapshotDistributionServer(initEnv, ackTracker)

	lis := bufconn.Listen(2 * 1024 * 1024)
	grpcServer := grpc.NewServer()
	snapshotv1.RegisterSnapshotDistributionServiceServer(grpcServer, distServer)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	cp := &TestControlPlane{
		DistServer: distServer,
		AckTracker: ackTracker,
		GRPCServer: grpcServer,
		Listener:   lis,
		Signer:     signer,
		Verifier:   verifier,
	}

	cp.Dialer = func(ctx context.Context, s string) (net.Conn, error) {
		if cp.severed.Load() {
			return nil, errors.New("control plane severed")
		}
		return lis.Dial()
	}

	return cp
}

func (c *ChaosCluster) spawnNode(parentCtx context.Context, gatewayID string) *TestGatewayNode {
	nodeCtx, nodeCancel := context.WithCancel(parentCtx)

	mgr := snapshot.NewManager(c.ControlPlane.Verifier)
	draining := proxy.NewDrainingState()

	streamClient := snapshot.NewStreamClient(
		"bufnet",
		gatewayID,
		mgr,
		c.ControlPlane.Verifier,
		grpc.WithContextDialer(c.ControlPlane.Dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)

	spoolDir, err := os.MkdirTemp("", fmt.Sprintf("aegis-spool-%s-*", gatewayID))
	if err != nil {
		panic(err)
	}

	spool, err := audit.NewDiskSpool(audit.DiskSpoolConfig{
		SpoolDir:         spoolDir,
		MaxSegmentBytes:  10 * 1024 * 1024,
		VolumeQuotaBytes: 100 * 1024 * 1024,
	})
	if err != nil {
		panic(err)
	}

	node := &TestGatewayNode{
		ID:            gatewayID,
		Manager:       mgr,
		StreamClient:  streamClient,
		DrainingState: draining,
		cancel:        nodeCancel,
		Spool:         spool,
		SpoolDir:      spoolDir,
	}

	coreHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = "test-" + uuid.NewString()[:8]
			r.Header.Set("X-Request-ID", reqID)
			w.Header().Set("X-Request-ID", reqID)
		}

		// Hard lease boundary fail-closed check
		if mgr.IsLeaseExpired(60 * time.Second) {
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Policy freshness lease expired (>60s)", "https://aegis.local/errors/policy-lease-expired", reqID)
			return
		}

		state := mgr.Active()
		if state == nil {
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Gateway configuration uninitialized", "https://aegis.local/errors/uninitialized", reqID)
			return
		}

		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		// Security: Strip client-supplied user headers
		r.Header.Del("X-Aegis-User")

		// Authentication
		var claims *identity.UserClaims
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			var authErr error
			claims, authErr = c.Validator.ValidateBearerToken(authHeader)
			if authErr != nil {
				proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", authErr.Error(),
					"https://aegis.local/errors/unauthorized", reqID)
				return
			}
		} else {
			if c.RequireAuth {
				proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", "Missing authorization header",
					"https://aegis.local/errors/unauthorized", reqID)
				return
			}
			claims = &identity.UserClaims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject: "test-user",
					ID:      uuid.NewString(),
				},
				Roles: []string{"developer"},
			}
		}

		// Ephemeral Redis Revocation Check
		if c.RevStore != nil {
			revoked, reason, err := c.RevStore.CheckRevocation(r.Context(), claims.Subject, claims.ID)
			if err != nil {
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Authorization dependency check unavailable; failing closed.",
					"https://aegis.local/errors/dependency-unavailable",
					reqID,
				)
				return
			}
			if revoked {
				proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", reason,
					"https://aegis.local/errors/forbidden", reqID)
				return
			}
		}

		route, err := state.Router.Match(r.Method, canonicalPath)
		if err != nil {
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching upstream route found",
				"https://aegis.local/errors/not-found", reqID)
			return
		}

		// Policy Evaluation
		input := policy.PolicyInput{
			Principal: policy.PrincipalInput{
				ID:    claims.Subject,
				Kind:  "user",
				Roles: claims.Roles,
			},
			Resource: policy.ResourceInput{
				Service: route.ServiceID,
				Route:   route.RouteID,
			},
			Request: policy.RequestInput{
				Method: r.Method,
				Path:   canonicalPath,
			},
			SnapshotVersion: state.Version,
		}

		decision, err := state.PolicyEngine.Evaluate(r.Context(), input)
		if err != nil || !decision.Allow {
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", "DENIED",
				"https://aegis.local/errors/forbidden", reqID)
			return
		}

		// Pre-Forward Spool Saturation Safety Gate
		if node.Spool != nil {
			sat, err := node.Spool.CheckSaturation()
			if err != nil || sat {
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Audit spool saturated; halting admission",
					"https://aegis.local/errors/spool-saturated",
					reqID,
				)
				return
			}

			event := &audit.CompletionEvent{
				EventID:         uuid.NewString(),
				Timestamp:       time.Now().UTC(),
				RequestID:       reqID,
				PrincipalID:     claims.Subject,
				PrincipalKind:   "user",
				PrincipalRoles:  claims.Roles,
				ClientIP:        r.RemoteAddr,
				HTTPMethod:      r.Method,
				CanonicalPath:   canonicalPath,
				RouteID:         route.RouteID,
				ServiceID:       route.ServiceID,
				Decision:        "allow",
				ReasonCode:      decision.ReasonCode,
				SnapshotVersion: state.Version,
			}
			if appendErr := node.Spool.AppendPreForward(event); appendErr != nil {
				if errors.Is(appendErr, audit.ErrSpoolSaturated) {
					proxy.WriteProblemDetails(
						w,
						http.StatusServiceUnavailable,
						"Service Unavailable",
						"Audit spool saturated; halting admission",
						"https://aegis.local/errors/spool-saturated",
						reqID,
					)
					return
				}
				proxy.WriteProblemDetails(w, http.StatusInternalServerError, "Internal Server Error",
					"Audit append failure", "https://aegis.local/errors/internal-error", reqID)
				return
			}
		}

		c.BackendCallCount.Add(1)
		w.Header().Set("X-Snapshot-Version", fmt.Sprintf("%d", state.Version))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"ok","version":%d,"reason":"%s"}`, state.Version, decision.ReasonCode)
	})

	probeHandler := proxy.CreateProbeHandler(
		draining,
		mgr.IsLeaseExpired,
		func() bool { return mgr.Active() != nil },
		func() bool {
			if node.Spool != nil {
				sat, _ := node.Spool.CheckSaturation()
				return sat
			}
			return false
		},
		coreHandler,
	)

	server := httptest.NewServer(probeHandler)
	node.Server = server
	node.Handler = probeHandler

	go func() {
		_ = streamClient.Start(nodeCtx)
	}()

	return node
}

// SpawnTestGatewayNode spins up an isolated gateway replica connected to the test control plane.
func SpawnTestGatewayNode(t *testing.T, parentCtx context.Context, gatewayID string, cp *TestControlPlane) *TestGatewayNode {
	t.Helper()
	dummyCluster := NewChaosCluster(t, 0)
	dummyCluster.ControlPlane = cp
	return dummyCluster.spawnNode(parentCtx, gatewayID)
}

// NewChaosCluster initializes a test cluster with numGateways replicas, full dependencies,
// and a simulated round-robin reverse proxy load balancer.
func NewChaosCluster(t *testing.T, numGateways int) *ChaosCluster {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	cp := StartTestControlPlane(t, 1)

	// In-memory Redis
	mr, err := miniredis.Run()
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	revStore := revocation.NewStore(rdb)
	rateLimiter := ratelimit.NewRateLimiter(rdb)

	// Cryptographic keys
	issuerPub, issuerPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	issuer := "aegis-issuer"
	audience := "aegis-gateway"
	validator := identity.NewTokenValidator(issuer, audience, issuerPub)

	assertionPriv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	minter := identity.NewAssertionMinter(assertionPriv)

	cluster := &ChaosCluster{
		t:            t,
		ControlPlane: cp,
		ctx:          ctx,
		cancel:       cancel,
		RedisServer:  mr,
		RedisClient:  rdb,
		RevStore:     revStore,
		RateLimiter:  rateLimiter,
		IssuerPub:    issuerPub,
		IssuerPriv:   issuerPriv,
		Issuer:       issuer,
		Audience:     audience,
		Validator:    validator,
		AssertionMin: minter,
	}

	cluster.BackendServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cluster.BackendCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))

	nodes := make([]*TestGatewayNode, numGateways)
	for i := 0; i < numGateways; i++ {
		gwID := fmt.Sprintf("gw-%d", i+1)
		nodes[i] = cluster.spawnNode(ctx, gwID)
	}
	cluster.Nodes = nodes

	lbHandler := &RoundRobinLB{
		cluster: cluster,
		client: &http.Client{
			Timeout: 2 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 50,
				IdleConnTimeout:     10 * time.Second,
			},
		},
	}
	cluster.LBServer = httptest.NewServer(lbHandler)
	cluster.LBURL = cluster.LBServer.URL
	cluster.Client = cluster.LBServer.Client()

	if numGateways > 0 {
		require.Eventually(t, func() bool {
			for _, n := range cluster.Nodes {
				if n.Manager.Active() == nil {
					return false
				}
			}
			return true
		}, 3*time.Second, 10*time.Millisecond, "All cluster gateway nodes must activate initial snapshot")
	}

	return cluster
}

// NewTestCluster initializes a test cluster with numGateways replicas (alias for backwards compatibility).
func NewTestCluster(t *testing.T, numGateways int) *TestCluster {
	return NewChaosCluster(t, numGateways)
}

// PublishNewSnapshot signs and broadcasts a new snapshot version to all cluster replicas.
func (c *ChaosCluster) PublishNewSnapshot(t *testing.T, version int64, regoPolicy string) *snapshotv1.SnapshotEnvelope {
	t.Helper()
	env, err := CreateSignedTestSnapshot(c.ControlPlane.Signer, version, regoPolicy)
	require.NoError(t, err)
	c.ControlPlane.DistServer.BroadcastSnapshot(env)
	return env
}
