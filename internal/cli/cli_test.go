package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
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
	assert.Contains(t, stdout, "audit")
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
		assert.NotEmpty(t, info.Provenance.Status)
	})

	t.Run("Verify Unattested Custom Build", func(t *testing.T) {
		origSig := version.ReleaseSignature
		version.ReleaseSignature = "none"
		defer func() { version.ReleaseSignature = origSig }()

		stdout, _, err := executeCommand(ctx, "version", "--verify")
		require.Error(t, err)
		assert.Contains(t, stdout, "UNVERIFIED")
		assert.Contains(t, stdout, "UNATTESTED_CUSTOM_BUILD")
	})

	t.Run("Verify Cryptographically Signed Official Release", func(t *testing.T) {
		pubRel, privRel, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		version.SetReleaseVerificationPublicKey(pubRel)
		defer version.ResetReleaseVerificationPublicKey()

		origSig := version.ReleaseSignature
		origVer := version.Version
		origCommit := version.GitCommit
		origDate := version.BuildDate
		defer func() {
			version.ReleaseSignature = origSig
			version.Version = origVer
			version.GitCommit = origCommit
			version.BuildDate = origDate
		}()

		version.Version = "0.5.0"
		version.GitCommit = "4b825dc642cb"
		version.BuildDate = "2026-09-11T12:00:00Z"

		token, err := version.SignRelease(&version.ReleaseClaims{
			Version:   "0.5.0",
			GitCommit: "4b825dc642cb",
			BuildDate: "2026-09-11T12:00:00Z",
			Authority: "DIVMORA Technologies Release Authority",
		}, privRel)
		require.NoError(t, err)
		version.ReleaseSignature = token

		stdout, _, err := executeCommand(ctx, "version", "--verify")
		require.NoError(t, err)
		assert.Contains(t, stdout, "PASSED (Cryptographically verified official release)")
		assert.Contains(t, stdout, "DIVMORA Technologies Release Authority")
		assert.Contains(t, stdout, "VERIFIED_OFFICIAL_RELEASE")
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

func TestAuditCommand(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	t.Run("Audit Help", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "audit", "--help")
		require.NoError(t, err)
		assert.Contains(t, stdout, "audit")
		assert.Contains(t, stdout, "--modules")
		assert.Contains(t, stdout, "--format")
		assert.Contains(t, stdout, "--smtp-host")
		assert.Contains(t, stdout, "--smtp-to")
		assert.Contains(t, stdout, "--smtp-cc")
		assert.Contains(t, stdout, "--smtp-bcc")
	})

	t.Run("Audit Missing Config", func(t *testing.T) {
		_, _, err := executeCommand(ctx, "audit", "-c", "non-existent-file.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load configuration")
	})

	t.Run("Audit Execution With Mock Server", func(t *testing.T) {
		server := mockserver.NewMockGitLabServer()
		defer server.Close()

		// Populate mock project and compliant member
		proj := &gitlab.Project{
			ID:                201,
			Name:              "audit-project",
			Path:              "audit-project",
			PathWithNamespace: "compliance/audit-project",
			DefaultBranch:     "main",
		}
		server.State().AddProject(proj)
		isoTime := gitlab.ISOTime(gitlab.ISOTime{})
		_ = isoTime.UnmarshalJSON([]byte(`"2027-01-01"`))
		server.State().AddProjectMember(proj.ID, &gitlab.ProjectMember{
			ID:          1,
			Username:    "secops",
			AccessLevel: gitlab.DeveloperPermissions,
			ExpiresAt:   &isoTime,
		})
		server.State().ProtectBranch(proj.ID, &gitlab.ProtectedBranch{
			ID:                        1,
			Name:                      "main",
			AllowForcePush:            false,
			CodeOwnerApprovalRequired: true,
		})
		server.State().ProtectEnvironment(proj.ID, &gitlab.ProtectedEnvironment{
			Name:                  "production",
			RequiredApprovalCount: 1,
		})

		configYAML := fmt.Sprintf(`
settings:
  concurrency: 2
  gitlab:
    base_url: "%s"
    token: "mock-token"
targets:
  project_selector:
    namespaces_include: ["compliance"]
`, server.BaseURL())

		configFile := filepath.Join(tempDir, "audit_config.yaml")
		require.NoError(t, os.WriteFile(configFile, []byte(configYAML), 0600))

		// 1. Terminal Table Output
		stdout, stderr, err := executeCommand(ctx, "audit", "-c", configFile)
		require.NoError(t, err)
		assert.Contains(t, stdout+stderr, "GITLAB FLEET COMPLIANCE & SECURITY AUDIT")

		// 2. JSON Output to File
		jsonOut := filepath.Join(tempDir, "audit.json")
		_, _, err = executeCommand(ctx, "audit", "-c", configFile, "--format=json", "-o", jsonOut)
		require.NoError(t, err)
		require.FileExists(t, jsonOut)
		jsonBytes, err := os.ReadFile(jsonOut)
		require.NoError(t, err)
		assert.Contains(t, string(jsonBytes), "compliance/audit-project")

		// 3. Markdown Output to File
		mdOut := filepath.Join(tempDir, "audit.md")
		_, _, err = executeCommand(ctx, "audit", "-c", configFile, "--format=markdown", "-o", mdOut)
		require.NoError(t, err)
		require.FileExists(t, mdOut)
		mdBytes, err := os.ReadFile(mdOut)
		require.NoError(t, err)
		assert.Contains(t, string(mdBytes), "# GitLab Fleet Compliance & Security Audit Report")

		// 4. Excel (.xlsx) Output to File
		xlsxOut := filepath.Join(tempDir, "audit.xlsx")
		_, _, err = executeCommand(ctx, "audit", "-c", configFile, "-o", xlsxOut)
		require.NoError(t, err)
		require.FileExists(t, xlsxOut)

		// 5. Module Filtering
		_, _, err = executeCommand(ctx, "audit", "-c", configFile, "--modules=user_access", "--format=json", "-o", filepath.Join(tempDir, "user_access.json"))
		require.NoError(t, err)
	})
}

