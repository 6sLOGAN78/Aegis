package snapshot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/google/uuid"
	"google.golang.org/grpc"
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

// CalculateFullJitterBackoff computes exponential backoff with Full Jitter (CTRL-03).
// Formula: temp = min(maxBackoff, baseBackoff * (1.5 ^ attempt))
// Jitter is uniformly distributed in range [baseBackoff, temp].
// Bounds: never returns less than baseBackoff or greater than maxBackoff.
func CalculateFullJitterBackoff(attempt int, baseBackoff, maxBackoff time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if baseBackoff <= 0 {
		baseBackoff = 100 * time.Millisecond
	}
	if maxBackoff < baseBackoff {
		maxBackoff = baseBackoff
	}

	temp := float64(baseBackoff) * math.Pow(1.5, float64(attempt))
	if temp > float64(maxBackoff) {
		temp = float64(maxBackoff)
	}

	jitterRange := temp - float64(baseBackoff)
	if jitterRange <= 0 {
		return baseBackoff
	}

	sleep := float64(baseBackoff) + rand.Float64()*jitterRange
	dur := time.Duration(sleep)
	if dur < baseBackoff {
		return baseBackoff
	}
	if dur > maxBackoff {
		return maxBackoff
	}
	return dur
}

// Start initiates the streaming connection and reconnect loop. Blocks until ctx is canceled or Stop() is called.
func (c *StreamClient) Start(ctx context.Context) error {
	const baseBackoff = 100 * time.Millisecond
	const maxBackoff = 5 * time.Second

	var attempt int

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopCh:
			return nil
		default:
		}

		err := c.runStream(ctx, func() {
			attempt = 0
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-c.stopCh:
				return nil
			default:
			}

			sleepDuration := CalculateFullJitterBackoff(attempt, baseBackoff, maxBackoff)
			attempt++

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-c.stopCh:
				return nil
			case <-time.After(sleepDuration):
			}
		} else {
			attempt = 0
		}
	}
}

func (c *StreamClient) runStream(ctx context.Context, onFirstMessage func()) error {
	opts := append([]grpc.DialOption{}, c.dialOpts...)
	if len(opts) == 0 {
		// No plaintext fallback: fail closed and let the backoff loop retry (D-14).
		return errors.New("no transport credentials configured")
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-streamCtx.Done():
		case <-c.stopCh:
			cancel()
		}
	}()

	conn, err := grpc.DialContext(streamCtx, c.grpcAddr, opts...)
	if err != nil {
		return fmt.Errorf("failed to dial control plane at %s: %w", c.grpcAddr, err)
	}
	defer conn.Close()

	client := snapshotv1.NewSnapshotDistributionServiceClient(conn)
	stream, err := client.StreamSnapshots(streamCtx)
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

	var receivedFirst bool
	lastStatus := snapshotv1.AckStatus_ACK_STATUS_ACTIVATED
	lastErr := ""

	for {
		select {
		case <-streamCtx.Done():
			return streamCtx.Err()
		case <-c.stopCh:
			return nil
		default:
		}

		msg, err := stream.Recv()
		if err != nil {
			select {
			case <-c.stopCh:
				return nil
			default:
				return err
			}
		}

		if !receivedFirst {
			receivedFirst = true
			if onFirstMessage != nil {
				onFirstMessage()
			}
		}

		if msg == nil {
			continue
		}

		switch p := msg.Payload.(type) {
		case *snapshotv1.ControlPlaneMessage_Snapshot:
			var ack *snapshotv1.SnapshotAck
			ack, lastStatus, lastErr = c.processSnapshot(ctx, p.Snapshot, lastStatus, lastErr)

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
				// Heartbeat: ack each verified lease from this (the only) Send goroutine,
				// echoing the last real snapshot outcome so REJECTED is never masked.
				_ = stream.Send(&snapshotv1.GatewayMessage{
					GatewayId: c.gatewayID,
					Timestamp: timestamppb.Now(),
					Payload: &snapshotv1.GatewayMessage_Ack{
						Ack: buildHeartbeatAck(c.gatewayID, expectedVer, lastStatus, lastErr),
					},
				})
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

// buildHeartbeatAck constructs the ack sent in response to a verified lease.
func buildHeartbeatAck(gatewayID string, activeVersion int64, status snapshotv1.AckStatus, errMsg string) *snapshotv1.SnapshotAck {
	return &snapshotv1.SnapshotAck{
		GatewayId:      gatewayID,
		ActiveVersion:  activeVersion,
		Status:         status,
		ErrorMessage:   errMsg,
		AcknowledgedAt: timestamppb.Now(),
	}
}

// nextAckState maps a snapshot activation result to the status/error echoed by heartbeats.
func nextAckState(actErr error) (snapshotv1.AckStatus, string) {
	if actErr != nil {
		return snapshotv1.AckStatus_ACK_STATUS_REJECTED, actErr.Error()
	}
	return snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, ""
}

// processSnapshot handles one pushed snapshot and returns the ack to send plus the
// status/error echoed by subsequent heartbeats. Re-delivery of the already-active
// snapshot (same Version AND PayloadSha256, as happens on stream reconnect) is an
// idempotent ACTIVATED confirm that skips verification and leaves prior status untouched.
func (c *StreamClient) processSnapshot(
	ctx context.Context,
	snap *snapshotv1.SnapshotEnvelope,
	prevStatus snapshotv1.AckStatus,
	prevErr string,
) (*snapshotv1.SnapshotAck, snapshotv1.AckStatus, string) {
	ack := &snapshotv1.SnapshotAck{
		GatewayId:      c.gatewayID,
		AcknowledgedAt: timestamppb.Now(),
	}

	if active := c.manager.Active(); snap != nil && active != nil &&
		snap.Version == active.Version && snap.PayloadSha256 == active.PayloadSha256 {
		ack.Status = snapshotv1.AckStatus_ACK_STATUS_ACTIVATED
		ack.ActiveVersion = active.Version
		return ack, prevStatus, prevErr
	}

	actErr := c.manager.ValidateAndActivate(ctx, snap)
	status, errMsg := nextAckState(actErr)
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
	return ack, status, errMsg
}
