package security

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"aegis/internal/config"
	"aegis/internal/identity"
	"aegis/internal/pki"
	"aegis/internal/proxy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCluster struct {
	ca           *pki.CA
	serverCert   tls.Certificate
	workloadCert tls.Certificate
	userURL      string
	workloadURL  string
	dualServer   *proxy.DualServer
}

func setupTestCluster(t *testing.T) *testCluster {
	t.Helper()

	ca, err := pki.NewCA("Aegis Test CA")
	require.NoError(t, err)

	serverCert, err := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	require.NoError(t, err)

	workloadCert, err := ca.IssueWorkloadCert("spiffe://aegis.local/workload/orders")
	require.NoError(t, err)

	userLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	workloadLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	userPort := userLn.Addr().(*net.TCPAddr).Port
	workloadPort := workloadLn.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		Port:                  userPort,
		WorkloadPort:          workloadPort,
		MaxHeaderBytes:        16384,
		MaxBodyBytes:          1048576,
		ReadHeaderTimeout:     5 * time.Second,
		WriteTimeout:          5 * time.Second,
		MaxConcurrentRequests: 100,
	}

	userHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"caller":        "user",
			"authorization": r.Header.Get("Authorization"),
		})
	})

	workloadHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "missing peer certificate", http.StatusUnauthorized)
			return
		}
		spiffeID, err := identity.ExtractSPIFFEID(r.TLS.PeerCertificates[0], "aegis.local")
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"caller":    "workload",
			"spiffe_id": spiffeID,
		})
	})

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    ca.CertPool,
		MinVersion:   tls.VersionTLS13,
	}

	ds := proxy.NewDualServer(cfg, userHandler, workloadHandler, tlsCfg)

	go func() {
		_ = ds.Serve(userLn, workloadLn)
	}()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = ds.Shutdown(ctx)
		_ = userLn.Close()
		_ = workloadLn.Close()
	})

	return &testCluster{
		ca:           ca,
		serverCert:   serverCert,
		workloadCert: workloadCert,
		userURL:      "http://" + userLn.Addr().String(),
		workloadURL:  "https://" + workloadLn.Addr().String(),
		dualServer:   ds,
	}
}

func TestWorkloadIdentityListener_RequireClientCert(t *testing.T) {
	cluster := setupTestCluster(t)

	// Workload client connects WITHOUT client certificate
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    cluster.ca.CertPool,
				MinVersion: tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	_, err := client.Get(cluster.workloadURL + "/api/orders")
	require.Error(t, err, "client without certificate must fail TLS handshake on workload listener (:9443)")
}

func TestWorkloadIdentityListener_RogueCACertRejected(t *testing.T) {
	cluster := setupTestCluster(t)

	rogueCA, err := pki.NewCA("Rogue Untrusted CA")
	require.NoError(t, err)

	rogueCert, err := rogueCA.IssueWorkloadCert("spiffe://aegis.local/workload/orders")
	require.NoError(t, err)

	// Client presents certificate signed by rogue CA
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				GetClientCertificate: func(cri *tls.CertificateRequestInfo) (*tls.Certificate, error) {
					return &rogueCert, nil
				},
				RootCAs:    cluster.ca.CertPool,
				MinVersion: tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	_, err = client.Get(cluster.workloadURL + "/api/orders")
	require.Error(t, err, "certificate signed by untrusted CA must fail TLS handshake")
}

func TestWorkloadIdentityListener_ValidCertSuccess(t *testing.T) {
	cluster := setupTestCluster(t)

	// Workload client connects with valid workload client certificate
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cluster.workloadCert},
				RootCAs:      cluster.ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(cluster.workloadURL + "/api/orders")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("X-Request-ID"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(bodyBytes, &result)
	require.NoError(t, err)

	assert.Equal(t, "workload", result["caller"])
	assert.Equal(t, "spiffe://aegis.local/workload/orders", result["spiffe_id"])
}

func TestWorkloadIdentityListener_AmbiguousCredentialsRejected(t *testing.T) {
	cluster := setupTestCluster(t)

	// Workload client connects with valid cert AND Authorization: Bearer <jwt> header
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cluster.workloadCert},
				RootCAs:      cluster.ca.CertPool,
				MinVersion:   tls.VersionTLS13,
			},
		},
		Timeout: 2 * time.Second,
	}

	req, err := http.NewRequest(http.MethodGet, cluster.workloadURL+"/api/orders", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.dummy.sig")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Invariant 2 & AUTH-03: Ambiguous credentials rejected immediately with HTTP 401
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
	assert.NotEmpty(t, resp.Header.Get("X-Request-ID"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var problem proxy.RFC7807Problem
	err = json.Unmarshal(bodyBytes, &problem)
	require.NoError(t, err)

	assert.Equal(t, "https://aegis.local/errors/ambiguous-credentials", problem.Type)
	assert.Equal(t, "Unauthorized", problem.Title)
	assert.Equal(t, http.StatusUnauthorized, problem.Status)
	assert.Equal(t, "Ambiguous credentials: Bearer tokens are prohibited on the workload listener", problem.Detail)
	assert.Equal(t, resp.Header.Get("X-Request-ID"), problem.Instance)
}

func TestWorkloadIdentityListener_UserPortPermitsBearer(t *testing.T) {
	cluster := setupTestCluster(t)

	// User client connects to port :8080 with Bearer token (no mTLS cert required)
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	req, err := http.NewRequest(http.MethodGet, cluster.userURL+"/api/orders", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer user-jwt-token")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("X-Request-ID"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(bodyBytes, &result)
	require.NoError(t, err)

	assert.Equal(t, "user", result["caller"])
	assert.Equal(t, "Bearer user-jwt-token", result["authorization"])
}
