package e2e_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/cli"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ----------------------------------------------------------------------------
// Tier 1: CLI Subcommands (version, validate, run, lambda)
// ----------------------------------------------------------------------------

func TestE2E_Tier1_CLI_Version(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	t.Run("Standard Human-Readable Output", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "version")
		require.NoError(t, err, "stderr: %s", stderr)
		assert.Contains(t, stdout, "gitlab-fleet-governor")
		assert.Contains(t, stdout, "commit:")
		assert.Contains(t, stdout, "go:")
	})

	t.Run("Short Semver Output", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "version", "--short")
		require.NoError(t, err, "stderr: %s", stderr)
		assert.Equal(t, version.Version, strings.TrimSpace(stdout))
	})

	t.Run("Machine-Readable JSON Output", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "version", "--json")
		require.NoError(t, err, "stderr: %s", stderr)

		var info version.Info
		err = json.Unmarshal([]byte(stdout), &info)
		require.NoError(t, err)
		assert.Equal(t, version.Version, info.Version)
		assert.NotEmpty(t, info.GoVersion)
		assert.NotEmpty(t, info.Platform)
	})
}

func TestE2E_Tier1_CLI_Validate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	yamlConfig := h.BasePolicyYAML(`
  push_rules:
    author_email_regex: '@corp\\.com$'
    prevent_secrets: true
  protected_branches:
    - name: "main"
      allowed_to_push:
        - access_level: 0
      allowed_to_merge:
        - access_level: 40
`)
	yamlPath := h.WriteConfigFile("valid_policy.yaml", yamlConfig)

	jsonObj := map[string]any{
		"version": "v1",
		"settings": map[string]any{
			"concurrency": 8,
			"dry_run":     true,
			"gitlab": map[string]any{
				"base_url": h.Server.BaseURL(),
				"token":    "mock-token",
			},
		},
		"targets": map[string]any{
			"group_selector": map[string]any{
				"group_ids_include": []int{10},
			},
		},
		"policies": map[string]any{
			"pipeline_retention": map[string]any{
				"retention_days": 60,
			},
		},
	}
	jsonPath := h.WriteConfigFileJSON("valid_policy.json", jsonObj)

	t.Run("Validate Local YAML File", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "validate", "-c", yamlPath)
		require.NoError(t, err, "stderr: %s", stderr)
		assert.Contains(t, stdout, "valid")
	})

	t.Run("Validate Local JSON File with JSON Flag", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "validate", "-c", jsonPath, "--json")
		require.NoError(t, err, "stderr: %s", stderr)

		var out cli.ValidateJSONOutput
		err = json.Unmarshal([]byte(stdout), &out)
		require.NoError(t, err)
		assert.True(t, out.Valid)
		assert.Equal(t, "VALID", out.Status)
		assert.Empty(t, out.Errors)
		require.NotNil(t, out.Policies)
		assert.True(t, out.Policies.PipelineRetention)
	})

	t.Run("Validate Quiet Flag", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "validate", "-c", yamlPath, "--quiet")
		require.NoError(t, err, "stderr: %s", stderr)
		assert.Empty(t, stdout)
	})

	t.Run("Validate Standard Input (-)", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLIWithStdin(ctx, strings.NewReader(yamlConfig), "validate", "-c", "-")
		require.NoError(t, err, "stderr: %s", stderr)
		assert.Contains(t, stdout, "valid")
	})
}

