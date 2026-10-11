package control

import (
	"encoding/json"
	"log"
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

const (
	defaultGatewayStaleAfter = 15 * time.Minute
	gatewayPartitionAfter    = 60 * time.Second
	// minRejectedStaleAfter is the floor for hiding REJECTED replicas. A rejection
	// is a failure signal, so it stays listed far longer than an ordinary stale
	// record, but not forever (a decommissioned rejecter must not remain a ghost).
	minRejectedStaleAfter = 24 * time.Hour
)

// rejectedStaleAfter returns the (much longer, finite) cutoff for REJECTED
// replicas: max(24h, 4 * staleAfter).
func rejectedStaleAfter(staleAfter time.Duration) time.Duration {
	if d := 4 * staleAfter; d > minRejectedStaleAfter {
		return d
	}
	return minRejectedStaleAfter
}

// gatewayStaleAfterFromEnv returns the heartbeat age beyond which a gateway
// record is hidden from the list. Read from AEGIS_GATEWAY_STALE_AFTER (Go
// duration). Invalid, non-positive or <=60s values fall back to the default so
// the 60s fail-closed partitioned boundary is never shadowed; in that case a
// warning is logged so the misconfiguration is visible. Called once at server
// construction (NewAPIServer), not per request.
func gatewayStaleAfterFromEnv(getenv func(string) string) time.Duration {
	v := getenv("AEGIS_GATEWAY_STALE_AFTER")
	if v == "" {
		return defaultGatewayStaleAfter
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= gatewayPartitionAfter {
		log.Printf("WARNING: AEGIS_GATEWAY_STALE_AFTER=%q invalid or <= %s; using default %s",
			v, gatewayPartitionAfter, defaultGatewayStaleAfter)
		return defaultGatewayStaleAfter
	}
	return d
}

// isStaleReplica reports whether a replica should be omitted from the list.
// Rejected replicas use a much longer cutoff (rejectedStaleAfter) so a recent
// failure signal is never hidden, while a long-decommissioned rejecter
// eventually ages out. Hidden records are still reported via HiddenStaleCount.
func isStaleReplica(now, lastHb time.Time, statusString string, staleAfter time.Duration) bool {
	if statusString == "ACK_STATUS_REJECTED" {
		return now.Sub(lastHb) > rejectedStaleAfter(staleAfter)
	}
	return now.Sub(lastHb) > staleAfter
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

	staleAfter := s.gatewayStaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultGatewayStaleAfter
	}

	hiddenStale := 0
	for _, rep := range combined {
		// Stale records are hidden from the list (and thus the convergence
		// denominator), not deleted from storage. They are counted in
		// HiddenStaleCount so the operator always sees that replicas went silent.
		if isStaleReplica(now, rep.lastHeartbeatAt, rep.statusString, staleAfter) {
			hiddenStale++
			continue
		}
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
		Gateways:         items,
		HiddenStaleCount: hiddenStale,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
