package control

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	controlv1 "aegis/pkg/api/control/v1"
)

type replicaInfo struct {
	gatewayID       string
	activeVersion   int64
	statusString    string
	errorMessage    *string
	lastHeartbeatAt time.Time
	connectedAt     time.Time
	leaseExpiresAt  *time.Time
}

// HandleListGateways returns live gateway replica statuses and convergence telemetry.
func (s *APIServer) HandleListGateways(w http.ResponseWriter, r *http.Request) {
	var activeVersion int64 = 1
	if s.distServer != nil {
		active := s.distServer.GetActiveSnapshotInternal()
		if active != nil {
			activeVersion = active.Version
		}
	}

	combined := make(map[string]*replicaInfo)

	// 1. Fetch persistent database records if repository is present
	if s.snapshotRepo != nil {
		acks, err := s.snapshotRepo.ListGatewayAcks(r.Context())
		if err == nil {
			for _, ack := range acks {
				combined[ack.GatewayID] = &replicaInfo{
					gatewayID:       ack.GatewayID,
					activeVersion:   ack.ActiveVersion,
					statusString:    ack.Status,
					errorMessage:    ack.ErrorMessage,
					lastHeartbeatAt: ack.LastHeartbeatAt,
					connectedAt:     ack.ConnectedAt,
					leaseExpiresAt:  ack.LeaseExpiresAt,
				}
			}
		}
	}

	// 2. Fetch in-memory active replica statuses from AckTracker if available
	if s.distServer != nil && s.distServer.AckTracker() != nil {
		inMem := s.distServer.AckTracker().GetReplicaStatuses()
		for gwID, ack := range inMem {
			hb := time.Now().UTC()
			if ack.AcknowledgedAt != nil {
				hb = ack.AcknowledgedAt.AsTime()
			}
			if existing, ok := combined[gwID]; ok {
				if ack.AcknowledgedAt != nil && hb.After(existing.lastHeartbeatAt) {
					existing.activeVersion = ack.ActiveVersion
					existing.statusString = ack.Status.String()
					if ack.ErrorMessage != "" {
						existing.errorMessage = &ack.ErrorMessage
					}
					existing.lastHeartbeatAt = hb
				}
			} else {
				var errMsg *string
				if ack.ErrorMessage != "" {
					errMsg = &ack.ErrorMessage
				}
				leaseExp := hb.Add(10 * time.Second)
				combined[gwID] = &replicaInfo{
					gatewayID:       gwID,
					activeVersion:   ack.ActiveVersion,
					statusString:    ack.Status.String(),
					errorMessage:    errMsg,
					lastHeartbeatAt: hb,
					connectedAt:     hb,
					leaseExpiresAt:  &leaseExp,
				}
			}
		}
	}

	// 3. Compute health status for each replica
	now := time.Now().UTC()
	items := make([]controlv1.GatewayStatus, 0, len(combined))

	for _, rep := range combined {
		status := controlv1.Healthy

		// healthy: Active version equals active control plane snapshot version and lease expiration is in the future.
		// degraded: Gateway is connected and heartbeating but active version lags behind active control plane snapshot version.
		// partitioned: Gateway heartbeat is older than 60 seconds (exceeds fail-closed boundary) or status is ACK_STATUS_REJECTED.
		if rep.statusString == "ACK_STATUS_REJECTED" || rep.statusString == "partitioned" || now.Sub(rep.lastHeartbeatAt) > 60*time.Second {
			status = controlv1.Partitioned
		} else if rep.activeVersion < activeVersion {
			status = controlv1.Degraded
		}

		lastHb := rep.lastHeartbeatAt
		connAt := rep.connectedAt
		if connAt.IsZero() {
			connAt = rep.lastHeartbeatAt
		}

		item := controlv1.GatewayStatus{
			GatewayId:       rep.gatewayID,
			ActiveVersion:   rep.activeVersion,
			Status:          status,
			LastHeartbeatAt: &lastHb,
			ConnectedAt:     connAt,
			LeaseExpiresAt:  rep.leaseExpiresAt,
		}
		items = append(items, item)
	}

	// Sort gateways stably by ID
	sort.Slice(items, func(i, j int) bool {
		return items[i].GatewayId < items[j].GatewayId
	})

	resp := controlv1.GatewayListResponse{
		Gateways: items,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
