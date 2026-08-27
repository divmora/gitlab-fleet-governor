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

func TestComplianceReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has framework "SOC2"

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewComplianceReconciler()
	assert.Equal(t, "compliance", reconciler.Name())
	assert.Equal(t, 80, reconciler.Order())

	ctx := context.Background()

	t.Run("NilPolicy_Noop", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		res, err := reconciler.Plan(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, res.Action)

		applyRes, err := reconciler.Apply(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
	})

	t.Run("UpdateComplianceFramework", func(t *testing.T) {
		// Project 101 currently has SOC2 -> change to PCI-DSS
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Compliance: &config.ComplianceConfig{
					FrameworkName: "PCI-DSS",
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

	t.Run("PruneComplianceFramework", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Compliance: &config.ComplianceConfig{
					Prune: gogitlab.Ptr(true),
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionDelete, planRes.Action)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionDelete, applyRes.Action)
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
