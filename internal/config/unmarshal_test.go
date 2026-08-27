package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func TestUnmarshalStrict_ValidYAMLAndJSON(t *testing.T) {
	yamlData := []byte(`
version: "v1"
settings:
  concurrency: 12
  log_level: "debug"
`)

	jsonData := []byte(`{
  "version": "v1",
  "settings": {
    "concurrency": 12,
    "log_level": "debug"
  }
}`)

	var cfgYAML, cfgJSON config.PolicyConfig
	err := config.UnmarshalStrict(yamlData, &cfgYAML)
	require.NoError(t, err)

	err = config.UnmarshalStrict(jsonData, &cfgJSON)
	require.NoError(t, err)

	assert.Equal(t, "v1", cfgYAML.Version)
	assert.Equal(t, 12, cfgYAML.Settings.Concurrency)
	assert.Equal(t, "debug", cfgYAML.Settings.LogLevel)
	assert.Equal(t, cfgYAML, cfgJSON)
}

func TestUnmarshalStrict_RejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "Unknown top-level field",
			data: `
version: "v1"
invalid_root_key: "disallowed"
`,
		},
		{
			name: "Misspelled settings field (dryrun instead of dry_run)",
			data: `
version: "v1"
settings:
  dryrun: true
`,
		},
		{
			name: "Unknown push_rules field",
			data: `
version: "v1"
policies:
  push_rules:
    disallow_force_push_typo: true
`,
		},
		{
			name: "Unknown field in JSON format",
			data: `{"version": "v1", "unexpected": true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg config.PolicyConfig
			err := config.UnmarshalStrict([]byte(tt.data), &cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "configuration syntax or schema error")
		})
	}
}

func TestUnmarshalStrict_EmptyData(t *testing.T) {
	var cfg config.PolicyConfig
	err := config.UnmarshalStrict([]byte("   \n\t  "), &cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty configuration content")
}

func TestUnmarshalStrict_TrailingData(t *testing.T) {
	multiDoc := []byte(`
version: "v1"
---
version: "v2"
`)
	var cfg config.PolicyConfig
	err := config.UnmarshalStrict(multiDoc, &cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected extra data")
}
