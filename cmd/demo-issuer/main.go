package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultDeterministicSeed is a 32-byte seed ensuring deterministic demo key generation across restarts.
var DefaultDeterministicSeed = []byte("aegis-demo-issuer-secret-seed-32")

type SeedUser struct {
	UserID string   `json:"user_id"`
	Roles  []string `json:"roles"`
}

var seedDatabase = map[string]SeedUser{
	"developer":         {UserID: "usr_developer_01", Roles: []string{"developer"}},
	"finance":           {UserID: "usr_finance_01", Roles: []string{"finance"}},
	"application-admin": {UserID: "usr_admin_01", Roles: []string{"application-admin"}},
}

type IssuerServer struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	issuer     string
	audience   string
	mu         sync.Mutex
	attempts   map[string][]time.Time
}

func NewIssuerServer(privKey ed25519.PrivateKey, issuer, audience string) *IssuerServer {
	pubKey := privKey.Public().(ed25519.PublicKey)
	return &IssuerServer{
		privateKey: privKey,
		publicKey:  pubKey,
		issuer:     issuer,
		audience:   audience,
		attempts:   make(map[string][]time.Time),
	}
}

func (s *IssuerServer) checkRateLimit(clientIP string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-1 * time.Minute)

	times := s.attempts[clientIP]
	var recent []time.Time
	for _, ts := range times {
		if ts.After(windowStart) {
			recent = append(recent, ts)
		}
	}

	if len(recent) >= 20 {
		s.attempts[clientIP] = recent
		return false
	}

	recent = append(recent, now)
	s.attempts[clientIP] = recent
	return true
}

func (s *IssuerServer) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(clientIP); err == nil {
		clientIP = host
	}

	if !s.checkRateLimit(clientIP) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "RATE_LIMITED"})
		return
	}

	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "INVALID_REQUEST"})
		return
	}

	user, ok := seedDatabase[req.Role]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "UNKNOWN_ROLE"})
		return
	}

	now := time.Now()
	claims := struct {
		jwt.RegisteredClaims
		Roles []string `json:"roles"`
	}{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Audience:  jwt.ClaimStrings{s.audience},
			Subject:   user.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
		},
		Roles: user.Roles,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signedToken, err := token.SignedString(s.privateKey)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": signedToken,
		"token_type":   "Bearer",
		"expires_in":   300,
	})
}

func (s *IssuerServer) HandlePublicKey(w http.ResponseWriter, r *http.Request) {
	pubKeyBase64 := base64.StdEncoding.EncodeToString(s.publicKey)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"algorithm":  "EdDSA",
		"public_key": pubKeyBase64,
	})
}

func (s *IssuerServer) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", s.HandleLogin)
	mux.HandleFunc("/public-key", s.HandlePublicKey)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}

	privKey := ed25519.NewKeyFromSeed(DefaultDeterministicSeed)
	server := NewIssuerServer(privKey, "aegis-issuer", "aegis-gateway")

	log.Printf("Demo JWT Issuer starting on :%s", port)
	if err := http.ListenAndServe(":"+port, server.Routes()); err != nil {
		log.Fatalf("Demo JWT Issuer failed: %v", err)
	}
}
