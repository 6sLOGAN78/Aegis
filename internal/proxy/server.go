package proxy

import (
	"context"
	"fmt"
	"net/http"

	"aegis/internal/config"

	"github.com/google/uuid"
)

// Server wraps http.Server with edge request limits, concurrency bounding, and correlation tracing.
type Server struct {
	httpServer *http.Server
	cfg        *config.Config
	limiter    *ConcurrencyLimiter
	handler    http.Handler
}

// NewServer constructs a new HTTP Gateway Server configured with listener limits and middleware.
func NewServer(cfg *config.Config, handler http.Handler) *Server {
	limiter := NewConcurrencyLimiter(cfg.MaxConcurrentRequests)

	// Ingress middleware: assign UUID v4 X-Request-ID, enforce header/body limits
	ingressHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.NewString()
		r.Header.Set("X-Request-ID", reqID)
		w.Header().Set("X-Request-ID", reqID)

		// Enforce maximum header bytes limit at ingress
		var headerBytes int
		for k, vv := range r.Header {
			headerBytes += len(k)
			for _, v := range vv {
				headerBytes += len(v)
			}
		}
		if headerBytes > cfg.MaxHeaderBytes {
			WriteProblemDetails(
				w,
				http.StatusRequestHeaderFieldsTooLarge,
				"Request Header Fields Too Large",
				"Request header size exceeds limit",
				"https://aegis.local/errors/header-too-large",
				reqID,
			)
			return
		}

		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		}

		handler.ServeHTTP(w, r)
	})

	rootHandler := limiter.Wrap(ingressHandler)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           rootHandler,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	return &Server{
		httpServer: httpSrv,
		cfg:        cfg,
		limiter:    limiter,
		handler:    rootHandler,
	}
}

// Handler returns the root HTTP handler configured for the server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// HTTPServer returns the underlying http.Server.
func (s *Server) HTTPServer() *http.Server {
	return s.httpServer
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Close immediately closes the server.
func (s *Server) Close() error {
	return s.httpServer.Close()
}
