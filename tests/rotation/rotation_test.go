package rotation

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aegis/internal/identity"
	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Helper: mint test JWT
func mintTestToken(t *testing.T, priv ed25519.PrivateKey, issuer, audience, sub string, ttl time.Duration) string {
	t.Helper()
	claims := identity.UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			Subject:   sub,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Roles: []string{"developer"},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(priv)
	require.NoError(t, err)
	return signed
}

// Helper: create signed snapshot envelope
func createSignedSnapshot(t *testing.T, priv ed25519.PrivateKey, version int64) *snapshotv1.SnapshotEnvelope {
	t.Helper()
	payload := &snapshotv1.SnapshotPayload{
		Version: version,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:      fmt.Sprintf("route-v%d", version),
				ServiceId:    "orders",
				HttpMethod:   "GET",
				PathTemplate: "/api/orders",
				UpstreamUrl:  "https://orders:8081",
			},
		},
	}
	payloadBytes, err := proto.Marshal(payload)
	require.NoError(t, err)

	sum := sha256.Sum256(payloadBytes)
	shaHex := hex.EncodeToString(sum[:])
	sig := ed25519.Sign(priv, []byte(shaHex))

	return &snapshotv1.SnapshotEnvelope{
		Version:       version,
		PayloadSha256: shaHex,
		Signature:     sig,
		Payload:       payloadBytes,
	}
}

// Helper: create signed freshness lease
func createSignedLease(t *testing.T, priv ed25519.PrivateKey, leaseID string, version int64, validUntil time.Time) *snapshotv1.FreshnessLease {
	t.Helper()
	digest := snapshot.LeaseDigest(leaseID, version, validUntil.UnixNano())
	sig := ed25519.Sign(priv, digest)

	return &snapshotv1.FreshnessLease{
		LeaseId:         leaseID,
		SnapshotVersion: version,
		ValidUntil:      timestamppb.New(validUntil),
		LeaseSignature:  sig,
	}
}

