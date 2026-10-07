package snapshot

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
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

// Verifier verifies snapshot envelopes and freshness leases using one or more trusted Ed25519 public keys.
type Verifier struct {
	trustedKeys []ed25519.PublicKey
	mu          sync.RWMutex
}

// NewVerifier creates a new Verifier initialized with one or more trusted Ed25519 public keys.
func NewVerifier(pubKeys ...ed25519.PublicKey) *Verifier {
	keys := make([]ed25519.PublicKey, len(pubKeys))
	copy(keys, pubKeys)
	return &Verifier{
		trustedKeys: keys,
	}
}

// AddTrustedKey appends an additional trusted public key during zero-downtime key rotation.
func (v *Verifier) AddTrustedKey(pubKey ed25519.PublicKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.trustedKeys = append(v.trustedKeys, pubKey)
}

// SetTrustedKeys atomically replaces the trusted keyset (Phase 3 of rotation: retirement).
func (v *Verifier) SetTrustedKeys(keys []ed25519.PublicKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	copied := make([]ed25519.PublicKey, len(keys))
	copy(copied, keys)
	v.trustedKeys = copied
}

// VerifySnapshot validates the monotonic version, verifies the SHA-256 payload checksum,
// checks the Ed25519 digital signature against all trusted keys, and unmarshals the SnapshotPayload.
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

	// 3. Cryptographically verify signature over payload SHA-256 against trusted keyset
	v.mu.RLock()
	keys := make([]ed25519.PublicKey, len(v.trustedKeys))
	copy(keys, v.trustedKeys)
	v.mu.RUnlock()

	validSig := false
	for _, key := range keys {
		if ed25519.Verify(key, []byte(env.PayloadSha256), env.Signature) {
			validSig = true
			break
		}
	}
	if !validSig {
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

// VerifyLease validates the signature and expiration time of a FreshnessLease against all trusted keys.
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

	// Verify digital signature over deterministic lease digest against trusted keyset
	digest := LeaseDigest(lease.LeaseId, lease.SnapshotVersion, validUntil.UnixNano())
	v.mu.RLock()
	keys := make([]ed25519.PublicKey, len(v.trustedKeys))
	copy(keys, v.trustedKeys)
	v.mu.RUnlock()

	validSig := false
	for _, key := range keys {
		if ed25519.Verify(key, digest, lease.LeaseSignature) {
			validSig = true
			break
		}
	}
	if !validSig {
		return ErrInvalidSignature
	}

	return nil
}
