package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

// mockS3Client implements config.S3ClientAPI for testing S3 config loading.
type mockS3Client struct {
	objects map[string][]byte
	err     error
}

func (m *mockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	key := ""
	if params != nil && params.Key != nil {
		key = *params.Key
	}
	content, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("NoSuchKey: key %q not found in bucket %v", key, params.Bucket)
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(content)),
	}, nil
}

// ----------------------------------------------------------------------------
// Tier 3 Combinatorial Test 1:
// Pairwise / N-ary feature interactions: Multi-group BFS discovery + project filtering
// + push rules + protected branches + scoped variables + pipeline retention + webhooks
// + compliance + members + dry-run diffing in a single unified run.
// ----------------------------------------------------------------------------
func TestTier3_Combinatorial_MultiGroupBFS_ProjectFilter_FullGovernanceStack_DryRun(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	ctx := context.Background()
	now := time.Now()

	// 1. Seed Users
	server.State().AddUser(&gitlab.User{ID: 1, Username: "alice", Name: "Alice Admin", State: "active"})
	server.State().AddUser(&gitlab.User{ID: 2, Username: "bob", Name: "Bob Builder", State: "active"})
	server.State().AddUser(&gitlab.User{ID: 3, Username: "carol", Name: "Carol Compliance", State: "active"})
	server.State().AddUser(&gitlab.User{ID: 4, Username: "david", Name: "David Dev", State: "active"})

	// 2. Seed Multi-tier Group Hierarchy:
	// Root Group: 100 ("enterprise")
	//   Subgroup: 101 ("enterprise/platform", parent: 100)
	//     Subgroup: 102 ("enterprise/platform/core", parent: 101)
	// Excluded Root Group: 200 ("legacy-excluded")
	//   Subgroup: 201 ("legacy-excluded/infra", parent: 200)
	g100 := server.State().AddGroup(&gitlab.Group{
		ID:       100,
		Name:     "Enterprise",
		Path:     "enterprise",
		FullPath: "enterprise",
	})
	g101 := server.State().AddGroup(&gitlab.Group{
		ID:       101,
		Name:     "Platform",
		Path:     "platform",
		FullPath: "enterprise/platform",
		ParentID: 100,
	})
	g102 := server.State().AddGroup(&gitlab.Group{
		ID:       102,
		Name:     "Core",
		Path:     "core",
		FullPath: "enterprise/platform/core",
		ParentID: 101,
	})
	_ = server.State().AddGroup(&gitlab.Group{
		ID:       200,
		Name:     "Legacy Excluded",
		Path:     "legacy-excluded",
		FullPath: "legacy-excluded",
	})
	g201 := server.State().AddGroup(&gitlab.Group{
		ID:       201,
		Name:     "Infra Excluded",
		Path:     "infra",
		FullPath: "legacy-excluded/infra",
		ParentID: 200,
	})

	// 3. Seed Projects with diverse attributes:
	// Target Projects (should be matched):
	p1010 := server.State().AddProject(&gitlab.Project{
		ID:                1010,
		Name:              "svc-payment",
		Path:              "svc-payment",
		PathWithNamespace: "enterprise/svc-payment",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          false,
		Topics:            []string{"backend", "critical"},
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g100.ID, p1010.ID)

	p1020 := server.State().AddProject(&gitlab.Project{
		ID:                1020,
		Name:              "app-auth",
		Path:              "app-auth",
		PathWithNamespace: "enterprise/platform/app-auth",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          false,
		Topics:            []string{"auth", "security"},
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g101.ID, p1020.ID)

	p1030 := server.State().AddProject(&gitlab.Project{
		ID:                1030,
		Name:              "core-engine",
		Path:              "core-engine",
		PathWithNamespace: "enterprise/platform/core/core-engine",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          false,
		Topics:            []string{"core"},
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g102.ID, p1030.ID)

	// Non-Target Projects (should be filtered out):
	// p1040: Excluded by name regex (ends in -deprecated)
	p1040 := server.State().AddProject(&gitlab.Project{
		ID:                1040,
		Name:              "svc-legacy-deprecated",
		Path:              "svc-legacy-deprecated",
		PathWithNamespace: "enterprise/svc-legacy-deprecated",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          false,
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g100.ID, p1040.ID)

	// p1050: Excluded by archived flag (archived: true)
	p1050 := server.State().AddProject(&gitlab.Project{
		ID:                1050,
		Name:              "svc-archived-service",
		Path:              "svc-archived-service",
		PathWithNamespace: "enterprise/platform/svc-archived-service",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          true,
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g101.ID, p1050.ID)

	// p1060: Excluded by visibility (public != private)
	p1060 := server.State().AddProject(&gitlab.Project{
		ID:                1060,
		Name:              "svc-public-docs",
		Path:              "svc-public-docs",
		PathWithNamespace: "enterprise/platform/core/svc-public-docs",
		DefaultBranch:     "main",
		Visibility:        gitlab.PublicVisibility,
		Archived:          false,
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g102.ID, p1060.ID)

	// p2010: Excluded by group exclusion (in group 200/201)
	p2010 := server.State().AddProject(&gitlab.Project{
		ID:                2010,
		Name:              "svc-excluded-service",
		Path:              "svc-excluded-service",
		PathWithNamespace: "legacy-excluded/infra/svc-excluded-service",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          false,
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g201.ID, p2010.ID)

	// 4. Seed initial unmanaged drift on p1010
	server.State().SetProjectPushRule(p1010.ID, &gitlab.ProjectPushRules{
		ID:               p1010.ID,
		AuthorEmailRegex: `@outdated\.com$`,
		MaxFileSize:      5,
		PreventSecrets:   false,
	})
	server.State().ProtectBranch(p1010.ID, &gitlab.ProtectedBranch{
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
	server.State().SetProjectVariable(p1010.ID, &gitlab.ProjectVariable{
		Key:              "OLD_UNMANAGED_VAR",
		Value:            "old-val",
		VariableType:     "env_var",
		EnvironmentScope: "*",
	})
	server.State().AddProjectMember(p1010.ID, &gitlab.ProjectMember{
		ID:          4,
		Username:    "david",
		Name:        "David Dev",
		AccessLevel: gitlab.OwnerPermissions, // Over-privileged: owner on project
	})

	// 5. Construct Declarative Policy Config covering entire stack
	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			DryRun:      boolPtr(true),
			Concurrency: 4,
			LogLevel:    "debug",
			LogFormat:   "json",
			GitLab: config.GitLabSettingsConfig{
				BaseURL: server.BaseURL(),
				Token:   "mock-governor-token",
			},
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: []int{100},
				GroupIDsExclude: []int{200},
				Recursive:       boolPtr(true),
			},
			ProjectSelector: &config.ProjectSelector{
				ProjectNameRegexInclude: "^(svc|app|core)-.*",
				ProjectNameRegexExclude: ".*-deprecated$",
				Visibility:              "private",
				Archived:                boolPtr(false),
				IDRange:                 &config.IDRange{Min: 1000, Max: 1999},
			},
		},
		Policies: config.PoliciesConfig{
			PushRules: &config.PushRulesConfig{
				AuthorEmailRegex:      `.*@enterprise\.corp$`,
				BranchNameRegex:       `^(main|release/.*|feat/.*)$`,
				CommitMessageRegex:    `^\[(FEAT|FIX|CHORE|SEC)\]`,
				FileNameRegex:         `(id_rsa|.*\.pem)`,
				MaxFileSize:           intPtr(25),
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
						Name:              "Security Review",
						ApprovalsRequired: 2,
						UserUsernames:     []string{"bob", "carol"},
					},
				},
			},
			ProjectSettings: &config.ProjectSettingsConfig{
				SquashOption:                              "always",
				MergeMethod:                               "rebase_merge",
				OnlyAllowMergeIfPipelineSucceeds:          boolPtr(true),
				OnlyAllowMergeIfAllDiscussionsAreResolved: boolPtr(true),
				KeepLatestArtifact:                        boolPtr(true),
			},
			PipelineRetention: &config.PipelineRetentionConfig{
				RetentionDays: 30, // 2,592,000 seconds
			},
			Variables: []config.VariableConfig{
				{
					Key:              "AWS_REGION",
					Value:            "us-east-1",
					EnvironmentScope: "*",
					Protected:        boolPtr(true),
					Masked:           boolPtr(false),
				},
				{
					Key:              "PROD_API_KEY",
					Value:            "super-secret-key-12345",
					EnvironmentScope: "production",
					Protected:        boolPtr(true),
					Masked:           boolPtr(true),
				},
				{
					Key:              "STAGING_CONFIG",
					Value:            "staging-config-payload",
					EnvironmentScope: "staging",
					Protected:        boolPtr(false),
					Masked:           boolPtr(false),
					VariableType:     "file",
				},
			},
			Webhooks: []config.WebhookConfig{
				{
					URL:                   "https://webhook.enterprise.corp/events",
					PushEvents:            boolPtr(true),
					MergeRequestsEvents:   boolPtr(true),
					EnableSSLVerification: boolPtr(true),
					SecretToken:           "secret-token-123",
				},
			},
			Compliance: &config.ComplianceConfig{
				FrameworkName: "SOC2",
			},
			Members: &config.MembersConfig{
				MaxAccessLevel:   intPtr(int(gitlab.MaintainerPermissions)), // 40 max
				EnforceExpiresAt: boolPtr(true),
			},
		},
	}

	// 6. Execute Dry-Run (Plan)
	client, err := server.GovernorClient()
	require.NoError(t, err)

	eng, err := engine.NewGovernanceEngine(client, cfg, engine.WithConcurrency(4), engine.WithDryRun(true))
	require.NoError(t, err)

	result, err := eng.Plan(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)

	// 7. Assertions on Execution Result
	assert.True(t, result.DryRun, "Execution mode must be dry-run")
	assert.Equal(t, "plan", result.Mode)
	assert.True(t, result.Success, "Dry-run execution should succeed")
	require.NotNil(t, result.Metrics)

	// Verify Discovery & Project Filtering Counts:
	// Groups scanned in hierarchy: 100, 101, 102 -> 3 groups
	assert.Equal(t, 3, result.Metrics.ScannedGroups, "Should scan 3 groups in BFS tree")
	assert.Equal(t, 3, result.Metrics.TargetedGroups, "Should target 3 groups")

	// Total projects in group 100 hierarchy: p1010, p1020, p1030, p1040, p1050, p1060 -> 6 scanned projects
	assert.Equal(t, 6, result.Metrics.ScannedProjects, "Should scan 6 projects in tree")

	// Target projects matched by selector pipeline: p1010, p1020, p1030 -> exactly 3
	assert.Equal(t, 3, result.Metrics.TargetedProjects, "Should target exactly 3 projects")
	assert.Equal(t, 6, result.Metrics.TotalChanged, "All 3 targeted projects and 3 groups should have unmanaged drift planned")
	assert.Equal(t, 0, result.Metrics.TotalFailed, "Should have 0 failures")

	// Verify that the planned changes contain all expected operations
	var foundPushRules, foundProtectedBranches, foundApprovalRules, foundSettings, foundRetention, foundVars, foundWebhooks, foundCompliance, foundMembers bool
	for _, tr := range result.ProjectResults {
		for _, op := range tr.Operations {
			if !op.HasChanges {
				continue
			}
			switch op.OperationName {
			case "push_rules":
				foundPushRules = true
			case "protected_branches":
				foundProtectedBranches = true
			case "approval_rules":
				foundApprovalRules = true
			case "project_settings":
				foundSettings = true
			case "pipeline_retention":
				foundRetention = true
			case "variables":
				foundVars = true
			case "webhooks":
				foundWebhooks = true
			case "compliance":
				foundCompliance = true
			case "members":
				foundMembers = true
			}
		}
	}

	assert.True(t, foundPushRules, "Push rules operation should have planned changes")
	assert.True(t, foundProtectedBranches, "Protected branches operation should have planned changes")
	assert.True(t, foundApprovalRules, "Approval rules operation should have planned changes")
	assert.True(t, foundSettings, "Project settings operation should have planned changes")
	assert.True(t, foundRetention, "Pipeline retention operation should have planned changes")
	assert.True(t, foundVars, "Variables operation should have planned changes")
	assert.True(t, foundWebhooks, "Webhooks operation should have planned changes")
	assert.True(t, foundCompliance, "Compliance operation should have planned changes")
	assert.True(t, foundMembers, "Members audit operation should have findings")

	// 8. OPAQUE-BOX DRY-RUN VERIFICATION:
	// Verify that in-memory mock server state was NEVER mutated during dry-run!
	// p1020 must NOT have push rules created:
	_, pushRuleFound := server.State().GetProjectPushRule(p1020.ID)
	assert.False(t, pushRuleFound, "Mock server state must NOT have push rules created during dry-run")

	// p1020 must NOT have protected branch created:
	branches := server.State().ListProtectedBranches(p1020.ID)
	assert.Empty(t, branches, "Mock server state must NOT have protected branches created during dry-run")

	// p1010 must still have its original outdated push rule:
	rule1010, found := server.State().GetProjectPushRule(p1010.ID)
	require.True(t, found)
	assert.Equal(t, `@outdated\.com$`, rule1010.AuthorEmailRegex, "Existing push rule must remain untouched during dry-run")

	// 9. Verify Report Generation across formats for this combinatorial run
	reportData := report.FromExecutionResult(result)
	require.NotNil(t, reportData)
	assert.Equal(t, 6, reportData.TotalTargeted)
	assert.Equal(t, 6, reportData.TotalChanged)

	// Render Markdown Report
	var mdBuf bytes.Buffer
	mdRep, err := report.NewReporter(report.FormatMarkdown, &mdBuf)
	require.NoError(t, err)
	require.NoError(t, mdRep.Render(reportData))
	mdOutput := mdBuf.String()
	assert.Contains(t, mdOutput, "GitLab Fleet Governor")
	assert.Contains(t, mdOutput, "enterprise/svc-payment")
	assert.Contains(t, mdOutput, "enterprise/platform/app-auth")
	assert.Contains(t, mdOutput, "enterprise/platform/core/core-engine")

	// Render JSON Report
	var jsonBuf bytes.Buffer
	jsonRep, err := report.NewReporter(report.FormatJSON, &jsonBuf)
	require.NoError(t, err)
	require.NoError(t, jsonRep.Render(reportData))
	var parsedJSON map[string]any
	require.NoError(t, json.Unmarshal(jsonBuf.Bytes(), &parsedJSON))
	assert.Equal(t, float64(6), parsedJSON["total_targeted"])
	assert.Equal(t, float64(6), parsedJSON["total_changed"])
}

// ----------------------------------------------------------------------------
// Tier 3 Combinatorial Test 2:
// S3 config loading + Lambda EventBridge trigger + direct parameter overrides.
// ----------------------------------------------------------------------------
func TestTier3_Combinatorial_S3Config_LambdaEventBridge_DirectOverrides(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	ctx := context.Background()
	now := time.Now()

	// Seed groups and projects
	g10 := server.State().AddGroup(&gitlab.Group{ID: 10, Name: "Fintech", Path: "fintech", FullPath: "fintech"})
	p100 := server.State().AddProject(&gitlab.Project{
		ID:                100,
		Name:              "payment-gateway",
		Path:              "payment-gateway",
		PathWithNamespace: "fintech/payment-gateway",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          false,
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g10.ID, p100.ID)

	p200 := server.State().AddProject(&gitlab.Project{
		ID:                200,
		Name:              "account-service",
		Path:              "account-service",
		PathWithNamespace: "fintech/account-service",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          false,
		CreatedAt:         &now,
	})
	server.State().AddGroupProject(g10.ID, p200.ID)

	// S3 Config Content
	s3YAML := fmt.Sprintf(`
version: "v1"
settings:
  dry_run: true
  concurrency: 2
  gitlab:
    base_url: "%s"
    token: "s3-mock-token"
targets:
  group_selector:
    group_ids_include: [10]
    recursive: true
  project_selector:
    visibility: "private"
policies:
  push_rules:
    author_email_regex: "@fintech\\.corp$"
    prevent_secrets: true
`, server.BaseURL())

	mockS3 := &mockS3Client{
		objects: map[string][]byte{
			"policies/enterprise-governance.yaml": []byte(s3YAML),
			"policies/auto-fleet.json": []byte(fmt.Sprintf(`{
				"version": "v1",
				"settings": {
					"dry_run": false,
					"concurrency": 2,
					"gitlab": {
						"base_url": "%s",
						"token": "s3-json-token"
					}
				},
				"targets": {
					"group_selector": { "group_ids_include": [10] }
				},
				"policies": {
					"pipeline_retention": { "retention_days": 7 }
				}
			}`, server.BaseURL())),
		},
	}

	// 1. Setup Lambda Handler with Mock S3 and Client Factory
	handler := lambda.NewHandler(
		lambda.WithS3Client(mockS3),
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gl.GitLabClient, error) {
			return gl.NewClientFromConfig(cfg)
		}),
	)

	// --- Subtest A: EventBridge Scheduled Cron with S3 URI & Parameter Overrides ---
	t.Run("EventBridge Scheduled Event with S3 Config and Overrides", func(t *testing.T) {
		ebPayload := []byte(`{
			"version": "0",
			"id": "eb-test-event-001",
			"detail-type": "Scheduled Event",
			"source": "aws.events",
			"account": "123456789012",
			"time": "2026-08-26T00:00:00Z",
			"region": "us-east-1",
			"resources": ["arn:aws:events:us-east-1:123456789012:rule/nightly-governance"],
			"detail": {
				"config_s3_uri": "s3://governance-bucket/policies/enterprise-governance.yaml",
				"dry_run": false,
				"concurrency": 4,
				"project_regex_include": ".*payment.*"
			}
		}`)

		respAny, err := handler.HandleRequest(ctx, ebPayload)
		require.NoError(t, err)

		resp, ok := respAny.(*lambda.LambdaResponse)
		require.True(t, ok, "Expected *lambda.LambdaResponse")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "SUCCESS", resp.Status)
		assert.Equal(t, lambda.EventTypeEventBridgeSchedule, resp.EventType)
		assert.Equal(t, "s3://governance-bucket/policies/enterprise-governance.yaml", resp.ConfigSource)
		assert.False(t, resp.DryRun, "dry_run override to false should be applied")
		require.NotNil(t, resp.Summary)

		// Only payment-gateway matched due to project_regex_include override
		assert.Equal(t, 1, resp.Summary.MatchedProjects, "Only payment-gateway should be matched")
		assert.Greater(t, resp.Summary.AppliedOperations, 0, "Mutating operations should have been applied")

		// Verify state mutation on p100 (payment-gateway)
		pRule, found := server.State().GetProjectPushRule(p100.ID)
		require.True(t, found, "Push rule must be created on payment-gateway")
		assert.Equal(t, `@fintech\.corp$`, pRule.AuthorEmailRegex)

		// Verify p200 was NOT mutated because it was filtered out by regex override
		_, p200RuleFound := server.State().GetProjectPushRule(p200.ID)
		assert.False(t, p200RuleFound, "account-service must NOT be mutated")
	})

	// --- Subtest B: Direct JSON Invocation with Inline Config & Overrides ---
	t.Run("Direct JSON Invocation with Inline Config", func(t *testing.T) {
		directPayload := []byte(fmt.Sprintf(`{
			"config_yaml": "version: 'v1'\nsettings:\n  dry_run: true\n  concurrency: 2\n  gitlab:\n    base_url: '%s'\n    token: 'direct-token'\ntargets:\n  group_selector:\n    group_ids_include: [10]\npolicies:\n  pipeline_retention:\n    retention_days: 15\n",
			"dry_run": true,
			"concurrency": 5
		}`, server.BaseURL()))

		respAny, err := handler.HandleRequest(ctx, directPayload)
		require.NoError(t, err)

		resp, ok := respAny.(*lambda.LambdaResponse)
		require.True(t, ok)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "SUCCESS", resp.Status)
		assert.Equal(t, lambda.EventTypeDirectInvocation, resp.EventType)
		assert.Equal(t, "inline:payload", resp.ConfigSource)
		assert.True(t, resp.DryRun)
		require.NotNil(t, resp.Summary)
		assert.Equal(t, 2, resp.Summary.MatchedProjects)
	})

	// --- Subtest C: S3 Object Created (Put) Event ---
	t.Run("S3 Put Object Event Notification", func(t *testing.T) {
		s3EventJSON := []byte(`{
			"Records": [
				{
					"eventVersion": "2.1",
					"eventSource": "aws:s3",
					"awsRegion": "us-east-1",
					"eventTime": "2026-08-26T00:00:00Z",
					"eventName": "ObjectCreated:Put",
					"s3": {
						"s3SchemaVersion": "1.0",
						"configurationId": "policy-upload",
						"bucket": {
							"name": "governance-bucket",
							"arn": "arn:aws:s3:::governance-bucket"
						},
						"object": {
							"key": "policies/auto-fleet.json",
							"size": 1024,
							"eTag": "d41d8cd98f00b204e9800998ecf8427e"
						}
					}
				}
			]
		}`)

		respAny, err := handler.HandleRequest(ctx, s3EventJSON)
		require.NoError(t, err)

		resp, ok := respAny.(*lambda.LambdaResponse)
		require.True(t, ok)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "SUCCESS", resp.Status)
		assert.Equal(t, lambda.EventTypeS3Put, resp.EventType)
		assert.Equal(t, "s3://governance-bucket/policies/auto-fleet.json", resp.ConfigSource)
		assert.False(t, resp.DryRun)
		require.NotNil(t, resp.Summary)
		assert.Equal(t, 2, resp.Summary.MatchedProjects)
		assert.Equal(t, 2, resp.Summary.AppliedOperations)

		// Verify pipeline retention was applied on both projects (7 days = 604800s)
		ret1, _ := server.State().GetProjectPipelineRetention(p100.ID)
		assert.Equal(t, 604800, ret1)

		ret2, _ := server.State().GetProjectPipelineRetention(p200.ID)
		assert.Equal(t, 604800, ret2)
	})
}