func TestE2E_Tier1_CLI_Run_Lifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	// Target project 101: initial settings has retention = 0, default_branch = main
	policyContent := fmt.Sprintf(`
version: "v1"
settings:
  dry_run: true
  concurrency: 2
  report_format: "json"
  gitlab:
    base_url: "%s"
    token: "mock-token"
targets:
  project_selector:
    namespaces_include:
      - "platform"
    project_name_regex_include: "^fleet-governor$"
policies:
  project_settings:
    squash_option: "always"
    merge_method: "rebase_merge"
    only_allow_merge_if_pipeline_succeeds: true
  pipeline_retention:
    retention_days: 90
`, h.Server.BaseURL())

	policyPath := h.WriteConfigFile("run_lifecycle_policy.yaml", policyContent)

	t.Run("Phase 1: Dry-Run Simulation Mode (No State Mutations)", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", policyPath, "--dry-run=true", "--report-format=json")
		require.NoError(t, err, "stderr: %s", stderr)

		var reportData report.ReportData
		err = json.Unmarshal([]byte(stdout), &reportData)
		require.NoError(t, err)

		assert.True(t, reportData.DryRun)
		assert.GreaterOrEqual(t, reportData.TotalTargeted, 1)
		assert.GreaterOrEqual(t, reportData.TotalChanged, 1)

		// Verify remote mock server state has NOT mutated
		p101, found := h.GetProject(101)
		require.True(t, found)
		retSec, _ := h.GetPipelineRetention(101)
		assert.Equal(t, 0, retSec, "dry-run must not mutate remote GitLab state")
		assert.NotEqual(t, gogitlab.SquashOptionAlways, p101.SquashOption)
	})

	t.Run("Phase 2: Live Mutating Apply Mode", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", policyPath, "--dry-run=false", "--report-format=json")
		require.NoError(t, err, "stderr: %s", stderr)

		var reportData report.ReportData
		err = json.Unmarshal([]byte(stdout), &reportData)
		require.NoError(t, err)

		assert.False(t, reportData.DryRun)
		assert.Equal(t, 0, reportData.TotalFailed)
		assert.GreaterOrEqual(t, reportData.TotalChanged, 1)

		// Verify remote mock server state HAS mutated
		p101, found := h.GetProject(101)
		require.True(t, found)
		retSecApply, _ := h.GetPipelineRetention(101)
		assert.Equal(t, 90*86400, retSecApply, "apply must mutate remote GitLab pipeline retention")
		assert.Equal(t, gogitlab.SquashOptionAlways, p101.SquashOption)
		assert.Equal(t, gogitlab.RebaseMerge, p101.MergeMethod)
		assert.True(t, p101.OnlyAllowMergeIfPipelineSucceeds)
	})

	t.Run("Phase 3: Idempotent Re-execution (Zero Drift)", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", policyPath, "--dry-run=false", "--report-format=json")
		require.NoError(t, err, "stderr: %s", stderr)

		var reportData report.ReportData
		err = json.Unmarshal([]byte(stdout), &reportData)
		require.NoError(t, err)

		assert.False(t, reportData.DryRun)
		assert.Equal(t, 0, reportData.TotalFailed)
		assert.Equal(t, 0, reportData.TotalChanged, "repeated run must be idempotent with 0 changed resources")
		assert.GreaterOrEqual(t, reportData.TotalUnchanged, 1)
	})
}

