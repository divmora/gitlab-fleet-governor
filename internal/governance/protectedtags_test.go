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

func TestProtectedTagsReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has "v*" protected with Maintainers (40)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewProtectedTagsReconciler()
	assert.Equal(t, "protected_tags", reconciler.Name())
	assert.Equal(t, 22, reconciler.Order())

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

	t.Run("MatchesExisting_Noop", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedTags: &config.ProtectedTagsConfig{
					Rules: []config.ProtectedTagRuleConfig{
						{
							Name: "v*",
							AllowedToCreate: []config.TagAccessDescription{
								{AccessLevel: 40}, // Maintainer
							},
						},
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planRes.Action)
		assert.False(t, planRes.HasChanges)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
	})

	t.Run("CreateNewProtectedTag", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedTags: &config.ProtectedTagsConfig{
					Rules: []config.ProtectedTagRuleConfig{
						{
							Name: "v*",
							AllowedToCreate: []config.TagAccessDescription{
								{AccessLevel: 40},
							},
						},
						{
							Name: "release-*",
							AllowedToCreate: []config.TagAccessDescription{
								{AccessLevel: 40},
							},
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

		// Verify tag is now in live state
		tag, resp, err := client.ProtectedTags().GetProtectedTag(proj.ID, "release-*")
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "release-*", tag.Name)
	})

	t.Run("UpdateExistingProtectedTag", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedTags: &config.ProtectedTagsConfig{
					Rules: []config.ProtectedTagRuleConfig{
						{
							Name: "v*",
							AllowedToCreate: []config.TagAccessDescription{
								{AccessLevel: 60}, // Changed from 40 to 60 (Admin)
							},
						},
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

		// Verify updated access level
		tag, _, err := client.ProtectedTags().GetProtectedTag(proj.ID, "v*")
		require.NoError(t, err)
		require.Len(t, tag.CreateAccessLevels, 1)
		assert.Equal(t, gogitlab.AccessLevelValue(60), tag.CreateAccessLevels[0].AccessLevel)
	})

	t.Run("PruneUnmanagedProtectedTags", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}
		// Seed an extra unmanaged tag
		_, _, err := client.ProtectedTags().ProtectRepositoryTags(proj.ID, &gogitlab.ProtectRepositoryTagsOptions{
			Name:              gogitlab.Ptr("unmanaged-tag-*"),
			CreateAccessLevel: gogitlab.Ptr(gogitlab.DeveloperPermissions),
		})
		require.NoError(t, err)

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				ProtectedTags: &config.ProtectedTagsConfig{
					Prune: gogitlab.Ptr(true),
					Rules: []config.ProtectedTagRuleConfig{
						{
							Name: "v*",
							AllowedToCreate: []config.TagAccessDescription{
								{AccessLevel: 60},
							},
						},
					},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, planRes.HasChanges)

		// Apply pruning
		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// Verify unmanaged tag is deleted
		_, _, err = client.ProtectedTags().GetProtectedTag(proj.ID, "unmanaged-tag-*")
		assert.Error(t, err)
	})

	t.Run("GroupNoop", func(t *testing.T) {
		grp := &gogitlab.Group{ID: 10, FullPath: "platform"}
		planRes, err := reconciler.PlanGroup(ctx, client, grp, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planRes.Action)

		applyRes, err := reconciler.ApplyGroup(ctx, client, grp, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
	})
}

func TestMatchesTagPattern(t *testing.T) {
	tests := []struct {
		pattern  string
		tag      string
		expected bool
	}{
		{"v*", "v1.0.0", true},
		{"v*", "v2.1.3", true},
		{"v*", "release-1.0", false},
		{"release-*", "release-2026.1", true},
		{"release-*", "v1.0", false},
		{"production-*", "production-hotfix", true},
		{"*", "anything", true},
		{"exact-tag", "exact-tag", true},
		{"exact-tag", "other-tag", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.tag, func(t *testing.T) {
			assert.Equal(t, tt.expected, governance.MatchesTagPattern(tt.pattern, tt.tag))
		})
	}
}
