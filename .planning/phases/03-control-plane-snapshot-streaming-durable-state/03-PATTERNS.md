# Phase 3: Control Plane, Snapshot Streaming & Durable State — Pattern Map

**Phase:** 03-control-plane-snapshot-streaming-durable-state  
**Status:** Approved Reference  
**Domain:** PostgreSQL 16 Persistence, Goose Migrations, gRPC Streaming, Ed25519 Freshness Leases, Redis GCRA Rate Limiting, Pre-Forward Disk WAL Spool

---

## 1. File Inventory & Analogs

```
aegis/
├── cmd/
│   ├── control-plane/main.go               # Analog: cmd/gateway/main.go (CLI flags, signal handling, graceful shutdown)
│   └── audit-worker/main.go                # Analog: cmd/services/*/main.go (daemon lifecycle, config loader)
├── internal/
│   ├── storage/
│   │   ├── db.go                           # pgxpool connection pool management
│   │   ├── migration.go                    # Goose migration runner with embed.FS (Analog: policies/loader.go)
│   │   ├── route_repo.go                   # Route repository (CRUD on routes & services)
│   │   ├── snapshot_repo.go                # Snapshot envelopes & outbox persistence
│   │   ├── ack_repo.go                     # Gateway replica acknowledgment persistence
│   │   └── audit_repo.go                   # Batch PostgreSQL writer for audit events (pgx.Batch)
│   ├── control/
│   │   ├── server.go                       # gRPC SnapshotDistributionService (Analog: internal/proxy/server.go)
│   │   ├── signer.go                       # Ed25519 snapshot & lease signer (Analog: internal/identity/assertion.go)
│   │   ├── lease.go                        # 10-second freshness lease background ticker
│   │   ├── validator.go                    # Route schema & Rego unit test validator (Analog: internal/policy/engine.go)
│   │   └── rollback.go                     # Monotonic rollback (republishing vN+1)
│   ├── snapshot/
│   │   ├── manager.go                      # sync/atomic.Pointer[ActiveState] (Analog: internal/policy/engine.go)
│   │   ├── client.go                       # gRPC stream client with reconnect jitter
│   │   └── verifier.go                     # Signature, checksum, and monotonic version verification
│   ├── ratelimit/
│   │   └── limiter.go                      # GCRA token bucket using redis_rate/v10 (Analog: internal/proxy/limiter.go)
│   ├── revocation/
│   │   └── store.go                        # Pipelined JTI & principal quarantine checks (Analog: internal/identity/jwt.go)
│   └── audit/
│       ├── spool.go                        # Pre-forward append-only WAL with fsync and 90% gate (Analog: internal/audit/logger.go)
│       ├── cursor.go                       # Persistent file offset checkpoint tracker
│       └── worker.go                       # Background WAL tailer and batch dispatcher
├── migrations/
│   ├── 000001_create_control_plane_tables.sql
│   └── 000002_create_partitioned_audit_tables.sql
└── tests/
    ├── failure/
    │   ├── redis_outage_test.go            # Fail-closed 503 verification on Redis down
    │   ├── lease_expiry_test.go            # Fail-closed 503 verification on lease >60s
    │   └── spool_saturation_test.go        # Fail-closed 503 verification on spool >=90%
    └── integration/
        └── snapshot_streaming_test.go      # End-to-end gRPC push, atomic swap, and ack
```

---

## 2. Key Code Analogs & Patterns

### Pattern 1: Database Migration Runner with `embed.FS`
```go
package storage

import (
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

func RunMigrations(db *sql.DB) error {
	goose.SetBaseFS(embedMigrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "migrations")
}
```

### Pattern 2: Atomic In-Memory Swap with `sync/atomic.Pointer`
```go
type ActiveState struct {
	Version      int64
	Router       *proxy.Router
	PolicyEngine *policy.Engine
	ActivatedAt  time.Time
}

type Manager struct {
	active             atomic.Pointer[ActiveState]
	lastLeaseRenewedAt atomic.Int64 // UnixNano
	trustedSigningKey  ed25519.PublicKey
}
```

### Pattern 3: Pipelined Redis Revocation with Rigid 200ms Deadline
```go
func (s *Store) CheckRevocation(ctx context.Context, principalID, jti string) (bool, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	pipe := s.client.Pipeline()
	quarCmd := pipe.Exists(ctx, "quarantine:principal:"+principalID)
	jtiCmd := pipe.Exists(ctx, "revocation:jti:"+jti)

	if _, err := pipe.Exec(ctx); err != nil {
		// Must fail closed (REV-04)
		return false, "", fmt.Errorf("redis check failed: %w", err)
	}
	if quarCmd.Val() > 0 {
		return true, "PRINCIPAL_QUARANTINED", nil
	}
	if jtiCmd.Val() > 0 {
		return true, "TOKEN_REVOKED", nil
	}
	return false, "", nil
}
```

### Pattern 4: Pre-Forward WAL Append with `fsync()` and 90% Saturation Gate
```go
func (s *DiskSpool) AppendPreForward(event *CompletionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check 90% saturation safety gate (AUD-02)
	if saturated, err := s.checkSaturation(0.90); err != nil || saturated {
		return ErrSpoolSaturated
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	checksum := crc32.ChecksumIEEE(payload)
	// Write magic, checksum, length, payload, newline
	// ...
	return s.activeFile.Sync() // Mandatory fsync before upstream network dispatch (AUD-01)
}
```
