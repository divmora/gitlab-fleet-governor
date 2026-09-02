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

func TestWebhooksReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has hook ID 1 ("https://webhook.fleetcorp.com/events")

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewWebhooksReconciler(true) // pruneUnmanaged = true
	assert.Equal(t, "webhooks", reconciler.Name())
	assert.Equal(t, 90, reconciler.Order())

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

	t.Run("Create_Update_And_Prune", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Webhooks: []config.WebhookConfig{
					{
						URL:                   "https://webhook.fleetcorp.com/events", // update triggers
						PushEvents:            gogitlab.Ptr(true),
						MergeRequestsEvents:   gogitlab.Ptr(true),
						PipelineEvents:        gogitlab.Ptr(true),
						EnableSSLVerification: gogitlab.Ptr(true),
						SecretToken:           "secure-hmac-token",
					},
					{
						URL:                   "https://security.fleetcorp.com/audit", // create
						PushEvents:            gogitlab.Ptr(true),
						TagPushEvents:         gogitlab.Ptr(true),
						EnableSSLVerification: gogitlab.Ptr(true),
					},
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
