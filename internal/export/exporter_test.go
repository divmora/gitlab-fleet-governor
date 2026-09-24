package export_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/export"
	glclient "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func setupMockServerWithFullProject(t *testing.T) (*mockserver.MockGitLabServer, *gitlab.Project) {
	server := mockserver.NewMockGitLabServer()

	// 1. Group & Subgroup setup
	grp := &gitlab.Group{
		ID:       10,
		Name:     "enterprise-fleet",
		Path:     "enterprise-fleet",
		FullPath: "enterprise-fleet",
	}
	server.State().AddGroup(grp)

	proj := &gitlab.Project{
		ID:                               42,
		Name:                             "app-service",
		Path:                             "app-service",
		PathWithNamespace:                "enterprise-fleet/app-service",
		DefaultBranch:                    "main",
		SquashOption:                     gitlab.SquashOptionValue("always"),
		MergeMethod:                      gitlab.MergeMethodValue("rebase_merge"),
		OnlyAllowMergeIfPipelineSucceeds: true,
		RemoveSourceBranchAfterMerge:     true,
		SharedRunnersEnabled:             true,
	}
	server.State().AddProject(proj)
	server.State().AddGroupProject(grp.ID, proj.ID)

	// 2. Push Rules
	server.State().SetProjectPushRule(proj.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@enterprise\\.com$",
		PreventSecrets:   true,
		MaxFileSize:      50,
	})

	// 3. Protected Branches
	server.State().ProtectBranch(proj.ID, &gitlab.ProtectedBranch{
		Name:                      "main",
		AllowForcePush:            false,
		CodeOwnerApprovalRequired: true,
		PushAccessLevels: []*gitlab.BranchAccessDescription{
			{AccessLevel: gitlab.NoPermissions},
		},
		MergeAccessLevels: []*gitlab.BranchAccessDescription{
			{AccessLevel: gitlab.DeveloperPermissions},
		},
	})

	// 4. Approval Rules & Configuration
	server.State().SetProjectApprovals(proj.ID, &gitlab.ProjectApprovals{
		ApprovalsBeforeMerge: 2,
		ResetApprovalsOnPush: true,
	})
	server.State().AddApprovalRule(proj.ID, &gitlab.ProjectApprovalRule{
		Name:              "Security Review",
		ApprovalsRequired: 1,
		RuleType:          "regular",
		Users: []*gitlab.BasicUser{
			{ID: 1, Username: "secops-admin"},
		},
	})

	// 5. Pipeline Retention
	server.State().SetPipelineRetention(proj.ID, 2592000) // 30 days in seconds

	// 6. CI/CD Variables
	server.State().SetProjectVariable(proj.ID, &gitlab.ProjectVariable{
		Key:              "DATABASE_URL",
		Value:            "postgres://user:pass@db:5432/app",
		Masked:           true,
		Protected:        true,
		EnvironmentScope: "*",
	})
	server.State().SetProjectVariable(proj.ID, &gitlab.ProjectVariable{
		Key:              "LOG_LEVEL",
		Value:            "info",
		Masked:           false,
		EnvironmentScope: "*",
	})

	// 7. Runners
	server.State().AddRunner(&gitlab.Runner{
		ID:          101,
		Description: "shared-docker-runner",
		Paused:      false,
	}, &gitlab.RunnerDetails{
		ID:          101,
		Description: "shared-docker-runner",
		AccessLevel: "not_protected",
		RunUntagged: true,
	})

	// 8. Compliance Framework
	server.State().SetComplianceFramework(proj.ID, mockserver.MockComplianceFramework{
		ID:   "gid://gitlab/ComplianceManagement::Framework/1",
		Name: "SOC2",
	})

	// 9. Webhooks
	server.State().AddProjectHook(proj.ID, &gitlab.ProjectHook{
		URL:                   "https://webhooks.enterprise.com/events",
		PushEvents:            true,
		MergeRequestsEvents:   true,
		EnableSSLVerification: true,
	})

	// 10. Members
	isoTime := gitlab.ISOTime(gitlab.ISOTime{})
	_ = isoTime.UnmarshalJSON([]byte(`"2027-12-31"`))
	server.State().AddProjectMember(proj.ID, &gitlab.ProjectMember{
		ID:          1,
		Username:    "secops-admin",
		AccessLevel: gitlab.MaintainerPermissions,
		ExpiresAt:   &isoTime,
	})

	// 11. Target Branch Rules
	server.State().AddTargetBranchRule(proj.ID, "feature/*", "develop")

	return server, proj
}

