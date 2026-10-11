package snapshot

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"aegis/internal/testpki"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type recordingServer struct {
	snapshotv1.UnimplementedSnapshotDistributionServiceServer
	mu  sync.Mutex
	ids []string
}

func (r *recordingServer) StreamSnapshots(stream snapshotv1.SnapshotDistributionService_StreamSnapshotsServer) error {
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.ids = append(r.ids, msg.GatewayId)
	r.mu.Unlock()
	<-stream.Context().Done()
	return nil
}

func TestCredentials_ClientWithoutCredsFailsClosed(t *testing.T) {
	c := NewStreamClient("127.0.0.1:1", "gw-nocreds", nil, nil)

	err := c.runStream(context.Background(), func() {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no transport credentials configured")

	// The backoff loop keeps retrying until the context ends.
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, c.Start(ctx), context.DeadlineExceeded)
}

func TestCredentials_ClientWithMTLSStreams(t *testing.T) {
	pki := testpki.New(t)
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.Creds(pki.ServerCreds()))
	rec := &recordingServer{}
	snapshotv1.RegisterSnapshotDistributionServiceServer(srv, rec)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	c, _ := newRedeliveryClient(t)
	c.dialOpts = []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(pki.ClientCreds("gw-test")),
	}
	c.grpcAddr = "bufnet"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Start(ctx) }()
	defer c.Stop()

	require.Eventually(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.ids) > 0 && rec.ids[0] == "gw-test"
	}, 3*time.Second, 20*time.Millisecond)
}
