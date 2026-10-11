// Package keyfile is the single parser for Ed25519 key material mounted from
// ConfigMaps and Secrets. It never falls back to a default key: any parse
// failure is returned as an error, and errors never include key bytes.
package keyfile

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ParseEd25519Public parses a base64 raw 32 byte key or a PEM PKIX
// "PUBLIC KEY" block.
func ParseEd25519Public(data []byte) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, errors.New("empty public key")
	}
	if block, _ := pem.Decode([]byte(trimmed)); block != nil {
		if block.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("unexpected PEM block type %q for public key", block.Type)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKIX public key: %w", err)
		}
		pub, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("PKIX public key is not Ed25519")
		}
		return pub, nil
	}
	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, errors.New("public key is neither PEM nor valid base64")
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key has %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// LoadEd25519Public reads and parses a public key file.
func LoadEd25519Public(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key %s: %w", path, err)
	}
	k, err := ParseEd25519Public(data)
	if err != nil {
		return nil, fmt.Errorf("invalid public key %s: %w", path, err)
	}
	return k, nil
}

// ParseEd25519Private parses a PEM PKCS8 key, raw 64 bytes, base64 of 64
// bytes, or base64 of a 32 byte seed.
func ParseEd25519Private(data []byte) (ed25519.PrivateKey, error) {
	if block, _ := pem.Decode(data); block != nil {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err == nil {
			priv, ok := parsed.(ed25519.PrivateKey)
			if !ok {
				return nil, errors.New("PKCS8 private key is not Ed25519")
			}
			return priv, nil
		}
		return fromRaw(block.Bytes, "PEM block")
	}
	if len(data) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(append([]byte(nil), data...)), nil
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, errors.New("empty private key")
	}
	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, errors.New("private key is not PEM, raw, or valid base64")
	}
	return fromRaw(raw, "base64 payload")
}

func fromRaw(raw []byte, what string) (ed25519.PrivateKey, error) {
	switch len(raw) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(append([]byte(nil), raw...)), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	default:
		return nil, fmt.Errorf("%s has %d bytes, want %d or %d", what, len(raw), ed25519.PrivateKeySize, ed25519.SeedSize)
	}
}

// LoadEd25519Private reads and parses a private key file.
func LoadEd25519Private(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key %s: %w", path, err)
	}
	k, err := ParseEd25519Private(data)
	if err != nil {
		return nil, fmt.Errorf("invalid private key %s: %w", path, err)
	}
	return k, nil
}