func TestExportFleetPolicy_SingleProject(t *testing.T) {
	ctx := context.Background()
	server, proj := setupMockServerWithFullProject(t)
	defer server.Close()

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	cfg := &config.PolicyConfig{
		Targets: config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				IDRange: &config.IDRange{
					Min: proj.ID,
					Max: proj.ID,
				},
			},
		},
	}

	opts := export.ExportOptions{
		Client:                    client,
		Config:                    cfg,
		IncludeSecretsPlaceholder: true,
		SkipReconcilers:           map[string]bool{},
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "version: v1")
	assert.Contains(t, yamlStr, "push_rules:")
	assert.Contains(t, yamlStr, "author_email_regex: '@enterprise\\.com$'")
	assert.Contains(t, yamlStr, "prevent_secrets: true")
	assert.Contains(t, yamlStr, "protected_branches:")
	assert.Contains(t, yamlStr, "name: main")
	assert.Contains(t, yamlStr, "code_owner_approval_required: true")
	assert.Contains(t, yamlStr, "project_settings:")
	assert.Contains(t, yamlStr, "default_branch: main")
	assert.Contains(t, yamlStr, "squash_option: always")
	assert.Contains(t, yamlStr, "merge_method: rebase_merge")
	assert.Contains(t, yamlStr, "pipeline_retention:")
	assert.Contains(t, yamlStr, "retention_days: 30")
	assert.Contains(t, yamlStr, "variables:")
	assert.Contains(t, yamlStr, "key: DATABASE_URL")
	assert.Contains(t, yamlStr, "${DATABASE_URL:-PLACEHOLDER}")
	assert.Contains(t, yamlStr, "compliance:")
	assert.Contains(t, yamlStr, "framework_name: SOC2")
	assert.Contains(t, yamlStr, "webhooks:")
	assert.Contains(t, yamlStr, "https://webhooks.enterprise.com/events")

	// Verify that exported YAML passes validate
	parsedCfg, err := config.LoadFromBytes(ctx, yamlBytes)
	require.NoError(t, err, "Exported policy must pass strict schema validation round-trip")
	assert.Equal(t, "v1", parsedCfg.Version)
	assert.NotNil(t, parsedCfg.Policies.PushRules)
}

func TestExportFleetPolicy_SkipReconcilers(t *testing.T) {
	ctx := context.Background()
	server, proj := setupMockServerWithFullProject(t)
	defer server.Close()

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	cfg := &config.PolicyConfig{
		Targets: config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				IDRange: &config.IDRange{
					Min: proj.ID,
					Max: proj.ID,
				},
			},
		},
	}

	opts := export.ExportOptions{
		Client:                    client,
		Config:                    cfg,
		IncludeSecretsPlaceholder: true,
		SkipReconcilers: map[string]bool{
			"variables": true,
			"webhooks":  true,
		},
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.NotContains(t, yamlStr, "DATABASE_URL")
	assert.NotContains(t, yamlStr, "https://webhooks.enterprise.com/events")
	assert.Contains(t, yamlStr, "push_rules:")
}

