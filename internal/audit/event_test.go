package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventJSON_BackwardCompatible(t *testing.T) {
	legacy := `{"event_id":"e","timestamp":"2026-01-01T00:00:00Z","request_id":"r","principal_id":"p","principal_kind":"user","principal_roles":["a"],"client_ip":"1.2.3.4","http_method":"GET","canonical_path":"/x","route_id":"r1","service_id":"s1","decision":"allow","reason_code":"ok","http_status":200,"duration_ms":1.5,"snapshot_version":3}`
	var e CompletionEvent
	require.NoError(t, json.Unmarshal([]byte(legacy), &e))
	assert.Equal(t, "", e.EventType)
	assert.Equal(t, 0, e.SuppressedCount)

	e2 := CompletionEvent{EventType: EventTypeDenial, SuppressedCount: 9, Timestamp: time.Unix(0, 0).UTC()}
	b, err := json.Marshal(e2)
	require.NoError(t, err)
	var back CompletionEvent
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, EventTypeDenial, back.EventType)
	assert.Equal(t, 9, back.SuppressedCount)

	empty, err := json.Marshal(CompletionEvent{})
	require.NoError(t, err)
	assert.NotContains(t, string(empty), "event_type")
	assert.NotContains(t, string(empty), "suppressed_count")
}

func TestNormalize_StripsNUL(t *testing.T) {
	e := &CompletionEvent{
		CanonicalPath: "/api/\x00x",
		PrincipalID:   "a\x00b",
		HTTPMethod:    "G\x00ET",
		ReasonCode:    "\x00",
	}
	e.Normalize()
	assert.Equal(t, "/api/x", e.CanonicalPath)
	assert.Equal(t, "ab", e.PrincipalID)
	assert.Equal(t, "GET", e.HTTPMethod)
	assert.Equal(t, "", e.ReasonCode)
}

func TestNormalize_ColumnWidths(t *testing.T) {
	long := strings.Repeat("a", 300)
	e := &CompletionEvent{
		EventType:     long,
		PrincipalID:   long,
		PrincipalKind: long,
		ServiceID:     long,
		RouteID:       long,
		HTTPMethod:    strings.Repeat("M", 28),
		Decision:      long,
		ReasonCode:    long,
		ClientIP:      long,
		ErrorCode:     long,
	}
	e.Normalize()
	assert.Len(t, e.EventType, 32)
	assert.Len(t, e.PrincipalID, 128)
	assert.Len(t, e.PrincipalKind, 32)
	assert.Len(t, e.ServiceID, 64)
	assert.Len(t, e.RouteID, 64)
	assert.Len(t, e.HTTPMethod, 16)
	assert.Len(t, e.Decision, 16)
	assert.Len(t, e.ReasonCode, 64)
	assert.Len(t, e.ClientIP, 64)
	assert.Len(t, e.ErrorCode, 64)
}

func TestNormalize_PathCapRuneBoundary(t *testing.T) {
	// 3-byte runes; 2048 is not a multiple of 3 so the cut falls mid-rune.
	e := &CompletionEvent{CanonicalPath: strings.Repeat("€", 1000)}
	e.Normalize()
	assert.LessOrEqual(t, len(e.CanonicalPath), MaxCanonicalPathBytes)
	assert.True(t, utf8.ValidString(e.CanonicalPath))
	assert.Equal(t, 2046, len(e.CanonicalPath))

	// Narrow column with multibyte runes never splits a rune either.
	m := &CompletionEvent{HTTPMethod: strings.Repeat("€", 10)}
	m.Normalize()
	assert.True(t, utf8.ValidString(m.HTTPMethod))
	assert.LessOrEqual(t, len(m.HTTPMethod), 16)

	// Short path untouched.
	s := &CompletionEvent{CanonicalPath: "/ok"}
	s.Normalize()
	assert.Equal(t, "/ok", s.CanonicalPath)
}

func TestNormalize_InvalidUTF8(t *testing.T) {
	e := &CompletionEvent{
		CanonicalPath: "/a\xff\xfeb",
		PrincipalID:   "p\xc3",
	}
	e.Normalize()
	assert.Equal(t, "/ab", e.CanonicalPath)
	assert.Equal(t, "p", e.PrincipalID)
	assert.True(t, utf8.ValidString(e.CanonicalPath))
}

