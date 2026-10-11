package dr

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"aegis/internal/audit"
	"aegis/internal/control"
	"aegis/internal/revocation"
	"aegis/internal/snapshot"
	"aegis/internal/storage"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// ReconcileVersionContinuity determines the next valid monotonic snapshot version
// following a database point-in-time recovery event, preventing ErrMonotonicVersionViolation.
func ReconcileVersionContinuity(dbLatestVersion int64, gatewayFleetMaxVersion int64) int64 {
	baseVersion := dbLatestVersion
	if gatewayFleetMaxVersion > baseVersion {
		baseVersion = gatewayFleetMaxVersion
	}
	return baseVersion + 1
}

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

func anyAuditArgs() []any {
	args := make([]any, 21)
	for i := 0; i < 21; i++ {
		args[i] = pgxmock.AnyArg()
	}
	return args
}

// TestRedisQuarantineReconstruction simulates catastrophic Redis memory loss and rapid recovery.
func TestRedisQuarantineReconstruction(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	// Gateway-style store: fails closed when the security-state marker is absent.
	store := revocation.NewStore(rdb, revocation.WithRequireSecurityMarker())

	ctx := context.Background()
	now := time.Now()
	repo := newMemSecRepo()
	repo.q["malicious-actor"] = storage.QuarantineRecord{
		PrincipalID: "malicious-actor", Reason: "SUSPECTED_TOKEN_LEAK", CreatedBy: "sec-ops",
		CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour),
	}
	repo.q["expired-bot"] = storage.QuarantineRecord{
		PrincipalID: "expired-bot", Reason: "OLD", CreatedBy: "sec-ops",
		CreatedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	repo.t["revoked-jti"] = storage.RevokedTokenRecord{
		JTI: "revoked-jti", Reason: "LEAK", CreatedBy: "sec-ops", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}

	ss := control.NewSecurityState(repo, store,
		control.WithMarkerCheckInterval(50*time.Millisecond),
		control.WithReconcileInterval(time.Hour))

	// 1. Initial reconstruct: quarantine and revocation are enforced.
	require.NoError(t, ss.Reconstruct(ctx))
	revoked, reason, err := store.CheckRevocation(ctx, "malicious-actor", "")
	require.NoError(t, err)
	assert.True(t, revoked)
	assert.Equal(t, "PRINCIPAL_QUARANTINED", reason)
	revoked, reason, err = store.CheckRevocation(ctx, "", "revoked-jti")
	require.NoError(t, err)
	assert.True(t, revoked)
	assert.Equal(t, "TOKEN_REVOKED", reason)
	assert.False(t, mr.Exists("quarantine:principal:expired-bot"))
	origTTL := mr.TTL("quarantine:principal:malicious-actor")

	// 2. Catastrophic event: Redis wiped. Gateways must fail closed, never "not revoked".
	mr.FlushAll()
	_, _, err = store.CheckRevocation(ctx, "malicious-actor", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, revocation.ErrSecurityStateMissing))

	// 3. The control plane notices marker loss and reconstructs within a few intervals.
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { ss.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()

	require.Eventually(t, func() bool { return mr.Exists(revocation.SecurityStateMarkerKey) },
		2*time.Second, 20*time.Millisecond)

	revoked, reason, err = store.CheckRevocation(ctx, "malicious-actor", "")
	require.NoError(t, err)
	assert.True(t, revoked, "principal must be blocked again after DR reconstruction")
	assert.Equal(t, "PRINCIPAL_QUARANTINED", reason)
	ttl := mr.TTL("quarantine:principal:malicious-actor")
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, origTTL)
	assert.False(t, mr.Exists("quarantine:principal:expired-bot"))

	revoked, _, err = store.CheckRevocation(ctx, "", "revoked-jti")
	require.NoError(t, err)
	assert.True(t, revoked)

	revoked, _, err = store.CheckRevocation(ctx, "legitimate-user", "")
	require.NoError(t, err)
	assert.False(t, revoked)
}