// TestJWTIssuerKeyRotation exercises zero-downtime JWT issuer key rotation across 3 phases.
func TestJWTIssuerKeyRotation(t *testing.T) {
	// Generate Key A and Key B
	pubA, privA, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pubB, privB, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	issuer := "https://auth.aegis.local"
	audience := "aegis-gateway"

	// Phase 1: Gateway starts with Key A only
	val := identity.NewTokenValidator(issuer, audience, pubA)
	tokenA := mintTestToken(t, privA, issuer, audience, "user-1", 10*time.Minute)
	claims, err := val.ValidateBearerToken("Bearer " + tokenA)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.Subject)

	// Token B signed with Key B fails in Phase 1
	tokenB := mintTestToken(t, privB, issuer, audience, "user-2", 10*time.Minute)
	_, err = val.ValidateBearerToken("Bearer " + tokenB)
	assert.Error(t, err, "Key B must fail before addition to keyset")

	// Phase 2: Overlap Window - Add Key B to keyset
	val.AddPublicKey(pubB)

	// Launch 20 concurrent goroutines executing 200 total requests (10 each)
	// alternating between Key A and Key B
	const concurrency = 20
	const requestsPerGoroutine = 10
	var totalSuccess atomic.Int64
	var totalErrors atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(routineID int) {
			defer wg.Done()
			for req := 0; req < requestsPerGoroutine; req++ {
				var tok string
				var expectedSub string
				if (routineID+req)%2 == 0 {
					tok = mintTestToken(t, privA, issuer, audience, fmt.Sprintf("user-a-%d-%d", routineID, req), 5*time.Minute)
					expectedSub = fmt.Sprintf("user-a-%d-%d", routineID, req)
				} else {
					tok = mintTestToken(t, privB, issuer, audience, fmt.Sprintf("user-b-%d-%d", routineID, req), 5*time.Minute)
					expectedSub = fmt.Sprintf("user-b-%d-%d", routineID, req)
				}

				clm, vErr := val.ValidateBearerToken("Bearer " + tok)
				if vErr != nil || clm == nil || clm.Subject != expectedSub {
					totalErrors.Add(1)
				} else {
					totalSuccess.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int64(concurrency*requestsPerGoroutine), totalSuccess.Load(), "All concurrent requests during overlap must succeed")
	assert.Equal(t, int64(0), totalErrors.Load(), "Zero verification errors during overlap window")

	// Phase 3: Retirement - Retire Key A (Keyset = [Key B])
	val.SetPublicKeys([]crypto.PublicKey{pubB})

	// Key B continues to validate
	claimsBAfter, errBAfter := val.ValidateBearerToken("Bearer " + tokenB)
	require.NoError(t, errBAfter)
	assert.Equal(t, "user-2", claimsBAfter.Subject)

	// Key A is now rejected
	_, errRetired := val.ValidateBearerToken("Bearer " + tokenA)
	assert.Error(t, errRetired, "Token signed by retired Key A must fail closed")
}

// TestSnapshotSigningKeyRotation exercises zero-downtime Ed25519 snapshot signing key rotation.
func TestSnapshotSigningKeyRotation(t *testing.T) {
	// Generate Control Plane Keypair 1 and Keypair 2
	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Phase 1: Initialize verifier with Key 1 at snapshot version 1
	verifier := snapshot.NewVerifier(pub1)

	env1 := createSignedSnapshot(t, priv1, 1)
	payload1, err := verifier.VerifySnapshot(env1, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), payload1.Version)

	lease1 := createSignedLease(t, priv1, "lease-v1", 1, time.Now().Add(10*time.Second))
	err = verifier.VerifyLease(lease1, 1)
	require.NoError(t, err)

	// Snapshot v2 signed with Key 2 fails before overlap
	env2Key2 := createSignedSnapshot(t, priv2, 2)
	_, err = verifier.VerifySnapshot(env2Key2, 1)
	require.ErrorIs(t, err, snapshot.ErrInvalidSignature)

	// Phase 2: Overlap - Add Key 2 to verifier
	verifier.AddTrustedKey(pub2)

	// Control plane publishes snapshot v2 signed with Key 2
	payload2, err := verifier.VerifySnapshot(env2Key2, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), payload2.Version)

	lease2 := createSignedLease(t, priv2, "lease-v2", 2, time.Now().Add(10*time.Second))
	err = verifier.VerifyLease(lease2, 2)
	require.NoError(t, err)

	// Phase 3: Retirement - Retire Key 1
	verifier.SetTrustedKeys([]ed25519.PublicKey{pub2})

	// Verifier accepts snapshot v3 signed with Key 2
	env3Key2 := createSignedSnapshot(t, priv2, 3)
	payload3, err := verifier.VerifySnapshot(env3Key2, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), payload3.Version)

	// Verifier rejects snapshot v4 signed with Key 1
	env4Key1 := createSignedSnapshot(t, priv1, 4)
	_, err = verifier.VerifySnapshot(env4Key1, 3)
	require.ErrorIs(t, err, snapshot.ErrInvalidSignature, "Snapshot signed by retired Key 1 must be rejected")
}

// Helpers for mTLS CA generation
func generateRootCA(t *testing.T, commonName string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Aegis Zero-Trust Root PKI"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            2,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, priv
}

func generateIntermediateCA(t *testing.T, parentCert *x509.Certificate, parentKey *ecdsa.PrivateKey, commonName string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Aegis Intermediate PKI"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(180 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parentCert, &priv.PublicKey, parentKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, priv
}

func issueClientCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, spiffeURIStr string) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	spiffeURI, err := url.Parse(spiffeURIStr)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   spiffeURI.Path,
			Organization: []string{"Aegis Workload"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:                  []*url.URL{spiffeURI},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &priv.PublicKey, caKey)
	require.NoError(t, err)

	return tls.Certificate{
		Certificate: [][]byte{der, caCert.Raw},
		PrivateKey:  priv,
	}
}

func generateServerCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"Aegis Gateway"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &priv.PublicKey, caKey)
	require.NoError(t, err)

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
	}
}

// DynamicTrustManager provides thread-safe ClientCAs updating for mTLS tests.
type DynamicTrustManager struct {
	mu        sync.RWMutex
	clientCAs *x509.CertPool
}

func (dtm *DynamicTrustManager) SetPool(pool *x509.CertPool) {
	dtm.mu.Lock()
	defer dtm.mu.Unlock()
	dtm.clientCAs = pool
}

func (dtm *DynamicTrustManager) GetPool() *x509.CertPool {
	dtm.mu.RLock()
	defer dtm.mu.RUnlock()
	return dtm.clientCAs
}

