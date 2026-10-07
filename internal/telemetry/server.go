package telemetry

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server manages a dedicated private HTTP server exporting Prometheus metrics (DIST-02).
type Server struct {
	httpServer *http.Server
	addr       string
}

// NewServer constructs a new private telemetry Server bound to the given address.
func NewServer(addr string, metrics *Metrics) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	return &Server{
		httpServer: httpSrv,
		addr:       addr,
	}
}

// Start boots the metrics HTTP server in a separate background goroutine.
func (s *Server) Start() {
	go func() {
		_ = s.httpServer.ListenAndServe()
	}()
}

// Shutdown gracefully drains active metrics scrape requests within the provided context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Handler returns the root http.Handler for unit testing or embedding.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}
