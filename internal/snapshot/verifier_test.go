package snapshot_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func createSignedSnapshot(t *testing.T, priv ed25519.PrivateKey, version int64) *snapshotv1.SnapshotEnvelope {
	t.Helper()
	payload := samplePayload(version)
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

func createSignedLease(t *testing.T, priv ed25519.PrivateKey, leaseID string, version int64, validUntil time.Time) *snapshotv1.FreshnessLease {
	t.Helper()
	digest := snapshot.LeaseDigest(leaseID, version, validUntil.UnixNano())
	sig := ed25519.Sign(priv, digest)

	return &snapshotv1.FreshnessLease{
		LeaseId:         leaseID,
		SnapshotVersion: version,
		ValidUntil:      timestamppb.New(validUntil),
		LeaseSignature:  sig,
	}
}

func TestVerifier_MultiKeyRotation(t *testing.T) {
	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Phase 1: Initialize verifier with Key 1 only
	v := snapshot.NewVerifier(pub1)

	// Snapshot v1 signed by Key 1 accepted
	env1 := createSignedSnapshot(t, priv1, 1)
	payload1, err := v.VerifySnapshot(env1, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), payload1.Version)

	// Lease v1 signed by Key 1 accepted
	lease1 := createSignedLease(t, priv1, "lease-1", 1, time.Now().Add(10*time.Second))
	err = v.VerifyLease(lease1, 1)
	require.NoError(t, err)

	// Snapshot v2 signed by Key 2 rejected in Phase 1
	env2Key2 := createSignedSnapshot(t, priv2, 2)
	_, err = v.VerifySnapshot(env2Key2, 1)
	require.ErrorIs(t, err, snapshot.ErrInvalidSignature)

	// Lease v1 signed by Key 2 rejected in Phase 1
	lease2Key2 := createSignedLease(t, priv2, "lease-2", 1, time.Now().Add(10*time.Second))
	err = v.VerifyLease(lease2Key2, 1)
	require.ErrorIs(t, err, snapshot.ErrInvalidSignature)

	// Phase 2: Overlap - Add Key 2 to trusted keys
	v.AddTrustedKey(pub2)

	// Snapshot v2 signed by Key 2 now accepted
	payload2, err := v.VerifySnapshot(env2Key2, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), payload2.Version)

	// Lease signed by Key 2 now accepted
	err = v.VerifyLease(lease2Key2, 1)
	require.NoError(t, err)

	// Monotonic version check still enforced: Snapshot v1 <= 2 rejected
	_, err = v.VerifySnapshot(env1, 2)
	require.ErrorIs(t, err, snapshot.ErrMonotonicVersionViolation)

	// Snapshot v3 signed by Key 1 still accepted during overlap
	env3Key1 := createSignedSnapshot(t, priv1, 3)
	payload3, err := v.VerifySnapshot(env3Key1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), payload3.Version)

	// Phase 3: Retirement - Replace keyset with Key 2 only
	v.SetTrustedKeys([]ed25519.PublicKey{pub2})

	// Snapshot v4 signed by Key 2 accepted
	env4Key2 := createSignedSnapshot(t, priv2, 4)
	payload4, err := v.VerifySnapshot(env4Key2, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(4), payload4.Version)

	// Snapshot v5 signed by retired Key 1 rejected
	env5Key1 := createSignedSnapshot(t, priv1, 5)
	_, err = v.VerifySnapshot(env5Key1, 4)
	require.ErrorIs(t, err, snapshot.ErrInvalidSignature, "Key 1 must be rejected after retirement")

	// Lease signed by retired Key 1 rejected
	leaseExpiredKey1 := createSignedLease(t, priv1, "lease-retired", 4, time.Now().Add(10*time.Second))
	err = v.VerifyLease(leaseExpiredKey1, 4)
	require.ErrorIs(t, err, snapshot.ErrInvalidSignature, "Lease signed by Key 1 must be rejected after retirement")
}

func TestVerifier_EdgeCases(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	v := snapshot.NewVerifier(pub)

	t.Run("nil snapshot envelope", func(t *testing.T) {
		_, err := v.VerifySnapshot(nil, 0)
		require.Error(t, err)
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		env := createSignedSnapshot(t, priv, 1)
		env.PayloadSha256 = "0000000000000000000000000000000000000000000000000000000000000000"
		_, err := v.VerifySnapshot(env, 0)
		require.ErrorIs(t, err, snapshot.ErrChecksumMismatch)
	})

	t.Run("nil lease", func(t *testing.T) {
		err := v.VerifyLease(nil, 1)
		require.Error(t, err)
	})

	t.Run("expired lease", func(t *testing.T) {
		pastTime := time.Now().Add(-5 * time.Minute)
		lease := createSignedLease(t, priv, "lease-expired", 1, pastTime)
		err := v.VerifyLease(lease, 1)
		require.ErrorIs(t, err, snapshot.ErrLeaseExpired)
	})

	t.Run("lease version mismatch", func(t *testing.T) {
		futureTime := time.Now().Add(10 * time.Second)
		lease := createSignedLease(t, priv, "lease-mismatch", 1, futureTime)
		err := v.VerifyLease(lease, 2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mismatch")
	})
}
