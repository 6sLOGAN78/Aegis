package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

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

// DualServer manages both User (:8080) and Workload (:9443 mTLS) ingress HTTP servers.
type DualServer struct {
	userServer      *http.Server
	workloadServer  *http.Server
	cfg             *config.Config
	limiter         *ConcurrencyLimiter
	userHandler     http.Handler
	workloadHandler http.Handler
	rejections      *rejectionHolder
	shutdownCh      chan struct{}
	shutdownOnce    sync.Once
}

// NewDualServer constructs the dual-listener setup with physical credential isolation.
func NewDualServer(
	cfg *config.Config,
	userHandler http.Handler,
	workloadHandler http.Handler,
	workloadTLSConfig *tls.Config,
) *DualServer {
	limiter := NewConcurrencyLimiter(cfg.MaxConcurrentRequests)
	rejections := &rejectionHolder{}

	// Ingress wrapper for User Listener (:8080)
	userIngress := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			rejections.record("header_too_large")
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
		userHandler.ServeHTTP(w, r)
	})

	// Ingress wrapper for Workload Listener (:9443 mTLS)
	workloadIngress := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.NewString()
		r.Header.Set("X-Request-ID", reqID)
		w.Header().Set("X-Request-ID", reqID)

		// AUTH-03: Strictly reject inbound Bearer tokens on workload port to eliminate Confused Deputy
		if r.Header.Get("Authorization") != "" {
			rejections.record("ambiguous_credentials")
			WriteProblemDetails(
				w,
				http.StatusUnauthorized,
				"Unauthorized",
				"Ambiguous credentials: Bearer tokens are prohibited on the workload listener",
				"https://aegis.local/errors/ambiguous-credentials",
				reqID,
			)
			return
		}

		// Enforce maximum header bytes limit at ingress
		var headerBytes int
		for k, vv := range r.Header {
			headerBytes += len(k)
			for _, v := range vv {
				headerBytes += len(v)
			}
		}
		if headerBytes > cfg.MaxHeaderBytes {
			rejections.record("header_too_large")
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
		workloadHandler.ServeHTTP(w, r)
	})

	wrappedUserHandler := limiter.Wrap(userIngress)
	wrappedWorkloadHandler := limiter.Wrap(workloadIngress)

	userSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           wrappedUserHandler,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	var tlsCfg *tls.Config
	if workloadTLSConfig != nil {
		tlsCfg = workloadTLSConfig.Clone()
	} else {
		tlsCfg = &tls.Config{}
	}
	tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert

	workloadSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.WorkloadPort),
		Handler:           wrappedWorkloadHandler,
		TLSConfig:         tlsCfg,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	return &DualServer{
		userServer:      userSrv,
		workloadServer:  workloadSrv,
		cfg:             cfg,
		limiter:         limiter,
		userHandler:     wrappedUserHandler,
		workloadHandler: wrappedWorkloadHandler,
		rejections:      rejections,
		shutdownCh:      make(chan struct{}),
	}
}

// SetRejectionRecorder attaches the metrics recorder for rejections that occur
// before the audit middleware (concurrency limit, oversized headers, ambiguous
// credentials). Safe to call after construction; nil disables counting (D-16).
func (d *DualServer) SetRejectionRecorder(r RejectionRecorder) {
	d.rejections.set(r)
	d.limiter.SetRejectionRecorder(r)
}

// UserServer returns the user http.Server.
func (d *DualServer) UserServer() *http.Server {
	return d.userServer
}

// WorkloadServer returns the workload http.Server.
func (d *DualServer) WorkloadServer() *http.Server {
	return d.workloadServer
}

// UserHandler returns the wrapped user ingress handler.
func (d *DualServer) UserHandler() http.Handler {
	return d.userHandler
}

// WorkloadHandler returns the wrapped workload ingress handler.
func (d *DualServer) WorkloadHandler() http.Handler {
	return d.workloadHandler
}

// ListenAndServe concurrently starts the user HTTP server and workload HTTPS mTLS server.
func (d *DualServer) ListenAndServe() error {
	errCh := make(chan error, 2)

	go func() {
		if err := d.userServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("user server error: %w", err)
		}
	}()

	go func() {
		certFile := d.cfg.TLSCertPath
		keyFile := d.cfg.TLSKeyPath
		if err := d.workloadServer.ListenAndServeTLS(certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("workload server error: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-d.shutdownCh:
		return http.ErrServerClosed
	}
}

// Serve concurrently serves requests on the provided user and workload listeners.
func (d *DualServer) Serve(userLn net.Listener, workloadLn net.Listener) error {
	errCh := make(chan error, 2)

	go func() {
		if err := d.userServer.Serve(userLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("user server error: %w", err)
		}
	}()

	go func() {
		certFile := d.cfg.TLSCertPath
		keyFile := d.cfg.TLSKeyPath
		if err := d.workloadServer.ServeTLS(workloadLn, certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("workload server error: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-d.shutdownCh:
		return http.ErrServerClosed
	}
}

// Shutdown gracefully shuts down both servers concurrently.
func (d *DualServer) Shutdown(ctx context.Context) error {
	d.shutdownOnce.Do(func() {
		close(d.shutdownCh)
	})

	var wg sync.WaitGroup
	var uErr, wErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		uErr = d.userServer.Shutdown(ctx)
	}()
	go func() {
		defer wg.Done()
		wErr = d.workloadServer.Shutdown(ctx)
	}()

	wg.Wait()

	if uErr != nil && wErr != nil {
		return fmt.Errorf("user shutdown: %v; workload shutdown: %w", uErr, wErr)
	}
	if uErr != nil {
		return fmt.Errorf("user shutdown: %w", uErr)
	}
	if wErr != nil {
		return fmt.Errorf("workload shutdown: %w", wErr)
	}
	return nil
}

// Close immediately closes both servers.
func (d *DualServer) Close() error {
	d.shutdownOnce.Do(func() {
		close(d.shutdownCh)
	})

	uErr := d.userServer.Close()
	wErr := d.workloadServer.Close()

	if uErr != nil && wErr != nil {
		return fmt.Errorf("user close: %v; workload close: %w", uErr, wErr)
	}
	if uErr != nil {
		return fmt.Errorf("user close: %w", uErr)
	}
	if wErr != nil {
		return fmt.Errorf("workload close: %w", wErr)
	}
	return nil
}
