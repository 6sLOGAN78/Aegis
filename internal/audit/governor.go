package audit

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Disposition is the governor's verdict for one denial event.
type Disposition int

const (
	// Record means the event must be written to the audit spool.
	Record Disposition = iota
	// Suppress means the event is counted into a window summary instead of written.
	Suppress
	// Drop means the event is discarded (unauthenticated rate cap); the caller counts it.
	Drop
)

// reasonClass decides which admission mechanism governs a deny reason.
type reasonClass int

const (
	// classOutage reasons describe a gateway-wide outage: keyed by reason only (D-14).
	classOutage reasonClass = iota + 1
	// classUnauth reasons come from unauthenticated requests: token-bucketed (D-05).
	classUnauth
	// classIdentified reasons come from an identified principal: suppressed per
	// (principal, route, reason) (D-04, D-15).
	classIdentified
)

// otherLabel is the closed-set label for any reason outside the classification table.
const otherLabel = "OTHER"

// reasonTable is the governor classification table. cmd/gateway/main.go literal deny
// reasons, the policy engine's own reason codes and the rego DENIED_* codes must all
// appear here; main_reasons_test.go fails the build when one is missing.
// POLICY_EVALUATION_ERROR is an error code set via ac.SetErrorCode, never a reason.
var reasonTable = map[string]reasonClass{
	// Outage class: one recorded row per reason per window, whoever hits it.
	"POLICY_LEASE_EXPIRED":    classOutage,
	"UNINITIALIZED":           classOutage,
	"DEPENDENCY_OUTAGE_REDIS": classOutage,
	"AUDIT_SPOOL_WRITE_ERROR": classOutage,

	// Unauthenticated class: per-reason token bucket.
	"BAD_REQUEST_INVALID_PATH":    classUnauth,
	"UNAUTHORIZED":                classUnauth,
	"UNAUTHORIZED_NO_CERT":        classUnauth,
	"UNAUTHORIZED_INVALID_SPIFFE": classUnauth,

	// Identified class: suppressor keyed (principal, route, reason).
	"RATE_LIMIT_EXCEEDED":              classIdentified,
	"AUDIT_SPOOL_SATURATED":            classIdentified,
	"ROUTE_NOT_FOUND":                  classIdentified,
	"PRINCIPAL_QUARANTINED":            classIdentified,
	"TOKEN_REVOKED":                    classIdentified,
	"DENIED_DEFAULT":                   classIdentified,
	"DENIED_WORKLOAD_FORBIDDEN":        classIdentified,
	"DENIED_DEVELOPER_ADMIN_FORBIDDEN": classIdentified,
	"DENIED_WORKLOAD_ADMIN_FORBIDDEN":  classIdentified,
	"DENIED_INVALID_PRINCIPAL":         classIdentified,
	"EVALUATION_ERROR":                 classIdentified,
	"DENIED_EMPTY_RESULT":              classIdentified,
	"MALFORMED_DECISION":               classIdentified,
}

// reasonClassOf returns the class of a reason and whether it is in the table.
func reasonClassOf(reason string) (reasonClass, bool) {
	c, ok := reasonTable[reason]
	return c, ok
}

// ReasonLabel maps a deny reason onto the closed metric label set. Anything outside the
// classification table (including the empty string) becomes "OTHER" so attacker-controlled
// strings can never create high-cardinality labels.
func ReasonLabel(reason string) string {
	if _, ok := reasonTable[reason]; ok {
		return reason
	}
	return otherLabel
}

// ClosedReasonLabels returns every reason label including "OTHER", sorted.
func ClosedReasonLabels() []string {
	out := make([]string, 0, len(reasonTable)+1)
	for r := range reasonTable {
		out = append(out, r)
	}
	out = append(out, otherLabel)
	sort.Strings(out)
	return out
}

// GovernorConfig tunes the admission policy. Zero values take defensive defaults;
// range validation lives in internal/config.
type GovernorConfig struct {
	SuppressWindow time.Duration
	MaxKeys        int
	UnauthRate     float64
	UnauthBurst    int
}

