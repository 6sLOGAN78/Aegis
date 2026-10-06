package middleware

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"

	"aegis/internal/identity"
)

type contextKey string

const AssertionClaimsKey contextKey = "aegis.assertion_claims"

// BackendAuthMiddleware wraps microservice routes with dual-layer mTLS and assertion verification.
func BackendAuthMiddleware(
	authorizedGatewaySPIFFE string,
	expectedServiceID string,
	gatewayAssertionPubKey ed25519.PublicKey,
	exemptPaths ...string,
) func(http.Handler) http.Handler {
	verifier := identity.NewAssertionVerifier(gatewayAssertionPubKey, expectedServiceID)
	exemptMap := make(map[string]bool, len(exemptPaths))
	for _, p := range exemptPaths {
		exemptMap[p] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt unauthenticated routes (e.g. /health for container liveness)
			if exemptMap[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Layer 1: Verify mTLS peer certificate presented and issued to Aegis Gateway
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				writeError(w, http.StatusUnauthorized, "mTLS client certificate required")
				return
			}

			peerCert := r.TLS.PeerCertificates[0]
			spiffeID, err := identity.ExtractSPIFFEID(peerCert, "aegis.local")
			if err != nil || spiffeID != authorizedGatewaySPIFFE {
				writeError(w, http.StatusForbidden, "unauthorized client identity: peer is not the Aegis Gateway")
				return
			}

			// Layer 2: Verify X-Aegis-Assertion header
			assertionHeader := r.Header.Get("X-Aegis-Assertion")
			if assertionHeader == "" {
				writeError(w, http.StatusUnauthorized, "missing required X-Aegis-Assertion header")
				return
			}

			claims, err := verifier.VerifyAssertion(assertionHeader, r.Method, r.URL.Path)
			if err != nil {
				writeError(w, http.StatusForbidden, "invalid assertion: "+err.Error())
				return
			}

			// Inject verified assertion claims into request context and proceed
			ctx := context.WithValue(r.Context(), AssertionClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext extracts verified AssertionClaims from request context.
func FromContext(ctx context.Context) (*identity.AssertionClaims, bool) {
	claims, ok := ctx.Value(AssertionClaimsKey).(*identity.AssertionClaims)
	return claims, ok
}

func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	problemType := "https://aegis.local/errors/unauthorized"
	if status == http.StatusForbidden {
		problemType = "https://aegis.local/errors/forbidden"
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"type":   problemType,
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}
