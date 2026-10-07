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
				ProtectedEnvironments: []config.ProtectedEnvironmentRuleConfig{
					{
						Name: "staging",
						DeployAccessLevels: []config.EnvironmentAccessDescription{
							{AccessLevel: 30}, // Developer
						},
						RequiredApprovalCount: gogitlab.Ptr(1),
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
		// First verify it's now compliant with staging having required_approval_count = 1
		cfgSame := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedEnvironments: []config.ProtectedEnvironmentRuleConfig{
					{
						Name: "staging",
						DeployAccessLevels: []config.EnvironmentAccessDescription{
							{AccessLevel: 30},
						},
						RequiredApprovalCount: gogitlab.Ptr(1),
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
				ProtectedEnvironments: []config.ProtectedEnvironmentRuleConfig{
					{
						Name: "staging",
						DeployAccessLevels: []config.EnvironmentAccessDescription{
							{AccessLevel: 40}, // Maintainer
						},
						RequiredApprovalCount: gogitlab.Ptr(2),
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