func TestE2E_Tier1_CLI_Lambda_Emulation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	directEvent := map[string]any{
		"action": "dry_run",
		"config": map[string]any{
			"version": "v1",
			"settings": map[string]any{
				"concurrency": 2,
				"dry_run":     true,
				"gitlab": map[string]any{
					"base_url": h.Server.BaseURL(),
					"token":    "mock-token",
				},
			},
			"targets": map[string]any{
				"group_selector": map[string]any{
					"group_ids_include": []int{10},
				},
			},
			"policies": map[string]any{
				"push_rules": map[string]any{
					"prevent_secrets": true,
				},
			},
		},
	}
	directEventFile := h.WriteConfigFileJSON("lambda_direct_event.json", directEvent)

	t.Run("Direct JSON Invocation via CLI", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "lambda", "--event", directEventFile)
		require.NoError(t, err, "stderr: %s", stderr)

		var resp lambda.LambdaResponse
		err = json.Unmarshal([]byte(stdout), &resp)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "SUCCESS", resp.Status)
		assert.Equal(t, lambda.EventTypeDirectInvocation, resp.EventType)
		assert.True(t, resp.DryRun)
	})

	t.Run("EventBridge Scheduled Trigger via Lambda Handler", func(t *testing.T) {
		// Set env vars so the default lambda resolver can resolve default mock gitlab URL
		t.Setenv("GITLAB_BASE_URL", h.Server.BaseURL())
		t.Setenv("GITLAB_TOKEN", "mock-token")

		policyYAML := h.BasePolicyYAML(`
  pipeline_retention:
    retention_days: 30
`)
		h.S3Client.Put("gov-bucket", "scheduled-policy.yaml", []byte(policyYAML))
		t.Setenv("CONFIG_SOURCE", "s3://gov-bucket/scheduled-policy.yaml")

		scheduledEvent := []byte(`{
			"version": "0",
			"id": "fe874281-70bf-404a-b5e0-fae9b380f2d9",
			"detail-type": "Scheduled Event",
			"source": "aws.events",
			"account": "123456789012",
			"time": "2026-08-25T12:00:00Z",
			"region": "us-east-1",
			"resources": ["arn:aws:events:us-east-1:123456789012:rule/nightly-fleet-governance"],
			"detail": {}
		}`)

		handler := h.NewLambdaHandler()
		respAny, err := handler.HandleRequest(ctx, scheduledEvent)
		require.NoError(t, err)

		resp, ok := respAny.(*lambda.LambdaResponse)
		require.True(t, ok)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "SUCCESS", resp.Status)
		assert.Equal(t, lambda.EventTypeEventBridgeSchedule, resp.EventType)
	})
}

// ----------------------------------------------------------------------------
// Tier 1: Multi-Source Configuration Ingestion & Env Var Expansion
// ----------------------------------------------------------------------------

func TestE2E_Tier1_Configuration_Sources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	rawYAML := h.BasePolicyYAML(`
  push_rules:
    author_email_regex: '@${CORP_DOMAIN:-example.com}$'
    prevent_secrets: true
`)
	rawJSON := `{
  "version": "v1",
  "settings": {
    "dry_run": true,
    "gitlab": {
      "base_url": "${MOCK_BASE_URL}",
      "token": "${MOCK_TOKEN:-default-token}"
    }
  },
  "targets": {
    "group_selector": {
      "group_ids_include": [10]
    }
  },
  "policies": {
    "pipeline_retention": {
      "retention_days": 45
    }
  }
}`

	yamlPath := h.WriteConfigFile("source_test.yaml", rawYAML)
	jsonPath := h.WriteConfigFile("source_test.json", rawJSON)

	h.S3Client.Put("my-test-bucket", "fleet/policy.yaml", []byte(rawYAML))

	t.Run("Source 1: Local YAML File with Env Expansion", func(t *testing.T) {
		t.Setenv("CORP_DOMAIN", "acme-corp.org")
		cfg, src, err := config.Load(ctx, yamlPath)
		require.NoError(t, err)
		assert.Equal(t, yamlPath, src)
		require.NotNil(t, cfg.Policies.PushRules)
		assert.Equal(t, `@acme-corp.org$`, cfg.Policies.PushRules.AuthorEmailRegex)
	})

	t.Run("Source 2: Local JSON File with Env Expansion", func(t *testing.T) {
		t.Setenv("MOCK_BASE_URL", h.Server.BaseURL())
		cfg, src, err := config.Load(ctx, jsonPath)
		require.NoError(t, err)
		assert.Equal(t, jsonPath, src)
		assert.Equal(t, h.Server.BaseURL(), cfg.Settings.GitLab.BaseURL)
		assert.Equal(t, "default-token", cfg.Settings.GitLab.Token)
	})

	t.Run("Source 3: Standard Input (-)", func(t *testing.T) {
		loader := config.NewLoader(config.WithStdin(strings.NewReader(rawYAML)))
		data, src, err := loader.LoadRaw(ctx, "-")
		require.NoError(t, err)
		assert.Equal(t, "-", src)
		assert.Equal(t, rawYAML, string(data))
	})

	t.Run("Source 4: CONFIG_CONTENT inline env var", func(t *testing.T) {
		t.Setenv("CONFIG_CONTENT", rawYAML)
		loader := config.NewLoader()
		data, src, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "env:CONFIG_CONTENT", src)
		assert.Equal(t, rawYAML, string(data))
	})

	t.Run("Source 5: CONFIG_YAML inline env var", func(t *testing.T) {
		t.Setenv("CONFIG_YAML", rawYAML)
		loader := config.NewLoader()
		data, src, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "env:CONFIG_YAML", src)
		assert.Equal(t, rawYAML, string(data))
	})

	t.Run("Source 6: CONFIG_JSON inline env var", func(t *testing.T) {
		t.Setenv("CONFIG_JSON", rawJSON)
		loader := config.NewLoader()
		data, src, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "env:CONFIG_JSON", src)
		assert.Equal(t, rawJSON, string(data))
	})

	t.Run("Source 7: CONFIG_SOURCE referencing file", func(t *testing.T) {
		t.Setenv("CONFIG_SOURCE", yamlPath)
		loader := config.NewLoader()
		data, src, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Contains(t, src, "env:CONFIG_SOURCE")
		assert.Equal(t, rawYAML, string(data))
	})

	t.Run("Source 8: AWS S3 URI (s3://...)", func(t *testing.T) {
		loader := config.NewLoader(config.WithS3Client(h.S3Client))
		data, src, err := loader.LoadRaw(ctx, "s3://my-test-bucket/fleet/policy.yaml")
		require.NoError(t, err)
		assert.Equal(t, "s3://my-test-bucket/fleet/policy.yaml", src)
		assert.Equal(t, rawYAML, string(data))
	})
}

