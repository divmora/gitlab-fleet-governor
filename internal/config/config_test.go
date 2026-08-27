package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func TestLoad_EndToEnd(t *testing.T) {
	ctx := context.Background()

	rawYAML := `
version: "v1"
settings:
  concurrency: ${FLEET_CONCURRENCY:-25}
  log_level: ${FLEET_LOG_LEVEL:-debug}
  gitlab:
    token: ${GITLAB_AUTH_TOKEN:-default_token_val}
policies:
  pipeline_retention:
    retention_days: 14
`

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "governance.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte(rawYAML), 0644))

	t.Run("Load with env substitutions and defaults", func(t *testing.T) {
		cfg, desc, err := config.Load(ctx, filePath)
		require.NoError(t, err)
		assert.Equal(t, filePath, desc)
		assert.Equal(t, 25, cfg.Settings.Concurrency)
		assert.Equal(t, "debug", cfg.Settings.LogLevel)
		assert.Equal(t, "default_token_val", cfg.Settings.GitLab.Token)
		require.NotNil(t, cfg.Settings.DryRun)
		assert.True(t, *cfg.Settings.DryRun) // Default applied
		require.NotNil(t, cfg.Policies.PipelineRetention)
		assert.Equal(t, 14, cfg.Policies.PipelineRetention.RetentionDays)
		assert.Equal(t, 14*86400, cfg.Policies.PipelineRetention.Seconds())
	})

	t.Run("LoadFromFile helper", func(t *testing.T) {
		cfg, err := config.LoadFromFile(ctx, filePath)
		require.NoError(t, err)
		assert.Equal(t, 25, cfg.Settings.Concurrency)
	})

	t.Run("LoadFromBytes helper", func(t *testing.T) {
		customEnv := map[string]string{
			"FLEET_CONCURRENCY": "50",
			"FLEET_LOG_LEVEL":   "warn",
		}
		lookup := func(k string) (string, bool) {
			val, ok := customEnv[k]
			return val, ok
		}

		cfg, err := config.LoadFromBytes(ctx, []byte(rawYAML), lookup)
		require.NoError(t, err)
		assert.Equal(t, 50, cfg.Settings.Concurrency)
		assert.Equal(t, "warn", cfg.Settings.LogLevel)
	})

	t.Run("Load with validation failure", func(t *testing.T) {
		invalidYAML := `
version: "v1"
policies:
  pipeline_retention:
    retention_days: -5
`
		invalidFile := filepath.Join(tmpDir, "invalid.yaml")
		require.NoError(t, os.WriteFile(invalidFile, []byte(invalidYAML), 0644))

		_, _, err := config.Load(ctx, invalidFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config validation error")
	})

	t.Run("Load with SkipValidate", func(t *testing.T) {
		invalidYAML := `
version: "v1"
policies:
  pipeline_retention:
    retention_days: -5
`
		invalidFile := filepath.Join(tmpDir, "invalid_skip.yaml")
		require.NoError(t, os.WriteFile(invalidFile, []byte(invalidYAML), 0644))

		cfg, _, err := config.Load(ctx, invalidFile, config.LoadOptions{SkipValidate: true})
		require.NoError(t, err)
		assert.Equal(t, -5, cfg.Policies.PipelineRetention.RetentionDays)
	})
}
