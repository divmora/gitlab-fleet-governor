package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFleetLicenseGen_InitKeys(t *testing.T) {
	tempDir := t.TempDir()

	cmd := newInitKeysCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--out-dir=" + tempDir})

	err := cmd.Execute()
	require.NoError(t, err)

	pubFile := filepath.Join(tempDir, "divmora-public.key")
	privFile := filepath.Join(tempDir, "divmora-private.key")

	assert.FileExists(t, pubFile)
	assert.FileExists(t, privFile)

	pubBytes, err := os.ReadFile(pubFile)
	require.NoError(t, err)
	assert.NotEmpty(t, strings.TrimSpace(string(pubBytes)))
}

func TestFleetLicenseGen_IssueAndInspect(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	privB64 := base64.StdEncoding.EncodeToString(priv)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	t.Setenv("DIVMORA_PUBLIC_KEY", pubB64)

	// Issue token
	issueCmd := newIssueCmd()
	var issueBuf bytes.Buffer
	issueCmd.SetOut(&issueBuf)
	issueCmd.SetArgs([]string{
		"--customer=Acme Test Corp",
		"--email=admin@acme.com",
		"--tier=enterprise",
		"--projects=200",
		"--valid-days=365",
		"--token-only",
		"--private-key=" + privB64,
	})

	err = issueCmd.Execute()
	require.NoError(t, err)
	token := strings.TrimSpace(issueBuf.String())
	assert.NotEmpty(t, token)
	assert.Contains(t, token, ".")

	// Inspect token
	inspectCmd := newInspectCmd()
	var inspectBuf bytes.Buffer
	inspectCmd.SetOut(&inspectBuf)
	inspectCmd.SetArgs([]string{
		"--license-key=" + token,
		"--public-key=" + pubB64,
	})

	err = inspectCmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, inspectBuf.String(), "Acme Test Corp")
	assert.Contains(t, strings.ToLower(inspectBuf.String()), "enterprise")
	assert.Contains(t, inspectBuf.String(), "200 Managed Projects")
}

func TestFleetLicenseGen_SignReleaseAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	privB64 := base64.StdEncoding.EncodeToString(priv)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	tempDir := t.TempDir()
	sigPath := filepath.Join(tempDir, "release.sig")

	// Sign release
	signCmd := newSignReleaseCmd()
	var signBuf bytes.Buffer
	signCmd.SetOut(&signBuf)
	signCmd.SetArgs([]string{
		"--version=0.5.0",
		"--commit=4b825dc642cb",
		"--build-date=2026-09-11T12:00:00Z",
		"--authority=DIVMORA Technologies Release Authority",
		"--authority-id=divmora-test-1",
		"--private-key=" + privB64,
		"--out-file=" + sigPath,
	})

	err = signCmd.Execute()
	require.NoError(t, err)
	assert.FileExists(t, sigPath)
	assert.Contains(t, signBuf.String(), "Cryptographic Release Attestation")
	assert.Contains(t, signBuf.String(), "0.5.0")
	assert.Contains(t, signBuf.String(), "4b825dc642cb")

	// Verify release via token-file
	verifyCmd := newVerifyReleaseCmd()
	var verifyBuf bytes.Buffer
	verifyCmd.SetOut(&verifyBuf)
	verifyCmd.SetArgs([]string{
		"--token-file=" + sigPath,
		"--public-key=" + pubB64,
	})

	err = verifyCmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, verifyBuf.String(), "VALID & VERIFIED")
	assert.Contains(t, verifyBuf.String(), "0.5.0")
	assert.Contains(t, verifyBuf.String(), "4b825dc642cb")
	assert.Contains(t, verifyBuf.String(), "DIVMORA Technologies Release Authority")
}
