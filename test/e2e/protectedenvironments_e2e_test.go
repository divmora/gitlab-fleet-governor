package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/testutil"
)

func TestE2E_ProtectedEnvironments_ReconciliationAndPrune(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	// Target project: ID 101 (platform/fleet-governor)
	projectID := 101

	// Seed project environments in mock server:
	// production, staging, dr-east-1, dr-west-1, legacy-env
	h.Server.State().AddEnvironment(projectID, &gogitlab.Environment{ID: 1, Name: "production"})
	h.Server.State().AddEnvironment(projectID, &gogitlab.Environment{ID: 2, Name: "staging"})
	h.Server.State().AddEnvironment(projectID, &gogitlab.Environment{ID: 3, Name: "dr-east-1"})
	h.Server.State().AddEnvironment(projectID, &gogitlab.Environment{ID: 4, Name: "dr-west-1"})
	h.Server.State().AddEnvironment(projectID, &gogitlab.Environment{ID: 5, Name: "legacy-env"})

	// Pre-seed an unmanaged protected environment to test pruning
	h.Server.State().ProtectEnvironment(projectID, &gogitlab.ProtectedEnvironment{
		Name:                  "legacy-env",
		RequiredApprovalCount: 0,
		DeployAccessLevels: []*gogitlab.EnvironmentAccessDescription{
			{AccessLevel: 30},
		},
	})

	// Pre-seed an initial production protected environment with 0 approvals
	h.Server.State().ProtectEnvironment(projectID, &gogitlab.ProtectedEnvironment{
		Name:                  "production",
		RequiredApprovalCount: 0,
		DeployAccessLevels: []*gogitlab.EnvironmentAccessDescription{
			{AccessLevel: 30},
		},
	})

	policyYAML := fmt.Sprintf(`
version: "v1"
settings:
  dry_run: true
  concurrency: 2
  log_level: "info"
  log_format: "text"
  report_format: "json"
  license:
    key: "%s"
  gitlab:
    base_url: "%s"
    token: "mock-token"
    rate_limit_rps: 100.0
    rate_limit_burst: 100
targets:
  group_selector:
    group_paths_include:
      - "platform"
    recursive: true
  project_selector:
    archived: false
policies:
  protected_environments:
    prune: true
    rules:
      - name: "production"
        required_approval_count: 2
        deploy_access_levels:
          - access_level: 40
        approval_rules:
          - access_level: 40
            required_approvals: 1
            group_path: "security"
      - name: "dr-*"
        required_approval_count: 1
        deploy_access_levels:
          - access_level: 30
            username: "alice"
`, testutil.ValidCompactToken, h.Server.BaseURL())

	cfgPath := h.WriteConfigFile("policy_pe.yaml", policyYAML)

	t.Run("Dry-Run Simulation", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", cfgPath, "--dry-run=true", "--report-format=json")
		require.NoError(t, err, "stderr: %s", stderr)
		assert.Contains(t, stdout, "production")

		// Verify state was not modified in dry-run
		peLegacy, found := h.Server.State().GetProtectedEnvironment(projectID, "legacy-env")
		assert.True(t, found, "legacy-env should not be pruned during dry-run")
		assert.NotNil(t, peLegacy)

		peProd, found := h.Server.State().GetProtectedEnvironment(projectID, "production")
		assert.True(t, found)
		assert.Equal(t, 0, peProd.RequiredApprovalCount, "production required approvals should not change in dry-run")

		_, foundEast := h.Server.State().GetProtectedEnvironment(projectID, "dr-east-1")
		assert.False(t, foundEast, "dr-east-1 should not be created in dry-run")
	})

	t.Run("Live Mutation Execution", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", cfgPath, "--dry-run=false", "--report-format=json")
		require.NoError(t, err, "stderr: %s\nstdout: %s", stderr, stdout)

		// 1. Verify legacy-env was pruned
		_, foundLegacy := h.Server.State().GetProtectedEnvironment(projectID, "legacy-env")
		assert.False(t, foundLegacy, "legacy-env should have been pruned")

		// 2. Verify production was updated with required_approval_count = 2 and security group rule
		peProd, foundProd := h.Server.State().GetProtectedEnvironment(projectID, "production")
		require.True(t, foundProd, "production protected environment must exist")
		assert.Equal(t, 2, peProd.RequiredApprovalCount)
		require.NotEmpty(t, peProd.ApprovalRules)
		assert.Equal(t, 20, peProd.ApprovalRules[0].GroupID, "security group (ID 20) should have been resolved")

		// 3. Verify wildcard expansion for dr-east-1 and dr-west-1
		peEast, foundEast := h.Server.State().GetProtectedEnvironment(projectID, "dr-east-1")
		require.True(t, foundEast, "dr-east-1 should have been created via wildcard expansion")
		assert.Equal(t, 1, peEast.RequiredApprovalCount)
		require.NotEmpty(t, peEast.DeployAccessLevels)
		assert.Equal(t, 1, peEast.DeployAccessLevels[0].UserID, "alice (ID 1) should have been resolved")

		peWest, foundWest := h.Server.State().GetProtectedEnvironment(projectID, "dr-west-1")
		require.True(t, foundWest, "dr-west-1 should have been created via wildcard expansion")
		assert.Equal(t, 1, peWest.RequiredApprovalCount)
		require.NotEmpty(t, peWest.DeployAccessLevels)
		assert.Equal(t, 1, peWest.DeployAccessLevels[0].UserID, "alice (ID 1) should have been resolved")
	})

	t.Run("Audit Detection of Unprotected Staging", func(t *testing.T) {
		auditYAML := fmt.Sprintf(`
version: "v1"
settings:
  license:
    key: "%s"
  gitlab:
    base_url: "%s"
    token: "mock-token"
targets:
  group_selector:
    group_paths_include:
      - "platform"
    recursive: true
`, testutil.ValidCompactToken, h.Server.BaseURL())

		auditCfgPath := h.WriteConfigFile("audit_pe.yaml", auditYAML)
		stdout, stderr, err := h.ExecuteCLI(ctx, "audit", "-c", auditCfgPath, "--format=json")
		// Audit command returns non-zero error when critical/high violations are found, which is expected here
		if err != nil {
			assert.Contains(t, err.Error(), "security violations", "stderr: %s", stderr)
		}

		var auditOutput struct {
			ProtectedEnvFindings []struct {
				EnvironmentName string   `json:"environment_name"`
				Severity        string   `json:"severity"`
				Violations      []string `json:"violations"`
			} `json:"protected_env_findings"`
		}
		_ = json.Unmarshal([]byte(stdout), &auditOutput)

		// staging environment was never protected, so audit should identify it
		foundStaging := false
		for _, f := range auditOutput.ProtectedEnvFindings {
			if f.EnvironmentName == "staging" {
				foundStaging = true
				assert.NotEmpty(t, f.Violations)
				break
			}
		}
		assert.True(t, foundStaging, "audit must detect unprotected staging environment")
	})
}
