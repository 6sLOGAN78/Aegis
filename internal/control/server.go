package control

import (
	"context"
	"log"
	"sync"

	"aegis/internal/identity"
	"aegis/internal/telemetry"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SnapshotDistributionServer implements the gRPC SnapshotDistributionService.
// It streams signed configuration snapshots and 10s freshness leases to connected gateways,
// and receives telemetry acknowledgments.
type SnapshotDistributionServer struct {
	snapshotv1.UnimplementedSnapshotDistributionServiceServer

	mu             sync.RWMutex
	activeSnapshot *snapshotv1.SnapshotEnvelope
	clients        map[string]chan *snapshotv1.ControlPlaneMessage
	ackTracker     *AckTracker
	metrics        *telemetry.Metrics
}

// SetMetrics wires the metrics registry used to count identity rejections.
// A nil registry (the default) disables counting.
func (s *SnapshotDistributionServer) SetMetrics(m *telemetry.Metrics) {
	s.mu.Lock()
	s.metrics = m
	s.mu.Unlock()
}

func (s *SnapshotDistributionServer) countRejection(reason string) {
	s.mu.RLock()
	m := s.metrics
	s.mu.RUnlock()
	if m != nil && m.GRPCIdentityRejectionsTotal != nil {
		m.GRPCIdentityRejectionsTotal.WithLabelValues(reason).Inc()
	}
}

// gatewayIdentityFromContext derives the gateway id from the verified peer
// certificate SPIFFE id (/gw/<id>). It is the only trusted identity source.
// The second return value is the closed metric reason on failure.
func gatewayIdentityFromContext(ctx context.Context) (string, string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return "", "missing_cert", status.Error(codes.Unauthenticated, "client certificate required")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.PeerCertificates) == 0 {
		return "", "missing_cert", status.Error(codes.Unauthenticated, "verified client certificate required")
	}
	spiffe, err := identity.ExtractSPIFFEID(tlsInfo.State.PeerCertificates[0], "aegis.local")
	if err != nil {
		return "", "invalid_identity", status.Error(codes.PermissionDenied, "client certificate has no valid SPIFFE identity")
	}
	id, err := identity.GatewayIDFromSPIFFE(spiffe)
	if err != nil {
		return "", "invalid_identity", status.Error(codes.PermissionDenied, "client certificate is not a gateway identity")
	}
	return id, "", nil
}

// NewSnapshotDistributionServer instantiates a SnapshotDistributionServer.
func NewSnapshotDistributionServer(initialSnapshot *snapshotv1.SnapshotEnvelope, ackTracker *AckTracker) *SnapshotDistributionServer {
	return &SnapshotDistributionServer{
		activeSnapshot: initialSnapshot,
		clients:        make(map[string]chan *snapshotv1.ControlPlaneMessage),
		ackTracker:     ackTracker,
	}
}

