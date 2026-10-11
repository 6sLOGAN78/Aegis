package snapshot

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"

	"aegis/internal/identity"
)

// NewClientTransportCredentials builds mTLS client credentials presenting the
// gateway's control-plane client certificate and trusting only caPath. There
// is no fallback: empty, missing or invalid inputs are errors.
func NewClientTransportCredentials(certPath, keyPath, caPath, serverName string) (credentials.TransportCredentials, error) {
	if certPath == "" || keyPath == "" || caPath == "" || serverName == "" {
		return nil, errors.New("snapshot: client cert, key, CA path and server name are all required")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot: load client key pair: %w", err)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot: read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("snapshot: CA file contains no valid certificates")
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// GatewayIDFromClientCert derives the gateway id from the SPIFFE /gw/<id> URI
// SAN of the control-plane client certificate.
func GatewayIDFromClientCert(certPath, keyPath string) (string, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return "", fmt.Errorf("snapshot: load client key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("snapshot: parse client cert: %w", err)
	}
	spiffe, err := identity.ExtractSPIFFEID(leaf, "aegis.local")
	if err != nil {
		return "", fmt.Errorf("snapshot: client cert identity: %w", err)
	}
	id, err := identity.GatewayIDFromSPIFFE(spiffe)
	if err != nil {
		return "", fmt.Errorf("snapshot: client cert identity: %w", err)
	}
	return id, nil
}