// TestMTLSCARotation exercises zero-downtime Intermediate CA rotation on mTLS listeners.
func TestMTLSCARotation(t *testing.T) {
	// Generate Root CA, Intermediate CA 1, and Intermediate CA 2
	rootCert, rootKey := generateRootCA(t, "Aegis Workload Root CA")
	ca1Cert, ca1Key := generateIntermediateCA(t, rootCert, rootKey, "Aegis Intermediate CA 1")
	ca2Cert, ca2Key := generateIntermediateCA(t, rootCert, rootKey, "Aegis Intermediate CA 2")

	// Generate client certificates signed by Intermediate CA 1 and Intermediate CA 2
	client1Cert := issueClientCert(t, ca1Cert, ca1Key, "spiffe://aegis.local/workload/client-1")
	client2Cert := issueClientCert(t, ca2Cert, ca2Key, "spiffe://aegis.local/workload/client-2")

	// Server cert issued by Root CA
	serverCert := generateServerCert(t, rootCert, rootKey)

	// Trust pool with Root CA for client verification of server
	serverRootPool := x509.NewCertPool()
	serverRootPool.AddCert(rootCert)

	// Dynamic trust manager for server ClientCAs
	poolCA1 := x509.NewCertPool()
	poolCA1.AddCert(ca1Cert)

	trustMgr := &DynamicTrustManager{clientCAs: poolCA1}

	// Server handler
	serverHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "missing peer certificate", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.TLS.PeerCertificates[0].URIs[0].String()))
	})

	server := httptest.NewUnstartedServer(serverHandler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			return &tls.Config{
				Certificates: []tls.Certificate{serverCert},
				ClientAuth:   tls.RequireAndVerifyClientCert,
				ClientCAs:    trustMgr.GetPool(),
				MinVersion:   tls.VersionTLS13,
			}, nil
		},
		MinVersion: tls.VersionTLS13,
	}
	server.StartTLS()
	defer server.Close()

	// Helper to create client with specific cert
	makeClient := func(cert tls.Certificate) *http.Client {
		return &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					Certificates: []tls.Certificate{cert},
					RootCAs:      serverRootPool,
					MinVersion:   tls.VersionTLS13,
				},
			},
			Timeout: 5 * time.Second,
		}
	}

	client1 := makeClient(client1Cert)
	client2 := makeClient(client2Cert)

	// Phase 1: Pool contains Intermediate CA 1 only
	resp1, err := client1.Get(server.URL)
	require.NoError(t, err)
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	assert.Equal(t, http.StatusOK, resp1.StatusCode)
	assert.Equal(t, "spiffe://aegis.local/workload/client-1", string(body1))

	// Client 2 (signed by CA 2) is rejected
	_, err = client2.Get(server.URL)
	assert.Error(t, err, "Client 2 must be rejected when CA 2 is not in trust pool")

	// Phase 2: Overlap Window - Add Intermediate CA 2 to trust pool
	poolOverlap := x509.NewCertPool()
	poolOverlap.AddCert(ca1Cert)
	poolOverlap.AddCert(ca2Cert)
	trustMgr.SetPool(poolOverlap)

	// Verify both client 1 and client 2 establish mTLS connections concurrently with 0 failures
	var wg sync.WaitGroup
	var client1Success, client2Success atomic.Int64
	var totalFailures atomic.Int64

	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			r, e := client1.Get(server.URL)
			if e != nil || r.StatusCode != http.StatusOK {
				totalFailures.Add(1)
				return
			}
			r.Body.Close()
			client1Success.Add(1)
		}()
		go func() {
			defer wg.Done()
			r, e := client2.Get(server.URL)
			if e != nil || r.StatusCode != http.StatusOK {
				totalFailures.Add(1)
				return
			}
			r.Body.Close()
			client2Success.Add(1)
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(0), totalFailures.Load(), "Zero failures during overlap window")
	assert.Equal(t, int64(10), client1Success.Load())
	assert.Equal(t, int64(10), client2Success.Load())

	// Phase 3: Retirement - Retire Intermediate CA 1 (Pool contains CA 2 only)
	poolCA2 := x509.NewCertPool()
	poolCA2.AddCert(ca2Cert)
	trustMgr.SetPool(poolCA2)

	// Client 2 continues to connect successfully
	resp2, err := client2.Get(server.URL)
	require.NoError(t, err)
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.Equal(t, "spiffe://aegis.local/workload/client-2", string(body2))

	// Client 1 is rejected now that CA 1 is retired
	_, err = client1.Get(server.URL)
	assert.Error(t, err, "Client 1 must be rejected after CA 1 is retired")
}
