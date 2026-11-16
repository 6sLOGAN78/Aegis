package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"aegis/internal/identity"
	"aegis/internal/keyfile"
	"aegis/internal/pki"
)

// cpClientIDs are the gateway ids that receive control-plane client certs.
var cpClientIDs = []string{
	"gateway", "gateway-1", "gateway-2", "gateway-3",
	"aegis-gateway-0", "aegis-gateway-1", "aegis-gateway-2",
}

func writeCertFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

// writeKeyFile writes private key material with mode 0600. os.WriteFile only
// applies the mode when it creates the file, so overwriting a key that was
// checked out of git as 0664 would keep it group/world readable (WR-08).
// Instead the data goes to a temp file in the same directory (created 0600)
// that is renamed over the destination, and the final mode is enforced.
func writeKeyFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

// restrictKeyFile tightens an existing private key (one the generator keeps
// rather than rewrites) to 0600.
func restrictKeyFile(path string) error {
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("restrict %s to 0600: %w", path, err)
	}
	return nil
}

// writeTLSPair writes <name>.crt (0644) and <name>.key (0600) into dir.
func writeTLSPair(dir, name string, c tls.Certificate) error {
	keyPEM, err := pki.EncodeKeyPEM(c.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		return fmt.Errorf("encode %s key: %w", name, err)
	}
	if err := writeCertFile(filepath.Join(dir, name+".crt"), pki.EncodeCertPEM(c.Certificate[0])); err != nil {
		return fmt.Errorf("write %s.crt: %w", name, err)
	}
	if err := writeKeyFile(filepath.Join(dir, name+".key"), keyPEM); err != nil {
		return fmt.Errorf("write %s.key: %w", name, err)
	}
	return nil
}

// writeEd25519 generates a fresh Ed25519 keypair; the private key (64 bytes)
// and public key (32 bytes) are written base64 + newline.
func writeEd25519(path, pubPath string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 key: %w", err)
	}
	if err := writeKeyFile(path, []byte(base64.StdEncoding.EncodeToString(priv)+"\n")); err != nil {
		return err
	}
	return writeCertFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func loadOrCreateCA(outDir string) (*pki.CA, error) {
	crtPath := filepath.Join(outDir, "root-ca.crt")
	keyPath := filepath.Join(outDir, "root-ca.key")
	if fileExists(crtPath) && fileExists(keyPath) {
		crt, err := os.ReadFile(crtPath)
		if err != nil {
			return nil, err
		}
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		ca, err := pki.LoadCA(crt, key)
		if err != nil {
			return nil, fmt.Errorf("load existing root CA: %w", err)
		}
		if err := restrictKeyFile(keyPath); err != nil {
			return nil, err
		}
		fmt.Println("  [=] Reusing existing Root CA (root-ca.crt, root-ca.key)")
		return ca, nil
	}
	ca, err := pki.NewCA("Aegis Root CA")
	if err != nil {
		return nil, fmt.Errorf("create root CA: %w", err)
	}
	keyPEM, err := pki.EncodeKeyPEM(ca.PrivateKey)
	if err != nil {
		return nil, err
	}
	if err := writeCertFile(crtPath, pki.EncodeCertPEM(ca.Certificate.Raw)); err != nil {
		return nil, err
	}
	if err := writeKeyFile(keyPath, keyPEM); err != nil {
		return nil, err
	}
	fmt.Println("  [+] Generated Root CA (root-ca.crt, root-ca.key)")
	return ca, nil
}

