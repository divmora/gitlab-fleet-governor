package governance_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ============================================================================
// 1. Adversarial CachingResolver Race Safety & High-Concurrency Stress Test
// ============================================================================

func TestAdversarial_CachingResolver_Concurrency(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	resolver := governance.NewCachingResolver()
	ctx := context.Background()

	// 50 users and 20 groups across 100 concurrent workers
	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	usernames := []string{"alice", "@Alice", "BOB", "@bob", "carol", "Alice", "@carol"}
	groupPaths := []string{"platform", "/Platform/", "security", "platform/core", "SECURITY"}

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			user := usernames[idx%len(usernames)]
			group := groupPaths[idx%len(groupPaths)]

			uid, uErr := resolver.ResolveUsername(ctx, client, user)
			assert.NoError(t, uErr)
			assert.Positive(t, uid)

			gid, gErr := resolver.ResolveGroupPath(ctx, client, group)
			assert.NoError(t, gErr)
			assert.Positive(t, gid)
		}(i)
	}

	wg.Wait()

	// Verify cache consistency
	uidAlice, err := resolver.ResolveUsername(ctx, client, "alice")
	require.NoError(t, err)
	assert.Equal(t, 1, uidAlice)

	gidPlatform, err := resolver.ResolveGroupPath(ctx, client, "platform")
	require.NoError(t, err)
	assert.Equal(t, 10, gidPlatform)
}

// ============================================================================
// 2. Adversarial Pipeline Retention Seconds Math & Boundary Conditions
// ============================================================================

func TestAdversarial_PipelineRetention_MathAndBoundaries(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewPipelineRetentionReconciler()
	ctx := context.Background()

	testCases := []struct {
		name            string
		initialSeconds  int
		retentionDays   int
		expectedSeconds int
		expectChanges   bool
	}{
		{
			name:            "ZeroDays_DisableRetention",
			initialSeconds:  2592000, // 30 days
			retentionDays:   0,
			expectedSeconds: 0,
			expectChanges:   true,
		},
		{
			name:            "OneDay_86400Seconds",
			initialSeconds:  0,
			retentionDays:   1,
			expectedSeconds: 86400,
			expectChanges:   true,
		},
		{
			name:            "30Days_2592000Seconds",
			initialSeconds:  86400,
			retentionDays:   30,
			expectedSeconds: 2592000,
			expectChanges:   true,
		},
		{
			name:            "365Days_31536000Seconds",
			initialSeconds:  2592000,
			retentionDays:   365,
			expectedSeconds: 31536000,
			expectChanges:   true,
		},
		{
			name:            "NonStandardLiveSeconds_OddOffset",
			initialSeconds:  3600, // 1 hour
			retentionDays:   7,
			expectedSeconds: 604800,
			expectChanges:   true,
		},
		{
			name:            "ExactMatch_NoChanges",
			initialSeconds:  604800,
			retentionDays:   7,
			expectedSeconds: 604800,
			expectChanges:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := &gogitlab.Project{
				ID:                101,
				PathWithNamespace: "platform/fleet-governor",
			}
			// Set initial seconds on mock server state
			srv.State().SetPipelineRetention(101, tc.initialSeconds)

			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PipelineRetention: &config.PipelineRetentionConfig{
						RetentionDays: tc.retentionDays,
					},
				},
			}

			planRes, err := reconciler.Plan(ctx, client, proj, cfg)
			require.NoError(t, err)
			assert.Equal(t, tc.expectChanges, planRes.HasChanges)

			if tc.expectChanges {
				assert.Equal(t, governance.ActionUpdate, planRes.Action)
				require.Len(t, planRes.Diffs, 1)

				applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
				require.NoError(t, err)
				assert.True(t, applyRes.Success)

				// Verify live state matches expected seconds
				retention := srv.State().GetPipelineRetention(101)
				assert.Equal(t, tc.expectedSeconds, retention)

				// Subsequent plan must be ActionNoop
				planAfter, err := reconciler.Plan(ctx, client, proj, cfg)
				require.NoError(t, err)
				assert.Equal(t, governance.ActionNoop, planAfter.Action)
				assert.False(t, planAfter.HasChanges)
			}
		})
	}
}

// ============================================================================
// 3. Adversarial Webhooks Drift Pruning & URL Normalization
// ============================================================================

