package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to execute commands with buffer capture.
func executeCommand(ctx context.Context, args ...string) (string, string, error) {
	cmd := NewRootCmd()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func TestRootCommand_Help(t *testing.T) {
	ctx := context.Background()
	stdout, _, err := executeCommand(ctx, "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "gitlab-fleet-governor")
	assert.Contains(t, stdout, "run")
	assert.Contains(t, stdout, "validate")
	assert.Contains(t, stdout, "version")
	assert.Contains(t, stdout, "lambda")
}

func TestVersionCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("Standard Text Output", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "version")
		require.NoError(t, err)
		assert.Contains(t, stdout, "gitlab-fleet-governor")
		assert.Contains(t, stdout, "commit:")
	})

	t.Run("Short Output", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "version", "--short")
		require.NoError(t, err)
		assert.Equal(t, version.Version, strings.TrimSpace(stdout))
	})

	t.Run("JSON Output", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "version", "--json")
		require.NoError(t, err)

		var info version.Info
		err = json.Unmarshal([]byte(stdout), &info)
		require.NoError(t, err)
		assert.NotEmpty(t, info.Version)
		assert.NotEmpty(t, info.GoVersion)
		assert.NotEmpty(t, info.Platform)
	})
}

func TestValidateCommand(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	validYAML := `
settings:
  concurrency: 5
  dry_run: true
  gitlab:
    base_url: "https://gitlab.example.com"
targets:
  group_selector:
    group_ids_include: [100, 200]
policies:
  push_rules:
    author_email_regex: "@company\\.com$"
`
	validFile := filepath.Join(tempDir, "valid.yaml")
	require.NoError(t, os.WriteFile(validFile, []byte(validYAML), 0600))

	invalidYAML := `
settings:
  concurrency: -5
targets:
  project_selector:
    project_name_regex_include: "[unclosed_regex"
`
	invalidFile := filepath.Join(tempDir, "invalid.yaml")
	require.NoError(t, os.WriteFile(invalidFile, []byte(invalidYAML), 0600))

	t.Run("Valid Config Standard Output", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "validate", "-c", validFile)
		require.NoError(t, err)
		assert.Contains(t, stdout, "valid")
	})

	t.Run("Valid Config Quiet Mode", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "validate", "-c", validFile, "--quiet")
		require.NoError(t, err)
		assert.Empty(t, stdout)
	})

	t.Run("Valid Config JSON Output", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "validate", "-c", validFile, "--json")
		require.NoError(t, err)

		var out ValidateJSONOutput
		err = json.Unmarshal([]byte(stdout), &out)
		require.NoError(t, err)
		assert.True(t, out.Valid)
		assert.Equal(t, "VALID", out.Status)
		assert.Empty(t, out.Errors)
	})

	t.Run("Invalid Config Error Reporting", func(t *testing.T) {
		_, _, err := executeCommand(ctx, "validate", "-c", invalidFile)
		require.Error(t, err)
	})

	t.Run("Invalid Config JSON Output", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "validate", "-c", invalidFile, "--json")
		require.Error(t, err)

		var out ValidateJSONOutput
		jsonErr := json.Unmarshal([]byte(stdout), &out)
		require.NoError(t, jsonErr)
		assert.False(t, out.Valid)
		assert.Equal(t, "INVALID", out.Status)
		assert.NotEmpty(t, out.Errors)
	})
}

func TestValidateCommand_JSON_MinimalConfig(t *testing.T) {
	ctx := context.Background()

	// Locate examples/minimal.yaml across different test working directories
	configPath := filepath.Join("..", "..", "examples", "minimal.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configPath = filepath.Join("examples", "minimal.yaml")
	}

	stdout, stderr, err := executeCommand(ctx, "validate", "-c", configPath, "--json")
	require.NoError(t, err, "stderr: %s", stderr)

	var out ValidateJSONOutput
	err = json.Unmarshal([]byte(stdout), &out)
	require.NoError(t, err)

	assert.True(t, out.Valid)
	assert.Equal(t, "VALID", out.Status)
	assert.Empty(t, out.Errors)

	require.NotNil(t, out.Targets)
	assert.Equal(t, 0, out.Targets.GroupIDsIncluded)
	assert.Equal(t, 1, out.Targets.GroupPathsIncluded)
	assert.True(t, out.Targets.HasProjectSelectors)

	require.NotNil(t, out.Policies)
	assert.True(t, out.Policies.PushRules)
	assert.Equal(t, 1, out.Policies.ProtectedBranches)
	assert.True(t, out.Policies.PipelineRetention)
	assert.Equal(t, 0, out.Policies.ApprovalRules)
	assert.Equal(t, 0, out.Policies.Runners)
	assert.Equal(t, 0, out.Policies.Members)
	assert.False(t, out.Policies.ProjectSettings)
	assert.False(t, out.Policies.Compliance)
}

func TestLambdaCommand(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	eventPayload := `{
  "action": "dry_run",
  "config": {
    "settings": {
      "concurrency": 2,
      "dry_run": true,
      "gitlab": {
        "token": "glpat-mock-token-12345",
        "base_url": "https://gitlab.example.com"
      }
    },
    "targets": {
      "group_selector": {
        "group_ids_include": [42]
      }
    }
  }
}`
	eventFile := filepath.Join(tempDir, "direct_event.json")
	require.NoError(t, os.WriteFile(eventFile, []byte(eventPayload), 0600))

	t.Run("Direct Event Execution", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "lambda", "--event", eventFile)
		require.NoError(t, err)
		assert.Contains(t, stdout, "DIRECT_INVOCATION")
	})

	t.Run("Missing Event Flag", func(t *testing.T) {
		_, _, err := executeCommand(ctx, "lambda")
		require.Error(t, err)
	})
}

func TestGlobalFlagsValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("Invalid Log Level", func(t *testing.T) {
		_, _, err := executeCommand(ctx, "version", "--log-level", "super_verbose")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid log level")
	})

	t.Run("Invalid Report Format", func(t *testing.T) {
		_, _, err := executeCommand(ctx, "version", "--report-format", "pdf")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported report format")
	})

	t.Run("Invalid Concurrency", func(t *testing.T) {
		_, _, err := executeCommand(ctx, "version", "--concurrency", "0")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "concurrency must be at least 1")
	})
}

func TestRunCommand_MissingConfig(t *testing.T) {
	ctx := context.Background()
	_, _, err := executeCommand(ctx, "run", "-c", "non-existent-config-file.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load configuration")
}
