package chaos

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aegis/internal/control"
	"aegis/internal/policy"
	"aegis/internal/proxy"
	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

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
}

// Close gracefully terminates the gateway node's HTTP server and gRPC stream client.
func (n *TestGatewayNode) Close() {
	if n.cancel != nil {
		n.cancel()
	}
	if n.StreamClient != nil {
		n.StreamClient.Stop()
	}
	if n.Server != nil {
		n.Server.Close()
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

// TestCluster coordinates the in-memory Control Plane and N gateway replica nodes.
type TestCluster struct {
	ControlPlane *TestControlPlane
	Nodes        []*TestGatewayNode
	ctx          context.Context
	cancel       context.CancelFunc
}

// Close terminates all cluster gateway nodes and the control plane.
func (c *TestCluster) Close() {
	c.cancel()
	for _, n := range c.Nodes {
		n.Close()
	}
	if c.ControlPlane != nil {
		c.ControlPlane.Close()
	}
}

// KillNode simulates abruptly terminating a gateway replica node.
func (c *TestCluster) KillNode(index int) {
	if index < 0 || index >= len(c.Nodes) {
		return
	}
	c.Nodes[index].Close()
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
				Permissions: []string{"orders:read"},
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

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	return &TestControlPlane{
		DistServer: distServer,
		AckTracker: ackTracker,
		GRPCServer: grpcServer,
		Listener:   lis,
		Signer:     signer,
		Verifier:   verifier,
		Dialer:     dialer,
	}
}

// SpawnTestGatewayNode spins up an isolated gateway replica connected to the test control plane.
func SpawnTestGatewayNode(t *testing.T, parentCtx context.Context, gatewayID string, cp *TestControlPlane) *TestGatewayNode {
	t.Helper()
	nodeCtx, nodeCancel := context.WithCancel(parentCtx)

	mgr := snapshot.NewManager(cp.Verifier)
	draining := proxy.NewDrainingState()

	streamClient := snapshot.NewStreamClient(
		"bufnet",
		gatewayID,
		mgr,
		cp.Verifier,
		grpc.WithContextDialer(cp.Dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)

	coreHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mgr.IsLeaseExpired(60 * time.Second) {
			http.Error(w, "LEASE_EXPIRED", http.StatusServiceUnavailable)
			return
		}

		state := mgr.Active()
		if state == nil {
			http.Error(w, "UNINITIALIZED", http.StatusServiceUnavailable)
			return
		}

		route, err := state.Router.Match(r.Method, r.URL.Path)
		if err != nil {
			http.Error(w, "NOT_FOUND", http.StatusNotFound)
			return
		}

		input := policy.PolicyInput{
			Principal: policy.PrincipalInput{
				ID:    "test-user",
				Kind:  "user",
				Roles: []string{"developer"},
			},
			Resource: policy.ResourceInput{
				Service: route.ServiceID,
				Route:   route.RouteID,
			},
			Request: policy.RequestInput{
				Method: r.Method,
				Path:   r.URL.Path,
			},
			SnapshotVersion: state.Version,
		}

		decision, err := state.PolicyEngine.Evaluate(r.Context(), input)
		if err != nil {
			http.Error(w, fmt.Sprintf("EVAL_ERROR: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("X-Snapshot-Version", fmt.Sprintf("%d", state.Version))
		w.Header().Set("Content-Type", "application/json")
		if decision.Allow {
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"status":"ok","version":%d,"reason":"%s"}`, state.Version, decision.ReasonCode)
		} else {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprintf(w, `{"status":"forbidden","version":%d,"reason":"%s"}`, state.Version, decision.ReasonCode)
		}
	})

	probeHandler := proxy.CreateProbeHandler(
		draining,
		mgr.IsLeaseExpired,
		func() bool { return mgr.Active() != nil },
		func() bool { return false },
		coreHandler,
	)

	server := httptest.NewServer(probeHandler)

	go func() {
		_ = streamClient.Start(nodeCtx)
	}()

	return &TestGatewayNode{
		ID:            gatewayID,
		Manager:       mgr,
		StreamClient:  streamClient,
		DrainingState: draining,
		Handler:       probeHandler,
		Server:        server,
		cancel:        nodeCancel,
	}
}

// NewTestCluster initializes a test cluster with numGateways replicas and an initial snapshot.
func NewTestCluster(t *testing.T, numGateways int) *TestCluster {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	cp := StartTestControlPlane(t, 1)

	nodes := make([]*TestGatewayNode, numGateways)
	for i := 0; i < numGateways; i++ {
		gwID := fmt.Sprintf("gw-%d", i+1)
		nodes[i] = SpawnTestGatewayNode(t, ctx, gwID, cp)
	}

	return &TestCluster{
		ControlPlane: cp,
		Nodes:        nodes,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// PublishNewSnapshot signs and broadcasts a new snapshot version to all cluster replicas.
func (c *TestCluster) PublishNewSnapshot(t *testing.T, version int64, regoPolicy string) *snapshotv1.SnapshotEnvelope {
	t.Helper()
	env, err := CreateSignedTestSnapshot(c.ControlPlane.Signer, version, regoPolicy)
	require.NoError(t, err)
	c.ControlPlane.DistServer.BroadcastSnapshot(env)
	return env
}
