package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"aegis/internal/pki"
)

func writeCertFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

func writeKeyFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0600)
}

func main() {
	outDirFlag := flag.String("out", "", "Output directory for certificates and keys")
	flag.Parse()

	outDir := *outDirFlag
	if outDir == "" {
		outDir = os.Getenv("CERT_DIR")
	}
	if outDir == "" {
		outDir = "deployments/certs"
	}

	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory %s: %v", outDir, err)
	}

	fmt.Printf("Generating development PKI and assertion keys into %s\n", outDir)

	// 1. Root CA
	ca, err := pki.NewCA("Aegis Root CA")
	if err != nil {
		log.Fatalf("Failed to create Root CA: %v", err)
	}
	caCertPEM := pki.EncodeCertPEM(ca.Certificate.Raw)
	caKeyPEM, err := pki.EncodeKeyPEM(ca.PrivateKey)
	if err != nil {
		log.Fatalf("Failed to encode CA key PEM: %v", err)
	}
	if err := writeCertFile(filepath.Join(outDir, "root-ca.crt"), caCertPEM); err != nil {
		log.Fatalf("Failed to write root-ca.crt: %v", err)
	}
	if err := writeKeyFile(filepath.Join(outDir, "root-ca.key"), caKeyPEM); err != nil {
		log.Fatalf("Failed to write root-ca.key: %v", err)
	}
	fmt.Println("  [+] Generated Root CA (root-ca.crt, root-ca.key)")

	// 2. Gateway Server Certificate
	gwServerCert, err := ca.IssueServerCert("gateway", []string{"gateway", "localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		log.Fatalf("Failed to issue gateway server certificate: %v", err)
	}
	gwServerKeyPEM, err := pki.EncodeKeyPEM(gwServerCert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		log.Fatalf("Failed to encode gateway server key PEM: %v", err)
	}
	if err := writeCertFile(filepath.Join(outDir, "gateway-server.crt"), pki.EncodeCertPEM(gwServerCert.Certificate[0])); err != nil {
		log.Fatalf("Failed to write gateway-server.crt: %v", err)
	}
	if err := writeKeyFile(filepath.Join(outDir, "gateway-server.key"), gwServerKeyPEM); err != nil {
		log.Fatalf("Failed to write gateway-server.key: %v", err)
	}
	fmt.Println("  [+] Generated Gateway server cert (gateway-server.crt, gateway-server.key)")

	// 3. Gateway Client Certificate (SPIFFE)
	gatewaySPIFFE := "spiffe://aegis.local/ns/gateway/sa/aegis-gateway"
	gwClientCert, err := ca.IssueWorkloadCert(gatewaySPIFFE)
	if err != nil {
		log.Fatalf("Failed to issue gateway client certificate: %v", err)
	}
	gwClientKeyPEM, err := pki.EncodeKeyPEM(gwClientCert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		log.Fatalf("Failed to encode gateway client key PEM: %v", err)
	}
	if err := writeCertFile(filepath.Join(outDir, "gateway-client.crt"), pki.EncodeCertPEM(gwClientCert.Certificate[0])); err != nil {
		log.Fatalf("Failed to write gateway-client.crt: %v", err)
	}
	if err := writeKeyFile(filepath.Join(outDir, "gateway-client.key"), gwClientKeyPEM); err != nil {
		log.Fatalf("Failed to write gateway-client.key: %v", err)
	}
	fmt.Println("  [+] Generated Gateway client cert (gateway-client.crt, gateway-client.key)")

	// 4. Workload Orders Certificate (SPIFFE)
	ordersSPIFFE := "spiffe://aegis.local/workload/orders"
	ordersClientCert, err := ca.IssueWorkloadCert(ordersSPIFFE)
	if err != nil {
		log.Fatalf("Failed to issue orders workload certificate: %v", err)
	}
	ordersClientKeyPEM, err := pki.EncodeKeyPEM(ordersClientCert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		log.Fatalf("Failed to encode orders workload key PEM: %v", err)
	}
	if err := writeCertFile(filepath.Join(outDir, "workload-orders.crt"), pki.EncodeCertPEM(ordersClientCert.Certificate[0])); err != nil {
		log.Fatalf("Failed to write workload-orders.crt: %v", err)
	}
	if err := writeKeyFile(filepath.Join(outDir, "workload-orders.key"), ordersClientKeyPEM); err != nil {
		log.Fatalf("Failed to write workload-orders.key: %v", err)
	}
	fmt.Println("  [+] Generated Workload Orders cert (workload-orders.crt, workload-orders.key)")

	// 5. Backend Microservices Server Certificates
	services := []string{"orders", "payments", "admin"}
	for _, svc := range services {
		svcCert, err := ca.IssueServerCert(svc, []string{svc, "localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
		if err != nil {
			log.Fatalf("Failed to issue server certificate for %s: %v", svc, err)
		}
		svcKeyPEM, err := pki.EncodeKeyPEM(svcCert.PrivateKey.(*ecdsa.PrivateKey))
		if err != nil {
			log.Fatalf("Failed to encode key PEM for %s: %v", svc, err)
		}
		if err := writeCertFile(filepath.Join(outDir, svc+".crt"), pki.EncodeCertPEM(svcCert.Certificate[0])); err != nil {
			log.Fatalf("Failed to write %s.crt: %v", svc, err)
		}
		if err := writeKeyFile(filepath.Join(outDir, svc+".key"), svcKeyPEM); err != nil {
			log.Fatalf("Failed to write %s.key: %v", svc, err)
		}
		fmt.Printf("  [+] Generated %s server cert (%s.crt, %s.key)\n", svc, svc, svc)
	}

	// 6. Ed25519 Gateway Assertion Key Pair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatalf("Failed to generate Ed25519 assertion key pair: %v", err)
	}
	privKeyB64 := base64.StdEncoding.EncodeToString(privKey)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)

	if err := writeKeyFile(filepath.Join(outDir, "assertion-ed25519.key"), []byte(privKeyB64+"\n")); err != nil {
		log.Fatalf("Failed to write assertion-ed25519.key: %v", err)
	}
	if err := writeCertFile(filepath.Join(outDir, "assertion-ed25519.pub"), []byte(pubKeyB64+"\n")); err != nil {
		log.Fatalf("Failed to write assertion-ed25519.pub: %v", err)
	}
	fmt.Println("  [+] Generated Ed25519 Assertion Key Pair (assertion-ed25519.key, assertion-ed25519.pub)")

	fmt.Println("Certificate generation completed successfully.")
}
