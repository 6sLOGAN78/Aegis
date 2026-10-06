package snapshot

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// StreamClient maintains a persistent bidirectional gRPC stream to the Control Plane
// with randomized exponential reconnect backoff and jitter (CTRL-03).
type StreamClient struct {
	grpcAddr  string
	gatewayID string
	manager   *Manager
	verifier  *Verifier
	dialOpts  []grpc.DialOption

	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
}

// NewStreamClient creates a new gRPC snapshot streaming client.
func NewStreamClient(
	grpcAddr string,
	gatewayID string,
	manager *Manager,
	verifier *Verifier,
	dialOpts ...grpc.DialOption,
) *StreamClient {
	if gatewayID == "" {
		gatewayID = "gateway-" + uuid.NewString()[:8]
	}

	return &StreamClient{
		grpcAddr:  grpcAddr,
		gatewayID: gatewayID,
		manager:   manager,
		verifier:  verifier,
		dialOpts:  dialOpts,
		stopCh:    make(chan struct{}),
	}
}

// Start initiates the streaming connection and reconnect loop. Blocks until ctx is canceled or Stop() is called.
func (c *StreamClient) Start(ctx context.Context) error {
	backoff := 100 * time.Millisecond
	const maxBackoff = 5 * time.Second
	const factor = 1.5
	const jitter = 0.2

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopCh:
			return nil
		default:
		}

		err := c.runStream(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-c.stopCh:
				return nil
			default:
			}

			// Apply randomized exponential backoff with jitter
			// jitter multiplier within [0.8, 1.2]
			multiplier := 1.0 + (rand.Float64()*2*jitter - jitter)
			sleepDuration := time.Duration(float64(backoff) * multiplier)
			if sleepDuration > maxBackoff {
				sleepDuration = maxBackoff
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-c.stopCh:
				return nil
			case <-time.After(sleepDuration):
			}

			backoff = time.Duration(float64(backoff) * factor)
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = 100 * time.Millisecond
		}
	}
}

func (c *StreamClient) runStream(ctx context.Context) error {
	opts := append([]grpc.DialOption{}, c.dialOpts...)
	if len(opts) == 0 {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.DialContext(ctx, c.grpcAddr, opts...)
	if err != nil {
		return fmt.Errorf("failed to dial control plane at %s: %w", c.grpcAddr, err)
	}
	defer conn.Close()

	client := snapshotv1.NewSnapshotDistributionServiceClient(conn)
	stream, err := client.StreamSnapshots(ctx)
	if err != nil {
		return fmt.Errorf("failed to open snapshot stream: %w", err)
	}

	// Send initial presence acknowledgment upon stream connection
	var activeVer int64 = 0
	if active := c.manager.Active(); active != nil {
		activeVer = active.Version
	}
	_ = stream.Send(&snapshotv1.GatewayMessage{
		GatewayId: c.gatewayID,
		Timestamp: timestamppb.Now(),
		Payload: &snapshotv1.GatewayMessage_Ack{
			Ack: &snapshotv1.SnapshotAck{
				GatewayId:      c.gatewayID,
				ActiveVersion:  activeVer,
				Status:         snapshotv1.AckStatus_ACK_STATUS_ACTIVATED,
				AcknowledgedAt: timestamppb.Now(),
			},
		},
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopCh:
			return nil
		default:
		}

		msg, err := stream.Recv()
		if err != nil {
			return err
		}

		if msg == nil {
			continue
		}

		switch p := msg.Payload.(type) {
		case *snapshotv1.ControlPlaneMessage_Snapshot:
			snap := p.Snapshot
			actErr := c.manager.ValidateAndActivate(ctx, snap)
			ack := &snapshotv1.SnapshotAck{
				GatewayId:      c.gatewayID,
				AcknowledgedAt: timestamppb.Now(),
			}

			if actErr != nil {
				ack.Status = snapshotv1.AckStatus_ACK_STATUS_REJECTED
				ack.ErrorMessage = actErr.Error()
				if active := c.manager.Active(); active != nil {
					ack.ActiveVersion = active.Version
				}
			} else {
				ack.Status = snapshotv1.AckStatus_ACK_STATUS_ACTIVATED
				ack.ActiveVersion = snap.Version
			}

			_ = stream.Send(&snapshotv1.GatewayMessage{
				GatewayId: c.gatewayID,
				Timestamp: timestamppb.Now(),
				Payload:   &snapshotv1.GatewayMessage_Ack{Ack: ack},
			})

		case *snapshotv1.ControlPlaneMessage_Lease:
			lease := p.Lease
			var expectedVer int64 = 0
			if active := c.manager.Active(); active != nil {
				expectedVer = active.Version
			}
			if err := c.verifier.VerifyLease(lease, expectedVer); err == nil {
				c.manager.RecordLeaseRenewal()
			}

		case *snapshotv1.ControlPlaneMessage_Ping:
			ping := p.Ping
			var currVer int64 = 0
			if active := c.manager.Active(); active != nil {
				currVer = active.Version
			}
			_ = stream.Send(&snapshotv1.GatewayMessage{
				GatewayId: c.gatewayID,
				Timestamp: timestamppb.Now(),
				Payload: &snapshotv1.GatewayMessage_Pong{
					Pong: &snapshotv1.HeartbeatPong{
						SequenceId:    ping.SequenceId,
						ActiveVersion: currVer,
					},
				},
			})
		}
	}
}

// Stop cleanly terminates the client streaming loop.
func (c *StreamClient) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stopped {
		c.stopped = true
		close(c.stopCh)
	}
}
