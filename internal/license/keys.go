package license

import (
	"crypto/ed25519"

	liblicense "github.com/divmora/license-go/pkg/license"
)

// DefaultPublicKeyBase64 is the embedded production Ed25519 public verification key for DIVMORA Technologies.
const DefaultPublicKeyBase64 = "o5nIs/8K/bCGz6jRB33Ig1h0ONr37yvVHpddzNnL46U="

// SetVerificationPublicKey overrides the active verification key (primarily used in tests).
func SetVerificationPublicKey(key ed25519.PublicKey) {
	liblicense.SetVerificationPublicKey(key)
}

// ResetVerificationPublicKey clears any programmatic override and returns to default resolution.
func ResetVerificationPublicKey() {
	liblicense.ResetVerificationPublicKey()
}

// GetVerificationKeyRing resolves the KeyRing containing trusted public verification keys.
// Resolution order:
// 1. In-memory programmatic override (via SetVerificationPublicKey or SetVerificationKeyRing).
// 2. DIVMORA_PUBLIC_KEYS_PEM environment variable (multi-key PKIX PEM bundle).
// 3. DIVMORA_PUBLIC_KEY environment variable (base64-encoded single key or PEM).
// 4. Embedded DefaultPublicKeyBase64.
func GetVerificationKeyRing() (*liblicense.KeyRing, error) {
	resolved, err := liblicense.ResolveKeyRingWithEnvPrecedence(DefaultPublicKeyBase64)
	if err != nil {
		return nil, err
	}
	return resolved.KeyRing, nil
}

// GetVerificationPublicKey resolves the primary Ed25519 public key used to verify license tokens.
func GetVerificationPublicKey() (ed25519.PublicKey, error) {
	ring, err := GetVerificationKeyRing()
	if err != nil {
		return nil, err
	}
	if ring.Primary() == nil {
		return nil, liblicense.ErrMissingPublicKey
	}
	return ring.Primary().PublicKey, nil
}
