package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"log"
	"net/http"
	"os"
	"strings"

	"aegis/services/middleware"
)

const defaultGatewaySPIFFE = "spiffe://aegis.local/ns/gateway/sa/aegis-gateway"

var defaultDemoSeed = []byte("aegis-demo-issuer-secret-seed-32")

type AdminUser struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

var sampleUsers = []AdminUser{
	{ID: "usr_admin_01", Role: "application-admin"},
	{ID: "usr_developer_01", Role: "developer"},
}

func HandleAdminUsers(w http.ResponseWriter, r *http.Request) {
	// Extract caller context if present
	_ , _ = middleware.FromContext(r.Context())

	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(sampleUsers)
	case http.MethodPost:
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "user created",
		})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func loadGatewayPublicKey() ed25519.PublicKey {
	defaultPubKey := ed25519.NewKeyFromSeed(defaultDemoSeed).Public().(ed25519.PublicKey)

	if pubKeyPath := os.Getenv("GATEWAY_ASSERTION_PUBKEY_PATH"); pubKeyPath != "" {
		if data, err := os.ReadFile(pubKeyPath); err == nil {
			trimmed := strings.TrimSpace(string(data))
			if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(decoded) == ed25519.PublicKeySize {
				return ed25519.PublicKey(decoded)
			}
			if len(data) == ed25519.PublicKeySize {
				return ed25519.PublicKey(data)
			}
			block, _ := pem.Decode(data)
			if block != nil {
				if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
					if edKey, ok := parsed.(ed25519.PublicKey); ok {
						return edKey
					}
				}
			}
		}
	}

	if pubKeyB64 := os.Getenv("GATEWAY_ASSERTION_PUBKEY"); pubKeyB64 != "" {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubKeyB64)); err == nil && len(decoded) == ed25519.PublicKeySize {
			return ed25519.PublicKey(decoded)
		}
	}

	return defaultPubKey
}

func Routes(assertionPubKeys ...ed25519.PublicKey) http.Handler {
	var pubKey ed25519.PublicKey
	if len(assertionPubKeys) > 0 && assertionPubKeys[0] != nil {
		pubKey = assertionPubKeys[0]
	} else {
		pubKey = loadGatewayPublicKey()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/users", HandleAdminUsers)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	authMW := middleware.BackendAuthMiddleware(
		defaultGatewaySPIFFE,
		"admin",
		pubKey,
		"/health",
	)

	return authMW(mux)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}

	pubKey := loadGatewayPublicKey()
	handler := Routes(pubKey)

	certPath := os.Getenv("TLS_CERT_PATH")
	keyPath := os.Getenv("TLS_KEY_PATH")
	caPath := os.Getenv("CA_CERT_PATH")

	if certPath != "" && keyPath != "" {
		serverCert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			log.Fatalf("Failed to load server TLS certificate: %v", err)
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			MinVersion:   tls.VersionTLS13,
		}

		if caPath != "" {
			caCertBytes, err := os.ReadFile(caPath)
			if err != nil {
				log.Fatalf("Failed to read CA certificate from %s: %v", caPath, err)
			}
			caPool := x509.NewCertPool()
			if !caPool.AppendCertsFromPEM(caCertBytes) {
				log.Fatalf("Failed to append CA certs to pool")
			}
			tlsConfig.ClientCAs = caPool
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}

		server := &http.Server{
			Addr:      ":" + port,
			Handler:   handler,
			TLSConfig: tlsConfig,
		}

		log.Printf("Admin microservice listening on :%s (HTTPS mTLS)", port)
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Admin microservice failed: %v", err)
		}
		return
	}

	log.Printf("Warning: TLS certificates not configured. Admin microservice starting on :%s (HTTP plain)", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Admin microservice failed: %v", err)
	}
}