// StreamSnapshots handles bidirectional gRPC streaming with a gateway replica.
func (s *SnapshotDistributionServer) StreamSnapshots(stream snapshotv1.SnapshotDistributionService_StreamSnapshotsServer) error {
	certID, reason, idErr := gatewayIdentityFromContext(stream.Context())
	if idErr != nil {
		s.countRejection(reason)
		log.Printf("control: rejected snapshot stream: %v", idErr)
		return idErr
	}

	connID := uuid.NewString()
	msgCh := make(chan *snapshotv1.ControlPlaneMessage, 64)

	s.mu.Lock()
	s.clients[connID] = msgCh
	initialSnapshot := s.activeSnapshot
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, connID)
		s.mu.Unlock()
	}()

	// Immediately push the active snapshot if available upon connection
	if initialSnapshot != nil {
		msgCh <- &snapshotv1.ControlPlaneMessage{
			MessageId: uuid.NewString(),
			Timestamp: timestamppb.Now(),
			Payload: &snapshotv1.ControlPlaneMessage_Snapshot{
				Snapshot: proto.Clone(initialSnapshot).(*snapshotv1.SnapshotEnvelope),
			},
		}
	}

	errCh := make(chan error, 2)

	// Goroutine sending outgoing messages to gateway stream
	go func() {
		for {
			select {
			case <-stream.Context().Done():
				errCh <- stream.Context().Err()
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				if err := stream.Send(msg); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	// Goroutine reading incoming messages (acknowledgments, heartbeats) from gateway stream
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			if req != nil {
				mismatch := req.GatewayId != certID
				if ack := req.GetAck(); ack != nil && ack.GatewayId != "" && ack.GatewayId != certID {
					mismatch = true
				}
				if mismatch {
					s.countRejection("id_mismatch")
					log.Printf("control: gateway id mismatch on stream: cert=%q claimed=%q", certID, req.GatewayId)
					errCh <- status.Error(codes.PermissionDenied, "gateway id does not match client certificate")
					return
				}
				if ack := req.GetAck(); ack != nil {
					ack.GatewayId = certID
					if ack.AcknowledgedAt == nil {
						ack.AcknowledgedAt = timestamppb.Now()
					}
					if s.ackTracker != nil {
						_ = s.ackTracker.RecordAck(stream.Context(), ack)
					}
				}
				if pong := req.GetPong(); pong != nil && s.ackTracker != nil {
					// A Pong is a liveness signal: refresh the heartbeat with server time,
					// but never upgrade a previously REJECTED replica to ACTIVATED.
					rec := &snapshotv1.SnapshotAck{
						GatewayId:      certID,
						ActiveVersion:  pong.ActiveVersion,
						Status:         snapshotv1.AckStatus_ACK_STATUS_ACTIVATED,
						AcknowledgedAt: timestamppb.Now(),
					}
					if prior, ok := s.ackTracker.GetReplicaStatuses()[certID]; ok && prior.Status == snapshotv1.AckStatus_ACK_STATUS_REJECTED {
						rec.Status = snapshotv1.AckStatus_ACK_STATUS_REJECTED
						rec.ErrorMessage = prior.ErrorMessage
					}
					_ = s.ackTracker.RecordAck(stream.Context(), rec)
				}
			}
		}
	}()

	return <-errCh
}

// GetActiveSnapshot returns the latest active snapshot envelope for cold-start initialization.
func (s *SnapshotDistributionServer) GetActiveSnapshot(ctx context.Context, req *snapshotv1.GetActiveSnapshotRequest) (*snapshotv1.SnapshotEnvelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.activeSnapshot == nil {
		return nil, status.Errorf(codes.NotFound, "no active snapshot available")
	}

	return proto.Clone(s.activeSnapshot).(*snapshotv1.SnapshotEnvelope), nil
}

// BroadcastSnapshot stores the new active snapshot and broadcasts it to all connected gateway clients.
func (s *SnapshotDistributionServer) BroadcastSnapshot(env *snapshotv1.SnapshotEnvelope) {
	if env == nil {
		return
	}

	s.mu.Lock()
	s.activeSnapshot = env
	msg := &snapshotv1.ControlPlaneMessage{
		MessageId: uuid.NewString(),
		Timestamp: timestamppb.Now(),
		Payload: &snapshotv1.ControlPlaneMessage_Snapshot{
			Snapshot: proto.Clone(env).(*snapshotv1.SnapshotEnvelope),
		},
	}

	for _, ch := range s.clients {
		select {
		case ch <- msg:
		default:
			// Non-blocking drop if channel is full to prevent lagging clients from stalling distribution
		}
	}
	s.mu.Unlock()
}

// BroadcastLease broadcasts a freshness lease renewal to all connected gateway clients.
func (s *SnapshotDistributionServer) BroadcastLease(lease *snapshotv1.FreshnessLease) {
	if lease == nil {
		return
	}

	msg := &snapshotv1.ControlPlaneMessage{
		MessageId: uuid.NewString(),
		Timestamp: timestamppb.Now(),
		Payload: &snapshotv1.ControlPlaneMessage_Lease{
			Lease: proto.Clone(lease).(*snapshotv1.FreshnessLease),
		},
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, ch := range s.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

// GetActiveSnapshotInternal returns the current active snapshot without gRPC status wrapping.
func (s *SnapshotDistributionServer) GetActiveSnapshotInternal() *snapshotv1.SnapshotEnvelope {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.activeSnapshot == nil {
		return nil
	}
	return proto.Clone(s.activeSnapshot).(*snapshotv1.SnapshotEnvelope)
}

// ConnectedClientsCount returns the number of active gateway client streams.
func (s *SnapshotDistributionServer) ConnectedClientsCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// AckTracker returns the AckTracker associated with this distribution server.
func (s *SnapshotDistributionServer) AckTracker() *AckTracker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ackTracker
}