// ----------------------------------------------------------------------------
// Tier 1: Fleet Discovery Target Selectors
// ----------------------------------------------------------------------------

func TestE2E_Tier1_Discovery_Target_Selectors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)
	client, err := h.GovernorClient()
	require.NoError(t, err)

	boolTrue := true
	boolFalse := false

	// Seed structure in mock server:
	// Groups: Group 10 (platform), Group 20 (security), Group 11 (platform/core, child of 10)
	// Projects:
	//   p101: platform/fleet-governor (ID 101, Private, Group 10)
	//   p102: platform/core/cloud-infra (ID 102, Internal, Group 11)
	//   p103: security/security-tools (ID 103, Public, Group 20)

	t.Run("Group Selector - Recursive BFS Expansion", func(t *testing.T) {
		targets := config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupPathsInclude: []string{"platform"},
				Recursive:         &boolTrue,
			},
		}

		fleet, err := discovery.DiscoverFleet(ctx, client, targets)
		require.NoError(t, err)
		assert.False(t, fleet.IsEmpty())

		// Group 10 and Subgroup 11 must be discovered
		assert.Contains(t, fleet.Groups, 10)
		assert.Contains(t, fleet.Groups, 11)
		assert.NotContains(t, fleet.Groups, 20)

		// Projects belonging to 10 and 11 must be discovered
		assert.Contains(t, fleet.Projects, 101)
		assert.Contains(t, fleet.Projects, 102)
		assert.NotContains(t, fleet.Projects, 103)
	})

	t.Run("Group Selector - Group IDs Include and Exclude", func(t *testing.T) {
		targets := config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: []int{10, 20},
				GroupIDsExclude: []int{20},
				Recursive:       &boolFalse,
			},
		}

		fleet, err := discovery.DiscoverFleet(ctx, client, targets)
		require.NoError(t, err)
		assert.Contains(t, fleet.Groups, 10)
		assert.NotContains(t, fleet.Groups, 20)
		assert.NotContains(t, fleet.Groups, 11) // non-recursive
	})

	t.Run("Project Selector - Namespace Filter", func(t *testing.T) {
		targets := config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				NamespacesInclude: []string{"platform/core"},
			},
		}

		fleet, err := discovery.DiscoverFleet(ctx, client, targets)
		require.NoError(t, err)
		assert.Contains(t, fleet.Projects, 102)
		assert.NotContains(t, fleet.Projects, 101)
		assert.NotContains(t, fleet.Projects, 103)
	})

	t.Run("Project Selector - Project Name Regex Include and Exclude", func(t *testing.T) {
		targets := config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				ProjectNameRegexInclude: `.*-(governor|tools)$`,
				ProjectNameRegexExclude: `^security-.*`,
			},
		}

		fleet, err := discovery.DiscoverFleet(ctx, client, targets)
		require.NoError(t, err)
		assert.Contains(t, fleet.Projects, 101)    // fleet-governor
		assert.NotContains(t, fleet.Projects, 102) // cloud-infra
		assert.NotContains(t, fleet.Projects, 103) // security-tools (excluded by regex exclude)
	})

	t.Run("Project Selector - Visibility and ID Range Filter", func(t *testing.T) {
		targets := config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				Visibility: "private",
				IDRange: &config.IDRange{
					Min: 100,
					Max: 102,
				},
			},
		}

		fleet, err := discovery.DiscoverFleet(ctx, client, targets)
		require.NoError(t, err)
		assert.Contains(t, fleet.Projects, 101)    // Private, ID 101
		assert.NotContains(t, fleet.Projects, 102) // Internal
		assert.NotContains(t, fleet.Projects, 103) // Public
	})
}

