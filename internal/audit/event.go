package audit

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Explicit audit record types stored in audit_events.event_type (D-03).
const (
	EventTypeDecision   = "decision"
	EventTypeCompletion = "completion"
	EventTypeDenial     = "denial"
)

// MaxCanonicalPathBytes caps CanonicalPath so a hostile URL cannot bloat a row (D-18).
const MaxCanonicalPathBytes = 2048

// Bounds applied by Normalize to the fields that are not plain varchar columns (WR-04).
const (
	// MaxPrincipalRoles caps how many roles one event carries into the roles JSONB column.
	MaxPrincipalRoles = 64
	// MaxPrincipalRoleBytes caps the length of each role.
	MaxPrincipalRoleBytes = 128
	// MaxIDBytes caps an EventID or RequestID that is not a UUID (the worker replaces it).
	MaxIDBytes = 64
)

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
// width (CanonicalPath to MaxCanonicalPathBytes). PrincipalRoles (a JSONB column) lose
// NUL bytes and invalid UTF-8 and are capped to MaxPrincipalRoles entries of at most
// MaxPrincipalRoleBytes; the slice is replaced, never mutated in place. EventID and
// RequestID are rewritten to the canonical UUID form when they parse (Postgres rejects
// forms such as urn:uuid:...) and are otherwise only length-bounded to MaxIDBytes; the
// worker replaces an unparseable ID. It mutates in place, is idempotent and nil-safe.
// Timestamp and numeric fields are left alone (D-18, WR-04).
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
	e.EventID = normalizeID(e.EventID)
	e.RequestID = normalizeID(e.RequestID)
	if len(e.PrincipalRoles) > 0 {
		n := len(e.PrincipalRoles)
		if n > MaxPrincipalRoles {
			n = MaxPrincipalRoles
		}
		roles := make([]string, n)
		for i := range roles {
			roles[i] = clipString(e.PrincipalRoles[i], MaxPrincipalRoleBytes)
		}
		e.PrincipalRoles = roles
	}
}

// canonicalUUID returns the canonical form of s when it parses as a UUID. uuid.Parse is
// more lenient than Postgres (it accepts urn:uuid:...), so callers must store the
// canonical form, never the original text.
func canonicalUUID(s string) (string, bool) {
	u, err := uuid.Parse(s)
	if err != nil {
		return "", false
	}
	return u.String(), true
}

// normalizeID canonicalizes a UUID and bounds anything else without altering its bytes.
func normalizeID(s string) string {
	if c, ok := canonicalUUID(s); ok {
		return c
	}
	if len(s) <= MaxIDBytes {
		return s
	}
	cut := MaxIDBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
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
