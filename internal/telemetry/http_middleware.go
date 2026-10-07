package telemetry

import (
	"context"
	"net/http"
)

type routeIDKeyType struct{}

var routeIDKey = routeIDKeyType{}

// WithRouteID associates a canonical route ID with the context.
func WithRouteID(ctx context.Context, routeID string) context.Context {
	return context.WithValue(ctx, routeIDKey, routeID)
}

// GetRouteID extracts the canonical route ID from context, returning "unknown" if unset.
func GetRouteID(ctx context.Context) string {
	if val, ok := ctx.Value(routeIDKey).(string); ok && val != "" {
		return val
	}
	return "unknown"
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.statusCode = code
	rec.ResponseWriter.WriteHeader(code)
}

// MetricsMiddleware wraps an http.Handler to capture HTTP status codes and report
// bounded request counters to the telemetry metrics collector.
func MetricsMiddleware(m *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(rec, r)

			routeID := GetRouteID(r.Context())
			m.RecordRequest(r.Method, routeID, rec.statusCode)
		})
	}
}
