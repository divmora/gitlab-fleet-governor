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

func TestVariablesReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has AWS_REGION="us-east-1", Group 10 has ORGANIZATION_NAME="FleetCorp"

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewVariablesReconciler(true) // with pruneUnmanaged = true
	assert.Equal(t, "variables", reconciler.Name())
	assert.Equal(t, 60, reconciler.Order())

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

	t.Run("Project_Create_Update_Redaction_And_Prune", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{
						Key:              "AWS_REGION",
						Value:            "eu-west-1", // Changed from us-east-1 -> update
						EnvironmentScope: "*",
						Protected:        gogitlab.Ptr(true),
					},
					{
						Key:              "SECRET_TOKEN_KEY",
						Value:            "my-super-secret-12345", // sensitive -> redacted in diff
						EnvironmentScope: "production",
						Masked:           gogitlab.Ptr(true),
						Protected:        gogitlab.Ptr(true),
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)
		assert.True(t, planRes.HasChanges)

		// Verify secret redaction in diff
		diffStr := ""
		for _, d := range planRes.Diffs {
			diffStr += d.String()
		}
		assert.NotContains(t, diffStr, "my-super-secret-12345")
		assert.Contains(t, diffStr, "******")

		// Apply
		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)
		assert.True(t, applyRes.Success)

		// Subsequent plan should be noop
		planAfter, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
	})

	t.Run("Group_Create_Update_And_Prune", func(t *testing.T) {
		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{
						Key:              "ORGANIZATION_NAME",
						Value:            "FleetCorp-Global", // Update
						EnvironmentScope: "*",
					},
					{
						Key:              "GROUP_API_KEY",
						Value:            "secret-api-key-999",
						EnvironmentScope: "staging",
						Masked:           gogitlab.Ptr(true),
					},
				},
			},
		}

		planRes, err := reconciler.PlanGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes.Action)

		applyRes, err := reconciler.ApplyGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)
		assert.True(t, applyRes.Success)
	})
}
