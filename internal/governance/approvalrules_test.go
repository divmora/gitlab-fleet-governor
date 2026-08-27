package governance_test

import (
	"context"
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestApprovalRulesReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has rule "Security Review" with user 2 (bob)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	resolver := governance.NewCachingResolver()
	reconciler := governance.NewApprovalRulesReconciler(resolver)
	assert.Equal(t, "approval_rules", reconciler.Name())
	assert.Equal(t, 30, reconciler.Order())

	ctx := context.Background()

	t.Run("CachingResolver", func(t *testing.T) {
		// User resolution: alice (ID 1), bob (ID 2), carol (ID 3)
		uid1, err := resolver.ResolveUsername(ctx, client, "alice")
		require.NoError(t, err)
		assert.Equal(t, 1, uid1)

		// Second call should hit cache
		uid1Cached, err := resolver.ResolveUsername(ctx, client, "@alice")
		require.NoError(t, err)
		assert.Equal(t, 1, uid1Cached)

		// Group resolution: platform (ID 10), security (ID 20)
		gid10, err := resolver.ResolveGroupPath(ctx, client, "platform")
		require.NoError(t, err)
		assert.Equal(t, 10, gid10)

		// Error on invalid
		_, err = resolver.ResolveUsername(ctx, client, "nonexistent-user-999")
		require.Error(t, err)

		_, err = resolver.ResolveGroupPath(ctx, client, "")
		require.Error(t, err)
	})

	t.Run("NilPolicy_Noop", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		res, err := reconciler.Plan(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, res.Action)

		applyRes, err := reconciler.Apply(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
	})

	t.Run("SettingsUpdate_And_NamedRuleUpsert", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ApprovalRules: &config.ApprovalRulesConfig{
					Settings: &config.ApprovalSettingsConfig{
						AllowAuthorApproval:                        gogitlab.Ptr(false),
						AllowCommitterApproval:                     gogitlab.Ptr(false),
						AllowOverridesToApproverListPerMergeRequest: gogitlab.Ptr(false),
						RetainApprovalsOnPush:                      gogitlab.Ptr(true),
						SelectiveCodeOwnerRemovals:                 gogitlab.Ptr(true),
					},
					Rules: []config.ApprovalRuleConfig{
						{
							Name:              "Security Review", // existing rule -> update approvals to 2
							ApprovalsRequired: 2,
							UserUsernames:     []string{"bob", "carol"},
						},
						{
							Name:              "Architecture Gate", // new rule -> create
							ApprovalsRequired: 1,
							UserUsernames:     []string{"alice"},
							GroupPaths:        []string{"platform"},
						},
					},
					Prune: gogitlab.Ptr(false),
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)
		assert.True(t, planRes.HasChanges)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)
		assert.True(t, applyRes.Success)
	})

	t.Run("DriftPruning", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		// Prune all except "Security Review"
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ApprovalRules: &config.ApprovalRulesConfig{
					Rules: []config.ApprovalRuleConfig{
						{
							Name:              "Security Review",
							ApprovalsRequired: 2,
							UserUsernames:     []string{"bob", "carol"},
						},
					},
					Prune: gogitlab.Ptr(true),
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
	})

	t.Run("Group_Skipped", func(t *testing.T) {
		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		planRes, err := reconciler.PlanGroup(ctx, client, group, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, planRes.Action)

		applyRes, err := reconciler.ApplyGroup(ctx, client, group, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, applyRes.Action)
	})
}
