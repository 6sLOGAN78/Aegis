package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"aegis/internal/control"
	"aegis/internal/snapshot"
	"aegis/internal/storage"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"google.golang.org/grpc"
)

var defaultDemoSeed = []byte("aegis-demo-issuer-secret-seed-32")

func main() {
	var (
		flagGRPCPort = flag.String("grpc-port", "", "gRPC distribution port (default :9090)")
		flagHTTPPort = flag.String("http-port", "", "Management HTTP port (default :8084)")
	)
	flag.Parse()

	// 1. Port configuration
	grpcPort := os.Getenv("AEGIS_GRPC_PORT")
	if *flagGRPCPort != "" {
		grpcPort = *flagGRPCPort
	}
	if grpcPort == "" {
		grpcPort = ":9090"
	}
	if !strings.HasPrefix(grpcPort, ":") {
		grpcPort = ":" + grpcPort
	}

	httpPort := os.Getenv("AEGIS_PORT")
	if httpPort == "" {
		httpPort = os.Getenv("AEGIS_HTTP_PORT")
	}
	if *flagHTTPPort != "" {
		httpPort = *flagHTTPPort
	}
	if httpPort == "" {
		httpPort = ":8084"
	}
	if !strings.HasPrefix(httpPort, ":") {
		httpPort = ":" + httpPort
	}

	// 2. Load or generate Ed25519 Signing Key (CTRL-02, Invariant 4)
	var privKey ed25519.PrivateKey
	if keyPath := os.Getenv("AEGIS_SIGNING_KEY_PATH"); keyPath != "" {
		if data, err := os.ReadFile(keyPath); err == nil {
			block, _ := pem.Decode(data)
			if block != nil {
				data = block.Bytes
			}
			if len(data) == ed25519.PrivateKeySize {
				privKey = ed25519.PrivateKey(data)
			} else if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); err == nil && len(b) == ed25519.PrivateKeySize {
				privKey = ed25519.PrivateKey(b)
			}
		} else {
			log.Printf("Warning: failed to read signing key from %s: %v", keyPath, err)
		}
	}
	if privKey == nil {
		if seedStr := os.Getenv("AEGIS_SIGNING_KEY_SEED"); seedStr != "" {
			seed := []byte(seedStr)
			if len(seed) >= 32 {
				privKey = ed25519.NewKeyFromSeed(seed[:32])
			}
		}
	}
	if privKey == nil {
		privKey = ed25519.NewKeyFromSeed(defaultDemoSeed)
	}

	keyID := os.Getenv("AEGIS_SIGNING_KEY_ID")
	if keyID == "" {
		keyID = "control-plane-key-v1"
	}
	signer := snapshot.NewSigner(privKey, keyID)

	// 3. Connect to PostgreSQL Connection Pool (CTRL-01, Invariant 11)
	poolCfg := storage.DefaultPoolConfig()
	if h := os.Getenv("AEGIS_DB_HOST"); h != "" {
		poolCfg.Host = h
	}
	if p := os.Getenv("AEGIS_DB_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil {
			poolCfg.Port = port
		}
	}
	if u := os.Getenv("AEGIS_DB_USER"); u != "" {
		poolCfg.User = u
	}
	if pass := os.Getenv("AEGIS_DB_PASSWORD"); pass != "" {
		poolCfg.Password = pass
	}
	if dbName := os.Getenv("AEGIS_DB_NAME"); dbName != "" {
		poolCfg.Database = dbName
	}
	if ssl := os.Getenv("AEGIS_DB_SSLMODE"); ssl != "" {
		poolCfg.SSLMode = ssl
	}

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dbCancel()

	var snapshotRepo *storage.SnapshotRepo
	var routeRepo *storage.RouteRepo
	pool, err := storage.NewPool(dbCtx, poolCfg)
	if err != nil {
		log.Printf("Notice: PostgreSQL unavailable (%s:%d): %v (running with in-memory state)", poolCfg.Host, poolCfg.Port, err)
	} else {
		log.Printf("Connected to PostgreSQL database %q at %s:%d", poolCfg.Database, poolCfg.Host, poolCfg.Port)
		snapshotRepo = storage.NewSnapshotRepo(pool)
		routeRepo = storage.NewRouteRepo(pool)
		_ = routeRepo
	}

	// 4. Retrieve latest snapshot from database or synthesize bootstrap snapshot v1
	var latestSnapshot *snapshotv1.SnapshotEnvelope
	if snapshotRepo != nil {
		snapCtx, snapCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer snapCancel()
		latest, err := snapshotRepo.GetLatestSnapshot(snapCtx)
		if err == nil {
			latestSnapshot = latest
			log.Printf("Loaded latest active snapshot version %d from database", latest.Version)
		} else if !errors.Is(err, storage.ErrNotFound) {
			log.Printf("Warning: failed to load snapshot from database: %v", err)
		}
	}

	if latestSnapshot == nil {
		initialPayload := &snapshotv1.SnapshotPayload{
			Version: 1,
			Routes:  []*snapshotv1.RouteDefinition{},
			PolicyModules: []*snapshotv1.PolicyModule{
				{
					PackageName: "aegis.authz",
					ModuleName:  "rules.rego",
					SourceRego: `package aegis.authz

default allow := false
default reason_code := "DENIED_DEFAULT"

decision := {
    "allow": allow,
    "reason_code": reason_code,
    "snapshot_version": input.snapshot_version,
}
`,
				},
			},
		}
		env, err := signer.SignSnapshot(initialPayload)
		if err != nil {
			log.Fatalf("Failed to initialize bootstrap snapshot: %v", err)
		}
		latestSnapshot = env
		if snapshotRepo != nil {
			saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer saveCancel()
			if err := snapshotRepo.SaveSnapshot(saveCtx, env, "system-bootstrap"); err != nil {
				log.Printf("Warning: failed to save bootstrap snapshot to database: %v", err)
			}
		}
		log.Printf("Initialized bootstrap snapshot version 1 (signing key: %s)", keyID)
	}

	// 5. Initialize Acknowledgment Tracker and Distribution Server
	ackTracker := control.NewAckTracker(snapshotRepo)
	distServer := control.NewSnapshotDistributionServer(latestSnapshot, ackTracker)

	// 6. Launch 10-Second Freshness Lease Generator Loop (CTRL-04, Invariant 1)
	leaseGen := control.NewLeaseGenerator(distServer, signer, 10*time.Second, 15*time.Second)
	leaseCtx, leaseCancel := context.WithCancel(context.Background())
	defer leaseCancel()
	go leaseGen.Start(leaseCtx)

	// 7. Start gRPC Distribution Server
	lis, err := net.Listen("tcp", grpcPort)
	if err != nil {
		log.Fatalf("Failed to bind gRPC listener on %s: %v", grpcPort, err)
	}

	grpcServer := grpc.NewServer()
	snapshotv1.RegisterSnapshotDistributionServiceServer(grpcServer, distServer)

	go func() {
		log.Printf("Aegis Control Plane gRPC server listening on %s", grpcPort)
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			log.Fatalf("gRPC server encountered unexpected error: %v", err)
		}
	}()

	// 8. Graceful shutdown handler
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down Aegis Control Plane daemon gracefully...")

	leaseCancel()
	leaseGen.Stop()
	grpcServer.GracefulStop()

	if pool != nil {
		pool.Close()
	}

	log.Println("Aegis Control Plane daemon stopped cleanly")
}