// ----------------------------------------------------------------------------
// Tier 1: All 10 Governance Reconcilers (Dry-Run & Apply & Idempotency)
// ----------------------------------------------------------------------------

func TestE2E_Tier1_All_10_Governance_Reconcilers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	// Comprehensive policy configuring all 10 governance modules
	policyYAML := h.BasePolicyYAML(`
  # 1. Push Rules
  push_rules:
    author_email_regex: '@fleetcorp\.io$'
    branch_name_regex: '^(main|develop|feat/.*)$'
    prevent_secrets: true
    reject_unsigned_commits: true
    deny_delete_tag: true
    max_file_size: 25

  # 2. Protected Branches
  protected_branches:
    - name: "main"
      allowed_to_push:
        - access_level: 0
      allowed_to_merge:
        - access_level: 40
      code_owner_approval_required: true
      allow_force_push: false

  # 3. Approval Rules
  approval_rules:
    settings:
      allow_author_approval: false
      allow_committer_approval: false
      retain_approvals_on_push: true
    rules:
      - name: "AppSec Approval Rule"
        approvals_required: 1
        user_usernames:
          - "alice"
        protected_branch_names:
          - "main"

  # 4. Project Settings
  project_settings:
    default_branch: "main"
    squash_option: "always"
    merge_method: "rebase_merge"
    only_allow_merge_if_pipeline_succeeds: true
    only_allow_merge_if_all_discussions_are_resolved: true
    keep_latest_artifact: true

  # 5. Pipeline Retention
  pipeline_retention:
    retention_days: 60

  # 6. CI/CD Variables
  variables:
    - key: "GLOBAL_API_ENDPOINT"
      value: "https://api.fleetcorp.io/v1"
      variable_type: "env_var"
      protected: true
      masked: false
      raw: true
      environment_scope: "*"
    - key: "SECRET_DEPLOY_KEY_PROD"
      value: "secret_value_12345"
      variable_type: "env_var"
      protected: true
      masked: true
      raw: true
      environment_scope: "production"

  # 7. Runners
  runners:
    shared_runners_enabled: true
    runners:
      - id: 1
        paused: false
        locked: true
        tag_list:
          - "docker"
          - "linux-amd64"
        access_level: "not_protected"

  # 8. Compliance
  compliance:
    framework_name: "SOC2"
    prune: false

  # 9. Webhooks
  webhooks:
    - url: "https://audit-gateway.fleetcorp.io/gitlab-events"
      push_events: true
      merge_requests_events: true
      enable_ssl_verification: true
      secret_token: "supersecrettoken"

  # 10. Members
  members:
    max_access_level: 40
    enforce_expires_at: false
    allowed_members:
      - username: "alice"
        access_level: 40
`)

	policyPath := h.WriteConfigFile("all_10_reconcilers.yaml", policyYAML)
	cfg, _, err := config.Load(ctx, policyPath)
	require.NoError(t, err)

	client, err := h.GovernorClient()
	require.NoError(t, err)

	reg := governance.NewDefaultRegistry(client)
	require.Equal(t, 10, len(reg.OrderedOperations()), "all 10 reconcilers must be registered")

	// 1. Dry Run Execution
	t.Run("Dry-Run Simulation: Validates Plan Diffs for All 10 Reconcilers", func(t *testing.T) {
		eng, err := engine.NewGovernanceEngine(client, cfg, engine.WithDryRun(true), engine.WithConcurrency(2))
		require.NoError(t, err)

		res, err := eng.Plan(ctx)
		require.NoError(t, err)
		assert.True(t, res.DryRun)
		assert.True(t, res.Success)
		assert.Empty(t, res.Errors)
		assert.Greater(t, len(res.TargetResults), 0)

		// Verify target 101 has operations planned
		var p101Target *engine.TargetResult
		for _, tr := range res.TargetResults {
			if tr.TargetID == 101 && tr.ResourceType == governance.ResourceTypeProject {
				p101Target = tr
				break
			}
		}
		require.NotNil(t, p101Target, "target project 101 should be present in target results")

		opNames := make(map[string]bool)
		for _, op := range p101Target.Operations {
			opNames[op.OperationName] = true
		}

		expectedOps := []string{
			"push_rules",
			"protected_branches",
			"approval_rules",
			"project_settings",
			"pipeline_retention",
			"variables",
			"runners",
			"compliance",
			"webhooks",
			"members",
		}
		for _, opName := range expectedOps {
			assert.True(t, opNames[opName], "reconciler operation %s should be executed for project", opName)
		}
	})

	// 2. Live Apply Execution
	t.Run("Live Apply: Applies Mutations Across All 10 Reconcilers", func(t *testing.T) {
		eng, err := engine.NewGovernanceEngine(client, cfg, engine.WithDryRun(false), engine.WithConcurrency(2))
		require.NoError(t, err)

		res, err := eng.Apply(ctx)
		require.NoError(t, err)
		assert.False(t, res.DryRun)
		assert.True(t, res.Success)
		assert.Empty(t, res.Errors)

		// Assert project 101 mutations in mock server state
		p101, found := h.GetProject(101)
		require.True(t, found)

		// Reconciler 4: Project Settings
		assert.Equal(t, gogitlab.SquashOptionAlways, p101.SquashOption)
		assert.Equal(t, gogitlab.RebaseMerge, p101.MergeMethod)
		assert.True(t, p101.OnlyAllowMergeIfPipelineSucceeds)

		// Reconciler 5: Pipeline Retention
		retSec714, _ := h.GetPipelineRetention(101)
		assert.Equal(t, 60*86400, retSec714)

		// Reconciler 1: Push Rules
		pr, found := h.Server.State().GetProjectPushRule("101")
		require.True(t, found)
		assert.Equal(t, `@fleetcorp\.io$`, pr.AuthorEmailRegex)
		assert.Equal(t, 25, pr.MaxFileSize)
		assert.True(t, pr.PreventSecrets)
		assert.True(t, pr.RejectUnsignedCommits)

		// Reconciler 6: CI/CD Variables
		vars := h.Server.State().ListProjectVariables(101)
		varFound := false
		for _, v := range vars {
			if v.Key == "GLOBAL_API_ENDPOINT" {
				varFound = true
				assert.Equal(t, "https://api.fleetcorp.io/v1", v.Value)
				assert.True(t, v.Protected)
			}
		}
		assert.True(t, varFound, "GLOBAL_API_ENDPOINT CI/CD variable should be created")

		// Reconciler 9: Webhooks
		hooks := h.Server.State().ListProjectHooks(101)
		hookFound := false
		for _, hk := range hooks {
			if hk.URL == "https://audit-gateway.fleetcorp.io/gitlab-events" {
				hookFound = true
				assert.True(t, hk.PushEvents)
				assert.True(t, hk.MergeRequestsEvents)
				assert.True(t, hk.EnableSSLVerification)
			}
		}
		assert.True(t, hookFound, "Webhook should be created on project 101")
	})

	// 3. Idempotency Verification
	t.Run("Idempotency: Re-applying Policy Yields Zero Drift", func(t *testing.T) {
		eng, err := engine.NewGovernanceEngine(client, cfg, engine.WithDryRun(false), engine.WithConcurrency(2))
		require.NoError(t, err)

		res, err := eng.Apply(ctx)
		require.NoError(t, err)
		assert.True(t, res.Success)
		assert.Equal(t, 0, res.Metrics.TotalFailed)
		assert.Equal(t, 0, res.Metrics.TotalChanged, "re-running all 10 reconcilers must have zero changed operations")
	})
}