// ----------------------------------------------------------------------------
// Tier 3 Combinatorial Test 3:
// Concurrent Fleet Scan + Token-Bucket Rate Limiter + Reactive Exponential
// Backoff with Full Jitter and Fault Injection.
// ----------------------------------------------------------------------------
func TestTier3_Combinatorial_ConcurrentFleetScan_TokenBucketRateLimiter_WithBackoff(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	ctx := context.Background()
	now := time.Now()

	// 1. Seed 20 projects across 4 groups
	var groupIDs []int
	for gIdx := 1; gIdx <= 4; gIdx++ {
		gid := gIdx * 10
		groupIDs = append(groupIDs, gid)
		g := server.State().AddGroup(&gitlab.Group{
			ID:       gid,
			Name:     fmt.Sprintf("Group-%d", gid),
			Path:     fmt.Sprintf("group-%d", gid),
			FullPath: fmt.Sprintf("group-%d", gid),
		})

		for pIdx := 1; pIdx <= 5; pIdx++ {
			pid := gid*100 + pIdx
			p := server.State().AddProject(&gitlab.Project{
				ID:                pid,
				Name:              fmt.Sprintf("service-%d", pid),
				Path:              fmt.Sprintf("service-%d", pid),
				PathWithNamespace: fmt.Sprintf("group-%d/service-%d", gid, pid),
				DefaultBranch:     "main",
				Visibility:        gitlab.PrivateVisibility,
				Archived:          false,
				CreatedAt:         &now,
			})
			server.State().AddGroupProject(g.ID, p.ID)
		}
	}

	// 2. Inject Deterministic Transient Faults via MockServer FaultEngine:
	// - Inject 429 Too Many Requests on GET /projects (2 failures before 200) with Retry-After: 1
	server.Faults().Inject429("GET", "/api/v4/projects", 2, 1)

	// - Inject 503 Service Unavailable on PUT /projects (2 failures before 200)
	server.Faults().Inject5xx("PUT", "/api/v4/projects", http.StatusServiceUnavailable, 2)

	// - Inject 500 Internal Server Error on GET /push_rule (2 failures before 200)
	server.Faults().Inject5xx("GET", "/api/v4/projects", http.StatusInternalServerError, 2)

	// 3. Track Retry Invocations via custom RetryListener
	var retryCount int64
	retryListener := func(attempt int, req *http.Request, resp *http.Response, err error, delay time.Duration) {
		atomic.AddInt64(&retryCount, 1)
	}

	// 4. Configure Client with Fast Backoff for Testing and Bounded Rate Limiting
	clientSettings := &config.GitLabSettingsConfig{
		BaseURL:          server.BaseURL(),
		Token:            "test-resilient-token",
		RateLimitRPS:     50.0,
		RateLimitBurst:   20,
		MaxRetries:       4,
		RetryBaseDelayMs: 25,  // Fast base backoff for test suite speed
		RetryMaxDelayMs:  200, // Fast max cap
		TimeoutSeconds:   15,
	}

	auth, err := gl.ResolveAuth(clientSettings)
	require.NoError(t, err)

	transportCfg := gl.DefaultGovernorTransportConfig()
	transportCfg.RateLimitRPS = 50.0
	transportCfg.RateLimitBurst = 20
	transportCfg.MaxRetries = 4
	transportCfg.BaseBackoff = 25 * time.Millisecond
	transportCfg.MaxBackoff = 200 * time.Millisecond
	transportCfg.JitterRatio = 0.15
	transportCfg.RetryListener = retryListener

	transport := gl.NewGovernorTransport(transportCfg)
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	client, err := gl.NewClient(auth, gl.WithHTTPClient(httpClient))
	require.NoError(t, err)

	// 5. Policy Configuration with High Concurrency
	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			DryRun:      boolPtr(false),
			Concurrency: 8, // Concurrency 8 across 20 projects
			GitLab:      *clientSettings,
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: groupIDs,
				Recursive:       boolPtr(true),
			},
		},
		Policies: config.PoliciesConfig{
			PushRules: &config.PushRulesConfig{
				AuthorEmailRegex: `@resilient\.corp$`,
				PreventSecrets:   boolPtr(true),
			},
			PipelineRetention: &config.PipelineRetentionConfig{
				RetentionDays: 21, // 1,814,400 seconds
			},
		},
	}

	// 6. Execute Live Apply Run with High Concurrency
	eng, err := engine.NewGovernanceEngine(client, cfg, engine.WithConcurrency(8), engine.WithDryRun(false))
	require.NoError(t, err)

	startTime := time.Now()
	result, err := eng.Apply(ctx)
	duration := time.Since(startTime)

	require.NoError(t, err)
	require.NotNil(t, result)

	// 7. Verify Results & Resilience
	assert.False(t, result.DryRun)
	assert.Equal(t, "apply", result.Mode)
	assert.True(t, result.Success, "All operations should succeed after retries")
	require.NotNil(t, result.Metrics)

	assert.Equal(t, 20, result.Metrics.TargetedProjects, "All 20 projects must be targeted")
	assert.Equal(t, 24, result.Metrics.TotalChanged, "All 20 projects and 4 groups must have had changes applied")
	assert.Equal(t, 0, result.Metrics.TotalFailed, "Zero failures allowed despite injected faults")
	assert.Greater(t, atomic.LoadInt64(&retryCount), int64(0), "Retry listener must have recorded retried requests")

	// 8. Verify all 20 projects successfully converged in mock server
	for _, gid := range groupIDs {
		for pIdx := 1; pIdx <= 5; pIdx++ {
			pid := gid*100 + pIdx
			pRule, found := server.State().GetProjectPushRule(pid)
			require.True(t, found, "Push rule must be set for project %d", pid)
			assert.Equal(t, `@resilient\.corp$`, pRule.AuthorEmailRegex)

			assert.Equal(t, 1814400, server.State().GetPipelineRetention(pid))
		}
	}

	t.Logf("Concurrent scan with rate limiter & backoff completed successfully in %v with %d retries handled.", duration, atomic.LoadInt64(&retryCount))
}
