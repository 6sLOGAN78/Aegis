package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aegis/internal/audit"
	"aegis/internal/config"
	"aegis/internal/identity"
	"aegis/internal/pki"
	"aegis/internal/policy"
	"aegis/internal/proxy"
)

var defaultDemoSeed = []byte("aegis-demo-issuer-secret-seed-32")

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	policyPath := os.Getenv("AEGIS_POLICY_PATH")
	if policyPath == "" {
		policyPath = "policies/rego/authz.rego"
	}

	policyBytes, err := os.ReadFile(policyPath)
	if err != nil {
		log.Fatalf("Failed to read policy from %q: %v", policyPath, err)
	}

	policyEngine, err := policy.NewEngine(context.Background(), string(policyBytes))
	if err != nil {
		log.Fatalf("Failed to initialize OPA policy engine: %v", err)
	}

	router, err := proxy.NewRouterFromJSON(cfg.RoutesFilePath)
	if err != nil {
		log.Fatalf("Failed to initialize router from %q: %v", cfg.RoutesFilePath, err)
	}

	issuer := os.Getenv("AEGIS_ISSUER")
	if issuer == "" {
		issuer = "aegis-issuer"
	}

	audience := os.Getenv("AEGIS_AUDIENCE")
	if audience == "" {
		audience = "aegis-gateway"
	}

	pubKey := ed25519.NewKeyFromSeed(defaultDemoSeed).Public()
	if pubKeyB64 := os.Getenv("AEGIS_ISSUER_PUBLIC_KEY"); pubKeyB64 != "" {
		if b, err := base64.StdEncoding.DecodeString(pubKeyB64); err == nil && len(b) == ed25519.PublicKeySize {
			pubKey = ed25519.PublicKey(b)
		} else {
			log.Printf("Warning: failed to decode AEGIS_ISSUER_PUBLIC_KEY, using default demo key: %v", err)
		}
	}

	tokenValidator := identity.NewTokenValidator(issuer, audience, pubKey)
	auditLogger := audit.NewLogger(nil)
	snapshotVersion := int64(1)

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
		// Clear paths so DualServer.ListenAndServeTLS("", "") uses configured Certificates
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

		// 3. Deterministic Route Resolution (GW-03)
		route, err := router.Match(r.Method, canonicalPath)
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

		// 4. In-Memory OPA Policy Evaluation (POL-01, POL-02)
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
			SnapshotVersion: snapshotVersion,
		}

		decision, err := policyEngine.Evaluate(r.Context(), input)
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
			snapshotVersion,
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

		// 3. Deterministic Route Resolution (GW-03)
		route, err := router.Match(r.Method, canonicalPath)
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

		// 4. In-Memory OPA Policy Evaluation (POL-01, Rule 5 in authz.rego)
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
			Context: policy.ContextInput{RiskScore: 0, RiskState: "available"},
			SnapshotVersion: snapshotVersion,
		}

		decision, err := policyEngine.Evaluate(r.Context(), input)
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
			snapshotVersion,
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
	auditedUserHandler := audit.AuditMiddleware(auditLogger, snapshotVersion)(gatewayHandler)
	auditedWorkloadHandler := audit.AuditMiddleware(auditLogger, snapshotVersion)(workloadHandler)

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := dualServer.Shutdown(ctx); err != nil {
		log.Printf("Error during dual server shutdown: %v", err)
	}
	log.Println("Aegis Gateway terminated")
}
