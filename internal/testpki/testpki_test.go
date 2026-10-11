package testpki

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"aegis/internal/identity"
)

type probeResult struct {
	chains    int
	spiffe    string
	gatewayID string
}

func startServer(t *testing.T, p *PKI) (*bufconn.Listener, <-chan probeResult) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	ch := make(chan probeResult, 4)
	srv := grpc.NewServer(
		grpc.Creds(p.ServerCreds()),
		grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			pr, ok := peer.FromContext(stream.Context())
			if !ok {
				return status.Error(codes.Internal, "no peer")
			}
			ti, ok := pr.AuthInfo.(credentials.TLSInfo)
			if !ok {
				return status.Error(codes.Internal, "no TLSInfo")
			}
			res := probeResult{chains: len(ti.State.VerifiedChains)}
			if len(ti.State.PeerCertificates) > 0 {
				sid, err := identity.ExtractSPIFFEID(ti.State.PeerCertificates[0], "aegis.local")
				if err == nil {
					res.spiffe = sid
					res.gatewayID, _ = identity.GatewayIDFromSPIFFE(sid)
				}
			}
			ch <- res
			return nil
		}),
	)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis, ch
}

func dialer(lis *bufconn.Listener) grpc.DialOption {
	return grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	})
}

func probe(ctx context.Context, conn *grpc.ClientConn) error {
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, "/spike/Probe")
	if err != nil {
		return err
	}
	_ = stream.CloseSend()
	var m any
	err = stream.RecvMsg(&m)
	_ = m
	return err
}

// codec-free probe: the server handler returns nil without reading, so the
// client observes either io.EOF (success) or a status error (failure).
func probeOK(t *testing.T, conn *grpc.ClientConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := probe(ctx, conn)
	if err != nil {
		st, _ := status.FromError(err)
		require.NotEqual(t, codes.Unavailable, st.Code(), "unexpected: %v", err)
	}
}

func TestSpikeA1_PeerTLSInfoAndGatewayID(t *testing.T) {
	p := New(t)
	lis, ch := startServer(t, p)

	conn, err := grpc.DialContext(context.Background(), "bufnet", dialer(lis), grpc.WithTransportCredentials(p.ClientCreds("gateway-1"))) //nolint:staticcheck
	require.NoError(t, err)
	defer conn.Close()
	probeOK(t, conn)

	select {
	case r := <-ch:
		assert.Greater(t, r.chains, 0)
		assert.Equal(t, identity.GatewaySPIFFEID("gateway-1"), r.spiffe)
		assert.Equal(t, "gateway-1", r.gatewayID)
	case <-time.After(5 * time.Second):
		t.Fatal("handler not reached")
	}
}

func TestSpikeA1_BareBackendIDIsNotGateway(t *testing.T) {
	p := New(t)
	lis, ch := startServer(t, p)
	conn, err := grpc.DialContext(context.Background(), "bufnet", dialer(lis), //nolint:staticcheck
		grpc.WithTransportCredentials(p.ClientCredsForSPIFFE("spiffe://aegis.local/ns/gateway/sa/aegis-gateway")))
	require.NoError(t, err)
	defer conn.Close()
	probeOK(t, conn)
	r := <-ch
	assert.NotEmpty(t, r.spiffe)
	assert.Empty(t, r.gatewayID)
}

func TestSpikeA1_RejectedClientsAreUnavailable(t *testing.T) {
	p := New(t)
	lis, _ := startServer(t, p)

	cases := map[string]grpc.DialOption{
		"foreign-ca": grpc.WithTransportCredentials(p.ForeignClientCreds("gateway-1")),
		"no-cert":    grpc.WithTransportCredentials(p.NoCertClientCreds()),
		"plaintext":  grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			conn, err := grpc.DialContext(context.Background(), "bufnet", dialer(lis), opt) //nolint:staticcheck
			require.NoError(t, err)
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = probe(ctx, conn)
			require.Error(t, err)
			assert.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
		})
	}
}

// A2: NewClient with a passthrough target and a context dialer over bufconn
// also works with TLS creds, and connects lazily.
func TestSpikeA2_NewClientPassthrough(t *testing.T) {
	p := New(t)
	lis, ch := startServer(t, p)
	conn, err := grpc.NewClient("passthrough:///bufnet", dialer(lis), grpc.WithTransportCredentials(p.ClientCreds("gateway-2")))
	require.NoError(t, err)
	defer conn.Close()
	probeOK(t, conn)
	r := <-ch
	assert.Equal(t, "gateway-2", r.gatewayID)
}
