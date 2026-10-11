package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"aegis/internal/identity"
	"aegis/internal/snapshot"
	snapshotv1 "aegis/pkg/api/snapshot/v1"
)

const kwIssuer, kwAudience = "iss", "aud"

func kwKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func kwWrite(t *testing.T, dir, name string, pub ed25519.PublicKey) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(base64.StdEncoding.EncodeToString(pub)), 0o600))
}

func kwToken(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	claims := identity.UserClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: kwIssuer, Audience: jwt.ClaimStrings{kwAudience}, Subject: "u",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}}
	s, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(priv)
	require.NoError(t, err)
	return "Bearer " + s
}

func kwEnvelope(t *testing.T, priv ed25519.PrivateKey, version int64) *snapshotv1.SnapshotEnvelope {
	t.Helper()
	payload, err := proto.Marshal(&snapshotv1.SnapshotPayload{Version: version})
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	d := hex.EncodeToString(sum[:])
	return &snapshotv1.SnapshotEnvelope{Version: version, Payload: payload, PayloadSha256: d, Signature: ed25519.Sign(priv, []byte(d))}
}

func kwGetenv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestKeysetJWTRotationWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	oldPub, oldPriv := kwKey(t)
	newPub, newPriv := kwKey(t)
	kwWrite(t, dir, "a.pub", oldPub)

	fallbackPub, _ := kwKey(t)
	validator := identity.NewTokenValidator(kwIssuer, kwAudience, fallbackPub)
	verifier := snapshot.NewVerifier(fallbackPub)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := map[string]string{"AEGIS_JWT_KEYSET_DIR": dir, "AEGIS_KEYSET_RELOAD_INTERVAL": "1s"}
	require.NoError(t, setupKeysets(ctx, kwGetenv(env), validator, verifier, nil))

	// The dir set replaces the fallback key synchronously at startup.
	_, err := validator.ValidateBearerToken(kwToken(t, oldPriv))
	require.NoError(t, err)

	// Add a second key: both validate after a reload.
	kwWrite(t, dir, "b.pub", newPub)
	require.Eventually(t, func() bool {
		_, e := validator.ValidateBearerToken(kwToken(t, newPriv))
		return e == nil
	}, 5*time.Second, 100*time.Millisecond)
	_, err = validator.ValidateBearerToken(kwToken(t, oldPriv))
	require.NoError(t, err)

	// A corrupt file keeps the previous set.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.pub"), []byte("not-a-key"), 0o600))
	time.Sleep(2500 * time.Millisecond)
	_, err = validator.ValidateBearerToken(kwToken(t, newPriv))
	require.NoError(t, err)
	_, err = validator.ValidateBearerToken(kwToken(t, oldPriv))
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, "c.pub")))

	// Removing the old key rejects old tokens.
	require.NoError(t, os.Remove(filepath.Join(dir, "a.pub")))
	require.Eventually(t, func() bool {
		_, e := validator.ValidateBearerToken(kwToken(t, oldPriv))
		return e != nil
	}, 5*time.Second, 100*time.Millisecond)
	_, err = validator.ValidateBearerToken(kwToken(t, newPriv))
	require.NoError(t, err)
}

func TestKeysetSnapshotDirIndependentOfJWT(t *testing.T) {
	jwtDir, snapDir := t.TempDir(), t.TempDir()
	jwtPub, _ := kwKey(t)
	snapPub, snapPriv := kwKey(t)
	kwWrite(t, jwtDir, "k.pub", jwtPub)
	kwWrite(t, snapDir, "k.pub", snapPub)

	validator := identity.NewTokenValidator(kwIssuer, kwAudience, jwtPub)
	verifier := snapshot.NewVerifier(jwtPub) // fallback = issuer key must be replaced
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := map[string]string{"AEGIS_JWT_KEYSET_DIR": jwtDir, "AEGIS_SNAPSHOT_KEYSET_DIR": snapDir}
	require.NoError(t, setupKeysets(ctx, kwGetenv(env), validator, verifier, nil))

	_, err := verifier.VerifySnapshot(kwEnvelope(t, snapPriv, 1), 0)
	require.NoError(t, err)
}

func TestKeysetStartupFailsClosed(t *testing.T) {
	pub, _ := kwKey(t)
	for _, name := range []string{"AEGIS_JWT_KEYSET_DIR", "AEGIS_SNAPSHOT_KEYSET_DIR"} {
		empty := t.TempDir()
		err := setupKeysets(context.Background(), kwGetenv(map[string]string{name: empty}),
			identity.NewTokenValidator(kwIssuer, kwAudience, pub), snapshot.NewVerifier(pub), nil)
		require.Error(t, err, name)
		err = setupKeysets(context.Background(), kwGetenv(map[string]string{name: filepath.Join(empty, "missing")}),
			identity.NewTokenValidator(kwIssuer, kwAudience, pub), snapshot.NewVerifier(pub), nil)
		require.Error(t, err, name)
	}
}

func TestKeysetEnvFallbackWhenNoDir(t *testing.T) {
	pub, priv := kwKey(t)
	validator := identity.NewTokenValidator(kwIssuer, kwAudience, pub)
	verifier := snapshot.NewVerifier(pub)
	require.NoError(t, setupKeysets(context.Background(), kwGetenv(nil), validator, verifier, nil))
	_, err := validator.ValidateBearerToken(kwToken(t, priv))
	require.NoError(t, err)
	_, err = verifier.VerifySnapshot(kwEnvelope(t, priv, 1), 0)
	require.NoError(t, err)
}
