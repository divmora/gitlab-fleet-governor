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

func TestProtectedBranchesReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has "main" protected

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewProtectedBranchesReconciler()
	assert.Equal(t, "protected_branches", reconciler.Name())
	assert.Equal(t, 20, reconciler.Order())

	ctx := context.Background()

	t.Run("NilOrEmptyPolicy_Noop", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		res, err := reconciler.Plan(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, res.Action)

		applyRes, err := reconciler.Apply(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
	})

	t.Run("CreateNewProtectedBranch", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "release/*",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 40}, // Maintainer
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 30}, // Developer
						},
						AllowForcePush:            gogitlab.Ptr(false),
						CodeOwnerApprovalRequired: gogitlab.Ptr(true),
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, planRes.Action)
		assert.True(t, planRes.HasChanges)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, applyRes.Action)
		assert.True(t, applyRes.Success)
	})

	t.Run("UpdateExisting_OnlyCodeOwnerChanged", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "main",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 30},
						},
						AllowForcePush:            gogitlab.Ptr(false),
						CodeOwnerApprovalRequired: gogitlab.Ptr(false), // Changed from true to false
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
	})

	t.Run("UpdateExisting_PermissionsChanged_Recreation", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "main",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 0}, // No one
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 40}, // Maintainers only
						},
						AllowForcePush:            gogitlab.Ptr(false),
						CodeOwnerApprovalRequired: gogitlab.Ptr(false),
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
	})

	t.Run("Group_Skipped", func(t *testing.T) {
		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		planRes, err := reconciler.PlanGroup(ctx, client, group, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, planRes.Action)

		applyRes, err := reconciler.ApplyGroup(ctx, client, group, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, applyRes.Action)
		assert.Equal(t, governance.StatusSkipped, applyRes.Status)
	})
}
