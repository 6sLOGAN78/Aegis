package telemetry

import (
	"context"
	"net/http"
)

type routeIDHolder struct {
	value string
}

type routeIDKeyType struct{}

var routeIDKey = routeIDKeyType{}

// WithRouteID associates a canonical route ID with the context.
func WithRouteID(ctx context.Context, routeID string) context.Context {
	if holder, ok := ctx.Value(routeIDKey).(*routeIDHolder); ok && holder != nil {
		holder.value = routeID
		return ctx
	}
	return context.WithValue(ctx, routeIDKey, &routeIDHolder{value: routeID})
}

// SetRouteID updates the canonical route ID in the request context.
func SetRouteID(ctx context.Context, routeID string) {
	if holder, ok := ctx.Value(routeIDKey).(*routeIDHolder); ok && holder != nil {
		holder.value = routeID
	}
}

// GetRouteID extracts the canonical route ID from context, returning "unknown" if unset.
func GetRouteID(ctx context.Context) string {
	if holder, ok := ctx.Value(routeIDKey).(*routeIDHolder); ok && holder != nil && holder.value != "" {
		return holder.value
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
			holder := &routeIDHolder{value: "unknown"}
			ctx := context.WithValue(r.Context(), routeIDKey, holder)
			r = r.WithContext(ctx)

			rec := &statusRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(rec, r)

			m.RecordRequest(r.Method, holder.value, rec.statusCode)
		})
	}
}
