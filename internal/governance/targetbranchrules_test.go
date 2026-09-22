package governance_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func TestTargetBranchRulesReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewTargetBranchRulesReconciler()
	ctx := context.Background()

	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

	t.Run("NameAndOrder", func(t *testing.T) {
		assert.Equal(t, "target_branch_rules", reconciler.Name())
		assert.Equal(t, 45, reconciler.Order())
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
				TargetBranchRules: &config.TargetBranchRulesConfig{
					Rules: []config.TargetBranchRuleConfig{
						{Name: "feature/*", TargetBranch: "develop"},
					},
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

	t.Run("Create_Update_Prune_Lifecycle", func(t *testing.T) {
		// 1. Initial creation of target branch rules
		cfg1 := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				TargetBranchRules: &config.TargetBranchRulesConfig{
					Prune: gogitlab.Ptr(true),
					Rules: []config.TargetBranchRuleConfig{
						{Name: "feature/*", TargetBranch: "develop"},
						{Name: "hotfix/*", TargetBranch: "main"},
					},
				},
			},
		}

		plan1, err := reconciler.Plan(ctx, client, proj, cfg1)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, plan1.Action)
		assert.True(t, plan1.HasChanges)
		require.Len(t, plan1.Diffs, 2)

		apply1, err := reconciler.Apply(ctx, client, proj, cfg1)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, apply1.Action)
		assert.True(t, apply1.Success)

		// 2. Subsequent Plan is NOOP
		plan2, err := reconciler.Plan(ctx, client, proj, cfg1)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan2.Action)
		assert.False(t, plan2.HasChanges)

		// 3. Update target branch for feature/* and remove hotfix/* (triggering pruning)
		cfg2 := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				TargetBranchRules: &config.TargetBranchRulesConfig{
					Prune: gogitlab.Ptr(true),
					Rules: []config.TargetBranchRuleConfig{
						{Name: "feature/*", TargetBranch: "staging"},
					},
				},
			},
		}

		plan3, err := reconciler.Plan(ctx, client, proj, cfg2)
		require.NoError(t, err)
		assert.True(t, plan3.HasChanges)

		hasUpdate := false
		hasDelete := false
		for _, d := range plan3.Diffs {
			if d.Action == governance.ActionUpdate && d.Resource == "target_branch_rule:feature/*" {
				hasUpdate = true
			}
			if d.Action == governance.ActionDelete && d.Resource == "target_branch_rule:hotfix/*" {
				hasDelete = true
			}
		}
		assert.True(t, hasUpdate, "Expected ActionUpdate for feature/*")
		assert.True(t, hasDelete, "Expected ActionDelete for hotfix/*")

		apply3, err := reconciler.Apply(ctx, client, proj, cfg2)
		require.NoError(t, err)
		assert.True(t, apply3.Success)

		// 4. Verify live state after update & prune
		liveRules, err := client.TargetBranchRules().GetTargetBranchRules(ctx, proj.PathWithNamespace)
		require.NoError(t, err)
		require.Len(t, liveRules, 1)
		assert.Equal(t, "feature/*", liveRules[0].Name)
		assert.Equal(t, "staging", liveRules[0].TargetBranch)

		// 5. Subsequent Plan is NOOP
		plan4, err := reconciler.Plan(ctx, client, proj, cfg2)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, plan4.Action)
	})
}
