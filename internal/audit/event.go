package audit

import "time"

// CompletionEvent defines the structured completion audit log model (AUD-04),
// mirroring pkg/api/control/v1/types.gen.go:AuditEvent semantics.
type CompletionEvent struct {
	EventID         string    `json:"event_id"`
	Timestamp       time.Time `json:"timestamp"`
	RequestID       string    `json:"request_id"`
	PrincipalID     string    `json:"principal_id"`
	PrincipalKind   string    `json:"principal_kind"`
	PrincipalRoles  []string  `json:"principal_roles"`
	ClientIP        string    `json:"client_ip"`
	HTTPMethod      string    `json:"http_method"`
	CanonicalPath   string    `json:"canonical_path"`
	RouteID         string    `json:"route_id"`
	ServiceID       string    `json:"service_id"`
	Decision        string    `json:"decision"`
	ReasonCode      string    `json:"reason_code"`
	HTTPStatus      int       `json:"http_status"`
	DurationMS      float64   `json:"duration_ms"`
	SnapshotVersion int64     `json:"snapshot_version"`
	ErrorCode       string    `json:"error_code,omitempty"`
}