func TestLicenseCommand(t *testing.T) {
	ctx := context.Background()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	license.SetVerificationPublicKey(pub)
	defer license.ResetVerificationPublicKey()

	claims := &license.Claims{
		ID: "lic_cli_test",
		Customer: license.Customer{
			Name:  "Test CLI Corp",
			Email: "cli@test.com",
		},
		Tier:        "enterprise",
		MaxProjects: 250,
		Features:    []string{"all"},
		IssuedAt:    time.Now().UTC().Add(-24 * time.Hour),
		ExpiresAt:   time.Now().UTC().Add(365 * 24 * time.Hour),
	}
	validToken, err := license.SignLicense(claims, priv)
	require.NoError(t, err)

	t.Run("License Help", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "license", "--help")
		require.NoError(t, err)
		assert.Contains(t, stdout, "license")
		assert.Contains(t, stdout, "status")
		assert.Contains(t, stdout, "check")
	})

	t.Run("License Status Without License (Free Tier)", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "license", "status")
		require.NoError(t, err)
		assert.Contains(t, stdout, "Free Community Tier")
		assert.Contains(t, stdout, "25 managed projects")
	})

	t.Run("License Status JSON Without License", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "license", "status", "--json")
		require.NoError(t, err)
		assert.Contains(t, stdout, `"tier": "community"`)
		assert.Contains(t, stdout, `"free_tier_limit": 25`)
	})

	t.Run("License Status With Valid Token Flag", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "license", "status", "--license-key="+validToken)
		require.NoError(t, err)
		assert.Contains(t, stdout, "Test CLI Corp")
		assert.Contains(t, stdout, "ENTERPRISE")
		assert.Contains(t, stdout, "250 Managed Projects")
		assert.Contains(t, stdout, "VERIFIED (Ed25519")
	})

	t.Run("License Status JSON With Valid Token", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "license", "status", "--license-key="+validToken, "--json")
		require.NoError(t, err)
		assert.Contains(t, stdout, `"valid": true`)
		assert.Contains(t, stdout, `"tier": "enterprise"`)
	})

	t.Run("License Check Without Token", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "license", "check")
		require.NoError(t, err)
		assert.Contains(t, stdout, "OK: No commercial license provided")
	})

	t.Run("License Check With Valid Token", func(t *testing.T) {
		stdout, _, err := executeCommand(ctx, "license", "check", "--license-key="+validToken)
		require.NoError(t, err)
		assert.Contains(t, stdout, "OK: License lic_cli_test is valid for Test CLI Corp")
	})

	t.Run("License Check With Invalid Token", func(t *testing.T) {
		_, _, err := executeCommand(ctx, "license", "check", "--license-key=invalid.token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "license check failed")
	})

	t.Run("License Status When Converted to Apache 2.0", func(t *testing.T) {
		origDate := version.BuildDate
		origEpoch := version.ProductGenesisEpoch
		origSig := version.ReleaseSignature
		defer func() {
			version.BuildDate = origDate
			version.ProductGenesisEpoch = origEpoch
			version.ReleaseSignature = origSig
		}()
		version.ProductGenesisEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		fourYearsAgo := time.Now().UTC().AddDate(-4, 0, 0).Format(time.RFC3339)
		version.BuildDate = fourYearsAgo

		// Sign test release for this build
		pubRel, privRel, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		version.SetReleaseVerificationPublicKey(pubRel)
		defer version.ResetReleaseVerificationPublicKey()

		token, err := version.SignRelease(&version.ReleaseClaims{
			Version:   version.Version,
			GitCommit: version.GitCommit,
			BuildDate: fourYearsAgo,
			Authority: "DIVMORA Technologies",
		}, privRel)
		require.NoError(t, err)
		version.ReleaseSignature = token

		stdout, _, err := executeCommand(ctx, "license", "status")
		require.NoError(t, err)
		assert.Contains(t, stdout, "Apache License, Version 2.0")
		assert.Contains(t, stdout, "100% Free & Open Source")

		stdoutJSON, _, err := executeCommand(ctx, "license", "status", "--json")
		require.NoError(t, err)
		assert.Contains(t, stdoutJSON, `"status": "apache_2_converted"`)
		assert.Contains(t, stdoutJSON, `"license": "Apache-2.0"`)

		stdoutCheck, _, err := executeCommand(ctx, "license", "check")
		require.NoError(t, err)
		assert.Contains(t, stdoutCheck, "converted to Apache License 2.0")
	})
}