func TestExportFleetPolicy_MultiProject_DivergenceWarning(t *testing.T) {
	ctx := context.Background()
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	grp := &gitlab.Group{
		ID:       10,
		Name:     "enterprise-fleet",
		Path:     "enterprise-fleet",
		FullPath: "enterprise-fleet",
	}
	server.State().AddGroup(grp)

	proj1 := &gitlab.Project{
		ID:                101,
		Name:              "svc-a",
		Path:              "svc-a",
		PathWithNamespace: "enterprise-fleet/svc-a",
		DefaultBranch:     "main",
	}
	proj2 := &gitlab.Project{
		ID:                102,
		Name:              "svc-b",
		Path:              "svc-b",
		PathWithNamespace: "enterprise-fleet/svc-b",
		DefaultBranch:     "main",
	}
	server.State().AddProject(proj1)
	server.State().AddProject(proj2)
	server.State().AddGroupProject(grp.ID, proj1.ID)
	server.State().AddGroupProject(grp.ID, proj2.ID)

	// Divergent Push Rules
	server.State().SetProjectPushRule(proj1.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@enterprise\\.com$",
		PreventSecrets:   true,
	})
	server.State().SetProjectPushRule(proj2.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@subsidiary\\.com$",
		PreventSecrets:   false,
	})

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	rec := true
	cfg := &config.PolicyConfig{
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupPathsInclude: []string{"enterprise-fleet"},
				Recursive:         &rec,
			},
		},
	}

	var errBuf strings.Builder
	opts := export.ExportOptions{
		Client:                    client,
		Config:                    cfg,
		IncludeSecretsPlaceholder: true,
		SkipReconcilers:           map[string]bool{},
		ErrOut:                    &errBuf,
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "# WARNING: 2 projects have divergent push_rules — only first project's values shown")
	assert.Contains(t, errBuf.String(), "2 projects have divergent push_rules")

	// Ensure YAML still parses cleanly
	_, err = config.LoadFromBytes(ctx, yamlBytes)
	require.NoError(t, err)
}

func TestExportFleetPolicy_SecretPlaceholderDisabled(t *testing.T) {
	ctx := context.Background()
	server, proj := setupMockServerWithFullProject(t)
	defer server.Close()

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	cfg := &config.PolicyConfig{
		Targets: config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				IDRange: &config.IDRange{
					Min: proj.ID,
					Max: proj.ID,
				},
			},
		},
	}

	opts := export.ExportOptions{
		Client:                    client,
		Config:                    cfg,
		IncludeSecretsPlaceholder: false,
		SkipReconcilers:           map[string]bool{},
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "postgres://user:pass@db:5432/app")
	assert.NotContains(t, yamlStr, "${DATABASE_URL:-PLACEHOLDER}")
}

func TestExportToFile(t *testing.T) {
	tempDir := t.TempDir()
	outFile := filepath.Join(tempDir, "exported-policy.yaml")

	ctx := context.Background()
	server, proj := setupMockServerWithFullProject(t)
	defer server.Close()

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	cfg := &config.PolicyConfig{
		Targets: config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				IDRange: &config.IDRange{
					Min: proj.ID,
					Max: proj.ID,
				},
			},
		},
	}

	opts := export.ExportOptions{
		Client:                    client,
		Config:                    cfg,
		IncludeSecretsPlaceholder: true,
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	require.NoError(t, err)

	err = os.WriteFile(outFile, yamlBytes, 0644)
	require.NoError(t, err)

	fileBytes, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Contains(t, string(fileBytes), "version: v1")
}

