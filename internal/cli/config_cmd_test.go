package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const renderPolicy = `
version: "v1"
instances:
  - name: saas
    token: glpat-saas-secret
  - name: onprem
    base_url: "https://gitlab.corp.example/api/v4"
    token: glpat-onprem-secret
    policies:
      push_rules:
        max_file_size: 50
      protected_branches:
        - name: develop
          $remove: true
policies:
  push_rules: { prevent_secrets: true, max_file_size: 10 }
  protected_branches:
    - name: main
    - name: develop
  variables:
    - { key: API_KEY, value: masked-secret, masked: true }
    - { key: REGION, value: eu-west-1 }
  webhooks:
    - { url: "https://hooks.example/a", secret_token: hook-secret }
`

func writePolicy(t *testing.T, doc string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.yaml")
	require.NoError(t, os.WriteFile(p, []byte(doc), 0o600))
	return p
}

func TestConfigRender(t *testing.T) {
	ctx := context.Background()
	path := writePolicy(t, renderPolicy)

	out, _, err := executeCommand(ctx, "config", "render", "-c", path)
	require.NoError(t, err)
	assert.Contains(t, out, "instance: saas")
	assert.Contains(t, out, "instance: onprem")
	assert.Contains(t, out, "max_file_size: 50")
	assert.Contains(t, out, "eu-west-1", "unmasked values are shown")
	for _, secret := range []string{"glpat-saas-secret", "glpat-onprem-secret", "masked-secret", "hook-secret"} {
		assert.NotContains(t, out, secret)
	}

	out, _, err = executeCommand(ctx, "config", "render", "-c", path, "--instance", "onprem", "--format", "json")
	require.NoError(t, err)
	var doc struct {
		Instance string `json:"instance"`
		Settings struct {
			GitLab struct {
				BaseURL string `json:"base_url"`
				Token   string `json:"token"`
			} `json:"gitlab"`
		} `json:"settings"`
		Policies struct {
			ProtectedBranches []struct {
				Name string `json:"name"`
			} `json:"protected_branches"`
		} `json:"policies"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &doc), out)
	assert.Equal(t, "onprem", doc.Instance)
	assert.Equal(t, "https://gitlab.corp.example/api/v4", doc.Settings.GitLab.BaseURL)
	assert.Equal(t, redacted, doc.Settings.GitLab.Token)
	require.Len(t, doc.Policies.ProtectedBranches, 1)
	assert.Equal(t, "main", doc.Policies.ProtectedBranches[0].Name)

	_, _, err = executeCommand(ctx, "config", "render", "-c", path, "--instance", "nope")
	require.ErrorContains(t, err, `no such instance (have saas, onprem)`)

	_, _, err = executeCommand(ctx, "config", "render", "-c", path, "--format", "xml")
	require.ErrorContains(t, err, "invalid --format")
}

func TestConfigRender_SingleInstance(t *testing.T) {
	ctx := context.Background()
	path := writePolicy(t, "version: v1\nsettings:\n  gitlab:\n    token: glpat-single\npolicies:\n  push_rules: { prevent_secrets: true }\n")

	out, _, err := executeCommand(ctx, "config", "render", "-c", path)
	require.NoError(t, err)
	assert.NotContains(t, out, "instance:")
	assert.NotContains(t, out, "glpat-single")
	assert.Contains(t, out, "prevent_secrets: true")

	_, _, err = executeCommand(ctx, "config", "render", "-c", path, "--instance", "a")
	require.ErrorContains(t, err, "has no instances block")
}

func TestConfigRender_InvalidOverride(t *testing.T) {
	path := writePolicy(t, "version: v1\ninstances:\n  - name: a\n    token: t\n    policies:\n      protected_branches:\n        - name: gone\n          $remove: true\n")
	_, _, err := executeCommand(context.Background(), "config", "render", "-c", path)
	require.ErrorContains(t, err, "cannot remove name=gone")
}