func TestNormalize_IdempotentNilSafeAndUntouched(t *testing.T) {
	var nilEvt *CompletionEvent
	assert.NotPanics(t, func() { nilEvt.Normalize() })

	ts := time.Unix(1700000000, 0).UTC()
	e := &CompletionEvent{
		EventID:         "not-normalized-here",
		RequestID:       "also\x00left-alone",
		Timestamp:       ts,
		PrincipalRoles:  []string{"r\x00"},
		HTTPStatus:      403,
		DurationMS:      2.5,
		SnapshotVersion: 7,
		SuppressedCount: 4,
		CanonicalPath:   "/p\x00" + strings.Repeat("z", 3000),
		HTTPMethod:      strings.Repeat("X", 30),
	}
	e.Normalize()
	once := *e
	e.Normalize()
	assert.Equal(t, once, *e)

	assert.Equal(t, "not-normalized-here", e.EventID)
	assert.Equal(t, "also\x00left-alone", e.RequestID)
	assert.Equal(t, ts, e.Timestamp)
	assert.Equal(t, []string{"r"}, e.PrincipalRoles, "roles are normalized since WR-04")
	assert.Equal(t, 403, e.HTTPStatus)
	assert.Equal(t, 2.5, e.DurationMS)
	assert.Equal(t, int64(7), e.SnapshotVersion)
	assert.Equal(t, 4, e.SuppressedCount)
}

func TestValidEventType(t *testing.T) {
	assert.True(t, ValidEventType("decision"))
	assert.True(t, ValidEventType("completion"))
	assert.True(t, ValidEventType("denial"))
	assert.False(t, ValidEventType(""))
	assert.False(t, ValidEventType("DENIAL"))
	assert.False(t, ValidEventType("other"))
}

// WR-04: roles reach a JSONB column, so they must be NUL free and bounded.
func TestNormalize_PrincipalRoles(t *testing.T) {
	roles := []string{"ok", "bad\x00role", "bad\xffutf8", strings.Repeat("r", 500)}
	for i := 0; i < 200; i++ {
		roles = append(roles, "extra")
	}
	orig := append([]string(nil), roles...)
	e := &CompletionEvent{PrincipalRoles: roles}
	e.Normalize()

	assert.Len(t, e.PrincipalRoles, MaxPrincipalRoles)
	assert.Equal(t, "ok", e.PrincipalRoles[0])
	assert.Equal(t, "badrole", e.PrincipalRoles[1])
	assert.Equal(t, "badutf8", e.PrincipalRoles[2])
	assert.Equal(t, MaxPrincipalRoleBytes, len(e.PrincipalRoles[3]))
	for _, r := range e.PrincipalRoles {
		assert.NotContains(t, r, "\x00")
		assert.True(t, utf8.ValidString(r))
	}
	assert.Equal(t, orig, roles, "the caller's slice must not be mutated")

	once := *e
	e.Normalize()
	assert.Equal(t, once, *e, "idempotent")

	var none CompletionEvent
	none.Normalize()
	assert.Empty(t, none.PrincipalRoles)
}

// WR-04: an ID Postgres' uuid type would reject is canonicalized when it parses and
// bounded when it does not (the worker then replaces it).
func TestNormalize_IDs(t *testing.T) {
	const canonical = "123e4567-e89b-12d3-a456-426614174000"
	for _, in := range []string{
		"urn:uuid:" + canonical,
		"{" + canonical + "}",
		"123E4567E89B12D3A456426614174000",
		strings.ToUpper(canonical),
	} {
		e := &CompletionEvent{EventID: in, RequestID: in}
		e.Normalize()
		assert.Equal(t, canonical, e.EventID, in)
		assert.Equal(t, canonical, e.RequestID, in)
	}

	huge := &CompletionEvent{RequestID: strings.Repeat("x", 1<<20)}
	huge.Normalize()
	assert.LessOrEqual(t, len(huge.RequestID), MaxIDBytes, "an unparseable id must be bounded")
}
