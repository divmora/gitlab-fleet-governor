package config

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// SSHKeyFingerprint returns the OpenSSH SHA256 fingerprint
// ("SHA256:<base64, no padding>") of an authorized_keys-style public key.
func SSHKeyFingerprint(publicKey string) (string, error) {
	fields := strings.Fields(publicKey)
	if len(fields) < 2 {
		return "", fmt.Errorf("must be an SSH public key such as \"ssh-ed25519 AAAA... comment\"")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(blob) < 4 {
		return "", fmt.Errorf("must be an SSH public key: the key data is not valid base64")
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}
