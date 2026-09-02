package governance_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ============================================================================
// 1. Push Rules: 404 Creation -> 200 Update -> NOOP Idempotency & Error Paths
// ============================================================================

func TestAdversarial_PushRules_LifecycleIdempotency(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewPushRulesReconciler()
	ctx := context.Background()

	t.Run("Project_404_Create_Then_NOOP_Then_200_Update_Then_NOOP", func(t *testing.T) {
		// Project 103 has no push rules seeded (clean 404)
		proj := &gogitlab.Project{ID: 103, PathWithNamespace: "platform/billing-service"}

		desiredPolicy := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex:           `^[a-zA-Z0-9._%+-]+@corp\.example\.com$`,
					BranchNameRegex:            `^(main|release/v[0-9]+\.[0-9]+|hotfix/.*)$`,
					CommitMessageRegex:         `^(feat|fix|docs|chore|refactor)\([a-z-]+\):\s.+`,
					CommitMessageNegativeRegex: `(?i)wip|do not merge|fixup!`,
					FileNameRegex:              `(\.pem|\.key|\.id_rsa|\.pfx)$`,
					MaxFileSize:                gogitlab.Ptr(25),
					CommitCommitterCheck:       gogitlab.Ptr(true),
					MemberCheck:                gogitlab.Ptr(true),
					PreventSecrets:             gogitlab.Ptr(true),
					DenyDeleteTag:              gogitlab.Ptr(true),
					RejectUnsignedCommits:      gogitlab.Ptr(true),
					RejectNonDCOCommits:        gogitlab.Ptr(true),
				},
			},
		}

		// Step 1: Initial Plan on 404 resource must indicate ActionCreate
		plan1, err := reconciler.Plan(ctx, client, proj, desiredPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, plan1.Action)
		assert.True(t, plan1.HasChanges)
		require.Len(t, plan1.Diffs, 1)
		assert.Equal(t, "push_rule", plan1.Diffs[0].Resource)
		assert.GreaterOrEqual(t, len(plan1.Diffs[0].Fields), 10)

		// Step 2: Apply 404 resource must create via AddProjectPushRule (POST)
		apply1, err := reconciler.Apply(ctx, client, proj, desiredPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, apply1.Action)
		assert.Equal(t, governance.StatusSuccess, apply1.Status)
		assert.True(t, apply1.Success)

		// Step 3: Immediate Plan must be NOOP (idempotent)
		plan2, err := reconciler.Plan(ctx, client, proj, desiredPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan2.Action)
		assert.False(t, plan2.HasChanges)

		// Step 4: Immediate Apply must be NOOP
		apply2, err := reconciler.Apply(ctx, client, proj, desiredPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, apply2.Action)
		assert.Equal(t, governance.StatusNoop, apply2.Status)
		assert.True(t, apply2.Success)

		// Step 5: Update policy configuration (triggering 200 update)
		updatedPolicy := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex:           `^[a-zA-Z0-9._%+-]+@corp\.example\.com$`,
					BranchNameRegex:            `^(main|release/.*)$`, // Changed regex
					CommitMessageRegex:         `^(feat|fix|docs|chore|refactor)\([a-z-]+\):\s.+`,
					CommitMessageNegativeRegex: `(?i)wip|do not merge|fixup!`,
					FileNameRegex:              `(\.pem|\.key|\.id_rsa|\.pfx)$`,
					MaxFileSize:                gogitlab.Ptr(100), // Changed 25 -> 100
					CommitCommitterCheck:       gogitlab.Ptr(true),
					MemberCheck:                gogitlab.Ptr(true),
					PreventSecrets:             gogitlab.Ptr(false), // Changed true -> false
					DenyDeleteTag:              gogitlab.Ptr(true),
					RejectUnsignedCommits:      gogitlab.Ptr(true),
					RejectNonDCOCommits:        gogitlab.Ptr(true),
				},
			},
		}

		plan3, err := reconciler.Plan(ctx, client, proj, updatedPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, plan3.Action)
		assert.True(t, plan3.HasChanges)
		require.Len(t, plan3.Diffs, 1)
		assert.Len(t, plan3.Diffs[0].Fields, 3, "Expected exactly 3 drifted fields: branch_name_regex, max_file_size, prevent_secrets")

		// Step 6: Apply update must modify via EditProjectPushRule (PUT)
		apply3, err := reconciler.Apply(ctx, client, proj, updatedPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, apply3.Action)
		assert.Equal(t, governance.StatusSuccess, apply3.Status)
		assert.True(t, apply3.Success)

		// Step 7: Subsequent Plan must be NOOP again
		plan4, err := reconciler.Plan(ctx, client, proj, updatedPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan4.Action)
		assert.False(t, plan4.HasChanges)
	})

	t.Run("Group_404_Create_Then_NOOP_Then_200_Update_Then_NOOP", func(t *testing.T) {
		// Group 30 has no push rule in seed
		grp := &gogitlab.Group{ID: 30, FullPath: "infrastructure"}

		groupPolicy := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex:      `@infra\.example\.com$`,
					PreventSecrets:        gogitlab.Ptr(true),
					RejectUnsignedCommits: gogitlab.Ptr(true),
				},
			},
		}

		// 1. Plan 404 -> CREATE
		plan1, err := reconciler.PlanGroup(ctx, client, grp, groupPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, plan1.Action)

		// 2. Apply -> CREATE
		apply1, err := reconciler.ApplyGroup(ctx, client, grp, groupPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, apply1.Action)
		assert.True(t, apply1.Success)

		// 3. Plan -> NOOP
		plan2, err := reconciler.PlanGroup(ctx, client, grp, groupPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan2.Action)

		// 4. Update -> UPDATE
		updatedGroupPolicy := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex:      `@infra\.example\.com$`,
					PreventSecrets:        gogitlab.Ptr(true),
					RejectUnsignedCommits: gogitlab.Ptr(false), // changed
				},
			},
		}

		plan3, err := reconciler.PlanGroup(ctx, client, grp, updatedGroupPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, plan3.Action)

		apply3, err := reconciler.ApplyGroup(ctx, client, grp, updatedGroupPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, apply3.Action)
		assert.True(t, apply3.Success)

		// 5. Subsequent Plan -> NOOP
		plan4, err := reconciler.PlanGroup(ctx, client, grp, updatedGroupPolicy)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan4.Action)
	})
}

