package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"aegis/internal/control"
	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestSnapshotStreaming(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer := snapshot.NewSigner(priv, "cp-key-1")
	verifier := snapshot.NewVerifier(pub)
	manager := snapshot.NewManager(verifier)

	// 1. Initial snapshot v1
	v1Payload := &snapshotv1.SnapshotPayload{
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
				SourceRego:  `package aegis.authz
default allow = false
allow if { input.request.path == "/api/orders" }
`,
			},
		},
	}
	v1Env, err := signer.SignSnapshot(v1Payload)
	require.NoError(t, err)

	ackTracker := control.NewAckTracker(nil)
	distServer := control.NewSnapshotDistributionServer(v1Env, ackTracker)

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	snapshotv1.RegisterSnapshotDistributionServiceServer(grpcServer, distServer)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer func() {
		grpcServer.Stop()
		_ = lis.Close()
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	// 2. Gateway StreamClient connecting to control plane
	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()

	streamClient := snapshot.NewStreamClient(
		"bufnet",
		"gw-replica-1",
		manager,
		verifier,
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)

	go func() {
		_ = streamClient.Start(clientCtx)
	}()
	defer streamClient.Stop()

	// 3. Verify gateway automatically activates snapshot v1 on cold connect
	require.Eventually(t, func() bool {
		state := manager.Active()
		return state != nil && state.Version == 1
	}, 3*time.Second, 20*time.Millisecond, "gateway should activate snapshot v1")

	stateV1 := manager.Active()
	route, err := stateV1.Router.Match("GET", "/api/orders")
	require.NoError(t, err)
	assert.Equal(t, "orders.list", route.RouteID)

	// Verify ACK was received by control plane
	require.Eventually(t, func() bool {
		statuses := ackTracker.GetReplicaStatuses()
		ack, ok := statuses["gw-replica-1"]
		return ok && ack.ActiveVersion == 1 && ack.Status == snapshotv1.AckStatus_ACK_STATUS_ACTIVATED
	}, 3*time.Second, 20*time.Millisecond, "control plane should receive activation ack for v1")

	// 4. Broadcast updated snapshot v2 with added route
	v2Payload := &snapshotv1.SnapshotPayload{
		Version: 2,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:      "orders.list",
				ServiceId:    "orders",
				HttpMethod:   "GET",
				PathTemplate: "/api/orders",
				UpstreamUrl:  "https://orders:8081",
			},
			{
				RouteId:      "payments.charge",
				ServiceId:    "payments",
				HttpMethod:   "POST",
				PathTemplate: "/api/payments/charge",
				UpstreamUrl:  "https://payments:8082",
			},
		},
		PolicyModules: []*snapshotv1.PolicyModule{
			{
				PackageName: "aegis.authz",
				ModuleName:  "rules.rego",
				SourceRego:  `package aegis.authz
default allow = false
allow if { true }
`,
			},
		},
	}
	v2Env, err := signer.SignSnapshot(v2Payload)
	require.NoError(t, err)

	distServer.BroadcastSnapshot(v2Env)

	// Verify gateway atomically swaps to v2
	require.Eventually(t, func() bool {
		state := manager.Active()
		return state != nil && state.Version == 2
	}, 3*time.Second, 20*time.Millisecond, "gateway should atomically swap to snapshot v2")

	stateV2 := manager.Active()
	paymentRoute, err := stateV2.Router.Match("POST", "/api/payments/charge")
	require.NoError(t, err)
	assert.Equal(t, "payments.charge", paymentRoute.RouteID)

	// Verify ACK for v2
	require.Eventually(t, func() bool {
		statuses := ackTracker.GetReplicaStatuses()
		ack, ok := statuses["gw-replica-1"]
		return ok && ack.ActiveVersion == 2 && ack.Status == snapshotv1.AckStatus_ACK_STATUS_ACTIVATED
	}, 3*time.Second, 20*time.Millisecond)

	// 5. Injected invalid snapshot (corrupted signature)
	corruptedEnv := &snapshotv1.SnapshotEnvelope{
		Version:       3,
		SchemaVersion: 1,
		PayloadSha256: v2Env.PayloadSha256,
		Payload:       v2Env.Payload,
		SigningKeyId:  "cp-key-1",
		Signature:     []byte("invalid-signature-bytes"),
	}
	distServer.BroadcastSnapshot(corruptedEnv)

	// Verify control plane receives rejection ACK and gateway keeps v2 active
	require.Eventually(t, func() bool {
		statuses := ackTracker.GetReplicaStatuses()
		ack, ok := statuses["gw-replica-1"]
		return ok && ack.Status == snapshotv1.AckStatus_ACK_STATUS_REJECTED
	}, 3*time.Second, 20*time.Millisecond, "gateway must reject corrupted snapshot")

	// Ensure gateway retained active v2 state without interruption
	activeState := manager.Active()
	assert.Equal(t, int64(2), activeState.Version)

	// 6. Injected rollback attack (version 1 <= active version 2)
	distServer.BroadcastSnapshot(v1Env)

	require.Eventually(t, func() bool {
		statuses := ackTracker.GetReplicaStatuses()
		ack, ok := statuses["gw-replica-1"]
		return ok && ack.Status == snapshotv1.AckStatus_ACK_STATUS_REJECTED
	}, 3*time.Second, 20*time.Millisecond, "gateway must reject non-monotonic version")

	assert.Equal(t, int64(2), manager.Active().Version)
}
