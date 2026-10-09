package audit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

type auditContextKey struct{}

// AuditContext tracks per-request security context and policy decision data for audit emission.
type AuditContext struct {
	mu              sync.Mutex
	RequestID       string
	PrincipalID     string
	PrincipalKind   string
	PrincipalRoles  []string
	CanonicalPath   string
	RouteID         string
	ServiceID       string
	Decision        string
	ReasonCode      string
	ErrorCode       string
	SnapshotVersion int64

	// decisionSet is true once a handler called SetDecision; the middleware then
	// treats ac.Decision as authoritative instead of guessing from the status.
	decisionSet bool
	// durableRecorded is true once the denial was handed to the durable sink.
	durableRecorded bool
}

// WithAuditContext creates a new AuditContext and attaches it to ctx.
func WithAuditContext(ctx context.Context, reqID string) (context.Context, *AuditContext) {
	ac := &AuditContext{
		RequestID:      reqID,
		PrincipalID:    "anonymous",
		PrincipalKind:  "anonymous",
		PrincipalRoles: []string{},
		Decision:       "deny",
	}
	return context.WithValue(ctx, auditContextKey{}, ac), ac
}

// FromContext extracts the AuditContext from ctx if present.
func FromContext(ctx context.Context) *AuditContext {
	if ac, ok := ctx.Value(auditContextKey{}).(*AuditContext); ok {
		return ac
	}
	return nil
}

// SetPrincipal sets the authenticated principal attributes on the audit context.
func (ac *AuditContext) SetPrincipal(id, kind string, roles []string) {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.PrincipalID = id
	ac.PrincipalKind = kind
	if roles == nil {
		ac.PrincipalRoles = []string{}
	} else {
		ac.PrincipalRoles = roles
	}
}

// SetRoute sets the matched route and service IDs on the audit context.
func (ac *AuditContext) SetRoute(routeID, serviceID string) {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.RouteID = routeID
	ac.ServiceID = serviceID
}

// SetCanonicalPath sets the canonical request path on the audit context.
func (ac *AuditContext) SetCanonicalPath(path string) {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.CanonicalPath = path
}

// SetDecision sets the policy evaluation decision and reason code.
func (ac *AuditContext) SetDecision(decision, reasonCode string) {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.Decision = decision
	ac.ReasonCode = reasonCode
	ac.decisionSet = true
}

// markRecorded notes that a denial row was handed to the durable sink.
func (ac *AuditContext) markRecorded() {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.durableRecorded = true
}

