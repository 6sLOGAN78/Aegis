package control

import (
	"encoding/json"
	"net/http"
	"time"

	controlv1 "aegis/pkg/api/control/v1"
)

// HandleListGateways returns live gateway replica statuses and convergence telemetry.
func (s *APIServer) HandleListGateways(w http.ResponseWriter, r *http.Request) {
	var activeVersion int64 = 1
	if s.distServer != nil {
		active := s.distServer.GetActiveSnapshotInternal()
		if active != nil {
			activeVersion = active.Version
		}
	}

	items := make([]controlv1.GatewayStatus, 0)
	if s.snapshotRepo != nil {
		acks, err := s.snapshotRepo.ListGatewayAcks(r.Context())
		if err == nil {
			for _, ack := range acks {
				status := controlv1.Healthy
				if ack.ActiveVersion < activeVersion {
					status = controlv1.Degraded
				}
				if time.Since(ack.LastHeartbeatAt) > 60*time.Second {
					status = controlv1.Partitioned
				}

				lastHb := ack.LastHeartbeatAt
				item := controlv1.GatewayStatus{
					GatewayId:       ack.GatewayID,
					ActiveVersion:   ack.ActiveVersion,
					Status:          status,
					LastHeartbeatAt: &lastHb,
					ConnectedAt:     ack.ConnectedAt,
					LeaseExpiresAt:  ack.LeaseExpiresAt,
				}
				items = append(items, item)
			}
		}
	}

	resp := controlv1.GatewayListResponse{
		Gateways: items,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
