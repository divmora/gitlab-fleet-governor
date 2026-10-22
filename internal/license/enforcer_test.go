package license_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/license"
)

func TestResolveToken_KeyDirect(t *testing.T) {
	token, err := license.ResolveToken("direct-token-123", "")
	require.NoError(t, err)
	assert.Equal(t, "direct-token-123", token)
}

func TestResolveToken_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	licFile := filepath.Join(tmpDir, "test.lic")
	err := os.WriteFile(licFile, []byte("  file-token-456\n"), 0600)
	require.NoError(t, err)

	token, err := license.ResolveToken("", licFile)
	require.NoError(t, err)
	assert.Equal(t, "file-token-456", token)
}

func TestResolveToken_NullByteInPath(t *testing.T) {
	_, err := license.ResolveToken("", "test\x00.lic")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "null byte")
}

func TestResolveToken_NonExistentFile(t *testing.T) {
	_, err := license.ResolveToken("", "/nonexistent/path/to/license.lic")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open license file")
}

func TestResolveToken_DirectoryInsteadOfFile(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := license.ResolveToken("", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a regular file")
}

func TestResolveToken_OversizedFile(t *testing.T) {
	tmpDir := t.TempDir()
	largeFile := filepath.Join(tmpDir, "large.lic")
	// Write 65 KB (exceeds 64 KB limit)
	data := make([]byte, 65*1024)
	err := os.WriteFile(largeFile, data, 0600)
	require.NoError(t, err)

	_, err = license.ResolveToken("", largeFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum allowed size")
}

func TestResolveCRL_NullByte(t *testing.T) {
	_, err := license.ResolveCRL("path/with/\x00/null")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "null byte")
}

func TestResolveCRL_Empty(t *testing.T) {
	crl, err := license.ResolveCRL("")
	require.NoError(t, err)
	assert.Empty(t, crl)
}
