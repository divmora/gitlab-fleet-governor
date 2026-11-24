package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCommand(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	out := filepath.Join(dir, "policy.yaml")

	list, _, err := executeCommand(ctx, "init", "--list")
	require.NoError(t, err)
	for _, name := range []string{"soc2", "iso-27001", "pci-dss", "hipaa", "nist-800-53", "fedramp"} {
		assert.Contains(t, list, name)
	}

	_, stderr, err := executeCommand(ctx, "init", "--template", "soc2", "--group", "acme/platform", "--email-domain", "acme.com", "--reviewer-group", "acme/security", "-o", out)
	require.NoError(t, err)
	assert.Contains(t, stderr, "not a certification")
	assert.NotContains(t, stderr, "Still to fill in")
	body, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(body), `group_paths_include: ["acme/platform"]`)

	// The written file is a valid policy for the other commands.
	_, _, err = executeCommand(ctx, "validate", "-c", out)
	require.NoError(t, err)

	_, _, err = executeCommand(ctx, "init", "--template", "soc2", "-o", out)
	require.ErrorContains(t, err, "already exists; pass --force")
	_, stderr, err = executeCommand(ctx, "init", "--template", "hipaa", "--force", "-o", out)
	require.NoError(t, err)
	assert.Contains(t, stderr, "Still to fill in: REPLACE_WITH_EMAIL_DOMAIN, REPLACE_WITH_GROUP_PATH, REPLACE_WITH_REVIEWER_GROUP")

	stdout, _, err := executeCommand(ctx, "init", "--template", "fedramp", "-o", "-")
	require.NoError(t, err)
	assert.Contains(t, stdout, "forking_access_level: disabled")

	readme, _, err := executeCommand(ctx, "init", "--template", "pci-dss", "--readme")
	require.NoError(t, err)
	assert.Contains(t, readme, "8.4.2")

	_, _, err = executeCommand(ctx, "init")
	require.ErrorContains(t, err, "--template is required")
	_, _, err = executeCommand(ctx, "init", "--template", "gdpr")
	require.ErrorContains(t, err, "unknown template")
	_, _, err = executeCommand(ctx, "init", "--template", "soc2", "--group", "acme\"]", "-o", "-")
	require.ErrorContains(t, err, "is not a GitLab group path")
}
