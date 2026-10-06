package snapshot_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"regexp"
	"testing"
	"time"

	"aegis/internal/control"
	"aegis/internal/snapshot"
	"aegis/internal/storage"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func generateKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func samplePayload(version int64) *snapshotv1.SnapshotPayload {
	return &snapshotv1.SnapshotPayload{
		Version: version,
		Routes: []*snapshotv1.RouteDefinition{
			{
				RouteId:                "orders.list",
				ServiceId:              "orders",
				HttpMethod:             "GET",
				PathTemplate:           "/api/orders",
				UpstreamUrl:            "https://orders:8081",
				UpstreamSpiffeId:       "spiffe://aegis.local/service/orders",
				RequiresWorkloadMtls:   true,
				RateLimit:              &snapshotv1.RateLimitPolicy{RequestsPerSecond: 100, Burst: 200},
				Timeout:                &snapshotv1.TimeoutPolicy{RequestTimeoutMs: 15000, UpstreamTimeoutMs: 5000},
			},
		},
		PolicyModules: []*snapshotv1.PolicyModule{
			{
				PackageName: "aegis.authz",
				ModuleName:  "rules.rego",
				SourceRego:  `package aegis.authz\ndefault allow = false\nallow if { input.user == "admin" }`,
			},
		},
		PolicyDataJson: []byte(`{"roles":{"admin":["*"]}}`),
		IdentityMappings: []*snapshotv1.IdentityMapping{
			{
				Role:        "admin",
				Permissions: []string{"*"},
			},
		},
	}
}

func TestSigner_SignAndVerifySnapshot(t *testing.T) {
	pub, priv := generateKeyPair(t)
	signer := snapshot.NewSigner(priv, "test-key-1")
	verifier := snapshot.NewVerifier(pub)

	payload := samplePayload(1)
	envelope, err := signer.SignSnapshot(payload)
	require.NoError(t, err)
	require.NotNil(t, envelope)

	assert.Equal(t, int64(1), envelope.Version)
	assert.Equal(t, "test-key-1", envelope.SigningKeyId)
	assert.NotEmpty(t, envelope.PayloadSha256)
	assert.NotEmpty(t, envelope.Signature)
	assert.NotNil(t, envelope.CreatedAt)
	assert.NotNil(t, envelope.ExpiresAt)

	// Verify against initial baseline version 0
	verifiedPayload, err := verifier.VerifySnapshot(envelope, 0)
	require.NoError(t, err)
	require.NotNil(t, verifiedPayload)
	assert.Equal(t, int64(1), verifiedPayload.Version)
	assert.Len(t, verifiedPayload.Routes, 1)
	assert.Equal(t, "orders.list", verifiedPayload.Routes[0].RouteId)
}

func TestSigner_CorruptedPayload(t *testing.T) {
	pub, priv := generateKeyPair(t)
	signer := snapshot.NewSigner(priv, "test-key-1")
	verifier := snapshot.NewVerifier(pub)

	payload := samplePayload(2)
	envelope, err := signer.SignSnapshot(payload)
	require.NoError(t, err)

	// Case 1: Corrupt payload bytes directly -> checksum mismatch
	corruptedEnv := proto.Clone(envelope).(*snapshotv1.SnapshotEnvelope)
	corruptedEnv.Payload[0] ^= 0xff
	_, err = verifier.VerifySnapshot(corruptedEnv, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, snapshot.ErrChecksumMismatch)

	// Case 2: Corrupt signature bytes -> invalid signature
	corruptedSigEnv := proto.Clone(envelope).(*snapshotv1.SnapshotEnvelope)
	corruptedSigEnv.Signature[0] ^= 0xff
	_, err = verifier.VerifySnapshot(corruptedSigEnv, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, snapshot.ErrInvalidSignature)

	// Case 3: Update checksum to match corrupted payload but without new signature
	corruptedBothEnv := proto.Clone(envelope).(*snapshotv1.SnapshotEnvelope)
	corruptedBothEnv.Payload[0] ^= 0xff
	corruptedBothEnv.PayloadSha256 = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = verifier.VerifySnapshot(corruptedBothEnv, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, snapshot.ErrChecksumMismatch)
}

func TestSigner_MonotonicRejection(t *testing.T) {
	pub, priv := generateKeyPair(t)
	signer := snapshot.NewSigner(priv, "test-key-1")
	verifier := snapshot.NewVerifier(pub)

	payload := samplePayload(3)
	envelope, err := signer.SignSnapshot(payload)
	require.NoError(t, err)

	// Monotonic violation: current version is 3 (equal)
	_, err = verifier.VerifySnapshot(envelope, 3)
	require.Error(t, err)
	assert.ErrorIs(t, err, snapshot.ErrMonotonicVersionViolation)

	// Monotonic violation: current version is 4 (higher)
	_, err = verifier.VerifySnapshot(envelope, 4)
	require.Error(t, err)
	assert.ErrorIs(t, err, snapshot.ErrMonotonicVersionViolation)

	// Valid monotonic progression: current version is 2
	verifiedPayload, err := verifier.VerifySnapshot(envelope, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), verifiedPayload.Version)
}