// wasRecorded reports whether a denial row was already handed to the durable sink.
func (ac *AuditContext) wasRecorded() bool {
	if ac == nil {
		return false
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.durableRecorded
}

// SetErrorCode sets an error code on the audit context.
func (ac *AuditContext) SetErrorCode(errCode string) {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.ErrorCode = errCode
}

// SetSnapshotVersion sets the configuration snapshot version on the audit context.
func (ac *AuditContext) SetSnapshotVersion(v int64) {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.SnapshotVersion = v
}

// ToCompletionEvent thread-safely extracts context fields into a CompletionEvent
// prior to upstream dispatch for pre-forward WAL spooling (AUD-01, Invariant 10).
func (ac *AuditContext) ToCompletionEvent(method, clientIP string, snapshotVersion int64) *CompletionEvent {
	if ac == nil {
		return nil
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()

	decision := ac.Decision
	if decision == "" {
		decision = "deny"
	}

	reasonCode := ac.ReasonCode
	if reasonCode == "" {
		if decision == "allow" {
			reasonCode = "ALLOWED"
		} else {
			reasonCode = "UNKNOWN_REASON"
		}
	}

	snapVer := snapshotVersion
	if snapVer <= 0 && ac.SnapshotVersion > 0 {
		snapVer = ac.SnapshotVersion
	}

	var roles []string
	if ac.PrincipalRoles != nil {
		roles = make([]string, len(ac.PrincipalRoles))
		copy(roles, ac.PrincipalRoles)
	} else {
		roles = []string{}
	}

	ev := &CompletionEvent{
		EventID:         uuid.NewString(),
		Timestamp:       time.Now().UTC(),
		RequestID:       ac.RequestID,
		PrincipalID:     ac.PrincipalID,
		PrincipalKind:   ac.PrincipalKind,
		PrincipalRoles:  roles,
		ClientIP:        clientIP,
		HTTPMethod:      method,
		CanonicalPath:   ac.CanonicalPath,
		RouteID:         ac.RouteID,
		ServiceID:       ac.ServiceID,
		Decision:        decision,
		ReasonCode:      reasonCode,
		SnapshotVersion: snapVer,
		ErrorCode:       ac.ErrorCode,
		EventType:       EventTypeDecision,
	}
	ev.Normalize()
	return ev
}

// Logger wraps a structured log/slog.Logger writing JSON audit records.
type Logger struct {
	logger *slog.Logger
}

// NewLogger instantiates a new Logger. Defaults to os.Stdout if w is nil.
func NewLogger(w io.Writer) *Logger {
	if w == nil {
		w = os.Stdout
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return &Logger{logger: slog.New(handler)}
}

// LogCompletion writes a structured JSON log entry for a request completion event (AUD-04).
func (l *Logger) LogCompletion(event CompletionEvent) {
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	attrs := []any{
		"event_id", event.EventID,
		"timestamp", event.Timestamp.Format(time.RFC3339Nano),
		"request_id", event.RequestID,
		"principal_id", event.PrincipalID,
		"principal_kind", event.PrincipalKind,
		"principal_roles", event.PrincipalRoles,
		"client_ip", event.ClientIP,
		"http_method", event.HTTPMethod,
		"method", event.HTTPMethod,
		"canonical_path", event.CanonicalPath,
		"path", event.CanonicalPath,
		"route_id", event.RouteID,
		"service_id", event.ServiceID,
		"decision", event.Decision,
		"reason_code", event.ReasonCode,
		"http_status", event.HTTPStatus,
		"status", event.HTTPStatus,
		"duration_ms", event.DurationMS,
		"snapshot_version", event.SnapshotVersion,
		"error_code", event.ErrorCode,
	}
	l.logger.Info("audit_completion", attrs...)
}

// Sink receives durable audit events from AuditMiddleware.
//
// RecordDenial may block briefly until the record is durable (or dropped or
// suppressed) and must never panic. EnqueueCompletion must be non-blocking.
// Both receive a normalized copy the callee may keep.
type Sink interface {
	RecordDenial(ev *CompletionEvent)
	EnqueueCompletion(ev *CompletionEvent)
}

type middlewareConfig struct {
	sink Sink
}

// Option configures AuditMiddleware.
type Option func(*middlewareConfig)

// WithSink attaches a durable audit sink. A nil sink is ignored.
func WithSink(s Sink) Option {
	return func(c *middlewareConfig) {
		c.sink = s
	}
}

// StatusCaptureResponseWriter wraps http.ResponseWriter to capture the HTTP response status code.
type StatusCaptureResponseWriter struct {
	http.ResponseWriter
	StatusCode  int
	wroteHeader bool
	// onFirstHeader, when set, runs once with the final (>= 200) status before the
	// header reaches the underlying writer.
	onFirstHeader func(code int)
}

// NewStatusCaptureResponseWriter creates a new StatusCaptureResponseWriter defaulting to status 200.
func NewStatusCaptureResponseWriter(w http.ResponseWriter) *StatusCaptureResponseWriter {
	return &StatusCaptureResponseWriter{
		ResponseWriter: w,
		StatusCode:     http.StatusOK,
	}
}

// WriteHeader captures the status code before writing to the wrapped response writer.
// Informational 1xx responses (except 101) are forwarded without being treated as
// the final response.
func (rw *StatusCaptureResponseWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		rw.ResponseWriter.WriteHeader(code)
		return
	}
	if !rw.wroteHeader {
		rw.StatusCode = code
		rw.wroteHeader = true
		if rw.onFirstHeader != nil && code >= 200 {
			rw.onFirstHeader(code)
		}
		rw.ResponseWriter.WriteHeader(code)
	}
}

// Write ensures WriteHeader(http.StatusOK) is called if not called yet.
func (rw *StatusCaptureResponseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.wroteHeader = true
	}
	return rw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher if supported by underlying ResponseWriter.
func (rw *StatusCaptureResponseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter.
func (rw *StatusCaptureResponseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// buildAuditEvent snapshots the AuditContext into an event for the given status.
// An explicit handler decision (decisionSet) is authoritative; otherwise the legacy
// status-based formula applies.
func buildAuditEvent(ac *AuditContext, reqID, clientIP, method string, status int, start time.Time, snapshotVersion int64) CompletionEvent {
	duration := float64(time.Since(start).Microseconds()) / 1000.0

	ac.mu.Lock()
	defer ac.mu.Unlock()

	decision := ac.Decision
	if !ac.decisionSet {
		if status >= 200 && status < 300 {
			if decision != "deny" {
				decision = "allow"
			}
		} else if status >= 400 {
			decision = "deny"
		}
	}

	reasonCode := ac.ReasonCode
	if reasonCode == "" {
		if decision == "allow" {
			reasonCode = "ALLOWED"
		} else {
			reasonCode = fmt.Sprintf("HTTP_%d", status)
		}
	}

	snapVer := snapshotVersion
	if ac.SnapshotVersion > 0 {
		snapVer = ac.SnapshotVersion
	}

	roles := make([]string, len(ac.PrincipalRoles))
	copy(roles, ac.PrincipalRoles)

	eventType := EventTypeCompletion
	if decision == "deny" {
		eventType = EventTypeDenial
	}

	return CompletionEvent{
		EventID:         uuid.NewString(),
		Timestamp:       time.Now().UTC(),
		RequestID:       reqID,
		PrincipalID:     ac.PrincipalID,
		PrincipalKind:   ac.PrincipalKind,
		PrincipalRoles:  roles,
		ClientIP:        clientIP,
		HTTPMethod:      method,
		CanonicalPath:   ac.CanonicalPath,
		RouteID:         ac.RouteID,
		ServiceID:       ac.ServiceID,
		Decision:        decision,
		ReasonCode:      reasonCode,
		HTTPStatus:      status,
		DurationMS:      duration,
		SnapshotVersion: snapVer,
		ErrorCode:       ac.ErrorCode,
		EventType:       eventType,
	}
}

// AuditMiddleware returns an HTTP middleware recording execution duration and emitting
// structured completion audit events on 100% of proxy responses (AUD-04).
//
// With WithSink, a denial is handed to the sink before the first response header is
// written (D-07) and an allowed request is handed over after the handler returns,
// without blocking (D-08).
func AuditMiddleware(logger *Logger, snapshotVersion int64, opts ...Option) func(http.Handler) http.Handler {
	cfg := &middlewareConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Extract or generate request ID
			reqID := r.Header.Get("X-Request-ID")
			if reqID == "" {
				reqID = uuid.NewString()
				r.Header.Set("X-Request-ID", reqID)
			}
			w.Header().Set("X-Request-ID", reqID)

			// Capture client IP strictly from socket connection (ignoring forwarding headers)
			clientIP := r.RemoteAddr
			if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
				clientIP = host
			}
			if clientIP == "" {
				clientIP = r.RemoteAddr
			}

			ctx, ac := WithAuditContext(r.Context(), reqID)
			ac.CanonicalPath = r.URL.Path
			r = r.WithContext(ctx)
			method := r.Method

			captureWriter := NewStatusCaptureResponseWriter(w)

			if cfg.sink != nil {
				sink := cfg.sink
				captureWriter.onFirstHeader = func(code int) {
					ev := buildAuditEvent(ac, reqID, clientIP, method, code, start, snapshotVersion)
					if ev.Decision != "deny" {
						return
					}
					durable := ev
					durable.Normalize()
					ac.markRecorded()
					sink.RecordDenial(&durable)
				}
			}

			defer func() {
				event := buildAuditEvent(ac, reqID, clientIP, method, captureWriter.StatusCode, start, snapshotVersion)

				logger.LogCompletion(event)

				if cfg.sink == nil {
					return
				}
				durable := event
				durable.Normalize()
				if durable.Decision == "deny" {
					if !ac.wasRecorded() {
						ac.markRecorded()
						cfg.sink.RecordDenial(&durable)
					}
				} else if durable.Decision == "allow" {
					cfg.sink.EnqueueCompletion(&durable)
				}
			}()

			next.ServeHTTP(captureWriter, r)
		})
	}
}

// WrapUpstreamErrorHandler records an AUD-04 error code on the request's AuditContext
// and then delegates to next unchanged, so the wire response is not altered.
func WrapUpstreamErrorHandler(next func(http.ResponseWriter, *http.Request, error)) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if r != nil {
			FromContext(r.Context()).SetErrorCode(classifyUpstreamError(err))
		}
		if next != nil {
			next(w, r, err)
		}
	}
}

func classifyUpstreamError(err error) string {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		return "REQUEST_BODY_TOO_LARGE"
	}
	if errors.Is(err, context.Canceled) {
		return "CLIENT_CANCELED"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "UPSTREAM_TIMEOUT"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "UPSTREAM_TIMEOUT"
	}
	return "UPSTREAM_UNAVAILABLE"
}
