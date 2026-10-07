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

	t.Run("Deterministic_ReverseCreation_Sequencing", func(t *testing.T) {
		// Clean project state
		projSeq := &gogitlab.Project{ID: 102, PathWithNamespace: "platform/reverse-seq"}
		srv.State().AddProject(projSeq)

		// Declared order: feature/* (top/evaluated 1st), hotfix/* (2nd), * (bottom/evaluated 3rd)
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				TargetBranchRules: &config.TargetBranchRulesConfig{
					Rules: []config.TargetBranchRuleConfig{
						{Name: "feature/*", TargetBranch: "develop"},
						{Name: "hotfix/*", TargetBranch: "main"},
						{Name: "*", TargetBranch: "main"},
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, projSeq, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, planRes.Action)
		require.Len(t, planRes.Diffs, 3)

		applyRes, err := reconciler.Apply(ctx, client, projSeq, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// Verify live rules returned in exact declared top-to-bottom order
		liveRules, err := client.TargetBranchRules().GetTargetBranchRules(ctx, projSeq.PathWithNamespace)
		require.NoError(t, err)
		require.Len(t, liveRules, 3)

		assert.Equal(t, "feature/*", liveRules[0].Name, "rules[0] must sit at the top of GitLab UI")
		assert.Equal(t, "develop", liveRules[0].TargetBranch)

		assert.Equal(t, "hotfix/*", liveRules[1].Name, "rules[1] must sit at index 1")
		assert.Equal(t, "main", liveRules[1].TargetBranch)

		assert.Equal(t, "*", liveRules[2].Name, "catch-all * must sit at the bottom")
		assert.Equal(t, "main", liveRules[2].TargetBranch)

		// Subsequent plan must be cleanly NOOP
		planNoop, err := reconciler.Plan(ctx, client, projSeq, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planNoop.Action)
		assert.False(t, planNoop.HasChanges)
	})

	t.Run("OrderDrift_Detection_And_Resequencing", func(t *testing.T) {
		projDrift := &gogitlab.Project{ID: 103, PathWithNamespace: "platform/order-drift"}
		srv.State().AddProject(projDrift)

		// Pre-seed rules in INVERTED order in GitLab (e.g. * created last so it sits at the top):
		// Creating feature/*, then hotfix/*, then * means * is newest and sits at index 0.
		_, ok := srv.State().AddTargetBranchRule(projDrift.ID, "feature/*", "develop")
		require.True(t, ok)
		_, ok = srv.State().AddTargetBranchRule(projDrift.ID, "hotfix/*", "main")
		require.True(t, ok)
		_, ok = srv.State().AddTargetBranchRule(projDrift.ID, "*", "main")
		require.True(t, ok)

		// Verify initial inverted live state
		initialLive, err := client.TargetBranchRules().GetTargetBranchRules(ctx, projDrift.PathWithNamespace)
		require.NoError(t, err)
		require.Len(t, initialLive, 3)
		assert.Equal(t, "*", initialLive[0].Name, "Initially * is at the top (shadowing feature/*)")
		assert.Equal(t, "hotfix/*", initialLive[1].Name)
		assert.Equal(t, "feature/*", initialLive[2].Name)

		// Desired evaluation order: feature/* (1st), hotfix/* (2nd), * (3rd)
		desiredCfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				TargetBranchRules: &config.TargetBranchRulesConfig{
					Rules: []config.TargetBranchRuleConfig{
						{Name: "feature/*", TargetBranch: "develop"},
						{Name: "hotfix/*", TargetBranch: "main"},
						{Name: "*", TargetBranch: "main"},
					},
				},
			},
		}

		// Plan MUST detect order drift
		planRes, err := reconciler.Plan(ctx, client, projDrift, desiredCfg)
		require.NoError(t, err)
		assert.True(t, planRes.HasChanges, "Plan must flag changes on order drift")
		assert.Equal(t, governance.ActionUpdate, planRes.Action)

		var orderDiff *governance.Diff
		for i := range planRes.Diffs {
			if planRes.Diffs[i].Resource == "target_branch_rules:evaluation_order" {
				orderDiff = &planRes.Diffs[i]
				break
			}
		}
		require.NotNil(t, orderDiff, "Expected diff for target_branch_rules:evaluation_order")
		assert.Equal(t, governance.ActionUpdate, orderDiff.Action)

		// Apply MUST atomically resequence rules so feature/* is on top
		applyRes, err := reconciler.Apply(ctx, client, projDrift, desiredCfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// Verify live state is now in exact declared order
		resequencedLive, err := client.TargetBranchRules().GetTargetBranchRules(ctx, projDrift.PathWithNamespace)
		require.NoError(t, err)
		require.Len(t, resequencedLive, 3)
		assert.Equal(t, "feature/*", resequencedLive[0].Name)
		assert.Equal(t, "hotfix/*", resequencedLive[1].Name)
		assert.Equal(t, "*", resequencedLive[2].Name)

		// Subsequent plan MUST be clean NOOP
		subsequentPlan, err := reconciler.Plan(ctx, client, projDrift, desiredCfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, subsequentPlan.Action)
		assert.False(t, subsequentPlan.HasChanges)
	})

	t.Run("RuleUpdate_PreservesOrder_NoShadowing", func(t *testing.T) {
		projUpdate := &gogitlab.Project{ID: 104, PathWithNamespace: "platform/rule-update"}
		srv.State().AddProject(projUpdate)

		cfgInitial := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				TargetBranchRules: &config.TargetBranchRulesConfig{
					Rules: []config.TargetBranchRuleConfig{
						{Name: "feature/*", TargetBranch: "develop"},
						{Name: "*", TargetBranch: "main"},
					},
				},
			},
		}

		applyInit, err := reconciler.Apply(ctx, client, projUpdate, cfgInitial)
		require.NoError(t, err)
		assert.True(t, applyInit.Success)

		// Now update the catch-all rule * target branch to production
		cfgUpdated := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				TargetBranchRules: &config.TargetBranchRulesConfig{
					Rules: []config.TargetBranchRuleConfig{
						{Name: "feature/*", TargetBranch: "develop"},
						{Name: "*", TargetBranch: "production"},
					},
				},
			},
		}

		planUpdate, err := reconciler.Plan(ctx, client, projUpdate, cfgUpdated)
		require.NoError(t, err)
		assert.True(t, planUpdate.HasChanges)

		applyUpdate, err := reconciler.Apply(ctx, client, projUpdate, cfgUpdated)
		require.NoError(t, err)
		assert.True(t, applyUpdate.Success)

		// Verify live order: feature/* MUST STILL be on top, * MUST be at the bottom
		liveRules, err := client.TargetBranchRules().GetTargetBranchRules(ctx, projUpdate.PathWithNamespace)
		require.NoError(t, err)
		require.Len(t, liveRules, 2)
		assert.Equal(t, "feature/*", liveRules[0].Name, "feature/* must remain at the top after updating catch-all")
		assert.Equal(t, "develop", liveRules[0].TargetBranch)
		assert.Equal(t, "*", liveRules[1].Name, "* must remain at the bottom")
		assert.Equal(t, "production", liveRules[1].TargetBranch)
	})
}