func TestExportFleetPolicy_StrategyStrict_Error(t *testing.T) {
	ctx := context.Background()
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	grp := &gitlab.Group{
		ID:       10,
		Name:     "enterprise-fleet",
		Path:     "enterprise-fleet",
		FullPath: "enterprise-fleet",
	}
	server.State().AddGroup(grp)

	proj1 := &gitlab.Project{
		ID:                101,
		Name:              "svc-a",
		Path:              "svc-a",
		PathWithNamespace: "enterprise-fleet/svc-a",
		DefaultBranch:     "main",
	}
	proj2 := &gitlab.Project{
		ID:                102,
		Name:              "svc-b",
		Path:              "svc-b",
		PathWithNamespace: "enterprise-fleet/svc-b",
		DefaultBranch:     "main",
	}
	server.State().AddProject(proj1)
	server.State().AddProject(proj2)
	server.State().AddGroupProject(grp.ID, proj1.ID)
	server.State().AddGroupProject(grp.ID, proj2.ID)

	server.State().SetProjectPushRule(proj1.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@enterprise\\.com$",
	})
	server.State().SetProjectPushRule(proj2.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@subsidiary\\.com$",
	})

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	rec := true
	cfg := &config.PolicyConfig{
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupPathsInclude: []string{"enterprise-fleet"},
				Recursive:         &rec,
			},
		},
	}

	opts := export.ExportOptions{
		Client:   client,
		Config:   cfg,
		Strategy: export.StrategyStrict,
	}

	_, err = export.ExportFleetPolicy(ctx, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "strict export failed: 2 projects have divergent push_rules")
}

func TestExportFleetPolicy_StrategyConsensus_Omission(t *testing.T) {
	ctx := context.Background()
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	grp := &gitlab.Group{
		ID:       10,
		Name:     "enterprise-fleet",
		Path:     "enterprise-fleet",
		FullPath: "enterprise-fleet",
	}
	server.State().AddGroup(grp)

	proj1 := &gitlab.Project{
		ID:                101,
		Name:              "svc-a",
		Path:              "svc-a",
		PathWithNamespace: "enterprise-fleet/svc-a",
		DefaultBranch:     "main",
	}
	proj2 := &gitlab.Project{
		ID:                102,
		Name:              "svc-b",
		Path:              "svc-b",
		PathWithNamespace: "enterprise-fleet/svc-b",
		DefaultBranch:     "main",
	}
	server.State().AddProject(proj1)
	server.State().AddProject(proj2)
	server.State().AddGroupProject(grp.ID, proj1.ID)
	server.State().AddGroupProject(grp.ID, proj2.ID)

	// Divergent Push Rules (differ between proj1 and proj2)
	server.State().SetProjectPushRule(proj1.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@enterprise\\.com$",
	})
	server.State().SetProjectPushRule(proj2.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@subsidiary\\.com$",
	})

	// Identical MR Approvals
	server.State().SetProjectApprovals(proj1.ID, &gitlab.ProjectApprovals{
		ApprovalsBeforeMerge: 2,
	})
	server.State().SetProjectApprovals(proj2.ID, &gitlab.ProjectApprovals{
		ApprovalsBeforeMerge: 2,
	})

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	rec := true
	cfg := &config.PolicyConfig{
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupPathsInclude: []string{"enterprise-fleet"},
				Recursive:         &rec,
			},
		},
	}

	var errBuf strings.Builder
	opts := export.ExportOptions{
		Client:   client,
		Config:   cfg,
		Strategy: export.StrategyConsensus,
		ErrOut:   &errBuf,
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	require.NoError(t, err)

	// Under consensus strategy, divergent push_rules must be omitted from policy
	parsedCfg, err := config.LoadFromBytes(ctx, yamlBytes)
	require.NoError(t, err)
	assert.Nil(t, parsedCfg.Policies.PushRules, "divergent push rules should be omitted under consensus strategy")
	require.NotNil(t, parsedCfg.Policies.ApprovalRules, "consensus approval rules should be preserved")
	require.NotNil(t, parsedCfg.Policies.ApprovalRules.ApprovalsBeforeMerge)
	assert.Equal(t, 2, *parsedCfg.Policies.ApprovalRules.ApprovalsBeforeMerge)
	assert.Contains(t, errBuf.String(), "omitted under consensus strategy")
}

