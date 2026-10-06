package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"aegis/internal/audit"
	"aegis/internal/config"
	"aegis/internal/identity"
	"aegis/internal/pki"
	"aegis/internal/policy"
	"aegis/internal/proxy"
	"aegis/internal/ratelimit"
	"aegis/internal/revocation"
	"aegis/internal/snapshot"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var defaultDemoSeed = []byte("aegis-demo-issuer-secret-seed-32")

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	issuer := os.Getenv("AEGIS_ISSUER")
	if issuer == "" {
		issuer = "aegis-issuer"
	}

	audience := os.Getenv("AEGIS_AUDIENCE")
	if audience == "" {
		audience = "aegis-gateway"
	}

	pubKey := ed25519.PublicKey(ed25519.NewKeyFromSeed(defaultDemoSeed).Public().(ed25519.PublicKey))
	if pubKeyB64 := os.Getenv("AEGIS_ISSUER_PUBLIC_KEY"); pubKeyB64 != "" {
		if b, err := base64.StdEncoding.DecodeString(pubKeyB64); err == nil && len(b) == ed25519.PublicKeySize {
			pubKey = ed25519.PublicKey(b)
		} else {
			log.Printf("Warning: failed to decode AEGIS_ISSUER_PUBLIC_KEY, using default demo key: %v", err)
		}
	}

	// Pinned Control Plane Ed25519 Public Key (CTRL-02, Invariant 4)
	controlPlanePubKey := pubKey
	if cpKeyB64 := os.Getenv("AEGIS_CONTROL_PLANE_PUBLIC_KEY"); cpKeyB64 != "" {
		if b, err := base64.StdEncoding.DecodeString(cpKeyB64); err == nil && len(b) == ed25519.PublicKeySize {
			controlPlanePubKey = ed25519.PublicKey(b)
		} else {
			log.Printf("Warning: failed to decode AEGIS_CONTROL_PLANE_PUBLIC_KEY, using default demo key: %v", err)
		}
	}

	snapVerifier := snapshot.NewVerifier(controlPlanePubKey)
	snapManager := snapshot.NewManager(snapVerifier)

	// Connect gRPC Snapshot Stream Client with randomized backoff and jitter
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	gatewayID := os.Getenv("AEGIS_GATEWAY_ID")
	if gatewayID == "" {
		gatewayID = "gateway-" + uuid.NewString()[:8]
	}

	streamClient := snapshot.NewStreamClient(cfg.ControlPlaneGRPCAddr, gatewayID, snapManager, snapVerifier)
	go func() {
		if err := streamClient.Start(streamCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("Control plane stream client exited: %v", err)
		}
	}()

	// Initialize Redis client, revocation store, and rate limiter (REV-01, REV-03, REV-04, ADR-0005)
	var rdb *redis.Client
	var revStore *revocation.Store
	var rateLimiter *ratelimit.RateLimiter
	if cfg.RedisAddr != "" {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
		})
		revStore = revocation.NewStore(rdb)
		rateLimiter = ratelimit.NewRateLimiter(rdb)
	}

	tokenValidator := identity.NewTokenValidator(issuer, audience, pubKey)
	auditLogger := audit.NewLogger(nil)

	// Initialize Assertion Signing Key
	var assertionPrivKey ed25519.PrivateKey
	if cfg.AssertionPrivateKeyPath != "" {
		keyBytes, err := os.ReadFile(cfg.AssertionPrivateKeyPath)
		if err == nil {
			block, _ := pem.Decode(keyBytes)
			if block != nil {
				keyBytes = block.Bytes
			}
			if len(keyBytes) == ed25519.PrivateKeySize {
				assertionPrivKey = ed25519.PrivateKey(keyBytes)
			} else if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyBytes))); err == nil && len(b) == ed25519.PrivateKeySize {
				assertionPrivKey = ed25519.PrivateKey(b)
			} else if parsedKey, err := x509.ParsePKCS8PrivateKey(keyBytes); err == nil {
				if edKey, ok := parsedKey.(ed25519.PrivateKey); ok {
					assertionPrivKey = edKey
				}
			}
		} else {
			log.Printf("Warning: failed to read assertion private key from %s: %v", cfg.AssertionPrivateKeyPath, err)
		}
	}
	if assertionPrivKey == nil {
		if b64 := os.Getenv("AEGIS_ASSERTION_PRIVATE_KEY"); b64 != "" {
			if b, err := base64.StdEncoding.DecodeString(b64); err == nil && len(b) == ed25519.PrivateKeySize {
				assertionPrivKey = ed25519.PrivateKey(b)
			}
		}
	}
	if assertionPrivKey == nil {
		assertionPrivKey = ed25519.NewKeyFromSeed(defaultDemoSeed)
	}
	assertionMinter := identity.NewAssertionMinter(assertionPrivKey)

	// Load or generate Gateway Client mTLS Certificate and Root CA Pool
	var gwClientCert tls.Certificate
	if cfg.ClientCertPath != "" && cfg.ClientKeyPath != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCertPath, cfg.ClientKeyPath)
		if err == nil {
			gwClientCert = cert
		} else {
			log.Printf("Warning: failed to load client cert from %s / %s: %v", cfg.ClientCertPath, cfg.ClientKeyPath, err)
		}
	}

	var caCertPool *x509.CertPool
	if cfg.WorkloadCACertPath != "" {
		caBytes, err := os.ReadFile(cfg.WorkloadCACertPath)
		if err == nil {
			caCertPool = x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(caBytes)
		} else {
			log.Printf("Warning: failed to load CA cert from %s: %v", cfg.WorkloadCACertPath, err)
		}
	}

	// Fallback in-memory PKI if certificates or CA pool were unconfigured/missing
	if caCertPool == nil || len(gwClientCert.Certificate) == 0 {
		demoCA, err := pki.NewCA("Aegis Gateway Internal CA")
		if err != nil {
			log.Fatalf("Failed to initialize demo CA: %v", err)
		}
		if caCertPool == nil {
			caCertPool = demoCA.CertPool
		}
		if len(gwClientCert.Certificate) == 0 {
			gwClientCert, err = demoCA.IssueWorkloadCert("spiffe://aegis.local/ns/gateway/sa/aegis-gateway")
			if err != nil {
				log.Fatalf("Failed to issue fallback gateway client cert: %v", err)
			}
		}
	}

	// Shared Upstream Connection-Pooled mTLS Transport
	upstreamTransport := proxy.CreateUpstreamTransport(gwClientCert, caCertPool)

	// Load or generate Server Certificate for Workload Listener (:9443)
	var serverCert tls.Certificate
	if cfg.TLSCertPath != "" && cfg.TLSKeyPath != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertPath, cfg.TLSKeyPath)
		if err == nil {
			serverCert = cert
		} else {
			log.Printf("Warning: failed to load server TLS certificate from %s / %s: %v", cfg.TLSCertPath, cfg.TLSKeyPath, err)
			cfg.TLSCertPath = ""
			cfg.TLSKeyPath = ""
		}
	}
	if len(serverCert.Certificate) == 0 {
		demoCA, err := pki.NewCA("Aegis Gateway Workload CA")
		if err != nil {
			log.Fatalf("Failed to initialize workload server CA: %v", err)
		}
		serverCert, err = demoCA.IssueServerCert("aegis-gateway", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
		if err != nil {
			log.Fatalf("Failed to generate fallback server TLS certificate: %v", err)
		}
		cfg.TLSCertPath = ""
		cfg.TLSKeyPath = ""
	}

	workloadTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS13,
	}

	// Build Gateway Core Request Pipeline for User Ingress (:8080)
	gatewayHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		ac := audit.FromContext(r.Context())

		// Step 1: Bounded Freshness Lease Check (CTRL-04, Invariant 1, ADR-0004)
		if snapManager.IsLeaseExpired(60 * time.Second) {
			if ac != nil {
				ac.SetDecision("deny", "POLICY_LEASE_EXPIRED")
				ac.SetErrorCode("POLICY_LEASE_EXPIRED")
			}
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Policy freshness lease expired (>60s)",
				"https://aegis.local/errors/policy-lease-expired",
				reqID,
			)
			return
		}

		// Step 2: Active Snapshot Initialization Check (CTRL-02, Invariant 1)
		state := snapManager.Active()
		if state == nil {
			if ac != nil {
				ac.SetDecision("deny", "UNINITIALIZED")
				ac.SetErrorCode("UNINITIALIZED")
			}
			proxy.WriteProblemDetails(
				w,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"Gateway configuration uninitialized",
				"https://aegis.local/errors/uninitialized",
				reqID,
			)
			return
		}

		if ac != nil {
			ac.SetSnapshotVersion(state.Version)
		}

		// 1. Strict Zero-Repair Path Validation (GW-02)
		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			if ac != nil {
				ac.SetDecision("deny", "BAD_REQUEST_INVALID_PATH")
				ac.SetErrorCode("INVALID_PATH")
			}
			proxy.WriteProblemDetails(
				w,
				http.StatusBadRequest,
				"Bad Request",
				err.Error(),
				"https://aegis.local/errors/invalid-path",
				reqID,
			)
			return
		}

		// 2. Cryptographic JWT Bearer Token Authentication (AUTH-01)
		authHeader := r.Header.Get("Authorization")
		claims, err := tokenValidator.ValidateBearerToken(authHeader)
		if err != nil {
			if ac != nil {
				ac.SetCanonicalPath(canonicalPath)
				ac.SetDecision("deny", "UNAUTHORIZED")
				ac.SetErrorCode("UNAUTHORIZED")
			}
			proxy.WriteProblemDetails(
				w,
				http.StatusUnauthorized,
				"Unauthorized",
				err.Error(),
				"https://aegis.local/errors/unauthorized",
				reqID,
			)
			return
		}

		if ac != nil {
			ac.SetPrincipal(claims.Subject, "user", claims.Roles)
			ac.SetCanonicalPath(canonicalPath)
		}

		// 2b. Ephemeral Redis Token Revocation and Principal Quarantine Check (REV-03, REV-04, Invariant 1)
		if revStore != nil {
			revoked, reason, err := revStore.CheckRevocation(r.Context(), claims.Subject, claims.ID)
			if err != nil {
				if ac != nil {
					ac.SetDecision("deny", "DEPENDENCY_OUTAGE_REDIS")
					ac.SetErrorCode("DEPENDENCY_OUTAGE_REDIS")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Authorization dependency check unavailable; failing closed.",
					"https://aegis.local/errors/dependency-unavailable",
					reqID,
				)
				return
			}
			if revoked {
				if ac != nil {
					ac.SetDecision("deny", reason)
					ac.SetErrorCode(reason)
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusForbidden,
					"Forbidden",
					reason,
					"https://aegis.local/errors/forbidden",
					reqID,
				)
				return
			}
		}

		// 3. Deterministic Route Resolution against Active Snapshot (GW-03, Invariant 9)
		route, err := state.Router.Match(r.Method, canonicalPath)
		if err != nil {
			if ac != nil {
				ac.SetDecision("deny", "ROUTE_NOT_FOUND")
				ac.SetErrorCode("NOT_FOUND")
			}
			proxy.WriteProblemDetails(
				w,
				http.StatusNotFound,
				"Not Found",
				"No matching upstream route found",
				"https://aegis.local/errors/not-found",
				reqID,
			)
			return
		}

		if ac != nil {
			ac.SetRoute(route.RouteID, route.ServiceID)
		}

		// 3b. Distributed GCRA Token Bucket Rate Limiting (REV-01, ADR-0005)
		if rateLimiter != nil {
			var rps, burst int
			if route.RateLimit != nil {
				rps = int(route.RateLimit.RequestsPerSecond)
				burst = int(route.RateLimit.Burst)
			}
			res, err := rateLimiter.Allow(r.Context(), claims.Subject, route.RouteID, rps, burst)
			if err != nil {
				if ac != nil {
					ac.SetDecision("deny", "DEPENDENCY_OUTAGE_REDIS")
					ac.SetErrorCode("DEPENDENCY_OUTAGE_REDIS")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Authorization dependency check unavailable; failing closed.",
					"https://aegis.local/errors/dependency-unavailable",
					reqID,
				)
				return
			}
			if !res.Allowed {
				retrySec := int(math.Ceil(res.RetryAfter.Seconds()))
				if retrySec <= 0 {
					retrySec = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySec))
				if ac != nil {
					ac.SetDecision("deny", "RATE_LIMIT_EXCEEDED")
					ac.SetErrorCode("RATE_LIMIT_EXCEEDED")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusTooManyRequests,
					"Too Many Requests",
					"Rate limit exceeded",
					"https://aegis.local/errors/rate-limit-exceeded",
					reqID,
				)
				return
			}
		}

		// 4. In-Memory OPA Policy Evaluation against Precompiled Query (POL-01, POL-02)
		input := policy.PolicyInput{
			Principal: policy.PrincipalInput{
				ID:    claims.Subject,
				Kind:  "user",
				Roles: claims.Roles,
			},
			Resource: policy.ResourceInput{
				Service: route.ServiceID,
				Route:   route.RouteID,
			},
			Request: policy.RequestInput{
				Method: r.Method,
				Path:   canonicalPath,
			},
			Context: policy.ContextInput{
				RiskScore: 0,
				RiskState: "available",
			},
			SnapshotVersion: state.Version,
		}

		decision, err := state.PolicyEngine.Evaluate(r.Context(), input)
		if err != nil || !decision.Allow {
			reason := "DENIED_DEFAULT"
			if decision.ReasonCode != "" {
				reason = decision.ReasonCode
			}
			if ac != nil {
				ac.SetDecision("deny", reason)
				if err != nil {
					ac.SetErrorCode("POLICY_EVALUATION_ERROR")
				} else {
					ac.SetErrorCode("FORBIDDEN")
				}
			}
			proxy.WriteProblemDetails(
				w,
				http.StatusForbidden,
				"Forbidden",
				reason,
				"https://aegis.local/errors/forbidden",
				reqID,
			)
			return
		}

		if ac != nil {
			allowReason := "ALLOWED"
			if decision.ReasonCode != "" {
				allowReason = decision.ReasonCode
			}
			ac.SetDecision("allow", allowReason)
		}

		// 5. Mint Backend Assertion Token (AUTH-05)
		assertionToken, err := assertionMinter.MintAssertion(
			claims.Subject,
			"user",
			claims.Roles,
			route.ServiceID,
			r.Method,
			canonicalPath,
			reqID,
			state.Version,
		)
		if err != nil {
			log.Printf("Failed to mint backend assertion: %v", err)
			if ac != nil {
				ac.SetErrorCode("INTERNAL_ASSERTION_MINT_ERROR")
			}
			proxy.WriteProblemDetails(
				w,
				http.StatusInternalServerError,
				"Internal Server Error",
				"Failed to mint backend assertion",
				"https://aegis.local/errors/internal-error",
				reqID,
			)
			return
		}

		// 6. Reverse Proxy Forwarding over mTLS with Header Scrubbing and Assertion Injection (GW-04, GW-05, BYP-01)
		rp := proxy.NewReverseProxyWithMTLS(route.ParsedURL(), canonicalPath, reqID, assertionToken, upstreamTransport)
		rp.ServeHTTP(w, r)
	})

	// Build Workload Ingress Request Pipeline on Port :9443 (Pattern 8)
	workloadHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		ac := audit.FromContext(r.Context())

		// Step 1: Bounded Freshness Lease Check (CTRL-04, Invariant 1, ADR-0004)
		if snapManager.IsLeaseExpired(60 * time.Second) {
			if ac != nil {
				ac.SetDecision("deny", "POLICY_LEASE_EXPIRED")
				ac.SetErrorCode("POLICY_LEASE_EXPIRED")
			}
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Policy freshness lease expired (>60s)", "https://aegis.local/errors/policy-lease-expired", reqID)
			return
		}

		// Step 2: Active Snapshot Initialization Check (CTRL-02, Invariant 1)
		state := snapManager.Active()
		if state == nil {
			if ac != nil {
				ac.SetDecision("deny", "UNINITIALIZED")
				ac.SetErrorCode("UNINITIALIZED")
			}
			proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
				"Gateway configuration uninitialized", "https://aegis.local/errors/uninitialized", reqID)
			return
		}

		if ac != nil {
			ac.SetSnapshotVersion(state.Version)
		}

		// 1. Strict Zero-Repair Path Validation (GW-02)
		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
			if ac != nil {
				ac.SetDecision("deny", "BAD_REQUEST_INVALID_PATH")
				ac.SetErrorCode("INVALID_PATH")
			}
			proxy.WriteProblemDetails(w, http.StatusBadRequest, "Bad Request", err.Error(),
				"https://aegis.local/errors/invalid-path", reqID)
			return
		}

		// 2. SPIFFE Identity Extraction from TLS Peer Certificate (AUTH-02)
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			if ac != nil {
				ac.SetDecision("deny", "UNAUTHORIZED_NO_CERT")
				ac.SetErrorCode("UNAUTHORIZED")
			}
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", "Client certificate required",
				"https://aegis.local/errors/unauthorized", reqID)
			return
		}
		spiffeID, err := identity.ExtractSPIFFEID(r.TLS.PeerCertificates[0], "aegis.local")
		if err != nil {
			if ac != nil {
				ac.SetDecision("deny", "UNAUTHORIZED_INVALID_SPIFFE")
				ac.SetErrorCode("UNAUTHORIZED")
			}
			proxy.WriteProblemDetails(w, http.StatusUnauthorized, "Unauthorized", err.Error(),
				"https://aegis.local/errors/invalid-workload-identity", reqID)
			return
		}

		if ac != nil {
			ac.SetPrincipal(spiffeID, "workload", []string{"workload"})
			ac.SetCanonicalPath(canonicalPath)
		}

		// 2b. Ephemeral Redis Principal Quarantine Check (REV-03, REV-04, Invariant 1)
		if revStore != nil {
			revoked, reason, err := revStore.CheckRevocation(r.Context(), spiffeID, "")
			if err != nil {
				if ac != nil {
					ac.SetDecision("deny", "DEPENDENCY_OUTAGE_REDIS")
					ac.SetErrorCode("DEPENDENCY_OUTAGE_REDIS")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Authorization dependency check unavailable; failing closed.",
					"https://aegis.local/errors/dependency-unavailable",
					reqID,
				)
				return
			}
			if revoked {
				if ac != nil {
					ac.SetDecision("deny", reason)
					ac.SetErrorCode(reason)
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusForbidden,
					"Forbidden",
					reason,
					"https://aegis.local/errors/forbidden",
					reqID,
				)
				return
			}
		}

		// 3. Deterministic Route Resolution against Active Snapshot (GW-03, Invariant 9)
		route, err := state.Router.Match(r.Method, canonicalPath)
		if err != nil {
			if ac != nil {
				ac.SetDecision("deny", "ROUTE_NOT_FOUND")
				ac.SetErrorCode("NOT_FOUND")
			}
			proxy.WriteProblemDetails(w, http.StatusNotFound, "Not Found", "No matching route found",
				"https://aegis.local/errors/not-found", reqID)
			return
		}

		if ac != nil {
			ac.SetRoute(route.RouteID, route.ServiceID)
		}

		// 3b. Distributed GCRA Token Bucket Rate Limiting (REV-01, ADR-0005)
		if rateLimiter != nil {
			var rps, burst int
			if route.RateLimit != nil {
				rps = int(route.RateLimit.RequestsPerSecond)
				burst = int(route.RateLimit.Burst)
			}
			res, err := rateLimiter.Allow(r.Context(), spiffeID, route.RouteID, rps, burst)
			if err != nil {
				if ac != nil {
					ac.SetDecision("deny", "DEPENDENCY_OUTAGE_REDIS")
					ac.SetErrorCode("DEPENDENCY_OUTAGE_REDIS")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusServiceUnavailable,
					"Service Unavailable",
					"Authorization dependency check unavailable; failing closed.",
					"https://aegis.local/errors/dependency-unavailable",
					reqID,
				)
				return
			}
			if !res.Allowed {
				retrySec := int(math.Ceil(res.RetryAfter.Seconds()))
				if retrySec <= 0 {
					retrySec = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySec))
				if ac != nil {
					ac.SetDecision("deny", "RATE_LIMIT_EXCEEDED")
					ac.SetErrorCode("RATE_LIMIT_EXCEEDED")
				}
				proxy.WriteProblemDetails(
					w,
					http.StatusTooManyRequests,
					"Too Many Requests",
					"Rate limit exceeded",
					"https://aegis.local/errors/rate-limit-exceeded",
					reqID,
				)
				return
			}
		}

		// 4. In-Memory OPA Policy Evaluation against Precompiled Query
		input := policy.PolicyInput{
			Principal: policy.PrincipalInput{
				ID:    spiffeID,
				Kind:  "workload",
				Roles: []string{"workload"},
			},
			Resource: policy.ResourceInput{
				Service: route.ServiceID,
				Route:   route.RouteID,
			},
			Request: policy.RequestInput{
				Method: r.Method,
				Path:   canonicalPath,
			},
			Context: policy.ContextInput{
				RiskScore: 0,
				RiskState: "available",
			},
			SnapshotVersion: state.Version,
		}

		decision, err := state.PolicyEngine.Evaluate(r.Context(), input)
		if err != nil || !decision.Allow {
			reason := "DENIED_WORKLOAD_FORBIDDEN"
			if decision.ReasonCode != "" {
				reason = decision.ReasonCode
			}
			if ac != nil {
				ac.SetDecision("deny", reason)
				if err != nil {
					ac.SetErrorCode("POLICY_EVALUATION_ERROR")
				} else {
					ac.SetErrorCode("FORBIDDEN")
				}
			}
			proxy.WriteProblemDetails(w, http.StatusForbidden, "Forbidden", reason,
				"https://aegis.local/errors/forbidden", reqID)
			return
		}

		if ac != nil {
			allowReason := "ALLOWED"
			if decision.ReasonCode != "" {
				allowReason = decision.ReasonCode
			}
			ac.SetDecision("allow", allowReason)
		}

		// 5. Mint Backend Assertion JWT (AUTH-05)
		assertionJWT, err := assertionMinter.MintAssertion(
			spiffeID,
			"workload",
			[]string{"workload"},
			route.ServiceID,
			r.Method,
			canonicalPath,
			reqID,
			state.Version,
		)
		if err != nil {
			log.Printf("Failed to mint backend assertion for workload: %v", err)
			if ac != nil {
				ac.SetErrorCode("INTERNAL_ASSERTION_MINT_ERROR")
			}
			proxy.WriteProblemDetails(w, http.StatusInternalServerError, "Internal Server Error", "Failed to mint assertion",
				"https://aegis.local/errors/internal-error", reqID)
			return
		}

		// 6. Forward over Mutual TLS to Backend (GW-05)
		rp := proxy.NewReverseProxyWithMTLS(route.ParsedURL(), canonicalPath, reqID, assertionJWT, upstreamTransport)
		rp.ServeHTTP(w, r)
	})

	// Wrap handlers with audit middleware
	auditedUserHandler := audit.AuditMiddleware(auditLogger, 0)(gatewayHandler)
	auditedWorkloadHandler := audit.AuditMiddleware(auditLogger, 0)(workloadHandler)

	// Dual-Listener Server Manager (:8080 and :9443 mTLS)
	dualServer := proxy.NewDualServer(cfg, auditedUserHandler, auditedWorkloadHandler, workloadTLSConfig)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Aegis Gateway listening on :%d (User) and :%d (Workload mTLS)", cfg.Port, cfg.WorkloadPort)
		if err := dualServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down Aegis Gateway gracefully...")
	streamCancel()
	streamClient.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := dualServer.Shutdown(ctx); err != nil {
		log.Printf("Error during dual server shutdown: %v", err)
	}
	if rdb != nil {
		_ = rdb.Close()
	}
	log.Println("Aegis Gateway terminated")
}
