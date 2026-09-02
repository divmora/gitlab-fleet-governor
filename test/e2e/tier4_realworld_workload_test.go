package e2e_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/cli"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// ----------------------------------------------------------------------------
// Tier 4 Real-World Workload Test 1:
// Enterprise multi-group fleet topology spanning 60+ projects across root and
// nested subgroups, archived and active projects, public/private visibility,
// compliance framework labels, member permissions audits, live apply mode
// converging unmanaged drift, and multi-format report rendering.
// ----------------------------------------------------------------------------
func TestTier4_RealWorld_EnterpriseTopology_50PlusProjects_FullDriftConvergence(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	ctx := context.Background()
	now := time.Now()

	// 1. Seed Enterprise Users
	u1 := server.State().AddUser(&gitlab.User{ID: 1, Username: "alice", Name: "Alice ChiefSec", State: "active"})
	u2 := server.State().AddUser(&gitlab.User{ID: 2, Username: "bob", Name: "Bob Architect", State: "active"})
	u3 := server.State().AddUser(&gitlab.User{ID: 3, Username: "carol", Name: "Carol Auditor", State: "active"})
	u4 := server.State().AddUser(&gitlab.User{ID: 4, Username: "david", Name: "David Lead", State: "active"})
	u5 := server.State().AddUser(&gitlab.User{ID: 5, Username: "eve", Name: "Eve Contractor", State: "active"})
	_ = u1
	_ = u2
	_ = u3
	_ = u4
	_ = u5

	// 2. Seed Enterprise Multi-tier Group Topology (5 Root Groups + 12 Subgroups = 17 Groups)
	type groupDef struct {
		id       int
		name     string
		path     string
		fullPath string
		parentID int
	}

	groups := []groupDef{
		// 1. Enterprise Core (Root 1000)
		{1000, "Enterprise Core", "enterprise-core", "enterprise-core", 0},
		{1100, "Banking Platform", "banking", "enterprise-core/banking", 1000},
		{1110, "Card Services", "cards", "enterprise-core/banking/cards", 1100},
		{1120, "Loan Services", "loans", "enterprise-core/banking/loans", 1100},
		{1200, "Billing & Invoicing", "billing", "enterprise-core/billing", 1000},

		// 2. Cloud Infrastructure (Root 2000)
		{2000, "Cloud Infrastructure", "cloud-infra", "cloud-infra", 0},
		{2100, "Kubernetes Platform", "k8s", "cloud-infra/k8s", 2000},
		{2110, "Production Clusters", "clusters", "cloud-infra/k8s/clusters", 2100},
		{2200, "Terraform Modules", "terraform", "cloud-infra/terraform", 2000},
		{2300, "Observability & Telemetry", "observability", "cloud-infra/observability", 2000},

		// 3. Cyber Security & Governance (Root 3000)
		{3000, "Cyber Security", "cyber-security", "cyber-security", 0},
		{3100, "Compliance Frameworks", "compliance", "cyber-security/compliance", 3000},
		{3200, "Audit & Forensics", "audit", "cyber-security/audit", 3000},

		// 4. Fintech Division (Root 4000)
		{4000, "Fintech Division", "fintech-division", "fintech-division", 0},
		{4100, "Trading Engine", "trading", "fintech-division/trading", 4000},
		{4200, "Settlements & Clearing", "settlements", "fintech-division/settlements", 4000},

		// 5. Data Analytics (Root 5000)
		{5000, "Data Analytics", "analytics-data", "analytics-data", 0},
		{5100, "Data Pipelines", "pipelines", "analytics-data/pipelines", 5000},
		{5200, "Machine Learning Models", "ml-models", "analytics-data/ml-models", 5000},
	}

	var rootGroupIDs []int
	for _, g := range groups {
		server.State().AddGroup(&gitlab.Group{
			ID:       g.id,
			Name:     g.name,
			Path:     g.path,
			FullPath: g.fullPath,
			ParentID: g.parentID,
		})
		if g.parentID == 0 {
			rootGroupIDs = append(rootGroupIDs, g.id)
		}
	}

	// 3. Seed 65 Total Projects Across Groups:
	// - 45 Active Private projects (Targeted by policy)
	// - 10 Active Internal projects (Filtered out or targeted based on selector)
	// - 5 Active Public projects (Filtered out)
	// - 5 Legacy Archived projects (Filtered out)
	var activePrivateProjectIDs []int
	projectCounter := 1

	for _, g := range groups {
		// Each group gets 3 active private projects
		for i := 1; i <= 3; i++ {
			if len(activePrivateProjectIDs) >= 45 {
				break
			}
			pid := 10000 + projectCounter
			projectCounter++
			pName := fmt.Sprintf("svc-core-%s-%d", g.path, i)
			p := server.State().AddProject(&gitlab.Project{
				ID:                pid,
				Name:              pName,
				Path:              pName,
				PathWithNamespace: fmt.Sprintf("%s/%s", g.fullPath, pName),
				DefaultBranch:     "main",
				Visibility:        gitlab.PrivateVisibility,
				Archived:          false,
				Topics:            []string{"tier1", "production"},
				CreatedAt:         &now,
			})
			server.State().AddGroupProject(g.id, p.ID)
			activePrivateProjectIDs = append(activePrivateProjectIDs, pid)

			// Seed initial unmanaged drift on each active private project:
			// Outdated push rule or missing push rule:
			if pid%2 == 0 {
				server.State().SetProjectPushRule(pid, &gitlab.ProjectPushRules{
					ID:               pid,
					AuthorEmailRegex: `@legacy-domain\.org$`,
					MaxFileSize:      5,
					PreventSecrets:   false,
				})
			}

			// Insecure default branch protection:
			server.State().ProtectBranch(pid, &gitlab.ProtectedBranch{
				ID:   1,
				Name: "main",
				PushAccessLevels: []*gitlab.BranchAccessDescription{
					{AccessLevel: gitlab.DeveloperPermissions, AccessLevelDescription: "Developers"},
				},
				MergeAccessLevels: []*gitlab.BranchAccessDescription{
					{AccessLevel: gitlab.DeveloperPermissions, AccessLevelDescription: "Developers"},
				},
				AllowForcePush:            true,
				CodeOwnerApprovalRequired: false,
			})

			// Outdated project settings:
			server.State().UpdateProject(pid, func(proj *gitlab.Project) {
				proj.SquashOption = "never"
				proj.MergeMethod = "merge"
				proj.OnlyAllowMergeIfPipelineSucceeds = false
			})
			server.State().SetPipelineRetention(pid, 0)

			// Add direct member with no expiration:
			server.State().AddProjectMember(pid, &gitlab.ProjectMember{
				ID:          u4.ID,
				Username:    u4.Username,
				Name:        u4.Name,
				AccessLevel: gitlab.MaintainerPermissions,
			})
		}
	}

	// Add 10 Active Internal Projects
	for i := 1; i <= 10; i++ {
		pid := 20000 + i
		pName := fmt.Sprintf("internal-tool-%d", i)
		p := server.State().AddProject(&gitlab.Project{
			ID:                pid,
			Name:              pName,
			Path:              pName,
			PathWithNamespace: fmt.Sprintf("cloud-infra/%s", pName),
			DefaultBranch:     "main",
			Visibility:        gitlab.InternalVisibility,
			Archived:          false,
			CreatedAt:         &now,
		})
		server.State().AddGroupProject(2000, p.ID)
	}

	// Add 5 Active Public Projects
	for i := 1; i <= 5; i++ {
		pid := 30000 + i
		pName := fmt.Sprintf("public-docs-%d", i)
		p := server.State().AddProject(&gitlab.Project{
			ID:                pid,
			Name:              pName,
			Path:              pName,
			PathWithNamespace: fmt.Sprintf("enterprise-core/%s", pName),
			DefaultBranch:     "main",
			Visibility:        gitlab.PublicVisibility,
			Archived:          false,
			CreatedAt:         &now,
		})
		server.State().AddGroupProject(1000, p.ID)
	}

	// Add 5 Legacy Archived Projects
	for i := 1; i <= 5; i++ {
		pid := 40000 + i
		pName := fmt.Sprintf("archived-legacy-%d", i)
		p := server.State().AddProject(&gitlab.Project{
			ID:                pid,
			Name:              pName,
			Path:              pName,
			PathWithNamespace: fmt.Sprintf("fintech-division/%s", pName),
			DefaultBranch:     "main",
			Visibility:        gitlab.PrivateVisibility,
			Archived:          true,
			CreatedAt:         &now,
		})
		server.State().AddGroupProject(4000, p.ID)
	}

	require.Equal(t, 45, len(activePrivateProjectIDs), "Should have seeded exactly 45 active private projects")

	// 4. Construct Full Declarative Enterprise Policy Config
	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			DryRun:      boolPtr(false),
			Concurrency: 16,
			GitLab: config.GitLabSettingsConfig{
				BaseURL: server.BaseURL(),
				Token:   "enterprise-admin-token",
			},
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: rootGroupIDs, // [1000, 2000, 3000, 4000, 5000]
				Recursive:       boolPtr(true),
			},
			ProjectSelector: &config.ProjectSelector{
				Visibility: "private",
				Archived:   boolPtr(false), // Exclude archived
			},
		},
		Policies: config.PoliciesConfig{
			PushRules: &config.PushRulesConfig{
				AuthorEmailRegex:      `@(enterprise|cloud|cyber|fintech|analytics)\.corp$`,
				BranchNameRegex:       `^(main|release/.*|feature/.*)$`,
				CommitMessageRegex:    `^\[(FEAT|FIX|CHORE|SEC|REFACTOR)\]`,
				FileNameRegex:         `(id_rsa|.*\.pem|.*\.key)`,
				MaxFileSize:           intPtr(50),
				PreventSecrets:        boolPtr(true),
				DenyDeleteTag:         boolPtr(true),
				RejectUnsignedCommits: boolPtr(true),
			},
			ProtectedBranches: []config.ProtectedBranchRuleConfig{
				{
					Name: "main",
					AllowedToPush: []config.BranchAccessDescription{
						{AccessLevel: int(gitlab.MaintainerPermissions)},
					},
					AllowedToMerge: []config.BranchAccessDescription{
						{AccessLevel: int(gitlab.DeveloperPermissions)},
					},
					AllowForcePush:            boolPtr(false),
					CodeOwnerApprovalRequired: boolPtr(true),
				},
				{
					Name: "release/*",
					AllowedToPush: []config.BranchAccessDescription{
						{AccessLevel: int(gitlab.MaintainerPermissions)},
					},
					AllowedToMerge: []config.BranchAccessDescription{
						{AccessLevel: int(gitlab.MaintainerPermissions)},
					},
				},
			},
			ApprovalRules: &config.ApprovalRulesConfig{
				Settings: &config.ApprovalSettingsConfig{
					AllowAuthorApproval:    boolPtr(false),
					AllowCommitterApproval: boolPtr(false),
					RetainApprovalsOnPush:  boolPtr(true),
				},
				Rules: []config.ApprovalRuleConfig{
					{
						Name:              "Security Sign-off",
						ApprovalsRequired: 2,
						UserUsernames:     []string{"alice", "bob"},
					},
				},
			},
			ProjectSettings: &config.ProjectSettingsConfig{
				SquashOption:                     "always",
				MergeMethod:                      "ff",
				OnlyAllowMergeIfPipelineSucceeds: boolPtr(true),
				OnlyAllowMergeIfAllDiscussionsAreResolved: boolPtr(true),
				KeepLatestArtifact:                        boolPtr(true),
			},
			PipelineRetention: &config.PipelineRetentionConfig{
				RetentionDays: 14, // 1,209,600 seconds
			},
			Variables: []config.VariableConfig{
				{
					Key:              "VAULT_ROLE",
					Value:            "enterprise-workload-role",
					EnvironmentScope: "*",
					Protected:        boolPtr(true),
					Masked:           boolPtr(false),
				},
				{
					Key:              "ENTERPRISE_API_KEY",
					Value:            "super-secret-token-value-12345",
					EnvironmentScope: "*",
					Protected:        boolPtr(true),
					Masked:           boolPtr(true),
				},
				{
					Key:              "ENVIRONMENT",
					Value:            "production",
					EnvironmentScope: "production",
					Protected:        boolPtr(true),
					Masked:           boolPtr(false),
				},
			},
			Runners: &config.RunnersConfig{
				SharedRunnersEnabled: boolPtr(true),
			},
			Compliance: &config.ComplianceConfig{
				FrameworkName: "SOC2",
			},
			Webhooks: []config.WebhookConfig{
				{
					URL:                   "https://audit.enterprise.corp/events",
					PushEvents:            boolPtr(true),
					MergeRequestsEvents:   boolPtr(true),
					ReleasesEvents:        boolPtr(true),
					EnableSSLVerification: boolPtr(true),
					SecretToken:           "corp-webhook-secret-token",
				},
			},
			Members: &config.MembersConfig{
				MaxAccessLevel: intPtr(int(gitlab.MaintainerPermissions)),
			},
		},
	}

	client, err := server.GovernorClient()
	require.NoError(t, err)

	eng, err := engine.NewGovernanceEngine(client, cfg, engine.WithConcurrency(16), engine.WithDryRun(false))
	require.NoError(t, err)

	// ========================================================================
	// Phase 1: Dry-Run Simulation (Plan)
	// ========================================================================
	t.Log("Executing Phase 1: Dry-Run Simulation Plan on Enterprise Fleet...")
	planResult, err := eng.Plan(ctx)
	require.NoError(t, err)
	require.NotNil(t, planResult)

	assert.True(t, planResult.DryRun)
	assert.Equal(t, "plan", planResult.Mode)
	assert.True(t, planResult.Success)
	require.NotNil(t, planResult.Metrics)

	// All 65 projects scanned in discovery
	assert.GreaterOrEqual(t, planResult.Metrics.ScannedProjects, 60, "Must scan all fleet projects")
	assert.Equal(t, 45, planResult.Metrics.TargetedProjects, "Must target exactly 45 active private projects")
	assert.Equal(t, 64, planResult.Metrics.TotalChanged, "All 45 targeted projects and 19 groups must have drift planned")
	assert.Equal(t, 0, planResult.Metrics.TotalFailed)

	// Verify server state was NOT mutated during Plan
	for _, pid := range activePrivateProjectIDs[:5] {
		assert.Equal(t, 0, server.State().GetPipelineRetention(pid), "Pipeline retention must still be 0 in dry-run")
	}

	// ========================================================================
	// Phase 2: Live Apply (Drift Reconciliation)
	// ========================================================================
	t.Log("Executing Phase 2: Live Mutating Apply on Enterprise Fleet...")
	applyStartTime := time.Now()
	applyResult, err := eng.Apply(ctx)
	applyDuration := time.Since(applyStartTime)

	require.NoError(t, err)
	require.NotNil(t, applyResult)

	assert.False(t, applyResult.DryRun)
	assert.Equal(t, "apply", applyResult.Mode)
	assert.True(t, applyResult.Success)
	require.NotNil(t, applyResult.Metrics)

	assert.Equal(t, 45, applyResult.Metrics.TargetedProjects)
	assert.Equal(t, 64, applyResult.Metrics.TotalChanged)
	assert.Greater(t, applyResult.Metrics.TotalAppliedOperations, 100, "Should have applied numerous operations")
	assert.Equal(t, 0, applyResult.Metrics.TotalFailed)

	t.Logf("Live apply completed across 45 projects in %v", applyDuration)

	// Verify server state was CONVERGED across all 45 targeted projects
	for _, pid := range activePrivateProjectIDs {
		// 1. Push Rules
		pRule, found := server.State().GetProjectPushRule(pid)
		require.True(t, found, "Push rule must exist on project %d", pid)
		assert.Equal(t, `@(enterprise|cloud|cyber|fintech|analytics)\.corp$`, pRule.AuthorEmailRegex)
		assert.True(t, pRule.PreventSecrets)
		assert.True(t, pRule.DenyDeleteTag)
		assert.True(t, pRule.RejectUnsignedCommits)

		// 2. Protected Branch
		pb, found := server.State().GetProtectedBranch(pid, "main")
		require.True(t, found, "Protected branch 'main' must exist on project %d", pid)
		assert.True(t, pb.CodeOwnerApprovalRequired)
		assert.False(t, pb.AllowForcePush)

		// 3. Project Settings & Pipeline Retention
		proj, found := server.State().GetProject(pid)
		require.True(t, found)
		assert.Equal(t, 1209600, server.State().GetPipelineRetention(pid), "Pipeline retention must be 14 days = 1209600s")
		assert.Equal(t, gitlab.FastForwardMerge, proj.MergeMethod)
		assert.Equal(t, gitlab.SquashOptionAlways, proj.SquashOption)
		assert.True(t, proj.OnlyAllowMergeIfPipelineSucceeds)
		assert.True(t, proj.OnlyAllowMergeIfAllDiscussionsAreResolved)

		// 4. Variables
		vars := server.State().ListProjectVariables(pid)
		varMap := make(map[string]*gitlab.ProjectVariable)
		for _, v := range vars {
			varMap[v.Key] = v
		}
		require.Contains(t, varMap, "VAULT_ROLE")
		assert.Equal(t, "enterprise-workload-role", varMap["VAULT_ROLE"].Value)
		assert.True(t, varMap["VAULT_ROLE"].Protected)

		require.Contains(t, varMap, "ENTERPRISE_API_KEY")
		assert.True(t, varMap["ENTERPRISE_API_KEY"].Masked)
		assert.True(t, varMap["ENTERPRISE_API_KEY"].Protected)

		// 5. Webhooks
		hooks := server.State().ListProjectHooks(pid)
		require.NotEmpty(t, hooks, "Project %d must have webhook configured", pid)
		assert.Equal(t, "https://audit.enterprise.corp/events", hooks[0].URL)
		assert.True(t, hooks[0].EnableSSLVerification)

		// 6. Compliance Framework
		frameworks := server.State().GetComplianceFrameworks(pid)
		require.NotEmpty(t, frameworks, "Project %d must have compliance framework", pid)
		assert.Equal(t, "SOC2", frameworks[0].Name)
	}

	// ========================================================================
	// Phase 3: Idempotency Gate (Zero-Drift Convergence Re-run)
	// ========================================================================
	t.Log("Executing Phase 3: Idempotency Verification Re-Run...")
	idempotencyResult, err := eng.Apply(ctx)
	require.NoError(t, err)
	require.NotNil(t, idempotencyResult)

	assert.True(t, idempotencyResult.Success)
	require.NotNil(t, idempotencyResult.Metrics)

	// In the second run, all 45 projects + 19 groups must have ZERO changes (TotalUnchanged == 64, TotalChanged == 0)
	assert.Equal(t, 45, idempotencyResult.Metrics.TargetedProjects)
	assert.Equal(t, 0, idempotencyResult.Metrics.TotalChanged, "Idempotency check: 0 targets should have changes")
	assert.Equal(t, 64, idempotencyResult.Metrics.TotalUnchanged, "Idempotency check: all 64 targets should be unchanged")
	assert.Equal(t, 0, idempotencyResult.Metrics.TotalFailed)
	assert.Equal(t, 0, idempotencyResult.TotalDiffs(), "Idempotency check: exactly 0 diffs expected on converged fleet")

	// ========================================================================
	// Phase 4: Multi-Format Report Rendering Verification
	// ========================================================================
	t.Log("Executing Phase 4: Multi-Format Report Rendering Verification...")
	reportData := report.FromExecutionResult(applyResult)
	require.NotNil(t, reportData)
	assert.Equal(t, 64, reportData.TotalTargeted)
	assert.Equal(t, 64, reportData.TotalChanged)

	// 1. Format Markdown
	t.Run("Report Format Markdown", func(t *testing.T) {
		var buf bytes.Buffer
		rep, err := report.NewReporter(report.FormatMarkdown, &buf)
		require.NoError(t, err)
		require.NoError(t, rep.Render(reportData))
		out := buf.String()

		assert.Contains(t, out, "GitLab Fleet Governor Execution Report")
		assert.Contains(t, out, "CHANGED")
		assert.Contains(t, out, "push_rules")
		assert.Contains(t, out, "protected_branches")
	})

	// 2. Format JSON
	t.Run("Report Format JSON", func(t *testing.T) {
		var buf bytes.Buffer
		rep, err := report.NewReporter(report.FormatJSON, &buf, report.WithIndent(true))
		require.NoError(t, err)
		require.NoError(t, rep.Render(reportData))

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))

		assert.Equal(t, float64(64), parsed["total_targeted"])
		assert.Equal(t, float64(64), parsed["total_changed"])
		assert.Equal(t, float64(0), parsed["total_failed"])

		targets, ok := parsed["targets"].([]any)
		require.True(t, ok)
		assert.Equal(t, 64, len(targets))
	})

	// 3. Format CSV
	t.Run("Report Format CSV", func(t *testing.T) {
		var buf bytes.Buffer
		rep, err := report.NewReporter(report.FormatCSV, &buf)
		require.NoError(t, err)
		require.NoError(t, rep.Render(reportData))

		csvReader := csv.NewReader(&buf)
		records, err := csvReader.ReadAll()
		require.NoError(t, err)

		// Header + 45 rows = 46 records
		assert.GreaterOrEqual(t, len(records), 46)
		header := records[0]
		assert.Contains(t, header, "resource_id")
		assert.Contains(t, header, "resource_path")
		assert.Contains(t, header, "status")
	})

	// 4. Format Table
	t.Run("Report Format Table", func(t *testing.T) {
		var buf bytes.Buffer
		rep, err := report.NewReporter(report.FormatTable, &buf, report.WithColor(false))
		require.NoError(t, err)
		require.NoError(t, rep.Render(reportData))
		out := buf.String()

		assert.Contains(t, out, "RESOURCE")
		assert.Contains(t, out, "STATUS")
		assert.Contains(t, out, "CHANGED")
	})

	// 5. Format Summary
	t.Run("Report Format Summary", func(t *testing.T) {
		var buf bytes.Buffer
		rep, err := report.NewReporter(report.FormatSummary, &buf, report.WithColor(false))
		require.NoError(t, err)
		require.NoError(t, rep.Render(reportData))
		out := buf.String()

		assert.Contains(t, out, "Targets:")
		assert.Contains(t, out, "Changed:")
		assert.Contains(t, out, "Failed: 0")
	})
}