// ============================================================================
// 2. Protected Branches: Wildcards, Access Levels, Re-creation vs PATCH
// ============================================================================

func TestAdversarial_ProtectedBranches_WildcardsAndPermissions(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has "main"

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewProtectedBranchesReconciler()
	ctx := context.Background()
	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

	t.Run("MultipleWildcards_And_ComplexPermissions", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "main",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 40}, // Maintainer
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 30}, // Developer
						},
						AllowForcePush:            gogitlab.Ptr(false),
						CodeOwnerApprovalRequired: gogitlab.Ptr(true),
					},
					{
						Name: "release/*",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 40},
							{UserID: 1}, // Specific user
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowForcePush:            gogitlab.Ptr(false),
						CodeOwnerApprovalRequired: gogitlab.Ptr(true),
					},
					{
						Name: "hotfix/v*.*",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowForcePush:            gogitlab.Ptr(false),
						CodeOwnerApprovalRequired: gogitlab.Ptr(false),
					},
				},
			},
		}

		// Plan: 'main' already exists (seeded) with code_owner_approval_required=true, so main is NOOP
		// 'release/*' and 'hotfix/v*.*' need to be CREATED
		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, planRes.Action)
		assert.True(t, planRes.HasChanges)
		require.Len(t, planRes.Diffs, 2, "Expected 2 new wildcard branch rules to create")

		// Apply creation
		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, applyRes.Action)
		assert.True(t, applyRes.Success)

		// Subsequent Plan must be NOOP
		planAfter, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
		assert.False(t, planAfter.HasChanges)
	})

	t.Run("PATCH_Optimization_When_Only_CodeOwner_Changes", func(t *testing.T) {
		// Update only CodeOwnerApprovalRequired on "hotfix/v*.*"
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "hotfix/v*.*",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowForcePush:            gogitlab.Ptr(false),
						CodeOwnerApprovalRequired: gogitlab.Ptr(true), // Changed false -> true
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)
		require.Len(t, planRes.Diffs, 1)
		assert.Len(t, planRes.Diffs[0].Fields, 1)
		assert.Equal(t, "code_owner_approval_required", planRes.Diffs[0].Fields[0].Field)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)
		assert.True(t, applyRes.Success)
	})

	t.Run("Recreation_When_AccessLevels_Change", func(t *testing.T) {
		// Update push access level on "hotfix/v*.*" (requires atomic unprotect -> protect recreation)
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "hotfix/v*.*",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 0}, // No one allowed to push
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowForcePush:            gogitlab.Ptr(false),
						CodeOwnerApprovalRequired: gogitlab.Ptr(true),
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)
		assert.True(t, applyRes.Success)

		// Subsequent plan is NOOP
		planAfter, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
	})
}

