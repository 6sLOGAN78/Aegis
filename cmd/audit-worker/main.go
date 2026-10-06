package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"aegis/internal/audit"
	"aegis/internal/storage"
)

func main() {
	log.Println("Starting Aegis Audit Ingestion Worker...")

	// 1. Spool & Ingestion Configuration
	spoolDir := os.Getenv("AEGIS_SPOOL_DIR")
	if spoolDir == "" {
		spoolDir = "/var/log/aegis/wal"
	}

	batchSize := 500
	if bsStr := os.Getenv("AEGIS_AUDIT_BATCH_SIZE"); bsStr != "" {
		if bs, err := strconv.Atoi(bsStr); err == nil && bs > 0 {
			batchSize = bs
		}
	}

	flushInterval := 200 * time.Millisecond
	if fiStr := os.Getenv("AEGIS_AUDIT_FLUSH_INTERVAL_MS"); fiStr != "" {
		if fi, err := strconv.Atoi(fiStr); err == nil && fi > 0 {
			flushInterval = time.Duration(fi) * time.Millisecond
		}
	}

	pruneArchived := os.Getenv("AEGIS_PRUNE_ARCHIVED") == "true"

	// 2. PostgreSQL Connection Configuration
	dbPort := 5432
	if portStr := os.Getenv("AEGIS_DB_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			dbPort = p
		}
	}

	dbHost := os.Getenv("AEGIS_DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}

	dbUser := os.Getenv("AEGIS_DB_USER")
	if dbUser == "" {
		dbUser = "postgres"
	}

	dbName := os.Getenv("AEGIS_DB_NAME")
	if dbName == "" {
		dbName = "aegis"
	}

	dbSSLMode := os.Getenv("AEGIS_DB_SSLMODE")
	if dbSSLMode == "" {
		dbSSLMode = "disable"
	}

	poolCfg := storage.PoolConfig{
		Host:     dbHost,
		Port:     dbPort,
		User:     dbUser,
		Password: os.Getenv("AEGIS_DB_PASSWORD"),
		Database: dbName,
		SSLMode:  dbSSLMode,
		MaxConns: 10,
		MinConns: 2,
	}

	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()

	pool, err := storage.NewPool(initCtx, poolCfg)
	if err != nil {
		log.Fatalf("Failed to initialize PostgreSQL connection pool: %v", err)
	}
	defer pool.Close()

	// 3. Initialize Audit Worker
	workerCfg := audit.WorkerConfig{
		SpoolDir:      spoolDir,
		BatchSize:     batchSize,
		FlushInterval: flushInterval,
		PruneArchived: pruneArchived,
	}

	worker, err := audit.NewAuditWorker(pool, workerCfg)
	if err != nil {
		log.Fatalf("Failed to initialize audit worker: %v", err)
	}

	// 4. Run Worker with graceful signal termination
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	workerErrCh := make(chan error, 1)
	go func() {
		log.Printf("Audit Worker tailing spool %s (batch=%d, flush=%v, prune=%v)",
			spoolDir, batchSize, flushInterval, pruneArchived)
		workerErrCh <- worker.Start(workerCtx)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down audit worker gracefully...", sig)
		workerCancel()
		if err := <-workerErrCh; err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("Worker shutdown warning: %v", err)
		}
	case err := <-workerErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("Worker exited unexpectedly: %v", err)
		}
	}

	log.Println("Aegis Audit Worker terminated cleanly")
}
