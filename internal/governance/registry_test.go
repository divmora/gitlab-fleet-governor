package governance_test

import (
	"context"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestOperationsRegistry(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reg := governance.NewDefaultRegistry(client)

	t.Run("DefaultRegistry_Contains10OperationsInStrictOrder", func(t *testing.T) {
		ordered := reg.OrderedOperations()
		require.Len(t, ordered, 10)

		expectedOrder := []struct {
			name  string
			order int
		}{
			{"push_rules", 10},
			{"protected_branches", 20},
			{"approval_rules", 30},
			{"project_settings", 40},
			{"pipeline_retention", 50},
			{"variables", 60},
			{"runners", 70},
			{"compliance", 80},
			{"webhooks", 90},
			{"members", 100},
		}

		for i, exp := range expectedOrder {
			assert.Equal(t, exp.name, ordered[i].Name())
			assert.Equal(t, exp.order, ordered[i].Order())
		}
	})

	t.Run("GetAndRegisterOperation", func(t *testing.T) {
		op, found := reg.Get("push_rules")
		assert.True(t, found)
		assert.Equal(t, "push_rules", op.Name())

		_, notFound := reg.Get("non_existent_op")
		assert.False(t, notFound)
	})

	ctx := context.Background()

	t.Run("PlanAndApplyProject_FullFleetPolicy", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex: `@example\.com$`,
					PreventSecrets:   gogitlab.Ptr(true),
				},
				ProjectSettings: &config.ProjectSettingsConfig{
					SquashOption: "always",
				},
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: 60,
				},
			},
		}

		// 1. PlanProject
		planResults, err := reg.PlanProject(ctx, proj, cfg)
		require.NoError(t, err)
		assert.Len(t, planResults, 10)

		// 2. ApplyProject
		applyResults, err := reg.ApplyProject(ctx, proj, cfg)
		require.NoError(t, err)
		assert.Len(t, applyResults, 10)

		for _, r := range applyResults {
			assert.True(t, r.Success, "Operation %s failed", r.OperationName)
		}
	})

	t.Run("PlanAndApplyGroup_FullFleetPolicy", func(t *testing.T) {
		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex: `@example\.com$`,
					PreventSecrets:   gogitlab.Ptr(true),
				},
				Variables: []config.VariableConfig{
					{
						Key:              "REGISTRY_GROUP_VAR",
						Value:            "val-123",
						EnvironmentScope: "*",
					},
				},
			},
		}

		planResults, err := reg.PlanGroup(ctx, group, cfg)
		require.NoError(t, err)
		assert.Len(t, planResults, 10)

		applyResults, err := reg.ApplyGroup(ctx, group, cfg)
		require.NoError(t, err)
		assert.Len(t, applyResults, 10)

		for _, r := range applyResults {
			assert.True(t, r.Success, "Operation %s failed", r.OperationName)
		}
	})

	t.Run("TargetProjectAndGroupAdapters", func(t *testing.T) {
		targetProj := &discovery.TargetProject{
			ID:                101,
			Name:              "fleet-governor",
			PathWithNamespace: "platform/fleet-governor",
		}
		cfg := &config.PolicyConfig{}

		planP, err := reg.PlanTargetProject(ctx, targetProj, cfg)
		require.NoError(t, err)
		assert.Len(t, planP, 10)

		applyP, err := reg.ApplyTargetProject(ctx, targetProj, cfg)
		require.NoError(t, err)
		assert.Len(t, applyP, 10)

		targetGroup := &discovery.TargetGroup{
			ID:       10,
			Name:     "Platform Engineering",
			FullPath: "platform",
		}

		planG, err := reg.PlanTargetGroup(ctx, targetGroup, cfg)
		require.NoError(t, err)
		assert.Len(t, planG, 10)

		applyG, err := reg.ApplyTargetGroup(ctx, targetGroup, cfg)
		require.NoError(t, err)
		assert.Len(t, applyG, 10)
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel() // cancel immediately

		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{}

		_, err := reg.PlanProject(cancelCtx, proj, cfg)
		assert.ErrorIs(t, err, context.Canceled)

		_, err = reg.ApplyProject(cancelCtx, proj, cfg)
		assert.ErrorIs(t, err, context.Canceled)

		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		_, err = reg.PlanGroup(cancelCtx, group, cfg)
		assert.ErrorIs(t, err, context.Canceled)

		_, err = reg.ApplyGroup(cancelCtx, group, cfg)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("ResultConstructors", func(t *testing.T) {
		now := time.Now()
		plan := governance.NewPlanResult("test_op", governance.ResourceTypeProject, 1, "path", governance.ActionCreate, []governance.Diff{})
		assert.Equal(t, "test_op", plan.OperationName)

		apply := governance.NewApplyResult("test_op", governance.ResourceTypeProject, 1, "path", governance.ActionCreate, governance.StatusSuccess, []governance.Diff{}, nil, now)
		assert.True(t, apply.Success)
		assert.Equal(t, governance.StatusSuccess, apply.Status)
	})
}