// ============================================================================
// 3. CI/CD Variables: Scoped Collisions, Secret Redaction & Pruning
// ============================================================================

func TestAdversarial_Variables_ScopedCollisionsRedactionAndPruning(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has AWS_REGION="us-east-1" (scope: *)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconcilerWithPruning := governance.NewVariablesReconciler(true)
	reconcilerNoPruning := governance.NewVariablesReconciler(false)
	ctx := context.Background()
	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

	t.Run("Scoped_Collisions_Multiple_Scopes_For_Same_Key", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{
						Key:              "DATABASE_URL",
						Value:            "postgres://default:pass@localhost:5432/db",
						EnvironmentScope: "*",
						Protected:        gogitlab.Ptr(true),
						Masked:           gogitlab.Ptr(false),
					},
					{
						Key:              "DATABASE_URL",
						Value:            "postgres://prod-admin:supersecret@prod-rds:5432/proddb",
						EnvironmentScope: "production",
						Protected:        gogitlab.Ptr(true),
						Masked:           gogitlab.Ptr(false),
					},
					{
						Key:              "DATABASE_URL",
						Value:            "postgres://stage-user:stagepass@stage-rds:5432/stagedb",
						EnvironmentScope: "staging",
						Protected:        gogitlab.Ptr(false),
						Masked:           gogitlab.Ptr(false),
					},
					{
						Key:              "DATABASE_URL",
						Value:            "postgres://review:pass@review-k8s:5432/reviewdb",
						EnvironmentScope: "review/*",
						Protected:        gogitlab.Ptr(false),
						Masked:           gogitlab.Ptr(false),
					},
				},
			},
		}

		// Plan: all 4 scoped variants of DATABASE_URL are new -> ActionUpdate (with 4 Create diffs)
		planRes, err := reconcilerNoPruning.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)
		assert.True(t, planRes.HasChanges)

		// Count diffs for DATABASE_URL
		dbDiffs := 0
		for _, d := range planRes.Diffs {
			if d.Resource == "variable:DATABASE_URL::*" ||
				d.Resource == "variable:DATABASE_URL::production" ||
				d.Resource == "variable:DATABASE_URL::staging" ||
				d.Resource == "variable:DATABASE_URL::review/*" {
				dbDiffs++
			}
		}
		assert.Equal(t, 4, dbDiffs, "Must create separate diffs for each composite key (key::scope)")

		// Apply all 4 scoped variables
		applyRes, err := reconcilerNoPruning.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)
		assert.True(t, applyRes.Success)

		// Verify that all 4 scoped variables exist independently in live state
		liveVars, _, err := client.Variables().ListProjectVariables(proj.ID, nil, gogitlab.WithContext(ctx))
		require.NoError(t, err)

		foundScopes := make(map[string]string)
		for _, lv := range liveVars {
			if lv.Key == "DATABASE_URL" {
				foundScopes[lv.EnvironmentScope] = lv.Value
			}
		}
		assert.Equal(t, 4, len(foundScopes), "Expected 4 distinct scoped instances of DATABASE_URL")
		assert.Equal(t, "postgres://default:pass@localhost:5432/db", foundScopes["*"])
		assert.Equal(t, "postgres://prod-admin:supersecret@prod-rds:5432/proddb", foundScopes["production"])
		assert.Equal(t, "postgres://stage-user:stagepass@stage-rds:5432/stagedb", foundScopes["staging"])
		assert.Equal(t, "postgres://review:pass@review-k8s:5432/reviewdb", foundScopes["review/*"])
	})

	t.Run("Secret_Masking_In_Diffs_Verification", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{
						Key:              "API_SECRET_TOKEN",
						Value:            "ghp_999999999999999999999999",
						EnvironmentScope: "*",
						Masked:           gogitlab.Ptr(true),
						Protected:        gogitlab.Ptr(true),
					},
					{
						Key:              "DEPLOYMENT_PASSWORD",
						Value:            "SuperDuperSecretPass123!",
						EnvironmentScope: "production",
						Masked:           gogitlab.Ptr(true),
					},
					{
						Key:              "PUBLIC_APP_TITLE",
						Value:            "My Awesome Platform",
						EnvironmentScope: "*",
						Masked:           gogitlab.Ptr(false),
					},
				},
			},
		}

		planRes, err := reconcilerNoPruning.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		require.NotNil(t, planRes)

		// Check every diff string representation
		for _, d := range planRes.Diffs {
			diffPlain := d.String()
			diffColored := d.ColoredString()

			// Secrets MUST NOT be exposed
			assert.NotContains(t, diffPlain, "ghp_999999999999999999999999", "Token must be redacted in plain diff")
			assert.NotContains(t, diffColored, "ghp_999999999999999999999999", "Token must be redacted in colored diff")
			assert.NotContains(t, diffPlain, "SuperDuperSecretPass123!", "Password must be redacted in plain diff")
			assert.NotContains(t, diffColored, "SuperDuperSecretPass123!", "Password must be redacted in colored diff")

			// Redaction placeholder '******' MUST be present for secret variables
			if d.Resource == "variable:API_SECRET_TOKEN::*" || d.Resource == "variable:DEPLOYMENT_PASSWORD::production" {
				assert.Contains(t, diffPlain, "******")
			}

			// Public non-secret variable value CAN be shown
			if d.Resource == "variable:PUBLIC_APP_TITLE::*" {
				assert.Contains(t, diffPlain, "My Awesome Platform")
			}
		}
	})

	t.Run("Unmanaged_Variable_Pruning_Safety", func(t *testing.T) {
		// We declare ONLY "DATABASE_URL" (scope: production).
		// Live state contains AWS_REGION, DATABASE_URL (*), DATABASE_URL (staging), etc.
		// When pruneUnmanaged = true, all unmanaged variables must be pruned,
		// but the managed "DATABASE_URL::production" must remain.
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{
						Key:              "DATABASE_URL",
						Value:            "postgres://prod-admin:supersecret@prod-rds:5432/proddb",
						EnvironmentScope: "production",
						Protected:        gogitlab.Ptr(true),
					},
				},
			},
		}

		planRes, err := reconcilerWithPruning.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		require.NotEmpty(t, planRes.Diffs)

		// Verify delete actions exist in plan
		deleteCount := 0
		for _, d := range planRes.Diffs {
			if d.Action == governance.ActionDelete {
				deleteCount++
				// Must not be our managed production DB URL
				assert.NotEqual(t, "variable:DATABASE_URL::production", d.Resource)
			}
		}
		assert.Greater(t, deleteCount, 0, "Pruning must plan delete actions for unmanaged variables")

		// Apply pruning
		applyRes, err := reconcilerWithPruning.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// Verify live state after pruning
		liveVarsAfter, _, err := client.Variables().ListProjectVariables(proj.ID, nil, gogitlab.WithContext(ctx))
		require.NoError(t, err)
		require.Len(t, liveVarsAfter, 1, "Only the single managed variable should remain after pruning")
		assert.Equal(t, "DATABASE_URL", liveVarsAfter[0].Key)
		assert.Equal(t, "production", liveVarsAfter[0].EnvironmentScope)

		// Second plan must be clean NOOP
		planAfter, err := reconcilerWithPruning.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
	})
}

