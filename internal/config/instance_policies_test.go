package config_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

const overridesDoc = `
version: "v1"
instances:
  - name: saas
    token: t1
  - name: onprem
    base_url: "https://gitlab.corp.example/api/v4"
    token: t2
    policies:
      push_rules:
        max_file_size: 50
      protected_branches:
        - name: release/*
      protected_tags:
        - name: "v*"
          allowed_to_create:
            - access_level: 40
      variables: null
policies:
  push_rules:
    prevent_secrets: true
    max_file_size: 10
  protected_branches:
    - name: main
    - name: develop
  variables:
    - key: TEAM
      value: platform
`

func instanceRuns(t *testing.T, cfg *config.PolicyConfig) map[string]*config.PolicyConfig {
	t.Helper()
	runs, err := cfg.InstanceRuns()
	require.NoError(t, err)
	out := map[string]*config.PolicyConfig{}
	for _, r := range runs {
		out[r.Name] = r.Config
	}
	return out
}

func TestInstancePolicies_Merge(t *testing.T) {
	cfg, err := loadMulti(t, overridesDoc)
	require.NoError(t, err)
	runs := instanceRuns(t, cfg)

	saas := runs["saas"].Policies
	assert.Equal(t, int64(10), *saas.PushRules.MaxFileSize, "no overrides: top-level policies")
	assert.Len(t, saas.ProtectedBranches, 2)
	assert.Len(t, saas.Variables, 1)
	assert.Nil(t, saas.ProtectedTags)

	onprem := runs["onprem"].Policies
	require.NotNil(t, onprem.PushRules)
	assert.Equal(t, int64(50), *onprem.PushRules.MaxFileSize, "mapping fields merge")
	assert.True(t, *onprem.PushRules.PreventSecrets, "untouched fields are kept")
	require.Len(t, onprem.ProtectedBranches, 1, "lists replace")
	assert.Equal(t, "release/*", onprem.ProtectedBranches[0].Name)
	require.NotNil(t, onprem.ProtectedTags, "list form of protected_tags works")
	assert.Equal(t, "v*", onprem.ProtectedTags.Rules[0].Name)
	assert.Empty(t, onprem.Variables, "null removes the section")

	// The top-level policies are not modified.
	assert.Equal(t, int64(10), *cfg.Policies.PushRules.MaxFileSize)
	assert.Len(t, cfg.Policies.ProtectedBranches, 2)
	assert.Len(t, cfg.Policies.Variables, 1)
}

func TestInstancePolicies_Validation(t *testing.T) {
	for name, tc := range map[string]struct{ policies, want string }{
		"unknown field":     {"push_rules:\n        bogus: true", "bogus"},
		"wrong type":        {"push_rules: [1]", "instances[0].policies"},
		"merged validation": {"variables:\n        - key: \"bad key\"\n          value: x", "instances[0].policies.variables[0].key"},
	} {
		t.Run(name, func(t *testing.T) {
			doc := "version: v1\ninstances:\n  - name: a\n    token: t\n    policies:\n      " + tc.policies + "\n"
			_, err := loadMulti(t, doc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestInstancePolicies_FromJSON(t *testing.T) {
	// Lambda decodes payload.config with encoding/json: numbers arrive as
	// float64 and nested objects as map[string]any.
	var cfg config.PolicyConfig
	require.NoError(t, json.Unmarshal([]byte(`{
		"version": "v1",
		"policies": {"push_rules": {"prevent_secrets": true}},
		"instances": [{"name": "a", "token": "t", "policies": {"push_rules": {"max_file_size": 25}}}]
	}`), &cfg))
	cfg.SetDefaults()
	runs := instanceRuns(t, &cfg)
	assert.Equal(t, int64(25), *runs["a"].Policies.PushRules.MaxFileSize)
	assert.True(t, *runs["a"].Policies.PushRules.PreventSecrets)
}

func TestUntrusted_InstancePolicyFiles(t *testing.T) {
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(local, []byte("operator-only\n"), 0o600))
	mock := &MockS3Client{Objects: map[string][]byte{"templates/CODEOWNERS": []byte("* @platform\n")}}

	doc := func(ref string) []byte {
		return []byte("version: v1\ninstances:\n  - name: a\n    token: t\n    policies:\n      repository_files:\n        - path: F\n          content_file: \"" + ref + "\"\n")
	}

	_, _, err := config.LoadInline(ctx, doc(local), "inline:test", untrustedOpts(mock))
	require.ErrorContains(t, err, "instances[0].policies.repository_files[0].content_file must be an s3:// URI")

	cfg, _, err := config.LoadInline(ctx, doc("s3://templates/CODEOWNERS"), "inline:test", untrustedOpts(mock))
	require.NoError(t, err)
	runs := instanceRuns(t, cfg)
	require.Len(t, runs["a"].Policies.RepositoryFiles, 1)
	assert.Equal(t, "* @platform\n", runs["a"].Policies.RepositoryFiles[0].Content)
	assert.Empty(t, runs["a"].Policies.RepositoryFiles[0].ContentFile)

	// A parsed event config (Lambda payload.config) gets the same check.
	var parsed config.PolicyConfig
	require.NoError(t, json.Unmarshal([]byte(`{"version":"v1","instances":[{"name":"a","token":"t","policies":{"repository_files":[{"path":"F","content_file":"/proc/self/environ"}]}}]}`), &parsed))
	opts := untrustedOpts(mock)
	require.ErrorContains(t, config.SanitizeUntrusted(ctx, &parsed, opts.Untrusted, opts), "instances[0].policies.repository_files[0].content_file")
}