// Admission is the governor's decision for one event.
type Admission struct {
	Disposition Disposition
	// Label is ReasonLabel(reason), for the metrics counters.
	Label string
	// Overflow is true when the suppressor map was full and the event was routed to the
	// per-label overflow bucket.
	Overflow bool
}

// entry is one suppression window: the first event plus the count of repeats.
type entry struct {
	windowStart time.Time
	count       int
	first       CompletionEvent
}

// stashed is a closed window summary waiting for the next Sweep/Flush.
type stashed struct {
	closedAt time.Time
	count    int
	first    CompletionEvent
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// Governor decides which denial events are recorded, suppressed into a window count,
// or dropped. Time is always an explicit parameter; the zero value is not usable, use
// NewGovernor.
type Governor struct {
	cfg GovernorConfig

	mu       sync.Mutex
	entries  map[string]*entry
	overflow map[string]*entry // per reason label, outside the MaxKeys bound
	stash    map[string]*stashed
	buckets  map[string]*tokenBucket
}

// NewGovernor builds a Governor, replacing zero config values with defaults.
func NewGovernor(cfg GovernorConfig) *Governor {
	if cfg.SuppressWindow <= 0 {
		cfg.SuppressWindow = 60 * time.Second
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = 4096
	}
	if cfg.UnauthRate <= 0 {
		cfg.UnauthRate = 10
	}
	if cfg.UnauthBurst <= 0 {
		cfg.UnauthBurst = 50
	}
	return &Governor{
		cfg:      cfg,
		entries:  make(map[string]*entry),
		overflow: make(map[string]*entry),
		stash:    make(map[string]*stashed),
		buckets:  make(map[string]*tokenBucket),
	}
}

// Admit classifies ev and returns what to do with it.
func (g *Governor) Admit(ev *CompletionEvent, now time.Time) Admission {
	if ev == nil {
		return Admission{Disposition: Drop, Label: otherLabel}
	}
	label := ReasonLabel(ev.ReasonCode)
	class, known := reasonClassOf(ev.ReasonCode)

	g.mu.Lock()
	defer g.mu.Unlock()

	switch {
	case known && class == classOutage:
		return g.admitSuppress(govKeyOutage(ev.ReasonCode), label, ev, now)
	case ev.PrincipalKind != "anonymous":
		return g.admitSuppress(govKeyIdentified(ev), label, ev, now)
	default:
		bucketKey := otherLabel
		if known && class == classUnauth {
			bucketKey = ev.ReasonCode
		}
		return g.admitBucket(bucketKey, label, now)
	}
}

func govKeyOutage(reason string) string {
	return "\x00\x00" + clipString(reason, 64)
}

func govKeyIdentified(ev *CompletionEvent) string {
	return clipString(ev.PrincipalID, 128) + "\x00" + clipString(ev.RouteID, 64) + "\x00" + clipString(ev.ReasonCode, 64)
}

func (g *Governor) admitBucket(bucketKey, label string, now time.Time) Admission {
	b, ok := g.buckets[bucketKey]
	if !ok {
		b = &tokenBucket{tokens: float64(g.cfg.UnauthBurst), last: now}
		g.buckets[bucketKey] = b
	} else {
		if elapsed := now.Sub(b.last); elapsed > 0 {
			b.tokens += elapsed.Seconds() * g.cfg.UnauthRate
			if burst := float64(g.cfg.UnauthBurst); b.tokens > burst {
				b.tokens = burst
			}
			b.last = now
		}
	}
	if b.tokens >= 1 {
		b.tokens--
		return Admission{Disposition: Record, Label: label}
	}
	return Admission{Disposition: Drop, Label: label}
}

func (g *Governor) newEntry(ev *CompletionEvent, now time.Time) *entry {
	e := &entry{windowStart: now, first: *ev}
	if len(ev.PrincipalRoles) > 0 {
		e.first.PrincipalRoles = append([]string(nil), ev.PrincipalRoles...)
	}
	e.first.Normalize()
	return e
}

func (g *Governor) admitSuppress(key, label string, ev *CompletionEvent, now time.Time) Admission {
	if e, ok := g.entries[key]; ok {
		return Admission{Disposition: g.touch(e, key, ev, now), Label: label}
	}
	if len(g.entries) < g.cfg.MaxKeys {
		g.entries[key] = g.newEntry(ev, now)
		return Admission{Disposition: Record, Label: label}
	}
	// Map full: route to the per-label overflow bucket. Never record unconditionally,
	// so a flood of distinct keys costs at most one fsynced row per label per window.
	if e, ok := g.overflow[label]; ok {
		return Admission{Disposition: g.touch(e, "overflow|"+label, ev, now), Label: label, Overflow: true}
	}
	g.overflow[label] = g.newEntry(ev, now)
	return Admission{Disposition: Record, Label: label, Overflow: true}
}

// touch applies one event to an existing entry. Caller holds g.mu.
func (g *Governor) touch(e *entry, key string, ev *CompletionEvent, now time.Time) Disposition {
	end := e.windowStart.Add(g.cfg.SuppressWindow)
	if now.Before(end) {
		e.count++
		return Suppress
	}
	if e.count > 0 {
		g.stashWindow(key, e, end)
	}
	*e = *g.newEntry(ev, now)
	return Record
}

// stashWindow remembers a closed window. A key that already has a stashed summary
// gets the counts added so the stash stays bounded. Caller holds g.mu.
func (g *Governor) stashWindow(key string, e *entry, closedAt time.Time) {
	if s, ok := g.stash[key]; ok {
		s.count += e.count
		if closedAt.After(s.closedAt) {
			s.closedAt = closedAt
		}
		return
	}
	g.stash[key] = &stashed{closedAt: closedAt, count: e.count, first: e.first}
}

func (g *Governor) summary(first *CompletionEvent, count int, at time.Time) *CompletionEvent {
	s := *first
	if len(first.PrincipalRoles) > 0 {
		s.PrincipalRoles = append([]string(nil), first.PrincipalRoles...)
	}
	s.EventID = uuid.NewString()
	s.EventType = EventTypeDenial
	s.SuppressedCount = count
	s.Timestamp = at.UTC()
	s.DurationMS = 0
	return &s
}

// Sweep returns summary events for windows that closed at or before now (keyed map,
// overflow buckets and the late-Admit stash) and frees the closed entries.
func (g *Governor) Sweep(now time.Time) []*CompletionEvent {
	g.mu.Lock()
	defer g.mu.Unlock()

	var out []*CompletionEvent
	for k, s := range g.stash {
		out = append(out, g.summary(&s.first, s.count, s.closedAt))
		delete(g.stash, k)
	}
	for _, m := range []map[string]*entry{g.entries, g.overflow} {
		for k, e := range m {
			end := e.windowStart.Add(g.cfg.SuppressWindow)
			if now.Before(end) {
				continue
			}
			if e.count > 0 {
				out = append(out, g.summary(&e.first, e.count, end))
			}
			delete(m, k)
		}
	}
	return out
}

// Flush is the shutdown path: summaries for every open window with count > 0, then
// all state is cleared.
func (g *Governor) Flush(now time.Time) []*CompletionEvent {
	g.mu.Lock()
	defer g.mu.Unlock()

	var out []*CompletionEvent
	for _, s := range g.stash {
		out = append(out, g.summary(&s.first, s.count, s.closedAt))
	}
	for _, m := range []map[string]*entry{g.entries, g.overflow} {
		for _, e := range m {
			if e.count > 0 {
				at := e.windowStart.Add(g.cfg.SuppressWindow)
				if now.Before(at) {
					at = now
				}
				out = append(out, g.summary(&e.first, e.count, at))
			}
		}
	}
	g.entries = make(map[string]*entry)
	g.overflow = make(map[string]*entry)
	g.stash = make(map[string]*stashed)
	return out
}

func (g *Governor) keyCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.entries)
}

func (g *Governor) overflowCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.overflow)
}

func (g *Governor) stashLen() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.stash)
}

func (g *Governor) bucketCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.buckets)
}