func TestSigner_FreshnessLease(t *testing.T) {
	pub, priv := generateKeyPair(t)
	signer := snapshot.NewSigner(priv, "test-key-1")
	verifier := snapshot.NewVerifier(pub)

	// 1. Valid lease
	lease, err := signer.SignLease(5, 10*time.Second)
	require.NoError(t, err)
	require.NotNil(t, lease)
	assert.NotEmpty(t, lease.LeaseId)
	assert.Equal(t, int64(5), lease.SnapshotVersion)

	err = verifier.VerifyLease(lease, 5)
	assert.NoError(t, err)

	// 2. Version mismatch
	err = verifier.VerifyLease(lease, 6)
	assert.Error(t, err)

	// 3. Expired lease
	expiredLease, err := signer.SignLease(5, -1*time.Second)
	require.NoError(t, err)
	err = verifier.VerifyLease(expiredLease, 5)
	assert.ErrorIs(t, err, snapshot.ErrLeaseExpired)

	// 4. Corrupted signature
	tamperedLease := proto.Clone(lease).(*snapshotv1.FreshnessLease)
	tamperedLease.LeaseSignature[0] ^= 0xff
	err = verifier.VerifyLease(tamperedLease, 5)
	assert.ErrorIs(t, err, snapshot.ErrInvalidSignature)
}

func TestSigner_RollbackEngine(t *testing.T) {
	pub, priv := generateKeyPair(t)
	signer := snapshot.NewSigner(priv, "test-key-1")
	verifier := snapshot.NewVerifier(pub)

	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := storage.NewSnapshotRepo(mock)
	engine := control.NewRollbackEngine(repo, signer)
	ctx := context.Background()

	// Target historical snapshot is v1
	v1Payload := samplePayload(1)
	v1Env, err := signer.SignSnapshot(v1Payload)
	require.NoError(t, err)

	// Current latest snapshot in database is v3
	v3Payload := samplePayload(3)
	v3Env, err := signer.SignSnapshot(v3Payload)
	require.NoError(t, err)

	// 1. Mock GetSnapshotByVersion for targetVersion 1
	targetRows := mock.NewRows([]string{
		"version", "schema_version", "payload_sha256", "payload_bytes",
		"signing_key_id", "signature", "created_at", "expires_at",
	}).AddRow(v1Env.Version, v1Env.SchemaVersion, v1Env.PayloadSha256, v1Env.Payload,
		v1Env.SigningKeyId, v1Env.Signature, v1Env.CreatedAt.AsTime(), v1Env.ExpiresAt.AsTime())

	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at FROM snapshots WHERE version = $1;")).
		WithArgs(int64(1)).
		WillReturnRows(targetRows)

	// 2. Mock GetLatestSnapshot -> returns v3
	latestRows := mock.NewRows([]string{
		"version", "schema_version", "payload_sha256", "payload_bytes",
		"signing_key_id", "signature", "created_at", "expires_at",
	}).AddRow(v3Env.Version, v3Env.SchemaVersion, v3Env.PayloadSha256, v3Env.Payload,
		v3Env.SigningKeyId, v3Env.Signature, v3Env.CreatedAt.AsTime(), v3Env.ExpiresAt.AsTime())

	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, schema_version, payload_sha256, payload_bytes, signing_key_id, signature, created_at, expires_at FROM snapshots ORDER BY version DESC LIMIT 1;")).
		WillReturnRows(latestRows)

	// 3. Mock SaveSnapshot for new version 4 (N+1 = 3+1 = 4)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO snapshots")).
		WithArgs(
			int64(4),
			int32(1),
			pgxmock.AnyArg(), // payload_sha256
			pgxmock.AnyArg(), // payload_bytes
			"test-key-1",
			pgxmock.AnyArg(), // signature
			pgxmock.AnyArg(), // created_at
			pgxmock.AnyArg(), // expires_at
			"admin-rollback",
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	rolledBackEnv, err := engine.RollbackToVersion(ctx, 1, "admin-rollback")
	require.NoError(t, err)
	require.NotNil(t, rolledBackEnv)

	// Verify monotonic property: version is strictly higher (N+1 = 4)
	assert.Equal(t, int64(4), rolledBackEnv.Version)

	// Verify payload contains historical v1 contents (e.g. routes) but updated version 4
	verifiedPayload, err := verifier.VerifySnapshot(rolledBackEnv, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(4), verifiedPayload.Version)
	assert.Len(t, verifiedPayload.Routes, 1)
	assert.Equal(t, "orders.list", verifiedPayload.Routes[0].RouteId)

	require.NoError(t, mock.ExpectationsWereMet())
}