func TestAdversarial_Webhooks_NormalizationAndPruning(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("URLNormalization_AvoidsFalsePositives", func(t *testing.T) {
		// Mock server has seeded hook: "https://webhook.fleetcorp.com/events"
		reconciler := governance.NewWebhooksReconciler(true)
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

		// Policy defines same URL with uppercase scheme/host and trailing slash
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Webhooks: []config.WebhookConfig{
					{
						URL:                   "HTTPS://Webhook.FleetCorp.Com/events/",
						PushEvents:            gogitlab.Ptr(true),
						EnableSSLVerification: gogitlab.Ptr(true),
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		// Should NOT create duplicate hook; should update if attributes differ or noop if identical
		for _, d := range planRes.Diffs {
			assert.NotEqual(t, governance.ActionCreate, d.Action, "Normalized URL should have matched existing webhook")
		}
	})

	t.Run("PruneUnmanaged_DeletesOnlyUnmanagedHooks", func(t *testing.T) {
		// Seed 3 extra hooks on project 101
		srv.State().AddProjectHook(101, &gogitlab.ProjectHook{
			ID:  201,
			URL: "https://legacy-tool.company.com/webhook",
		})
		srv.State().AddProjectHook(101, &gogitlab.ProjectHook{
			ID:  202,
			URL: "https://unmanaged-slack.company.com/alerts",
		})

		reconciler := governance.NewWebhooksReconciler(true)
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

		// Policy only manages the official webhook
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Webhooks: []config.WebhookConfig{
					{
						URL:        "https://webhook.fleetcorp.com/events",
						PushEvents: gogitlab.Ptr(true),
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)

		deleteCount := 0
		for _, d := range planRes.Diffs {
			if d.Action == governance.ActionDelete {
				deleteCount++
			}
		}
		assert.Equal(t, 2, deleteCount, "Must plan deletion of exactly 2 unmanaged webhooks")

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// Verify that IDs 201 and 202 are deleted, but hook 1 remains
		remainingHooks := srv.State().ListProjectHooks(101)
		require.Len(t, remainingHooks, 1)
		assert.Equal(t, 1, remainingHooks[0].ID)
	})
}

// ============================================================================
// 4. Adversarial Member Audit & Role Limit Violations
// ============================================================================

func TestAdversarial_Members_AuditViolations(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewMembersReconciler()
	ctx := context.Background()

	// Seed members with various violation profiles
	now := time.Now()
	expiredDate := gogitlab.ISOTime(now.AddDate(0, 0, -10))
	excessiveDate := gogitlab.ISOTime(now.AddDate(0, 0, 365))
	validDate := gogitlab.ISOTime(now.AddDate(0, 0, 30))

	// User 10: Over-privileged direct Owner (50)
	srv.State().AddProjectMember(101, &gogitlab.ProjectMember{
		ID:          10,
		Username:    "super_admin",
		AccessLevel: gogitlab.OwnerPermissions, // 50
		ExpiresAt:   &validDate,
	})

	// User 11: Denied member with uppercase username
	srv.State().AddProjectMember(101, &gogitlab.ProjectMember{
		ID:          11,
		Username:    "Suspicious_User",
		AccessLevel: gogitlab.DeveloperPermissions, // 30
		ExpiresAt:   &validDate,
	})

	// User 12: Under-privileged Guest (10)
	srv.State().AddProjectMember(101, &gogitlab.ProjectMember{
		ID:          12,
		Username:    "intern_guest",
		AccessLevel: gogitlab.GuestPermissions, // 10
		ExpiresAt:   &validDate,
	})

	// User 13: Direct member with NO expiration
	srv.State().AddProjectMember(101, &gogitlab.ProjectMember{
		ID:          13,
		Username:    "contractor_no_exp",
		AccessLevel: gogitlab.DeveloperPermissions, // 30
		ExpiresAt:   nil,
	})

	// User 14: Direct member with EXCESSIVE expiration (365 days)
	srv.State().AddProjectMember(101, &gogitlab.ProjectMember{
		ID:          14,
		Username:    "contractor_excessive",
		AccessLevel: gogitlab.DeveloperPermissions, // 30
		ExpiresAt:   &excessiveDate,
	})

	// User 15: Direct member with EXPIRED expiration (-10 days)
	srv.State().AddProjectMember(101, &gogitlab.ProjectMember{
		ID:          15,
		Username:    "contractor_expired",
		AccessLevel: gogitlab.DeveloperPermissions, // 30
		ExpiresAt:   &expiredDate,
	})

	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
	cfg := &config.PolicyConfig{
		Policies: config.PoliciesConfig{
			Members: &config.MembersConfig{
				MaxAccessLevel:    gogitlab.Ptr(40), // Maintainer max -> super_admin (50) is violation
				MinAccessLevel:    gogitlab.Ptr(20), // Reporter min -> intern_guest (10) is violation
				EnforceExpiresAt:  gogitlab.Ptr(true),
				MaxExpirationDays: gogitlab.Ptr(90),
				DeniedMembers:     []string{"suspicious_user"},
			},
		},
	}

	planRes, err := reconciler.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.ActionAudit, planRes.Action)
	assert.True(t, planRes.HasChanges)

	// Verify all violation types are detected in diffs
	diffMap := make(map[string]bool)
	for _, d := range planRes.Diffs {
		for _, f := range d.Fields {
			diffMap[f.Field] = true
		}
	}

	assert.True(t, diffMap["denied_member"], "Must flag denied member")
	assert.True(t, diffMap["access_level"], "Must flag access level limit violations")
	assert.True(t, diffMap["expires_at"], "Must flag missing or excessive expiration")

	applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.ActionAudit, applyRes.Action)
	assert.True(t, applyRes.Success)
}

// ============================================================================
// 5. Adversarial Project Settings & Full Matrix Validation
// ============================================================================

func TestAdversarial_ProjectSettings_Matrix(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewProjectSettingsReconciler()
	ctx := context.Background()
	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

	cfg := &config.PolicyConfig{
		Policies: config.PoliciesConfig{
			ProjectSettings: &config.ProjectSettingsConfig{
				DefaultBranch:                             "main",
				SquashOption:                              "always",
				MergeMethod:                               "ff",
				OnlyAllowMergeIfPipelineSucceeds:          gogitlab.Ptr(true),
				AllowMergeOnSkippedPipeline:               gogitlab.Ptr(false),
				OnlyAllowMergeIfAllDiscussionsAreResolved: gogitlab.Ptr(true),
				RemoveSourceBranchAfterMerge:              gogitlab.Ptr(true),
				KeepLatestArtifact:                        gogitlab.Ptr(true),
				PrintingMergeRequestLinkEnabled:           gogitlab.Ptr(false),
				AutoCancelPendingPipelines:                "enabled",
				AutoDevopsEnabled:                         gogitlab.Ptr(false),
			},
		},
	}

	planRes, err := reconciler.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.ActionUpdate, planRes.Action)

	applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.True(t, applyRes.Success)

	// Verify idempotency
	planAfter, err := reconciler.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.ActionNoop, planAfter.Action)
	assert.False(t, planAfter.HasChanges)
}