// ============================================================================
// 4. Approval Rules: Caching Resolver Concurrency & Inverted Booleans
// ============================================================================

func TestAdversarial_ApprovalRules_CachingAndBooleans(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Seeds users (admin, alice, bob) and group (platform)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	resolver := governance.NewCachingResolver()
	ctx := context.Background()

	t.Run("CachingResolver_HighConcurrency_RaceFreedom", func(t *testing.T) {
		const goroutines = 50
		var wg sync.WaitGroup
		wg.Add(goroutines * 2)

		errCh := make(chan error, goroutines*2)

		// Concurrently resolve usernames
		for i := 0; i < goroutines; i++ {
			go func(idx int) {
				defer wg.Done()
				user := "alice"
				if idx%2 == 0 {
					user = "@bob"
				}
				uid, err := resolver.ResolveUsername(ctx, client, user)
				if err != nil {
					errCh <- err
					return
				}
				if uid <= 0 {
					errCh <- fmt.Errorf("unexpected uid %d", uid)
				}
			}(i)
		}

		// Concurrently resolve group paths
		for i := 0; i < goroutines; i++ {
			go func(idx int) {
				defer wg.Done()
				grpPath := "platform"
				if idx%2 == 0 {
					grpPath = "/platform/"
				}
				gid, err := resolver.ResolveGroupPath(ctx, client, grpPath)
				if err != nil {
					errCh <- err
					return
				}
				if gid <= 0 {
					errCh <- fmt.Errorf("unexpected gid %d", gid)
				}
			}(i)
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			require.NoError(t, err)
		}
	})

	t.Run("MR_Approvals_InvertedBooleans_And_NamedRules_DriftPruning", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		reconciler := governance.NewApprovalRulesReconciler(resolver)

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ApprovalRules: &config.ApprovalRulesConfig{
					Settings: &config.ApprovalSettingsConfig{
						AllowAuthorApproval:                         gogitlab.Ptr(true),
						AllowCommitterApproval:                      gogitlab.Ptr(false), // Inverted in GitLab API
						AllowOverridesToApproverListPerMergeRequest: gogitlab.Ptr(false), // Inverted
						RetainApprovalsOnPush:                       gogitlab.Ptr(true),  // Inverted
						SelectiveCodeOwnerRemovals:                  gogitlab.Ptr(true),
						RequirePasswordToApprove:                    gogitlab.Ptr(false),
					},
					Rules: []config.ApprovalRuleConfig{
						{
							Name:              "Security & Compliance Approval",
							ApprovalsRequired: 2,
							UserUsernames:     []string{"alice", "bob"},
							GroupPaths:        []string{"platform"},
						},
						{
							Name:              "Architecture Lead",
							ApprovalsRequired: 1,
							UserUsernames:     []string{"admin"},
						},
					},
					Prune: gogitlab.Ptr(true),
				},
			},
		}

		// 1. Plan
		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)
		assert.True(t, planRes.HasChanges)

		// 2. Apply
		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)
		assert.True(t, applyRes.Success)

		// 3. Subsequent Plan must be NOOP
		planAfter, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
		assert.False(t, planAfter.HasChanges)
	})
}

