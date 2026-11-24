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
        - name: main
          allowed_to_merge:
            - access_level: 40
        - name: develop
          $remove: true
        - name: hotfix/*
      protected_tags:
        - name: "v*"
          allowed_to_create:
            - access_level: 30
      variables:
        - key: TEAM
          value: onprem
        - key: REGION
          value: eu
      webhooks:
        $replace:
          - url: https://hooks.corp.example/gitlab
      branch_merges: null
policies:
  push_rules:
    prevent_secrets: true
    max_file_size: 10
  protected_branches:
    - name: main
      allowed_to_merge:
        - access_level: 30
      code_owner_approval_required: true
    - name: develop
  protected_tags:
    prune: true
    rules:
      - name: "v*"
        allowed_to_create:
          - access_level: 40
      - name: "release-*"
        allowed_to_create:
          - access_level: 40
  variables:
    - key: TEAM
      value: platform
  webhooks:
    - url: https://hooks.example/a
    - url: https://hooks.example/b
  branch_merges:
    - source_branch: main
      target_branch: develop
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

func branchNames(rules []config.ProtectedBranchRuleConfig) []string {
	names := make([]string, len(rules))
	for i, r := range rules {
		names[i] = r.Name
	}
	return names
}

func TestInstancePolicies_Merge(t *testing.T) {
	cfg, err := loadMulti(t, overridesDoc)
	require.NoError(t, err)
	runs := instanceRuns(t, cfg)

	saas := runs["saas"].Policies
	assert.Equal(t, int64(10), *saas.PushRules.MaxFileSize, "no overrides: top-level policies")
	assert.Equal(t, []string{"main", "develop"}, branchNames(saas.ProtectedBranches))

	on := runs["onprem"].Policies
	// Mappings merge field by field.
	assert.Equal(t, int64(50), *on.PushRules.MaxFileSize)
	assert.True(t, *on.PushRules.PreventSecrets)

	// Keyed lists: matched items merge, $remove drops, new names append.
	assert.Equal(t, []string{"main", "hotfix/*"}, branchNames(on.ProtectedBranches))
	main := on.ProtectedBranches[0]
	require.Len(t, main.AllowedToMerge, 1, "lists inside an item replace")
	assert.Equal(t, 40, main.AllowedToMerge[0].AccessLevel)
	require.NotNil(t, main.CodeOwnerApprovalRequired, "fields the override doesn't mention are kept")
	assert.True(t, *main.CodeOwnerApprovalRequired)

	// List form of protected_tags merges into the top-level rules; prune is kept.
	require.NotNil(t, on.ProtectedTags)
	assert.True(t, *on.ProtectedTags.Prune)
	require.Len(t, on.ProtectedTags.Rules, 2)
	assert.Equal(t, "v*", on.ProtectedTags.Rules[0].Name)
	assert.Equal(t, 30, on.ProtectedTags.Rules[0].AllowedToCreate[0].AccessLevel)
	assert.Equal(t, "release-*", on.ProtectedTags.Rules[1].Name)

	// Variables match on key and environment scope.
	require.Len(t, on.Variables, 2)
	assert.Equal(t, "TEAM", on.Variables[0].Key)
	assert.Equal(t, "onprem", on.Variables[0].Value)
	assert.Equal(t, "REGION", on.Variables[1].Key)

	// $replace replaces the list; null removes the section.
	require.Len(t, on.Webhooks, 1)
	assert.Equal(t, "https://hooks.corp.example/gitlab", on.Webhooks[0].URL)
	assert.Empty(t, on.BranchMerges)

	// The top-level policies are not modified.
	assert.Equal(t, int64(10), *cfg.Policies.PushRules.MaxFileSize)
	assert.Equal(t, []string{"main", "develop"}, branchNames(cfg.Policies.ProtectedBranches))
	assert.Equal(t, 30, cfg.Policies.ProtectedBranches[0].AllowedToMerge[0].AccessLevel)
	assert.Len(t, cfg.Policies.Webhooks, 2)
	assert.Len(t, cfg.Policies.BranchMerges, 1)
}

func TestMergePolicies_KeyedListRules(t *testing.T) {
	base := config.PoliciesConfig{
		Variables: []config.VariableConfig{
			{Key: "A", Value: "all"},
			{Key: "A", Value: "prod", EnvironmentScope: "production"},
		},
		BranchMerges: []config.BranchMergeRuleConfig{{SourceBranch: "main", TargetBranch: "develop"}},
	}

	merged, err := config.MergePolicies(base, map[string]any{
		"variables": []any{
			map[string]any{"key": "A", "environment_scope": "production", "value": "prod2"},
			map[string]any{"key": "A", "environment_scope": "*", "$remove": true},
		},
		"branch_merges": []any{map[string]any{"source_branch": "main", "target_branch": "release"}},
	})
	require.NoError(t, err)
	require.Len(t, merged.Variables, 1, "an omitted environment_scope matches \"*\"")
	assert.Equal(t, "prod2", merged.Variables[0].Value)
	assert.Len(t, merged.BranchMerges, 2, "composite key: a different target is a new item")
	assert.Equal(t, "all", base.Variables[0].Value, "base is not modified")

	for name, tc := range map[string]struct {
		override map[string]any
		want     string
	}{
		"missing key": {
			map[string]any{"protected_branches": []any{map[string]any{"allowed_to_merge": []any{}}}},
			"protected_branches[0]: name is required",
		},
		"duplicate item": {
			map[string]any{"protected_branches": []any{map[string]any{"name": "x"}, map[string]any{"name": "x"}}},
			"protected_branches[1]: duplicate item name=x",
		},
		"remove unknown": {
			map[string]any{"protected_branches": []any{map[string]any{"name": "nope", "$remove": true}}},
			"cannot remove name=nope",
		},
		"remove not true": {
			map[string]any{"variables": []any{map[string]any{"key": "A", "$remove": "yes"}}},
			"$remove must be true",
		},
		"replace with other fields": {
			map[string]any{"webhooks": map[string]any{"$replace": []any{}, "url": "x"}},
			"$replace must be the only field",
		},
		"replace not a list": {
			map[string]any{"webhooks": map[string]any{"$replace": "x"}},
			"$replace takes a list",
		},
		"marker on unkeyed list": {
			map[string]any{"members": map[string]any{"denied_members": []any{map[string]any{"$remove": true}}}},
			"$remove is only allowed",
		},
		"marker on mapping": {
			map[string]any{"push_rules": map[string]any{"$remove": true}},
			"push_rules.$remove",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.MergePolicies(base, tc.override)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestInstancePolicies_Validation(t *testing.T) {
	for name, tc := range map[string]struct{ policies, want string }{
		"unknown field":     {"push_rules:\n        bogus: true", "bogus"},
		"wrong type":        {"push_rules: [1]", "instances[0].policies"},
		"merged validation": {"variables:\n        - key: \"bad key\"\n          value: x", "instances[0].policies.variables[0].key"},
		"bad marker":        {"protected_branches:\n        - name: gone\n          $remove: true", "cannot remove name=gone"},
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

	// The $replace form is checked too.
	replaced := []byte("version: v1\ninstances:\n  - name: a\n    token: t\n    policies:\n      repository_files:\n        $replace:\n          - path: F\n            content_file: \"" + local + "\"\n")
	_, _, err = config.LoadInline(ctx, replaced, "inline:test", untrustedOpts(mock))
	require.ErrorContains(t, err, "instances[0].policies.repository_files[0].content_file must be an s3:// URI")

	// A parsed event config (Lambda payload.config) gets the same check.
	var parsed config.PolicyConfig
	require.NoError(t, json.Unmarshal([]byte(`{"version":"v1","instances":[{"name":"a","token":"t","policies":{"repository_files":[{"path":"F","content_file":"/proc/self/environ"}]}}]}`), &parsed))
	opts := untrustedOpts(mock)
	require.ErrorContains(t, config.SanitizeUntrusted(ctx, &parsed, opts.Untrusted, opts), "instances[0].policies.repository_files[0].content_file")
}
