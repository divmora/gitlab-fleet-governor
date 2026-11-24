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

func groupSettingsPolicy() *config.PolicyConfig {
	return &config.PolicyConfig{Policies: config.PoliciesConfig{GroupSettings: &config.GroupSettingsConfig{
		Visibility:                 "private",
		RequireTwoFactorAuth:       gogitlab.Ptr(true),
		TwoFactorGracePeriod:       gogitlab.Ptr(int64(24)),
		ProjectCreationLevel:       "maintainer",
		SubGroupCreationLevel:      "owner",
		ShareWithGroupLock:         gogitlab.Ptr(true),
		PreventForkingOutsideGroup: gogitlab.Ptr(true),
		RequestAccessEnabled:       gogitlab.Ptr(false),
		IPRestrictionRanges:        &[]string{"10.0.0.0/8", "192.168.1.0/24"},
		AllowedEmailDomains:        &[]string{"Example.com"},
		DefaultBranchProtection: &config.DefaultBranchProtectionConfig{
			AllowedToPush:  &[]int{40},
			AllowedToMerge: &[]int{40, 30},
			AllowForcePush: gogitlab.Ptr(false),
		},
	}}}
}

func groupSettingsSetup(t *testing.T) (*mockserver.MockGitLabServer, *governance.GroupSettingsReconciler, *gogitlab.Group) {
	t.Helper()
	srv := mockserver.NewMockGitLabServer()
	t.Cleanup(srv.Close)
	srv.State().AddGroup(&gogitlab.Group{
		ID: 77, Name: "Fleet", Path: "fleet", FullPath: "fleet",
		Visibility:            gogitlab.InternalVisibility,
		ProjectCreationLevel:  gogitlab.DeveloperProjectCreation,
		SubGroupCreationLevel: gogitlab.MaintainerSubGroupCreationLevelValue,
		RequestAccessEnabled:  true,
		MentionsDisabled:      true,
		IPRestrictionRanges:   "192.168.1.0/24",
	})
	g, ok := srv.State().GetGroup(77)
	require.True(t, ok)
	return srv, governance.NewGroupSettingsReconciler(), g
}

func fieldNames(res *governance.PlanResult) []string {
	var names []string
	for _, d := range res.Diffs {
		for _, f := range d.Fields {
			names = append(names, f.Field)
		}
	}
	return names
}

func TestGroupSettings_PlanApplyConverges(t *testing.T) {
	ctx := context.Background()
	srv, rec, group := groupSettingsSetup(t)
	client, err := srv.GovernorClient()
	require.NoError(t, err)
	cfg := groupSettingsPolicy()

	plan, err := rec.PlanGroup(ctx, client, group, cfg)
	require.NoError(t, err)
	assert.True(t, plan.HasChanges)
	assert.ElementsMatch(t, []string{
		"visibility", "require_two_factor_authentication", "two_factor_grace_period",
		"project_creation_level", "subgroup_creation_level", "share_with_group_lock",
		"prevent_forking_outside_group", "request_access_enabled", "ip_restriction_ranges",
		"allowed_email_domains", "default_branch_protection.allowed_to_push",
		"default_branch_protection.allowed_to_merge",
	}, fieldNames(plan), "allow_force_push=false matches the default, so it is not drift")

	before, _ := srv.State().GetGroup(77)
	assert.Equal(t, gogitlab.InternalVisibility, before.Visibility, "plan does not change anything")

	applied, err := rec.ApplyGroup(ctx, client, group, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.StatusSuccess, applied.Status)

	after, _ := srv.State().GetGroup(77)
	assert.Equal(t, gogitlab.PrivateVisibility, after.Visibility)
	assert.True(t, after.RequireTwoFactorAuth)
	assert.Equal(t, int64(24), after.TwoFactorGracePeriod)
	assert.Equal(t, gogitlab.MaintainerProjectCreation, after.ProjectCreationLevel)
	assert.False(t, after.RequestAccessEnabled)
	assert.True(t, after.MentionsDisabled, "settings the policy doesn't mention are left alone")
	assert.Equal(t, "10.0.0.0/8,192.168.1.0/24", after.IPRestrictionRanges)
	assert.Equal(t, "example.com", after.AllowedEmailDomainsList)
	require.NotNil(t, after.DefaultBranchProtectionDefaults)
	require.Len(t, after.DefaultBranchProtectionDefaults.AllowedToMerge, 2)

	again, err := rec.PlanGroup(ctx, client, after, cfg)
	require.NoError(t, err)
	assert.False(t, again.HasChanges, "converged: %v", fieldNames(again))

	noop, err := rec.ApplyGroup(ctx, client, after, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.ActionNoop, noop.Action)
}

func TestGroupSettings_ListsCompareUnordered(t *testing.T) {
	ctx := context.Background()
	srv, rec, group := groupSettingsSetup(t)
	client, err := srv.GovernorClient()
	require.NoError(t, err)

	_, _, _ = srv.State().UpdateGroupFields(77, map[string]any{"ip_restriction_ranges": "192.168.1.0/24, 10.0.0.0/8", "allowed_email_domains_list": "example.com"})
	cfg := &config.PolicyConfig{Policies: config.PoliciesConfig{GroupSettings: &config.GroupSettingsConfig{
		IPRestrictionRanges: &[]string{"10.0.0.0/8", "192.168.1.0/24"},
		AllowedEmailDomains: &[]string{"EXAMPLE.com"},
	}}}
	plan, err := rec.PlanGroup(ctx, client, group, cfg)
	require.NoError(t, err)
	assert.False(t, plan.HasChanges, "%v", fieldNames(plan))

	// An empty list removes the restriction.
	cfg.Policies.GroupSettings.IPRestrictionRanges = &[]string{}
	plan, err = rec.PlanGroup(ctx, client, group, cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"ip_restriction_ranges"}, fieldNames(plan))
}

func TestGroupSettings_NotConfiguredOrProject(t *testing.T) {
	ctx := context.Background()
	srv, rec, group := groupSettingsSetup(t)
	client, err := srv.GovernorClient()
	require.NoError(t, err)

	plan, err := rec.PlanGroup(ctx, client, group, &config.PolicyConfig{})
	require.NoError(t, err)
	assert.Equal(t, governance.ActionNoop, plan.Action)

	proj := &gogitlab.Project{ID: 1, PathWithNamespace: "fleet/app"}
	pplan, err := rec.Plan(ctx, client, proj, groupSettingsPolicy())
	require.NoError(t, err)
	assert.Equal(t, governance.ActionNoop, pplan.Action, "no entry for projects")
}

func TestGroupSettings_UpdateError(t *testing.T) {
	ctx := context.Background()
	srv, rec, group := groupSettingsSetup(t)
	client, err := srv.GovernorClient()
	require.NoError(t, err)
	srv.Faults().AddRule(mockserver.FaultRule{Method: "PUT", PathPrefix: "/api/v4/groups/77", StatusCode: 403, FailCount: 1, Message: "403 Forbidden"})

	res, err := rec.ApplyGroup(ctx, client, group, groupSettingsPolicy())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update group settings for group 77")
	assert.Equal(t, governance.StatusFailed, res.Status)
}
