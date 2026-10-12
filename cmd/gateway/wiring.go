package main

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"google.golang.org/grpc/credentials"

	"aegis/internal/identity"
	"aegis/internal/keyfile"
	"aegis/internal/keyset"
	"aegis/internal/snapshot"
	"aegis/internal/telemetry"
)

const defaultKeysetInterval = 10 * time.Second

// setupKeysets wires hot-reloadable trusted key directories for JWT issuer keys
// (AEGIS_JWT_KEYSET_DIR) and snapshot signing keys (AEGIS_SNAPSHOT_KEYSET_DIR).
// A configured directory is loaded synchronously and any failure is returned
// (fail closed at startup); afterwards it is reloaded on an interval and SIGHUP,
// keeping the last good set on a bad reload. When a directory is not configured
// the caller's single-key fallback stays in effect and a warning is logged.
func setupKeysets(
	ctx context.Context,
	getenv func(string) string,
	validator *identity.TokenValidator,
	verifier *snapshot.Verifier,
	metrics *telemetry.Metrics,
) error {
	interval := keyset.ParseInterval(getenv("AEGIS_KEYSET_RELOAD_INTERVAL"), defaultKeysetInterval)

	var opts []keyset.Option
	if metrics != nil {
		opts = append(opts, keyset.WithMetrics(metrics))
	}

	if dir := getenv("AEGIS_JWT_KEYSET_DIR"); dir != "" {
		apply := func(keys []ed25519.PublicKey) {
			pks := make([]crypto.PublicKey, len(keys))
			for i, k := range keys {
				pks[i] = k
			}
			validator.SetPublicKeys(pks)
		}
		r := keyset.New("jwt", dir, interval, apply, opts...)
		if err := r.Load(); err != nil {
			return fmt.Errorf("jwt keyset: %w", err)
		}
		go r.Run(ctx)
	} else {
		log.Printf("Warning: AEGIS_JWT_KEYSET_DIR not set; using single-key fallback (AEGIS_ISSUER_PUBLIC_KEY or demo key) without rotation support")
	}

	if dir := getenv("AEGIS_SNAPSHOT_KEYSET_DIR"); dir != "" {
		r := keyset.New("snapshot", dir, interval, verifier.SetTrustedKeys, opts...)
		if err := r.Load(); err != nil {
			return fmt.Errorf("snapshot keyset: %w", err)
		}
		go r.Run(ctx)
	} else {
		log.Printf("Warning: AEGIS_SNAPSHOT_KEYSET_DIR not set; using single-key fallback (AEGIS_CONTROL_PLANE_PUBLIC_KEY or demo key) without rotation support")
	}
	return nil
}

// loadSingleKeys returns the single-key fallback trust anchors: the JWT
// issuer key (AEGIS_ISSUER_PUBLIC_KEY) and the snapshot signing key
// (AEGIS_CONTROL_PLANE_PUBLIC_KEY, defaulting to the issuer key). A variable
// that is set but does not parse as an Ed25519 public key (PEM or base64) is
// an error: falling back to the publicly known demo key on a typo would trust
// tokens and snapshots signed by anyone with repo access (WR-04, D-09). The
// demo key is used only when the variable is unset.
func loadSingleKeys(getenv func(string) string) (issuer, controlPlane ed25519.PublicKey, err error) {
	issuer = ed25519.NewKeyFromSeed(defaultDemoSeed).Public().(ed25519.PublicKey)
	if v := getenv("AEGIS_ISSUER_PUBLIC_KEY"); v != "" {
		if issuer, err = keyfile.ParseEd25519Public([]byte(v)); err != nil {
			return nil, nil, fmt.Errorf("AEGIS_ISSUER_PUBLIC_KEY is set but invalid: %w", err)
		}
	}
	controlPlane = issuer
	if v := getenv("AEGIS_CONTROL_PLANE_PUBLIC_KEY"); v != "" {
		if controlPlane, err = keyfile.ParseEd25519Public([]byte(v)); err != nil {
			return nil, nil, fmt.Errorf("AEGIS_CONTROL_PLANE_PUBLIC_KEY is set but invalid: %w", err)
		}
	}
	return issuer, controlPlane, nil
}

// resolveGatewayIdentity derives the gateway id from the control-plane client
// certificate and builds the mTLS transport credentials for the snapshot
// stream. There is no plaintext path and no generated id: missing cert, key or
// CA paths are errors, and an AEGIS_GATEWAY_ID that disagrees with the
// certificate is fatal.
func resolveGatewayIdentity(getenv func(string) string) (string, credentials.TransportCredentials, error) {
	certPath := getenv("AEGIS_CP_CLIENT_CERT_PATH")
	keyPath := getenv("AEGIS_CP_CLIENT_KEY_PATH")
	caPath := getenv("AEGIS_CP_CA_PATH")
	if certPath == "" || keyPath == "" || caPath == "" {
		return "", nil, errors.New("AEGIS_CP_CLIENT_CERT_PATH, AEGIS_CP_CLIENT_KEY_PATH and AEGIS_CP_CA_PATH are all required for the control plane stream")
	}

	gatewayID, err := snapshot.GatewayIDFromClientCert(certPath, keyPath)
	if err != nil {
		return "", nil, err
	}
	if env := getenv("AEGIS_GATEWAY_ID"); env != "" && env != gatewayID {
		return "", nil, fmt.Errorf("AEGIS_GATEWAY_ID %q does not match the gateway id %q in the control plane client certificate", env, gatewayID)
	}

	serverName := getenv("AEGIS_CP_SERVER_NAME")
	if serverName == "" {
		addr := getenv("AEGIS_CONTROL_PLANE_GRPC_ADDR")
		if addr == "" {
			addr = "localhost:9090"
		}
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}
		serverName = host
	}

	creds, err := snapshot.NewClientTransportCredentials(certPath, keyPath, caPath, serverName)
	if err != nil {
		return "", nil, err
	}
	return gatewayID, creds, nil
}

// loadAssertionKey returns the workload-assertion signing key. A configured
// path or AEGIS_ASSERTION_PRIVATE_KEY that cannot be read or parsed is an
// error; the demo seed is used only when neither is set (usedDemo=true).
func loadAssertionKey(path string, getenv func(string) string) (key ed25519.PrivateKey, usedDemo bool, err error) {
	if path != "" {
		if key, err = keyfile.LoadEd25519Private(path); err != nil {
			return nil, false, fmt.Errorf("assertion private key %s is configured but invalid: %w", path, err)
		}
		return key, false, nil
	}
	if v := getenv("AEGIS_ASSERTION_PRIVATE_KEY"); v != "" {
		if key, err = keyfile.ParseEd25519Private([]byte(v)); err != nil {
			return nil, false, fmt.Errorf("AEGIS_ASSERTION_PRIVATE_KEY is set but invalid: %w", err)
		}
		return key, false, nil
	}
	return ed25519.NewKeyFromSeed(defaultDemoSeed), true, nil
}