// ============================================================================
// 5. Project Settings & Pipeline Retention: Boundary Values & Conversion
// ============================================================================

func TestAdversarial_ProjectSettings_And_PipelineRetention(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)
	ctx := context.Background()
	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

	t.Run("PipelineRetention_Conversion_And_Idempotency", func(t *testing.T) {
		retentionReconciler := governance.NewPipelineRetentionReconciler()

		// 30 days = 30 * 86400 = 2,592,000 seconds
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: 30,
				},
			},
		}

		planRes, err := retentionReconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)
		require.Len(t, planRes.Diffs, 1)
		assert.Equal(t, 2592000, planRes.Diffs[0].Fields[0].NewValue)

		applyRes, err := retentionReconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// Verify project live state
		liveSeconds, _, err := client.Projects().GetProjectPipelineRetention(proj.ID, gogitlab.WithContext(ctx))
		require.NoError(t, err)
		assert.Equal(t, 2592000, liveSeconds)

		// Subsequent plan must be NOOP
		planAfter, err := retentionReconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
	})

	t.Run("ProjectSettings_Comprehensive_Enforcement", func(t *testing.T) {
		settingsReconciler := governance.NewProjectSettingsReconciler()

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProjectSettings: &config.ProjectSettingsConfig{
					DefaultBranch:                             "main",
					SquashOption:                              "always",
					MergeMethod:                               "rebase_merge",
					OnlyAllowMergeIfPipelineSucceeds:          gogitlab.Ptr(true),
					AllowMergeOnSkippedPipeline:               gogitlab.Ptr(false),
					OnlyAllowMergeIfAllDiscussionsAreResolved: gogitlab.Ptr(true),
					RemoveSourceBranchAfterMerge:              gogitlab.Ptr(true),
					KeepLatestArtifact:                        gogitlab.Ptr(true),
					PrintingMergeRequestLinkEnabled:           gogitlab.Ptr(true),
					AutoCancelPendingPipelines:                "enabled",
					AutoDevopsEnabled:                         gogitlab.Ptr(false),
				},
			},
		}

		planRes, err := settingsReconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)

		applyRes, err := settingsReconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		planAfter, err := settingsReconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
	})
}