// ----------------------------------------------------------------------------
// Tier 1: Multi-Format Summary Reports
// ----------------------------------------------------------------------------

func TestE2E_Tier1_Report_Formats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h := NewE2EHarness(t)
	policyYAML := h.BasePolicyYAML(`
  pipeline_retention:
    retention_days: 30
`)
	policyPath := h.WriteConfigFile("report_formats_test.yaml", policyYAML)

	formats := []struct {
		format    string
		validator func(t *testing.T, stdout string)
	}{
		{
			format: "table",
			validator: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "GitLab Fleet Governor Execution Report")
				assert.Contains(t, stdout, "TARGET")
				assert.Contains(t, stdout, "STATUS")
			},
		},
		{
			format: "json",
			validator: func(t *testing.T, stdout string) {
				var rd report.ReportData
				err := json.Unmarshal([]byte(stdout), &rd)
				require.NoError(t, err)
				assert.NotEmpty(t, rd.Title)
				assert.GreaterOrEqual(t, rd.TotalTargeted, 1)
				assert.NotEmpty(t, rd.Targets)
			},
		},
		{
			format: "csv",
			validator: func(t *testing.T, stdout string) {
				r := csv.NewReader(strings.NewReader(stdout))
				records, err := r.ReadAll()
				require.NoError(t, err)
				require.GreaterOrEqual(t, len(records), 2, "CSV must contain header row plus data rows")
				assert.Contains(t, records[0], "resource_type")
				assert.Contains(t, records[0], "resource_id")
				assert.Contains(t, records[0], "resource_path")
				assert.Contains(t, records[0], "status")
			},
		},
		{
			format: "markdown",
			validator: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "GitLab Fleet Governor Execution Report")
				assert.Contains(t, stdout, "Executive Summary")
				assert.Contains(t, stdout, "Target Results")
			},
		},
		{
			format: "summary",
			validator: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "GitLab Fleet Governor")
				assert.Contains(t, stdout, "Targets:")
				assert.Contains(t, stdout, "Changed:")
			},
		},
	}

	for _, tt := range formats {
		t.Run("Format: "+tt.format, func(t *testing.T) {
			stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", policyPath, "--report-format", tt.format)
			require.NoError(t, err, "stderr: %s", stderr)
			tt.validator(t, stdout)
		})
	}

	t.Run("Export Report to Output File (-o)", func(t *testing.T) {
		outFile := filepath.Join(h.TempDir, "exported_report.json")
		_, stderr, err := h.ExecuteCLI(ctx, "run", "-c", policyPath, "--report-format", "json", "-o", outFile)
		require.NoError(t, err, "stderr: %s", stderr)

		data, err := os.ReadFile(outFile)
		require.NoError(t, err)

		var rd report.ReportData
		err = json.Unmarshal(data, &rd)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, rd.TotalTargeted, 1)
	})
}
