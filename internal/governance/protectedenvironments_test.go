package governance_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"gopkg.in/yaml.v3"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func TestProtectedEnvironmentsReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewProtectedEnvironmentsReconciler()
	assert.Equal(t, "protected_environments", reconciler.Name())
	assert.Equal(t, 25, reconciler.Order())

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

	t.Run("CreateNewProtectedEnvironment", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedEnvironments: &config.ProtectedEnvironmentsConfig{
					Rules: []config.ProtectedEnvironmentRuleConfig{
						{
							Name: "staging",
							DeployAccessLevels: []config.EnvironmentAccessDescription{
								{AccessLevel: 30}, // Developer
							},
							RequiredApprovalCount: gogitlab.Ptr(1),
						},
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

	t.Run("UpdateExistingProtectedEnvironment", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfgSame := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedEnvironments: &config.ProtectedEnvironmentsConfig{
					Rules: []config.ProtectedEnvironmentRuleConfig{
						{
							Name: "staging",
							DeployAccessLevels: []config.EnvironmentAccessDescription{
								{AccessLevel: 30},
							},
							RequiredApprovalCount: gogitlab.Ptr(1),
						},
					},
				},
			},
		}
		planSame, err := reconciler.Plan(ctx, client, proj, cfgSame)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planSame.Action)

		// Now drift required_approval_count to 2
		cfgUpdated := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedEnvironments: &config.ProtectedEnvironmentsConfig{
					Rules: []config.ProtectedEnvironmentRuleConfig{
						{
							Name: "staging",
							DeployAccessLevels: []config.EnvironmentAccessDescription{
								{AccessLevel: 40}, // Maintainer
							},
							RequiredApprovalCount: gogitlab.Ptr(2),
						},
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfgUpdated)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)
		assert.True(t, planRes.HasChanges)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfgUpdated)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)
		assert.True(t, applyRes.Success)
	})

	t.Run("PruneUnmanagedProtectedEnvironments", func(t *testing.T) {
		projPrune := &gogitlab.Project{ID: 102, PathWithNamespace: "platform/prune-env"}
		srv.State().AddProject(projPrune)

		// Pre-seed an unmanaged environment "legacy-dev"
		srv.State().ProtectEnvironment(projPrune.ID, &gogitlab.ProtectedEnvironment{
			Name: "legacy-dev",
			DeployAccessLevels: []*gogitlab.EnvironmentAccessDescription{
				{AccessLevel: 30},
			},
		})

		cfgWithPrune := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedEnvironments: &config.ProtectedEnvironmentsConfig{
					Prune: gogitlab.Ptr(true),
					Rules: []config.ProtectedEnvironmentRuleConfig{
						{
							Name: "production",
							DeployAccessLevels: []config.EnvironmentAccessDescription{
								{AccessLevel: 40},
							},
							RequiredApprovalCount: gogitlab.Ptr(2),
						},
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, projPrune, cfgWithPrune)
		require.NoError(t, err)
		assert.True(t, planRes.HasChanges)

		// Plan should have create for production and delete for legacy-dev
		var hasCreate, hasDelete bool
		for _, d := range planRes.Diffs {
			if d.Resource == "protected_environment:production" && d.Action == governance.ActionCreate {
				hasCreate = true
			}
			if d.Resource == "protected_environment:legacy-dev" && d.Action == governance.ActionDelete {
				hasDelete = true
			}
		}
		assert.True(t, hasCreate, "expected create diff for production")
		assert.True(t, hasDelete, "expected delete diff for legacy-dev")

		// Apply should delete legacy-dev and create production
		applyRes, err := reconciler.Apply(ctx, client, projPrune, cfgWithPrune)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// Verify live state in mock
		liveEnvs := srv.State().ListProtectedEnvironments(projPrune.ID)
		require.Len(t, liveEnvs, 1)
		assert.Equal(t, "production", liveEnvs[0].Name)

		// Subsequent plan is NOOP
		subsequentPlan, err := reconciler.Plan(ctx, client, projPrune, cfgWithPrune)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, subsequentPlan.Action)
		assert.False(t, subsequentPlan.HasChanges)
	})

	t.Run("WildcardMatching", func(t *testing.T) {
		projWild := &gogitlab.Project{ID: 103, PathWithNamespace: "platform/wildcard-env"}
		srv.State().AddProject(projWild)

		// Add project environments matching dr-*
		srv.State().AddEnvironment(projWild.ID, &gogitlab.Environment{Name: "dr-us-east"})
		srv.State().AddEnvironment(projWild.ID, &gogitlab.Environment{Name: "dr-eu-west"})
		srv.State().AddEnvironment(projWild.ID, &gogitlab.Environment{Name: "review-feature-1"})

		cfgWild := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedEnvironments: &config.ProtectedEnvironmentsConfig{
					Rules: []config.ProtectedEnvironmentRuleConfig{
						{
							Name: "dr-*",
							DeployAccessLevels: []config.EnvironmentAccessDescription{
								{AccessLevel: 40},
							},
							RequiredApprovalCount: gogitlab.Ptr(2),
						},
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, projWild, cfgWild)
		require.NoError(t, err)
		assert.True(t, planRes.HasChanges)
		require.Len(t, planRes.Diffs, 2)

		diffNames := make(map[string]bool)
		for _, d := range planRes.Diffs {
			diffNames[d.Resource] = true
		}
		assert.True(t, diffNames["protected_environment:dr-us-east"])
		assert.True(t, diffNames["protected_environment:dr-eu-west"])
		assert.False(t, diffNames["protected_environment:review-feature-1"])

		applyRes, err := reconciler.Apply(ctx, client, projWild, cfgWild)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		liveEnvs := srv.State().ListProtectedEnvironments(projWild.ID)
		require.Len(t, liveEnvs, 2)
	})

	t.Run("UsernameAndGroupPathResolution", func(t *testing.T) {
		projRes := &gogitlab.Project{ID: 104, PathWithNamespace: "platform/resolve-env"}
		srv.State().AddProject(projRes)

		cfgResolve := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedEnvironments: &config.ProtectedEnvironmentsConfig{
					Rules: []config.ProtectedEnvironmentRuleConfig{
						{
							Name: "production",
							DeployAccessLevels: []config.EnvironmentAccessDescription{
								{AccessLevel: 40},
								{Username: "alice"},
								{GroupPath: "platform"},
							},
							ApprovalUsers: []config.EnvironmentApprovalUserConfig{
								{Username: "alice"},
							},
							ApprovalGroups: []config.EnvironmentApprovalGroupConfig{
								{GroupPath: "platform"},
							},
							RequiredApprovalCount: gogitlab.Ptr(1),
						},
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, projRes, cfgResolve)
		require.NoError(t, err)
		assert.True(t, planRes.HasChanges)

		applyRes, err := reconciler.Apply(ctx, client, projRes, cfgResolve)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		pe, found := srv.State().GetProtectedEnvironment(projRes.ID, "production")
		require.True(t, found)
		require.NotNil(t, pe)

		// Verify alice was resolved to user ID 1 and platform resolved to group ID 10
		var foundUser, foundGroup bool
		for _, acc := range pe.DeployAccessLevels {
			if acc.UserID == 1 {
				foundUser = true
			}
			if acc.GroupID == 10 {
				foundGroup = true
			}
		}
		assert.True(t, foundUser, "expected resolved user ID 1 in deploy access levels")
		assert.True(t, foundGroup, "expected resolved group ID 10 in deploy access levels")
	})

	t.Run("BackwardCompatibleYAMLUnmarshaling", func(t *testing.T) {
		// Test list syntax
		listYAML := `
version: "v1"
policies:
  protected_environments:
    - name: "production"
      required_approval_count: 2
      deploy_access_levels:
        - access_level: 40
`
		var cfgList config.PolicyConfig
		err := yaml.Unmarshal([]byte(listYAML), &cfgList)
		require.NoError(t, err)
		require.NotNil(t, cfgList.Policies.ProtectedEnvironments)
		require.Len(t, cfgList.Policies.ProtectedEnvironments.Rules, 1)
		assert.Equal(t, "production", cfgList.Policies.ProtectedEnvironments.Rules[0].Name)
		assert.Nil(t, cfgList.Policies.ProtectedEnvironments.Prune)

		// Test mapping syntax with prune: true
		mapYAML := `
version: "v1"
policies:
  protected_environments:
    prune: true
    rules:
      - name: "staging"
        required_approval_count: 1
`
		var cfgMap config.PolicyConfig
		err = yaml.Unmarshal([]byte(mapYAML), &cfgMap)
		require.NoError(t, err)
		require.NotNil(t, cfgMap.Policies.ProtectedEnvironments)
		require.NotNil(t, cfgMap.Policies.ProtectedEnvironments.Prune)
		assert.True(t, *cfgMap.Policies.ProtectedEnvironments.Prune)
		require.Len(t, cfgMap.Policies.ProtectedEnvironments.Rules, 1)
		assert.Equal(t, "staging", cfgMap.Policies.ProtectedEnvironments.Rules[0].Name)
	})

	t.Run("GroupLevelSkipped", func(t *testing.T) {
		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		planGroupRes, err := reconciler.PlanGroup(ctx, client, group, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, planGroupRes.Action)

		applyGroupRes, err := reconciler.ApplyGroup(ctx, client, group, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, applyGroupRes.Action)
	})
}
