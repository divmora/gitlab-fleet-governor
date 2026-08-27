package governance_test

import (
	"context"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestMembersReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Project 101 has direct member alice (Maintainer=40), Group 10 has carol (Owner=50)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewMembersReconciler()
	assert.Equal(t, "members", reconciler.Name())
	assert.Equal(t, 100, reconciler.Order())

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

	t.Run("Project_AuditViolations", func(t *testing.T) {
		proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

		// Add an expired member and excessive expiration member to mock state
		expiredDate := gogitlab.ISOTime(time.Now().AddDate(0, 0, -5))
		srv.State().AddProjectMember(101, &gogitlab.ProjectMember{
			ID:          2,
			Username:    "bob",
			Name:        "Bob Jones",
			AccessLevel: gogitlab.DeveloperPermissions, // 30
			ExpiresAt:   &expiredDate,
		})

		excessiveDate := gogitlab.ISOTime(time.Now().AddDate(0, 0, 400))
		srv.State().AddProjectMember(101, &gogitlab.ProjectMember{
			ID:          99,
			Username:    "malicious_actor",
			Name:        "Malicious",
			AccessLevel: gogitlab.OwnerPermissions, // 50
			ExpiresAt:   &excessiveDate,
		})

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Members: &config.MembersConfig{
					MaxAccessLevel:    gogitlab.Ptr(40),
					EnforceExpiresAt:  gogitlab.Ptr(true),
					MaxExpirationDays: gogitlab.Ptr(90),
					DeniedMembers:     []string{"malicious_actor"},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionAudit, planRes.Action)
		assert.True(t, planRes.HasChanges)
		assert.NotEmpty(t, planRes.Diffs)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionAudit, applyRes.Action)
		assert.True(t, applyRes.Success)
	})

	t.Run("Group_AuditViolations", func(t *testing.T) {
		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Members: &config.MembersConfig{
					DeniedMembers: []string{"carol"}, // Carol is owner of group 10
				},
			},
		}

		planRes, err := reconciler.PlanGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionAudit, planRes.Action)
		assert.True(t, planRes.HasChanges)

		applyRes, err := reconciler.ApplyGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionAudit, applyRes.Action)
		assert.True(t, applyRes.Success)
	})
}
