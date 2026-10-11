package control

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// NewServerTransportCredentials builds mTLS server credentials that require
// and verify a client certificate signed by clientCAPath. There is no
// plaintext fallback: any empty or invalid input is an error.
func NewServerTransportCredentials(certPath, keyPath, clientCAPath string) (credentials.TransportCredentials, error) {
	if certPath == "" || keyPath == "" || clientCAPath == "" {
		return nil, errors.New("control: server cert, key and client CA paths are all required")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("control: load server key pair: %w", err)
	}
	caPEM, err := os.ReadFile(clientCAPath)
	if err != nil {
		return nil, fmt.Errorf("control: read client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("control: client CA file contains no valid certificates")
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}), nil
}
