package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandsRejectMultiInstancePolicies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: v1\ninstances:\n  - name: a\n    token: t1\n  - name: b\n    base_url: https://b.example/api/v4\n    token: t2\n"), 0o600))

	for _, args := range [][]string{
		{"audit", "--config", path},
		{"export", "--config", path, "--project-id", "1"},
	} {
		_, _, err := executeCommand(context.Background(), args...)
		require.Error(t, err, args)
		assert.Contains(t, err.Error(), args[0]+" does not support policies with an instances block yet")
	}
}
