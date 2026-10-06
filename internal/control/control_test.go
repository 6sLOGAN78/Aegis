package control_test

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
	"google.golang.org/protobuf/types/known/timestamppb"
)

func setupTestControlPlane(t *testing.T) (
	*control.SnapshotDistributionServer,
	*control.AckTracker,
	*snapshot.Signer,
	*snapshot.Verifier,
	snapshotv1.SnapshotDistributionServiceClient,
	func(),
) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer := snapshot.NewSigner(priv, "cp-key-1")
	verifier := snapshot.NewVerifier(pub)

	initialPayload := &snapshotv1.SnapshotPayload{
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
	}
	initialEnv, err := signer.SignSnapshot(initialPayload)
	require.NoError(t, err)

	ackTracker := control.NewAckTracker(nil)
	distServer := control.NewSnapshotDistributionServer(initialEnv, ackTracker)

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	snapshotv1.RegisterSnapshotDistributionServiceServer(grpcServer, distServer)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.DialContext(
		context.Background(),
		"bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	client := snapshotv1.NewSnapshotDistributionServiceClient(conn)

	cleanup := func() {
		_ = conn.Close()
		grpcServer.Stop()
		_ = lis.Close()
	}

	return distServer, ackTracker, signer, verifier, client, cleanup
}

func TestControl_StreamInitialSnapshotAndAck(t *testing.T) {
	distServer, ackTracker, _, verifier, client, cleanup := setupTestControlPlane(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.StreamSnapshots(ctx)
	require.NoError(t, err)

	// Gateway should immediately receive the active snapshot v1 on connect
	msg, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, msg)

	snap := msg.GetSnapshot()
	require.NotNil(t, snap)
	assert.Equal(t, int64(1), snap.Version)

	// Verify cryptographic signature of received snapshot
	payload, err := verifier.VerifySnapshot(snap, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), payload.Version)

	// Send acknowledgment back from gateway
	ackMsg := &snapshotv1.GatewayMessage{
		GatewayId: "gateway-node-1",
		Timestamp: timestamppb.Now(),
		Payload: &snapshotv1.GatewayMessage_Ack{
			Ack: &snapshotv1.SnapshotAck{
				GatewayId:      "gateway-node-1",
				ActiveVersion:  1,
				Status:         snapshotv1.AckStatus_ACK_STATUS_ACTIVATED,
				AcknowledgedAt: timestamppb.Now(),
			},
		},
	}
	err = stream.Send(ackMsg)
	require.NoError(t, err)

	// Allow time for server to ingest ack
	assert.Eventually(t, func() bool {
		statuses := ackTracker.GetReplicaStatuses()
		ack, exists := statuses["gateway-node-1"]
		return exists && ack.ActiveVersion == 1 && ack.Status == snapshotv1.AckStatus_ACK_STATUS_ACTIVATED
	}, 2*time.Second, 20*time.Millisecond)

	assert.Equal(t, 1, distServer.ConnectedClientsCount())
}

func TestControl_BroadcastSnapshot(t *testing.T) {
	distServer, ackTracker, signer, verifier, client, cleanup := setupTestControlPlane(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.StreamSnapshots(ctx)
	require.NoError(t, err)

	// Consume initial snapshot v1
	initMsg, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, initMsg.GetSnapshot())

	// Sign and broadcast snapshot v2
	v2Payload := &snapshotv1.SnapshotPayload{
		Version: 2,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:      "payments.charge",
				ServiceId:    "payments",
				HttpMethod:   "POST",
				PathTemplate: "/api/payments/charge",
				UpstreamUrl:  "https://payments:8082",
			},
		},
	}
	v2Env, err := signer.SignSnapshot(v2Payload)
	require.NoError(t, err)

	distServer.BroadcastSnapshot(v2Env)

	// Gateway should receive v2
	v2Msg, err := stream.Recv()
	require.NoError(t, err)
	receivedSnap := v2Msg.GetSnapshot()
	require.NotNil(t, receivedSnap)
	assert.Equal(t, int64(2), receivedSnap.Version)

	verifiedPayload, err := verifier.VerifySnapshot(receivedSnap, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), verifiedPayload.Version)
	assert.Equal(t, "payments.charge", verifiedPayload.Routes[0].RouteId)

	// Send ack for v2
	ackMsg := &snapshotv1.GatewayMessage{
		GatewayId: "gateway-node-1",
		Timestamp: timestamppb.Now(),
		Payload: &snapshotv1.GatewayMessage_Ack{
			Ack: &snapshotv1.SnapshotAck{
				GatewayId:      "gateway-node-1",
				ActiveVersion:  2,
				Status:         snapshotv1.AckStatus_ACK_STATUS_ACTIVATED,
				AcknowledgedAt: timestamppb.Now(),
			},
		},
	}
	err = stream.Send(ackMsg)
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		statuses := ackTracker.GetReplicaStatuses()
		ack, exists := statuses["gateway-node-1"]
		return exists && ack.ActiveVersion == 2
	}, 2*time.Second, 20*time.Millisecond)
}

func TestControl_LeaseGeneration(t *testing.T) {
	distServer, _, signer, verifier, client, cleanup := setupTestControlPlane(t)
	defer cleanup()

	// Short interval for testing
	leaseGen := control.NewLeaseGenerator(distServer, signer, 50*time.Millisecond, 2*time.Second)
	genCtx, genCancel := context.WithCancel(context.Background())
	defer genCancel()

	go leaseGen.Start(genCtx)
	defer leaseGen.Stop()

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer streamCancel()

	stream, err := client.StreamSnapshots(streamCtx)
	require.NoError(t, err)

	// Wait for at least one lease message
	var receivedLease *snapshotv1.FreshnessLease
	for i := 0; i < 5; i++ {
		msg, err := stream.Recv()
		require.NoError(t, err)
		if l := msg.GetLease(); l != nil {
			receivedLease = l
			break
		}
	}

	require.NotNil(t, receivedLease, "expected to receive at least one FreshnessLease")
	assert.Equal(t, int64(1), receivedLease.SnapshotVersion)
	assert.NotEmpty(t, receivedLease.LeaseId)

	err = verifier.VerifyLease(receivedLease, 1)
	assert.NoError(t, err)
}

func TestControl_GetActiveSnapshotUnary(t *testing.T) {
	distServer, _, _, _, client, cleanup := setupTestControlPlane(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	snap, err := client.GetActiveSnapshot(ctx, &snapshotv1.GetActiveSnapshotRequest{
		GatewayId:      "gateway-node-init",
		CurrentVersion: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, int64(1), snap.Version)
	assert.Equal(t, "cp-key-1", snap.SigningKeyId)

	// Internal getter matches
	internalSnap := distServer.GetActiveSnapshotInternal()
	assert.Equal(t, snap.Version, internalSnap.Version)
}

func TestControl_ClientDisconnectUnregistersCleanly(t *testing.T) {
	distServer, _, _, _, client, cleanup := setupTestControlPlane(t)
	defer cleanup()

	streamCtx, streamCancel := context.WithCancel(context.Background())
	stream, err := client.StreamSnapshots(streamCtx)
	require.NoError(t, err)

	_, err = stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, 1, distServer.ConnectedClientsCount())

	// Cancel stream context
	streamCancel()

	// Wait for client to unregister
	assert.Eventually(t, func() bool {
		return distServer.ConnectedClientsCount() == 0
	}, 2*time.Second, 20*time.Millisecond)
}