// TestPostgresPITRVersionContinuity simulates database rollback to version M while fleet serves version N.
func TestPostgresPITRVersionContinuity(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	verifier := snapshot.NewVerifier(pub)

	// 1. Fleet actively serving snapshot version N = 7
	const fleetActiveVersion int64 = 7
	env7 := createSignedSnapshot(t, priv, fleetActiveVersion)
	payload7, err := verifier.VerifySnapshot(env7, 6)
	require.NoError(t, err)
	assert.Equal(t, fleetActiveVersion, payload7.Version)

	// 2. Database restored from Point-In-Time Recovery backup containing version M = 4
	const dbRestoredVersion int64 = 4

	// 3. Reconcile version continuity to prevent monotonic version violation
	nextVersion := ReconcileVersionContinuity(dbRestoredVersion, fleetActiveVersion)
	assert.Equal(t, int64(8), nextVersion, "Next version must be max(fleet, db) + 1 = 8")

	// 4. Verify publishing any stale version <= 7 is rejected by gateway verifier
	envStale := createSignedSnapshot(t, priv, 5) // M + 1 before reconciliation
	_, err = verifier.VerifySnapshot(envStale, fleetActiveVersion)
	require.ErrorIs(t, err, snapshot.ErrMonotonicVersionViolation, "Gateway must reject version <= active version")

	// 5. Verify reconciled version 8 is accepted by gateways
	envReconciled := createSignedSnapshot(t, priv, nextVersion)
	payload8, err := verifier.VerifySnapshot(envReconciled, fleetActiveVersion)
	require.NoError(t, err)
	assert.Equal(t, int64(8), payload8.Version)
}

// TestWALCrashRecoveryReplay simulates gateway worker crash and idempotent deduplicated replay.
func TestWALCrashRecoveryReplay(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	tmpSpoolDir := t.TempDir()

	// 1. Spool 50 events using DiskSpool
	spool, err := audit.NewDiskSpool(audit.DiskSpoolConfig{
		SpoolDir:         tmpSpoolDir,
		MaxSegmentBytes:  10 * 1024 * 1024,
		VolumeQuotaBytes: 50 * 1024 * 1024,
	})
	require.NoError(t, err)

	const eventCount = 50
	for i := 0; i < eventCount; i++ {
		evt := &audit.CompletionEvent{
			EventID:       fmt.Sprintf("evt-dr-%03d", i),
			Timestamp:     time.Now().UTC(),
			RequestID:     uuid.NewString(),
			PrincipalID:   "operator",
			PrincipalKind: "user",
			ClientIP:      "10.0.1.20",
			HTTPMethod:    "POST",
			CanonicalPath: "/api/orders",
			RouteID:       "orders.create",
			ServiceID:     "orders",
			Decision:      "allow",
			ReasonCode:    "ALLOWED",
			HTTPStatus:    200,
			DurationMS:    1.5,
		}
		require.NoError(t, spool.AppendPreForward(evt))
	}
	segName := spool.CurrentSegment()
	require.NoError(t, spool.Close())

	// 2. Expect batch insertion for all 50 events
	b1 := mock.ExpectBatch()
	for i := 0; i < eventCount; i++ {
		b1.ExpectExec("INSERT INTO audit_events").
			WithArgs(anyAuditArgs()...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
	}

	// 3. Worker pass 1: Ingests all 50 events and updates persistent cursor
	worker, err := audit.NewAuditWorker(mock, audit.WorkerConfig{
		SpoolDir:      tmpSpoolDir,
		BatchSize:     100,
		FlushInterval: 50 * time.Millisecond,
	})
	require.NoError(t, err)

	processed, err := worker.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, eventCount, processed)
	assert.NoError(t, mock.ExpectationsWereMet())

	cursorState, err := worker.Cursor().LoadOffset()
	require.NoError(t, err)
	assert.Equal(t, segName, cursorState.SegmentFile)
	assert.Greater(t, cursorState.Offset, int64(0))

	// 4. Simulate crash recovery replay: reset cursor to offset 0 to simulate replay of same segment
	err = worker.Cursor().SaveOffset(segName, 0)
	require.NoError(t, err)

	// Expect idempotent batch execution where all 50 records hit ON CONFLICT DO NOTHING (0 rows inserted)
	b2 := mock.ExpectBatch()
	for i := 0; i < eventCount; i++ {
		b2.ExpectExec("INSERT INTO audit_events").
			WithArgs(anyAuditArgs()...).
			WillReturnResult(pgxmock.NewResult("INSERT", 0)) // 0 rows inserted due to ON CONFLICT DO NOTHING
	}

	replayed, err := worker.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, eventCount, replayed, "All 50 events replayed idempotently")
	assert.NoError(t, mock.ExpectationsWereMet())

	cursorAfterReplay, err := worker.Cursor().LoadOffset()
	require.NoError(t, err)
	assert.Equal(t, cursorState.Offset, cursorAfterReplay.Offset, "Cursor advances to original position after replay")
}
