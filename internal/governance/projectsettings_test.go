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

func TestProjectSettingsReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewProjectSettingsReconciler()
	assert.Equal(t, "project_settings", reconciler.Name())
	assert.Equal(t, 40, reconciler.Order())

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

	t.Run("UpdateProjectSettings", func(t *testing.T) {
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
					PrintingMergeRequestLinkEnabled:           gogitlab.Ptr(true),
					AutoCancelPendingPipelines:                "enabled",
					AutoDevopsEnabled:                         gogitlab.Ptr(false),
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

		// Subsequent plan should be noop
		planAfter, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
		assert.False(t, planAfter.HasChanges)
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
