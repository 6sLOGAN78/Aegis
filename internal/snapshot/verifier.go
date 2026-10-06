package snapshot

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"google.golang.org/protobuf/proto"
)

var (
	// ErrMonotonicVersionViolation is returned when an incoming snapshot version is not strictly greater than current version.
	ErrMonotonicVersionViolation = errors.New("monotonic version violation: incoming snapshot version must be strictly greater than active version")

	// ErrChecksumMismatch is returned when the payload SHA-256 digest does not match the envelope checksum.
	ErrChecksumMismatch = errors.New("payload checksum mismatch")

	// ErrInvalidSignature is returned when the cryptographic signature is invalid or forged.
	ErrInvalidSignature = errors.New("invalid cryptographic signature")

	// ErrLeaseExpired is returned when the freshness lease validity period has elapsed.
	ErrLeaseExpired = errors.New("freshness lease expired")
)

// Verifier verifies snapshot envelopes and freshness leases using a trusted Ed25519 public key.
type Verifier struct {
	pubKey ed25519.PublicKey
}

// NewVerifier creates a new Verifier initialized with a trusted Ed25519 public key.
func NewVerifier(pubKey ed25519.PublicKey) *Verifier {
	return &Verifier{
		pubKey: pubKey,
	}
}

// VerifySnapshot validates the monotonic version, verifies the SHA-256 payload checksum,
// checks the Ed25519 digital signature, and unmarshals the SnapshotPayload.
func (v *Verifier) VerifySnapshot(env *snapshotv1.SnapshotEnvelope, currentVersion int64) (*snapshotv1.SnapshotPayload, error) {
	if env == nil {
		return nil, errors.New("cannot verify nil snapshot envelope")
	}

	// 1. Enforce strictly increasing monotonic version (CTRL-02, Invariant 9)
	if env.Version <= currentVersion {
		return nil, fmt.Errorf("%w: received version %d <= active version %d", ErrMonotonicVersionViolation, env.Version, currentVersion)
	}

	// 2. Validate SHA-256 checksum of raw payload bytes
	sum := sha256.Sum256(env.Payload)
	computedDigest := hex.EncodeToString(sum[:])
	if env.PayloadSha256 != computedDigest {
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, env.PayloadSha256, computedDigest)
	}

	// 3. Cryptographically verify signature over payload SHA-256
	if !ed25519.Verify(v.pubKey, []byte(env.PayloadSha256), env.Signature) {
		return nil, ErrInvalidSignature
	}

	// 4. Unmarshal payload
	var payload snapshotv1.SnapshotPayload
	if err := proto.Unmarshal(env.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal snapshot payload: %w", err)
	}

	// 5. Verify unmarshaled payload version matches envelope
	if payload.Version != env.Version {
		return nil, fmt.Errorf("payload version %d does not match envelope version %d", payload.Version, env.Version)
	}

	return &payload, nil
}

// VerifyLease validates the signature and expiration time of a FreshnessLease.
func (v *Verifier) VerifyLease(lease *snapshotv1.FreshnessLease, expectedVersion int64) error {
	if lease == nil {
		return errors.New("cannot verify nil freshness lease")
	}

	// Check version if an expected version is provided
	if expectedVersion > 0 && lease.SnapshotVersion != expectedVersion {
		return fmt.Errorf("lease snapshot version mismatch: expected %d, got %d", expectedVersion, lease.SnapshotVersion)
	}

	// Check lease expiration
	if lease.ValidUntil == nil {
		return ErrLeaseExpired
	}
	validUntil := lease.ValidUntil.AsTime()
	if time.Now().After(validUntil) {
		return ErrLeaseExpired
	}

	// Verify digital signature over deterministic lease digest
	digest := LeaseDigest(lease.LeaseId, lease.SnapshotVersion, validUntil.UnixNano())
	if !ed25519.Verify(v.pubKey, digest, lease.LeaseSignature) {
		return ErrInvalidSignature
	}

	return nil
}
