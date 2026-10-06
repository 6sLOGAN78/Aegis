package policy

// PolicyInput defines the typed authorization input structure matching policies/schemas/input.schema.json.
type PolicyInput struct {
	Principal       PrincipalInput `json:"principal"`
	Resource        ResourceInput  `json:"resource"`
	Request         RequestInput   `json:"request"`
	Context         ContextInput   `json:"context"`
	SnapshotVersion int64          `json:"snapshot_version"`
}

// PrincipalInput represents the identity attributes of the caller.
type PrincipalInput struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"` // "user", "workload", "anonymous"
	Roles []string `json:"roles"`
}

// ResourceInput represents the target microservice resource.
type ResourceInput struct {
	Service string `json:"service"` // "orders", "payments", "admin"
	Route   string `json:"route"`
}

// RequestInput represents the incoming HTTP request details.
type RequestInput struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// ContextInput represents dynamic runtime and risk attributes.
type ContextInput struct {
	RiskScore int    `json:"risk_score"`
	RiskState string `json:"risk_state"` // "available", "degraded", "unavailable"
}

// Decision represents the structured PDP decision matching policies/schemas/output.schema.json.
type Decision struct {
	Allow           bool   `json:"allow"`
	ReasonCode      string `json:"reason_code"`
	SnapshotVersion int64  `json:"snapshot_version"`
}