// ----------------------------------------------------------------------------
// Tier 4 Real-World Workload Test 2:
// End-to-End CLI Execution with Flag Parsing, File Export, and State Mutation.
// ----------------------------------------------------------------------------
func TestTier4_RealWorld_CLIExecution_EndToEnd(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	ctx := context.Background()
	tempDir := t.TempDir()
	now := time.Now()

	// Seed 5 projects in a group
	g := server.State().AddGroup(&gitlab.Group{ID: 50, Name: "CliGroup", Path: "cligroup", FullPath: "cligroup"})
	for i := 1; i <= 5; i++ {
		pid := 500 + i
		p := server.State().AddProject(&gitlab.Project{
			ID:                pid,
			Name:              fmt.Sprintf("cli-app-%d", i),
			Path:              fmt.Sprintf("cli-app-%d", i),
			PathWithNamespace: fmt.Sprintf("cligroup/cli-app-%d", i),
			DefaultBranch:     "main",
			Visibility:        gitlab.PrivateVisibility,
			Archived:          false,
			CreatedAt:         &now,
		})
		server.State().AddGroupProject(g.ID, p.ID)
	}

	// Write out policy YAML configuration
	configYAML := fmt.Sprintf(`
version: "v1"
settings:
  concurrency: 5
  dry_run: true # Will be overridden by CLI flag --dry-run=false
  gitlab:
    base_url: "%s"
    token: "cli-exec-token"
targets:
  group_selector:
    group_ids_include: [50]
    recursive: true
policies:
  push_rules:
    author_email_regex: "@clicorp\\.io$"
    prevent_secrets: true
  pipeline_retention:
    retention_days: 10
`, server.BaseURL())

	configFile := filepath.Join(tempDir, "policy.yaml")
	reportFile := filepath.Join(tempDir, "report.json")
	require.NoError(t, os.WriteFile(configFile, []byte(configYAML), 0600))

	// Execute CLI root command
	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{
		"run",
		"-c", configFile,
		"--dry-run=false",
		"--concurrency=5",
		"--report-format=json",
		"-o", reportFile,
	})

	err := cmd.ExecuteContext(ctx)
	require.NoError(t, err, "CLI execution should succeed")

	// Verify report file was written and is valid JSON
	reportBytes, err := os.ReadFile(reportFile)
	require.NoError(t, err)

	var reportData map[string]any
	require.NoError(t, json.Unmarshal(reportBytes, &reportData))
	assert.Equal(t, float64(6), reportData["total_targeted"])
	assert.Equal(t, float64(6), reportData["total_changed"])
	assert.Equal(t, float64(0), reportData["total_failed"])

	// Verify mock server state converged
	for i := 1; i <= 5; i++ {
		pid := 500 + i
		pRule, found := server.State().GetProjectPushRule(pid)
		require.True(t, found, "Push rule must exist on project %d", pid)
		assert.Equal(t, `@clicorp\.io$`, pRule.AuthorEmailRegex)
		assert.True(t, pRule.PreventSecrets)

		assert.Equal(t, 864000, server.State().GetPipelineRetention(pid), "Pipeline retention must be 10 days = 864000s")
	}
}
