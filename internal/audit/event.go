package audit

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Explicit audit record types stored in audit_events.event_type (D-03).
const (
	EventTypeDecision   = "decision"
	EventTypeCompletion = "completion"
	EventTypeDenial     = "denial"
)

// MaxCanonicalPathBytes caps CanonicalPath so a hostile URL cannot bloat a row (D-18).
const MaxCanonicalPathBytes = 2048

// ValidEventType reports whether s is one of the known audit record types.
func ValidEventType(s string) bool {
	switch s {
	case EventTypeDecision, EventTypeCompletion, EventTypeDenial:
		return true
	}
	return false
}

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
	// EventType is the explicit record type (decision, completion, denial).
	// Empty on records written before Phase 8; the worker then guesses from HTTPStatus.
	EventType string `json:"event_type,omitempty"`
	// SuppressedCount is set only on summary rows that stand for suppressed duplicate denials.
	SuppressedCount int `json:"suppressed_count,omitempty"`
}

// Normalize makes every string field safe for its audit_events column: it strips
// NUL bytes, drops invalid UTF-8, and truncates on a rune boundary to the column
// width (CanonicalPath to MaxCanonicalPathBytes). It mutates in place, is idempotent
// and nil-safe. RequestID, EventID, Timestamp, PrincipalRoles and numeric fields are
// left alone (D-18).
func (e *CompletionEvent) Normalize() {
	if e == nil {
		return
	}
	e.EventType = clipString(e.EventType, 32)
	e.PrincipalID = clipString(e.PrincipalID, 128)
	e.PrincipalKind = clipString(e.PrincipalKind, 32)
	e.ServiceID = clipString(e.ServiceID, 64)
	e.RouteID = clipString(e.RouteID, 64)
	e.HTTPMethod = clipString(e.HTTPMethod, 16)
	e.Decision = clipString(e.Decision, 16)
	e.ReasonCode = clipString(e.ReasonCode, 64)
	e.ClientIP = clipString(e.ClientIP, 64)
	e.ErrorCode = clipString(e.ErrorCode, 64)
	e.CanonicalPath = clipString(e.CanonicalPath, MaxCanonicalPathBytes)
}

func clipString(s string, maxBytes int) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ToValidUTF8(s, "")
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