func TestExportFleetPolicy_FromProject_Archetype(t *testing.T) {
	ctx := context.Background()
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	grp := &gitlab.Group{
		ID:       10,
		Name:     "enterprise-fleet",
		Path:     "enterprise-fleet",
		FullPath: "enterprise-fleet",
	}
	server.State().AddGroup(grp)

	proj1 := &gitlab.Project{
		ID:                101,
		Name:              "svc-a",
		Path:              "svc-a",
		PathWithNamespace: "enterprise-fleet/svc-a",
		DefaultBranch:     "main",
	}
	proj2 := &gitlab.Project{
		ID:                102,
		Name:              "svc-b",
		Path:              "svc-b",
		PathWithNamespace: "enterprise-fleet/svc-b",
		DefaultBranch:     "main",
	}
	server.State().AddProject(proj1)
	server.State().AddProject(proj2)
	server.State().AddGroupProject(grp.ID, proj1.ID)
	server.State().AddGroupProject(grp.ID, proj2.ID)

	server.State().SetProjectPushRule(proj1.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@enterprise-a\\.com$",
	})
	server.State().SetProjectPushRule(proj2.ID, &gitlab.ProjectPushRules{
		AuthorEmailRegex: "@golden-template\\.com$",
	})

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	rec := true
	cfg := &config.PolicyConfig{
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupPathsInclude: []string{"enterprise-fleet"},
				Recursive:         &rec,
			},
		},
	}

	// 1. Valid archetype project ID
	opts := export.ExportOptions{
		Client:      client,
		Config:      cfg,
		Strategy:    export.StrategyArchetype,
		FromProject: "102",
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	require.NoError(t, err)

	parsedCfg, err := config.LoadFromBytes(ctx, yamlBytes)
	require.NoError(t, err)
	require.NotNil(t, parsedCfg.Policies.PushRules)
	assert.Equal(t, "@golden-template\\.com$", parsedCfg.Policies.PushRules.AuthorEmailRegex)

	// 2. Archetype project by path
	optsByPath := export.ExportOptions{
		Client:      client,
		Config:      cfg,
		Strategy:    export.StrategyArchetype,
		FromProject: "enterprise-fleet/svc-b",
	}
	yamlBytesByPath, err := export.ExportFleetPolicy(ctx, optsByPath)
	require.NoError(t, err)
	parsedCfgByPath, err := config.LoadFromBytes(ctx, yamlBytesByPath)
	require.NoError(t, err)
	require.NotNil(t, parsedCfgByPath.Policies.PushRules)
	assert.Equal(t, "@golden-template\\.com$", parsedCfgByPath.Policies.PushRules.AuthorEmailRegex)

	// 3. Invalid archetype returns error
	optsInvalid := export.ExportOptions{
		Client:      client,
		Config:      cfg,
		Strategy:    export.StrategyArchetype,
		FromProject: "non-existent-project",
	}
	_, err = export.ExportFleetPolicy(ctx, optsInvalid)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "from-project 'non-existent-project' not found")
}

func TestExportFleetPolicy_DirectProjectLookup(t *testing.T) {
	ctx := context.Background()
	server, proj := setupMockServerWithFullProject(t)
	defer server.Close()

	client, err := glclient.NewClientFromConfig(&config.GitLabSettingsConfig{
		BaseURL: server.BaseURL(),
		Token:   "mock-token",
	})
	require.NoError(t, err)

	opts := export.ExportOptions{
		Client:                    client,
		ProjectID:                 proj.ID,
		IncludeSecretsPlaceholder: true,
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	require.NoError(t, err)

	parsedCfg, err := config.LoadFromBytes(ctx, yamlBytes)
	require.NoError(t, err)
	require.NotNil(t, parsedCfg.Targets.ProjectSelector)
	require.NotNil(t, parsedCfg.Targets.ProjectSelector.IDRange)
	assert.Equal(t, proj.ID, parsedCfg.Targets.ProjectSelector.IDRange.Min)
	assert.Equal(t, proj.ID, parsedCfg.Targets.ProjectSelector.IDRange.Max)
	require.NotNil(t, parsedCfg.Policies.PushRules)
}