func run(outDir, keysDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outDir, err)
	}
	fmt.Printf("Generating development PKI and assertion keys into %s\n", outDir)

	ca, err := loadOrCreateCA(outDir)
	if err != nil {
		return err
	}
	loopback := []net.IP{net.ParseIP("127.0.0.1")}

	// Gateway server cert.
	c, err := ca.IssueServerCert("gateway", []string{
		"gateway", "localhost", "aegis-gateway", "aegis-gateway.aegis-system",
		"aegis-gateway.aegis-system.svc", "aegis-gateway.aegis-system.svc.cluster.local",
		"gateway-1", "gateway-2", "gateway-3",
	}, loopback)
	if err != nil {
		return fmt.Errorf("issue gateway server cert: %w", err)
	}
	if err := writeTLSPair(outDir, "gateway-server", c); err != nil {
		return err
	}
	fmt.Println("  [+] Generated Gateway server cert (gateway-server.crt, gateway-server.key)")

	// Gateway client cert (SPIFFE).
	c, err = ca.IssueWorkloadCert("spiffe://aegis.local/ns/gateway/sa/aegis-gateway")
	if err != nil {
		return fmt.Errorf("issue gateway client cert: %w", err)
	}
	if err := writeTLSPair(outDir, "gateway-client", c); err != nil {
		return err
	}
	fmt.Println("  [+] Generated Gateway client cert (gateway-client.crt, gateway-client.key)")

	// Control-plane server cert.
	c, err = ca.IssueServerCert("control-plane", []string{
		"control-plane", "aegis-control-plane", "aegis-control-plane.aegis-system.svc", "localhost",
	}, loopback)
	if err != nil {
		return fmt.Errorf("issue control-plane server cert: %w", err)
	}
	if err := writeTLSPair(outDir, "control-plane-server", c); err != nil {
		return err
	}
	fmt.Println("  [+] Generated Control-plane server cert (control-plane-server.crt, control-plane-server.key)")

	// Per-gateway control-plane client certs.
	for _, id := range cpClientIDs {
		c, err = ca.IssueWorkloadCert(identity.GatewaySPIFFEID(id))
		if err != nil {
			return fmt.Errorf("issue cp-client cert for %s: %w", id, err)
		}
		name := "cp-client-" + id
		if err := writeTLSPair(outDir, name, c); err != nil {
			return err
		}
		fmt.Printf("  [+] Generated control-plane client cert for %s (%s.crt, %s.key)\n", id, name, name)
	}

	// Workload orders cert.
	c, err = ca.IssueWorkloadCert("spiffe://aegis.local/workload/orders")
	if err != nil {
		return fmt.Errorf("issue orders workload cert: %w", err)
	}
	if err := writeTLSPair(outDir, "workload-orders", c); err != nil {
		return err
	}
	fmt.Println("  [+] Generated Workload Orders cert (workload-orders.crt, workload-orders.key)")

	// Backend service server certs.
	for _, svc := range []string{"orders", "payments", "admin"} {
		c, err = ca.IssueServerCert(svc, []string{svc, "localhost"}, loopback)
		if err != nil {
			return fmt.Errorf("issue server cert for %s: %w", svc, err)
		}
		if err := writeTLSPair(outDir, svc, c); err != nil {
			return err
		}
		fmt.Printf("  [+] Generated %s server cert (%s.crt, %s.key)\n", svc, svc, svc)
	}

	// Assertion key pair.
	if err := writeEd25519(filepath.Join(outDir, "assertion-ed25519.key"), filepath.Join(outDir, "assertion-ed25519.pub")); err != nil {
		return err
	}
	fmt.Println("  [+] Generated Ed25519 Assertion Key Pair (assertion-ed25519.key, assertion-ed25519.pub)")

	if keysDir != "" {
		if err := writeKeys(keysDir); err != nil {
			return err
		}
	}
	fmt.Println("Certificate generation completed successfully.")
	return nil
}

// writeKeys writes separate JWT and snapshot signing keypairs plus keyset
// directories. Existing complete pairs are never overwritten. A half-present
// pair is never silently kept (WR-09): a private key without its public key
// gets the public key derived from it, a public key without its private key is
// an error, and a pair whose halves do not match is an error.
func writeKeys(keysDir string) error {
	for _, d := range []string{"keyset-jwt", "keyset-snapshot"} {
		if err := os.MkdirAll(filepath.Join(keysDir, d), 0755); err != nil {
			return err
		}
	}
	pairs := []struct{ key, pub string }{
		{"jwt-current.key", "keyset-jwt/current.pub"},
		{"snapshot-current.key", "keyset-snapshot/current.pub"},
	}
	for _, p := range pairs {
		k, pb := filepath.Join(keysDir, p.key), filepath.Join(keysDir, p.pub)
		hasKey, hasPub := fileExists(k), fileExists(pb)
		switch {
		case !hasKey && !hasPub:
			if err := writeEd25519(k, pb); err != nil {
				return err
			}
			fmt.Printf("  [+] Generated %s and %s\n", p.key, p.pub)
		case !hasKey && hasPub:
			return fmt.Errorf("%s exists but its private key %s is missing: restore the private key, or remove %s to generate a new pair (and update every keyset that trusts it)", p.pub, p.key, p.pub)
		default:
			if err := restrictKeyFile(k); err != nil {
				return err
			}
			priv, err := loadPrivate(k)
			if err != nil {
				return err
			}
			derived := priv.Public().(ed25519.PublicKey)
			if !hasPub {
				if err := writeCertFile(pb, []byte(base64.StdEncoding.EncodeToString(derived)+"\n")); err != nil {
					return err
				}
				fmt.Printf("  [+] Derived missing %s from existing %s\n", p.pub, p.key)
				continue
			}
			raw, err := os.ReadFile(pb)
			if err != nil {
				return err
			}
			pub, err := keyfile.ParseEd25519Public(raw)
			if err != nil {
				return fmt.Errorf("%s: %w", p.pub, err)
			}
			if !pub.Equal(derived) {
				return fmt.Errorf("%s does not match private key %s: fix or remove the mismatched file", p.pub, p.key)
			}
			fmt.Printf("  [=] Keeping existing %s\n", p.key)
		}
	}
	return nil
}

func loadPrivate(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	priv, err := keyfile.ParseEd25519Private(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return priv, nil
}

func main() {
	outDirFlag := flag.String("out", "", "Output directory for certificates and keys")
	keysDirFlag := flag.String("keys-dir", "", "Optional directory for JWT/snapshot signing keys and keysets")
	keypairFlag := flag.String("keypair", "", "Write a fresh Ed25519 keypair to <prefix>.key and <prefix>.pub and exit")
	flag.Parse()

	if *keypairFlag != "" {
		if err := writeEd25519(*keypairFlag+".key", *keypairFlag+".pub"); err != nil {
			log.Fatalf("Failed to write keypair: %v", err)
		}
		return
	}

	outDir := *outDirFlag
	if outDir == "" {
		outDir = os.Getenv("CERT_DIR")
	}
	if outDir == "" {
		outDir = "deployments/certs"
	}
	if err := run(outDir, *keysDirFlag); err != nil {
		log.Fatalf("Certificate generation failed: %v", err)
	}
}
