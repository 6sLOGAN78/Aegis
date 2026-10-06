package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aegis/internal/config"
	"aegis/internal/identity"
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

	// Build Gateway Core Request Pipeline
	gatewayHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")

		// 1. Strict Zero-Repair Path Validation (GW-02)
		canonicalPath, err := proxy.ValidatePathZeroRepair(r)
		if err != nil {
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

		// 3. Deterministic Route Resolution (GW-03)
		route, err := router.Match(r.Method, canonicalPath)
		if err != nil {
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
			SnapshotVersion: 1,
		}

		decision, err := policyEngine.Evaluate(r.Context(), input)
		if err != nil || !decision.Allow {
			reason := "DENIED_DEFAULT"
			if decision.ReasonCode != "" {
				reason = decision.ReasonCode
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

		// 5. Reverse Proxy Forwarding with Header Scrubbing (GW-04, BYP-01)
		rp := proxy.NewReverseProxy(route.ParsedURL(), canonicalPath, reqID)
		rp.ServeHTTP(w, r)
	})

	server := proxy.NewServer(cfg, gatewayHandler)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Aegis Gateway listening on :%d", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down Aegis Gateway gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Error during server shutdown: %v", err)
	}
	log.Println("Aegis Gateway terminated")
}