// ============================================================================
// 6. Adversarial OperationsRegistry: Concurrency & Order Safety
// ============================================================================

func TestAdversarial_OperationsRegistry_ConcurrencyAndOrder(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reg := governance.NewDefaultRegistry(client)

	// 1. Verify strict ordering 10 -> 100
	ops := reg.OrderedOperations()
	require.Len(t, ops, 10)
	for i := 0; i < len(ops)-1; i++ {
		assert.Less(t, ops[i].Order(), ops[i+1].Order(), "Operations must be strictly monotonically ordered")
	}

	ctx := context.Background()

	// 2. High concurrency Plan and Apply on shared registry
	const workers = 30
	var wg sync.WaitGroup
	wg.Add(workers)

	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
	group := &gogitlab.Group{ID: 10, FullPath: "platform"}
	cfg := &config.PolicyConfig{
		Policies: config.PoliciesConfig{
			PushRules: &config.PushRulesConfig{
				AuthorEmailRegex: `@example\.com$`,
				PreventSecrets:   gogitlab.Ptr(true),
			},
			PipelineRetention: &config.PipelineRetentionConfig{
				RetentionDays: 45,
			},
		},
	}

	for i := 0; i < workers; i++ {
		go func(workerID int) {
			defer wg.Done()
			if workerID%2 == 0 {
				planResults, pErr := reg.PlanProject(ctx, proj, cfg)
				assert.NoError(t, pErr)
				assert.Len(t, planResults, 10)

				applyResults, aErr := reg.ApplyProject(ctx, proj, cfg)
				assert.NoError(t, aErr)
				assert.Len(t, applyResults, 10)
			} else {
				planGroupResults, pgErr := reg.PlanGroup(ctx, group, cfg)
				assert.NoError(t, pgErr)
				assert.Len(t, planGroupResults, 10)

				applyGroupResults, agErr := reg.ApplyGroup(ctx, group, cfg)
				assert.NoError(t, agErr)
				assert.Len(t, applyGroupResults, 10)
			}
		}(i)
	}

	wg.Wait()
}
