package snapshot

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Signer signs snapshot envelopes and freshness leases using an Ed25519 private key.
type Signer struct {
	privKey ed25519.PrivateKey
	keyID   string
}

// NewSigner creates a new Signer with the specified Ed25519 private key and key ID.
func NewSigner(privKey ed25519.PrivateKey, keyID string) *Signer {
	return &Signer{
		privKey: privKey,
		keyID:   keyID,
	}
}

// SignSnapshot serializes the snapshot payload, calculates its SHA-256 digest,
// signs the hex-encoded digest with the Ed25519 private key, and returns an envelope.
func (s *Signer) SignSnapshot(payload *snapshotv1.SnapshotPayload) (*snapshotv1.SnapshotEnvelope, error) {
	if payload == nil {
		return nil, errors.New("cannot sign nil snapshot payload")
	}

	payloadBytes, err := proto.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal snapshot payload: %w", err)
	}

	sum := sha256.Sum256(payloadBytes)
	hexDigest := hex.EncodeToString(sum[:])

	signature := ed25519.Sign(s.privKey, []byte(hexDigest))

	now := time.Now()
	envelope := &snapshotv1.SnapshotEnvelope{
		Version:       payload.Version,
		SchemaVersion: 1,
		CreatedAt:     timestamppb.New(now),
		ExpiresAt:     timestamppb.New(now.Add(24 * time.Hour)),
		PayloadSha256: hexDigest,
		Payload:       payloadBytes,
		SigningKeyId:  s.keyID,
		Signature:     signature,
	}

	return envelope, nil
}

// LeaseDigest constructs the deterministic byte sequence used for signing and verifying leases.
func LeaseDigest(leaseID string, snapshotVersion int64, validUntilNano int64) []byte {
	return []byte(fmt.Sprintf("%s:%d:%d", leaseID, snapshotVersion, validUntilNano))
}

// SignLease generates a unique freshness lease for the given snapshot version and validity duration.
func (s *Signer) SignLease(snapshotVersion int64, duration time.Duration) (*snapshotv1.FreshnessLease, error) {
	leaseID := uuid.NewString()
	now := time.Now()
	validUntil := now.Add(duration)

	digest := LeaseDigest(leaseID, snapshotVersion, validUntil.UnixNano())
	signature := ed25519.Sign(s.privKey, digest)

	lease := &snapshotv1.FreshnessLease{
		LeaseId:         leaseID,
		SnapshotVersion: snapshotVersion,
		IssuedAt:        timestamppb.New(now),
		ValidUntil:      timestamppb.New(validUntil),
		LeaseSignature:  signature,
	}

	return lease, nil
}
