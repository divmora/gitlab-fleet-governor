package config_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

const multiInstanceDoc = `
version: "v1"
settings:
  gitlab:
    timeout_seconds: 45
    rate_limit_rps: 12
targets:
  group_selector:
    group_paths_include: ["shared"]
instances:
  - name: saas
    base_url: "https://gitlab.com/api/v4"
    token: "${SAAS_TOKEN}"
    targets:
      group_selector:
        group_paths_include: ["enterprise-fleet"]
  - name: self-managed
    base_url: "https://gitlab.internal.example/api/v4"
    token: "${SM_TOKEN}"
    rate_limit_rps: 5
policies:
  push_rules:
    prevent_secrets: true
`

func multiInstanceEnv() config.EnvLookupFunc {
	env := map[string]string{"SAAS_TOKEN": "glpat-saas", "SM_TOKEN": "glpat-sm"}
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func loadMulti(t *testing.T, doc string) (*config.PolicyConfig, error) {
	t.Helper()
	cfg, _, err := config.LoadInline(context.Background(), []byte(doc), "inline:test",
		config.LoadOptions{LoaderOptions: []config.LoaderOption{config.WithEnvLookup(multiInstanceEnv())}})
	return cfg, err
}

func TestInstances_LoadAndDefaults(t *testing.T) {
	cfg, err := loadMulti(t, multiInstanceDoc)
	require.NoError(t, err)
	require.True(t, cfg.IsMultiInstance())
	require.Len(t, cfg.Instances, 2)

	saas, sm := cfg.Instances[0], cfg.Instances[1]
	assert.Equal(t, "glpat-saas", saas.Token)
	assert.Equal(t, "https://gitlab.internal.example/api/v4", sm.BaseURL)

	// Unset fields inherit settings.gitlab, then the built-in defaults.
	assert.Equal(t, 45, saas.TimeoutSeconds)
	assert.InDelta(t, 12.0, saas.RateLimitRPS, 0)
	assert.InDelta(t, 5.0, sm.RateLimitRPS, 0, "an instance's own value wins")
	assert.Equal(t, 50, sm.RateLimitBurst)
	assert.Equal(t, "private_token", sm.TokenType)
}

func TestInstances_Runs(t *testing.T) {
	cfg, err := loadMulti(t, multiInstanceDoc)
	require.NoError(t, err)

	runs := cfg.InstanceRuns()
	require.Len(t, runs, 2)

	assert.Equal(t, "saas", runs[0].Name)
	assert.Equal(t, "glpat-saas", runs[0].Config.Settings.GitLab.Token)
	assert.Equal(t, []string{"enterprise-fleet"}, runs[0].Config.Targets.GroupSelector.GroupPathsInclude)
	assert.Equal(t, config.RunContext{Instance: "saas", InstanceCount: 2}, runs[0].Config.Run)
	assert.Empty(t, runs[0].Config.Instances, "a per-instance copy is a single-instance policy")

	assert.Equal(t, "self-managed", runs[1].Name)
	assert.Equal(t, "glpat-sm", runs[1].Config.Settings.GitLab.Token)
	assert.Equal(t, []string{"shared"}, runs[1].Config.Targets.GroupSelector.GroupPathsInclude, "falls back to top-level targets")

	// Copies must not alias each other or the original.
	runs[1].Config.Settings.GitLab.Token = "changed"
	assert.Equal(t, "glpat-sm", cfg.Instances[1].Token)
	assert.Equal(t, "glpat-saas", runs[0].Config.Settings.GitLab.Token)

	single := &config.PolicyConfig{Version: "v1"}
	singleRuns := single.InstanceRuns()
	require.Len(t, singleRuns, 1)
	assert.Same(t, single, singleRuns[0].Config)
	assert.Empty(t, singleRuns[0].Name)
}

func TestInstances_Validation(t *testing.T) {
	for name, tc := range map[string]struct {
		doc   string
		field string
	}{
		"missing token": {
			doc:   "version: \"v1\"\ninstances:\n  - name: a\n    base_url: \"https://a.example/api/v4\"\n",
			field: "instances[0].token",
		},
		"unexpanded token": {
			doc:   "version: \"v1\"\ninstances:\n  - name: a\n    token: \"${NOT_SET}\"\n",
			field: "instances[0].token",
		},
		"duplicate name": {
			doc:   "version: \"v1\"\ninstances:\n  - name: a\n    token: t1\n  - name: a\n    token: t2\n",
			field: "instances[1].name",
		},
		"bad name": {
			doc:   "version: \"v1\"\ninstances:\n  - name: \"Has Spaces\"\n    token: t1\n",
			field: "instances[0].name",
		},
		"empty name": {
			doc:   "version: \"v1\"\ninstances:\n  - token: t1\n",
			field: "instances[0].name",
		},
		"shared token": {
			doc:   "version: \"v1\"\nsettings:\n  gitlab:\n    token: shared\ninstances:\n  - name: a\n    token: t1\n",
			field: "settings.gitlab.token",
		},
		"instance settings": {
			doc:   "version: \"v1\"\ninstances:\n  - name: a\n    token: t1\n    token_type: basic\n",
			field: "instances[0].token_type",
		},
		"unknown field": {
			doc:   "version: \"v1\"\ninstances:\n  - name: a\n    token: t1\n    bogus: 1\n",
			field: "bogus",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadMulti(t, tc.doc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.field)
		})
	}
}
