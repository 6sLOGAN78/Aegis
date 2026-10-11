package control_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aegis/internal/control"
	"aegis/internal/telemetry"
	"aegis/internal/testpki"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type mtlsEnv struct {
	dist    *control.SnapshotDistributionServer
	tracker *control.AckTracker
	metrics *telemetry.Metrics
	lis     *bufconn.Listener
	pki     *testpki.PKI
}

// newMTLSEnv starts a server; serverCreds nil means a server with no TLS.
func newMTLSEnv(t *testing.T, tlsServer bool) *mtlsEnv {
	t.Helper()
	pki := testpki.New(t)
	tracker := control.NewAckTracker(nil)
	dist := control.NewSnapshotDistributionServer(nil, tracker)
	m := telemetry.NewMetrics()
	dist.SetMetrics(m)
	lis := bufconn.Listen(1024 * 1024)
	var srv *grpc.Server
	if tlsServer {
		srv = grpc.NewServer(grpc.Creds(pki.ServerCreds()))
	} else {
		srv = grpc.NewServer()
	}
	snapshotv1.RegisterSnapshotDistributionServiceServer(srv, dist)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	return &mtlsEnv{dist: dist, tracker: tracker, metrics: m, lis: lis, pki: pki}
}

func (e *mtlsEnv) client(t *testing.T, creds credentials.TransportCredentials) snapshotv1.SnapshotDistributionServiceClient {
	t.Helper()
	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return e.lis.Dial() }),
		grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return snapshotv1.NewSnapshotDistributionServiceClient(conn)
}

func rejections(e *mtlsEnv, reason string) float64 {
	var m dto.Metric
	_ = e.metrics.GRPCIdentityRejectionsTotal.WithLabelValues(reason).Write(&m)
	return m.GetCounter().GetValue()
}

func ackMsg(streamID, ackID string) *snapshotv1.GatewayMessage {
	return &snapshotv1.GatewayMessage{
		GatewayId: streamID,
		Payload: &snapshotv1.GatewayMessage_Ack{Ack: &snapshotv1.SnapshotAck{
			GatewayId: ackID, ActiveVersion: 1,
			Status: snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, AcknowledgedAt: timestamppb.Now(),
		}},
	}
}

func pongMsg(id string) *snapshotv1.GatewayMessage {
	return &snapshotv1.GatewayMessage{
		GatewayId: id,
		Payload:   &snapshotv1.GatewayMessage_Pong{Pong: &snapshotv1.HeartbeatPong{SequenceId: "1", ActiveVersion: 1}},
	}
}

// expectDenied sends msg and expects the stream to end with PermissionDenied.
func expectDenied(t *testing.T, e *mtlsEnv, msg *snapshotv1.GatewayMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := e.client(t, e.pki.ClientCreds("gateway-1")).StreamSnapshots(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(msg))
	_, err = stream.Recv()
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Empty(t, e.tracker.GetReplicaStatuses(), "nothing may be recorded")
	assert.Equal(t, 1.0, rejections(e, "id_mismatch"))
}

func TestMTLS_ValidClientRecordedUnderCertID(t *testing.T) {
	e := newMTLSEnv(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := e.client(t, e.pki.ClientCreds("gateway-1")).StreamSnapshots(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(ackMsg("gateway-1", "")))
	require.Eventually(t, func() bool {
		_, ok := e.tracker.GetReplicaStatuses()["gateway-1"]
		return ok
	}, 2*time.Second, 20*time.Millisecond)

	require.NoError(t, stream.Send(pongMsg("gateway-1")))
	require.NoError(t, stream.Send(ackMsg("gateway-1", "gateway-1")))
	assert.Len(t, e.tracker.GetReplicaStatuses(), 1)
}

func TestMTLS_PlaintextClientRejected(t *testing.T) {
	e := newMTLSEnv(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return e.lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	stream, err := snapshotv1.NewSnapshotDistributionServiceClient(conn).StreamSnapshots(ctx)
	if err == nil {
		_, err = stream.Recv()
	}
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Zero(t, e.dist.ConnectedClientsCount())
}

func TestMTLS_NoCertClientRejected(t *testing.T) {
	e := newMTLSEnv(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := e.client(t, e.pki.NoCertClientCreds()).StreamSnapshots(ctx)
	if err == nil {
		_, err = stream.Recv()
	}
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestMTLS_WrongCAClientRejected(t *testing.T) {
	e := newMTLSEnv(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := e.client(t, e.pki.ForeignClientCreds("gateway-1")).StreamSnapshots(ctx)
	if err == nil {
		_, err = stream.Recv()
	}
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Zero(t, e.dist.ConnectedClientsCount())
}

func TestMTLS_RequestIDMismatch(t *testing.T) {
	e := newMTLSEnv(t, true)
	expectDenied(t, e, ackMsg("gateway-2", ""))
}

func TestMTLS_AckIDMismatch(t *testing.T) {
	e := newMTLSEnv(t, true)
	expectDenied(t, e, ackMsg("gateway-1", "gateway-2"))
}

func TestMTLS_PongIDMismatch(t *testing.T) {
	e := newMTLSEnv(t, true)
	expectDenied(t, e, pongMsg("gateway-2"))
}

func TestMTLS_HeartbeatEmptyIDMismatch(t *testing.T) {
	e := newMTLSEnv(t, true)
	expectDenied(t, e, pongMsg(""))
}

func TestMTLS_NonGatewaySPIFFERejected(t *testing.T) {
	e := newMTLSEnv(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	creds := e.pki.ClientCredsForSPIFFE("spiffe://aegis.local/gateway")
	stream, err := e.client(t, creds).StreamSnapshots(ctx)
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Equal(t, 1.0, rejections(e, "invalid_identity"))
}

func TestMTLS_ServerWithoutTLSUnauthenticated(t *testing.T) {
	e := newMTLSEnv(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := e.client(t, insecure.NewCredentials()).StreamSnapshots(ctx)
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Equal(t, 1.0, rejections(e, "missing_cert"))
}

func TestMTLS_NewServerTransportCredentials(t *testing.T) {
	pki := testpki.New(t)
	dir := t.TempDir()
	pki.WriteFiles(dir, "gateway-1")

	creds, err := control.NewServerTransportCredentials(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"), filepath.Join(dir, "ca.crt"))
	require.NoError(t, err)
	require.NotNil(t, creds)

	_, err = control.NewServerTransportCredentials("", "", "")
	assert.Error(t, err)
	_, err = control.NewServerTransportCredentials(filepath.Join(dir, "nope.crt"), filepath.Join(dir, "server.key"), filepath.Join(dir, "ca.crt"))
	assert.Error(t, err)

	bad := filepath.Join(dir, "bad.pem")
	require.NoError(t, os.WriteFile(bad, []byte("not pem"), 0o600))
	_, err = control.NewServerTransportCredentials(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"), bad)
	assert.Error(t, err)
}