// ============================================================================
// 6. OperationsRegistry: Full Concurrent Execution & Zero Race Conditions
// ============================================================================

func TestAdversarial_OperationsRegistry_HighConcurrencyAndOrdering(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)
	registry := governance.NewDefaultRegistry(client)

	t.Run("Registry_Ordering_Invariant", func(t *testing.T) {
		orderedOps := registry.OrderedOperations()
		require.Len(t, orderedOps, 10)

		expectedOrders := []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
		for i, op := range orderedOps {
			assert.Equal(t, expectedOrders[i], op.Order(), "Operation %s out of order", op.Name())
		}
	})

	t.Run("Concurrent_Plan_And_Apply_ZeroRaceConditions", func(t *testing.T) {
		ctx := context.Background()
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex: `@example\.com$`,
					PreventSecrets:   gogitlab.Ptr(true),
				},
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "main",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 30},
						},
					},
				},
				Variables: []config.VariableConfig{
					{
						Key:              "CONCURRENT_VAR",
						Value:            "secret_pass_123",
						EnvironmentScope: "*",
						Masked:           gogitlab.Ptr(true),
					},
				},
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: 14,
				},
			},
		}

		targetProjects := []*discovery.TargetProject{
			{ID: 101, Name: "fleet-governor", PathWithNamespace: "platform/fleet-governor"},
			{ID: 102, Name: "cloud-infra", PathWithNamespace: "platform/core/cloud-infra"},
			{ID: 103, Name: "billing-service", PathWithNamespace: "platform/billing-service"},
		}

		const workers = 30
		var wg sync.WaitGroup
		wg.Add(workers)

		errCh := make(chan error, workers)

		for i := 0; i < workers; i++ {
			targetProj := targetProjects[i%len(targetProjects)]
			go func(tp *discovery.TargetProject, idx int) {
				defer wg.Done()
				if idx%2 == 0 {
					// Plan target project
					planResults, err := registry.PlanTargetProject(ctx, tp, cfg)
					if err != nil {
						errCh <- err
						return
					}
					if len(planResults) == 0 {
						errCh <- fmt.Errorf("empty plan results for target project %d", tp.ID)
					}
				} else {
					// Apply target project
					applyResults, err := registry.ApplyTargetProject(ctx, tp, cfg)
					if err != nil {
						errCh <- err
						return
					}
					if len(applyResults) == 0 {
						errCh <- fmt.Errorf("empty apply results for target project %d", tp.ID)
					}
				}
			}(targetProj, i)
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			require.NoError(t, err)
		}
	})

	t.Run("Context_Cancellation_Aborts_Gracefully", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex: `@corp\.com$`,
				},
			},
		}

		results, err := registry.PlanProject(cancelCtx, proj, cfg)
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, cancelCtx.Err())
		_ = results
	})
}
