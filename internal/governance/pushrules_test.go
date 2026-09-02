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

func TestPushRulesReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewPushRulesReconciler()
	assert.Equal(t, "push_rules", reconciler.Name())
	assert.Equal(t, 10, reconciler.Order())

	ctx := context.Background()

	t.Run("Project_NilConfig_Noop", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		res, err := reconciler.Plan(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, res.Action)
		assert.False(t, res.HasChanges)

		applyRes, err := reconciler.Apply(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
		assert.True(t, applyRes.Success)
	})

	t.Run("Project_404_Create", func(t *testing.T) {
		// Project 102 has no push rule in seed
		proj := &gogitlab.Project{ID: 102, PathWithNamespace: "platform/core/cloud-infra"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex:      `@example\.com$`,
					BranchNameRegex:       `^(main|release/.*)$`,
					CommitMessageRegex:    `^\[(FEAT|FIX)\]`,
					PreventSecrets:        gogitlab.Ptr(true),
					RejectUnsignedCommits: gogitlab.Ptr(true),
				},
			},
		}

		// 1. Plan should show ActionCreate
		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, planRes.Action)
		assert.True(t, planRes.HasChanges)
		require.Len(t, planRes.Diffs, 1)

		// 2. Apply should create push rules
		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, applyRes.Action)
		assert.Equal(t, governance.StatusSuccess, applyRes.Status)
		assert.True(t, applyRes.Success)

		// 3. Subsequent Plan should be ActionNoop
		planAfter, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planAfter.Action)
		assert.False(t, planAfter.HasChanges)
	})

	t.Run("Project_200_Update", func(t *testing.T) {
		// Project 101 has push rule seeded with MaxFileSize=10
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex: `@example\.com$`,
					MaxFileSize:      gogitlab.Ptr(50), // Changed from 10 to 50
					PreventSecrets:   gogitlab.Ptr(true),
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
		assert.Equal(t, governance.StatusSuccess, applyRes.Status)
		assert.True(t, applyRes.Success)
	})

	t.Run("Group_404_Create_And_Update", func(t *testing.T) {
		// Group 20 (Security) has no push rule in seed
		group := &gogitlab.Group{ID: 20, FullPath: "security"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex: `@security\.example\.com$`,
					PreventSecrets:   gogitlab.Ptr(true),
				},
			},
		}

		planRes, err := reconciler.PlanGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, planRes.Action)
		assert.True(t, planRes.HasChanges)

		applyRes, err := reconciler.ApplyGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, applyRes.Action)
		assert.True(t, applyRes.Success)

		// Group 10 has push rule in seed -> test update
		group10 := &gogitlab.Group{ID: 10, FullPath: "platform"}
		cfgUpdate := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex: `@corp\.com$`, // updated
					PreventSecrets:   gogitlab.Ptr(true),
				},
			},
		}

		planRes10, err := reconciler.PlanGroup(ctx, client, group10, cfgUpdate)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, planRes10.Action)

		applyRes10, err := reconciler.ApplyGroup(ctx, client, group10, cfgUpdate)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionUpdate, applyRes10.Action)
		assert.True(t, applyRes10.Success)
	})
}
