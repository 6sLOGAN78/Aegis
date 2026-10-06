package audit

import (
	"context"
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
	mu             sync.Mutex
	RequestID      string
	PrincipalID    string
	PrincipalKind  string
	PrincipalRoles []string
	CanonicalPath  string
	RouteID        string
	ServiceID      string
	Decision       string
	ReasonCode     string
	ErrorCode      string
	SnapshotVersion int64
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

// StatusCaptureResponseWriter wraps http.ResponseWriter to capture the HTTP response status code.
type StatusCaptureResponseWriter struct {
	http.ResponseWriter
	StatusCode  int
	wroteHeader bool
}

// NewStatusCaptureResponseWriter creates a new StatusCaptureResponseWriter defaulting to status 200.
func NewStatusCaptureResponseWriter(w http.ResponseWriter) *StatusCaptureResponseWriter {
	return &StatusCaptureResponseWriter{
		ResponseWriter: w,
		StatusCode:     http.StatusOK,
	}
}

// WriteHeader captures the status code before writing to the wrapped response writer.
func (rw *StatusCaptureResponseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.StatusCode = code
		rw.wroteHeader = true
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

// AuditMiddleware returns an HTTP middleware recording execution duration and emitting
// structured completion audit events on 100% of proxy responses (AUD-04).
func AuditMiddleware(logger *Logger, snapshotVersion int64) func(http.Handler) http.Handler {
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

			captureWriter := NewStatusCaptureResponseWriter(w)

			defer func() {
				duration := float64(time.Since(start).Microseconds()) / 1000.0

				ac.mu.Lock()
				decision := ac.Decision
				if captureWriter.StatusCode >= 200 && captureWriter.StatusCode < 300 {
					if decision != "deny" {
						decision = "allow"
					}
				} else if captureWriter.StatusCode >= 400 {
					decision = "deny"
				}

				reasonCode := ac.ReasonCode
				if reasonCode == "" {
					if decision == "allow" {
						reasonCode = "ALLOWED"
					} else {
						reasonCode = fmt.Sprintf("HTTP_%d", captureWriter.StatusCode)
					}
				}

				snapVer := snapshotVersion
				if ac.SnapshotVersion > 0 {
					snapVer = ac.SnapshotVersion
				}

				event := CompletionEvent{
					EventID:         uuid.NewString(),
					Timestamp:       time.Now().UTC(),
					RequestID:       reqID,
					PrincipalID:     ac.PrincipalID,
					PrincipalKind:   ac.PrincipalKind,
					PrincipalRoles:  ac.PrincipalRoles,
					ClientIP:        clientIP,
					HTTPMethod:      r.Method,
					CanonicalPath:   ac.CanonicalPath,
					RouteID:         ac.RouteID,
					ServiceID:       ac.ServiceID,
					Decision:        decision,
					ReasonCode:      reasonCode,
					HTTPStatus:      captureWriter.StatusCode,
					DurationMS:      duration,
					SnapshotVersion: snapVer,
					ErrorCode:       ac.ErrorCode,
				}
				ac.mu.Unlock()

				logger.LogCompletion(event)
			}()

			next.ServeHTTP(captureWriter, r)
		})
	}
}
