package governance_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func TestRepositoryFilesReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewRepositoryFilesReconciler()
	ctx := context.Background()

	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor", DefaultBranch: "main"}

	t.Run("NameAndOrder", func(t *testing.T) {
		assert.Equal(t, "repository_files", reconciler.Name())
		assert.Equal(t, 15, reconciler.Order())
	})

	t.Run("NilPolicy_YieldsNoop", func(t *testing.T) {
		planRes, err := reconciler.Plan(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planRes.Action)
		assert.False(t, planRes.HasChanges)

		applyRes, err := reconciler.Apply(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
		assert.True(t, applyRes.Success)
	})

	t.Run("Group_Skipped", func(t *testing.T) {
		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{Path: "CODEOWNERS", Content: "* @security", TargetBranch: "main"},
				},
			},
		}

		planRes, err := reconciler.PlanGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, planRes.Action)

		applyRes, err := reconciler.ApplyGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, applyRes.Action)
	})

	t.Run("DirectCommit_Create_Update_And_Idempotency", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "CODEOWNERS",
						Content:      "* @security-team",
						TargetBranch: "main",
						Enforcement:  "direct_commit",
					},
				},
			},
		}

		// 1. Plan detects missing file -> ActionCreate
		plan1, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, plan1.Action)
		assert.True(t, plan1.HasChanges)

		// 2. Apply creates file directly
		apply1, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, apply1.Action)
		assert.True(t, apply1.Success)

		// 3. Verify live state
		content, found := srv.State().GetRawFile(101, "CODEOWNERS", "main")
		assert.True(t, found)
		assert.Contains(t, string(content), "* @security-team")
		assert.Contains(t, string(content), "Auto-managed by gitlab-fleet-governor")

		// 4. Re-plan is NOOP (Idempotency)
		plan2, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan2.Action)
		assert.False(t, plan2.HasChanges)

		// 5. Update policy content
		cfg2 := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "CODEOWNERS",
						Content:      "* @security-team @platform-leads",
						TargetBranch: "main",
						Enforcement:  "direct_commit",
					},
				},
			},
		}

		plan3, err := reconciler.Plan(ctx, client, proj, cfg2)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, plan3.Action)

		apply3, err := reconciler.Apply(ctx, client, proj, cfg2)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, apply3.Action)
		assert.True(t, apply3.Success)

		content2, _ := srv.State().GetRawFile(101, "CODEOWNERS", "main")
		assert.Contains(t, string(content2), "@platform-leads")
	})

	t.Run("MergeRequestFlow_DuplicateMRPrevention", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "SECURITY.md",
						Content:      "# Security Policy\nContact security@corp.com",
						TargetBranch: "main",
						Enforcement:  "merge_request",
						MRTitle:      "chore: sync SECURITY.md",
						MRLabels:     []string{"security", "governance"},
					},
				},
			},
		}

		// 1. Plan detects missing file
		plan1, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, plan1.Action)

		// 2. Apply creates branch and opens MR
		apply1, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, apply1.Action)
		assert.True(t, apply1.Success)

		mrs1 := srv.State().ListMergeRequests(101, "governance/sync-security-md", "main", "opened")
		require.Len(t, mrs1, 1)
		assert.Equal(t, "chore: sync SECURITY.md", mrs1[0].Title)

		// 3. Second Apply on same state should NOT create duplicate MR
		apply2, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, apply2.Success)

		mrs2 := srv.State().ListMergeRequests(101, "governance/sync-security-md", "main", "opened")
		assert.Len(t, mrs2, 1) // still exactly 1 MR
	})

	t.Run("EnsureContains_Matching", func(t *testing.T) {
		// Seed existing .gitlab-ci.yml without required include
		srv.State().SetFile(101, ".gitlab-ci.yml", "main", []byte("stages:\n  - test\n"))

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path: ".gitlab-ci.yml",
						EnsureContains: []string{
							"include:",
							"project: 'platform/ci-templates'",
						},
						TargetBranch: "main",
						Enforcement:  "direct_commit",
					},
				},
			},
		}

		plan1, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, plan1.Action)

		apply1, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, apply1.Success)

		updatedContent, _ := srv.State().GetRawFile(101, ".gitlab-ci.yml", "main")
		assert.Contains(t, string(updatedContent), "include:")
		assert.Contains(t, string(updatedContent), "project: 'platform/ci-templates'")

		// Re-plan should be NOOP
		plan2, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan2.Action)
	})

	t.Run("EnvVarSubstitution", func(t *testing.T) {
		t.Setenv("SEC_EMAIL", "sec-team@enterprise.com")

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "CONTACT.txt",
						Content:      "Security Team: ${SEC_EMAIL}",
						TargetBranch: "main",
						Enforcement:  "direct_commit",
					},
				},
			},
		}

		apply, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, apply.Success)

		content, _ := srv.State().GetRawFile(101, "CONTACT.txt", "main")
		assert.Contains(t, string(content), "sec-team@enterprise.com")
	})

	t.Run("ContentFile_LocalLoading", func(t *testing.T) {
		tmpDir := t.TempDir()
		templatePath := filepath.Join(tmpDir, "SECURITY.md.tmpl")
		err := os.WriteFile(templatePath, []byte("# Local Template\nSecurity Policy"), 0644)
		require.NoError(t, err)

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "TEMPLATED_SECURITY.md",
						ContentFile:  templatePath,
						TargetBranch: "main",
						Enforcement:  "direct_commit",
					},
				},
			},
		}

		apply, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, apply.Success)

		content, _ := srv.State().GetRawFile(101, "TEMPLATED_SECURITY.md", "main")
		assert.Contains(t, string(content), "Local Template")
	})

	t.Run("AuditOnly_NoMutationsPerformed", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "AUDIT_ONLY.txt",
						Content:      "Audit content",
						TargetBranch: "main",
						Enforcement:  "audit_only",
					},
				},
			},
		}

		plan, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, plan.Action)

		apply, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, apply.Success)

		// File should NOT exist in state because mode is audit_only
		_, found := srv.State().GetRawFile(101, "AUDIT_ONLY.txt", "main")
		assert.False(t, found)
	})

	t.Run("TargetBranch_DynamicFallback_ToProjectDefaultBranch", func(t *testing.T) {
		customProj := &gogitlab.Project{ID: 102, PathWithNamespace: "platform/custom-repo", DefaultBranch: "trunk"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:        "README.md",
						Content:     "# Custom Project",
						Enforcement: "direct_commit",
						// TargetBranch omitted to verify dynamic fallback to customProj.DefaultBranch ("trunk")
					},
				},
			},
		}

		apply, err := reconciler.Apply(ctx, client, customProj, cfg)
		require.NoError(t, err)
		assert.True(t, apply.Success)

		// Verify file was committed to "trunk"
		content, found := srv.State().GetRawFile(102, "README.md", "trunk")
		assert.True(t, found)
		assert.Contains(t, string(content), "# Custom Project")
	})

	t.Run("LineEnding_Normalization_CRLF_vs_LF", func(t *testing.T) {
		// Seed file with CRLF line endings
		crlfContent := "# Policy File\r\nline1\r\nline2\r\n"
		srv.State().SetFile(101, "CRLF.txt", "main", []byte(crlfContent))

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "CRLF.txt",
						Content:      "# Policy File\nline1\nline2\n",
						TargetBranch: "main",
						Enforcement:  "audit_only",
					},
				},
			},
		}

		plan, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan.Action, "CRLF and LF differences should normalize and yield zero drift")
		assert.False(t, plan.HasChanges)
	})

	t.Run("MergeRequest_ExistingBranch_OutdatedContent_UpdatesExistingBranchWithoutDuplicateMR", func(t *testing.T) {
		cfg1 := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "FEATURE.md",
						Content:      "# Version 1",
						TargetBranch: "main",
						Enforcement:  "merge_request",
						MRTitle:      "chore: sync FEATURE.md",
					},
				},
			},
		}

		// Initial apply creates branch and MR
		apply1, err := reconciler.Apply(ctx, client, proj, cfg1)
		require.NoError(t, err)
		assert.True(t, apply1.Success)

		mrs1 := srv.State().ListMergeRequests(101, "governance/sync-feature-md", "main", "opened")
		require.Len(t, mrs1, 1)

		// Now update policy content
		cfg2 := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "FEATURE.md",
						Content:      "# Version 2",
						TargetBranch: "main",
						Enforcement:  "merge_request",
						MRTitle:      "chore: sync FEATURE.md",
					},
				},
			},
		}

		// Plan detects outdated branch content
		plan2, err := reconciler.Plan(ctx, client, proj, cfg2)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, plan2.Action)

		// Apply updates the existing feature branch without creating a duplicate MR
		apply2, err := reconciler.Apply(ctx, client, proj, cfg2)
		require.NoError(t, err)
		assert.True(t, apply2.Success)

		mrs2 := srv.State().ListMergeRequests(101, "governance/sync-feature-md", "main", "opened")
		assert.Len(t, mrs2, 1, "Must not create duplicate MR")

		// Verify feature branch content updated to Version 2
		featContent, found := srv.State().GetRawFile(101, "FEATURE.md", "governance/sync-feature-md")
		assert.True(t, found)
		assert.Contains(t, string(featContent), "# Version 2")
	})

	t.Run("ProtectedBranch_DirectCommit_HTTP403_ActionableError", func(t *testing.T) {
		// Protect branch "protected-release" on project 101 with allowed_to_push = 0
		srv.State().ProtectBranch(101, &gogitlab.ProtectedBranch{
			ID:   2,
			Name: "protected-release",
			PushAccessLevels: []*gogitlab.BranchAccessDescription{
				{AccessLevel: gogitlab.NoPermissions, AccessLevelDescription: "No access"},
			},
		})

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "RESTRICTED.txt",
						Content:      "Protected content",
						TargetBranch: "protected-release",
						Enforcement:  "direct_commit",
					},
				},
			},
		}

		apply, err := reconciler.Apply(ctx, client, proj, cfg)
		require.Error(t, err)
		assert.False(t, apply.Success)
		assert.Contains(t, err.Error(), "HTTP 403 Forbidden")
		assert.Contains(t, err.Error(), "push permissions are restricted; please use 'enforcement: merge_request' for protected branches")
	})
}
