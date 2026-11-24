package governance_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func seededSecurityServer(t *testing.T) (*mockserver.MockGitLabServer, gitlab.GitLabClient, *gogitlab.Project) {
	t.Helper()
	srv := mockserver.NewMockGitLabServer()
	t.Cleanup(srv.Close)
	srv.Seed()
	client, err := srv.GovernorClient()
	require.NoError(t, err)
	proj, ok := srv.State().GetProject(101)
	require.True(t, ok)
	return srv, client, proj
}

func planFields(res *governance.PlanResult) []string {
	var out []string
	for _, d := range res.Diffs {
		for _, f := range d.Fields {
			out = append(out, f.Field)
		}
	}
	return out
}

func TestProjectSettings_SecurityFields(t *testing.T) {
	ctx := context.Background()
	srv, client, proj := seededSecurityServer(t)
	rec := governance.NewProjectSettingsReconciler()
	cfg := &config.PolicyConfig{Policies: config.PoliciesConfig{ProjectSettings: &config.ProjectSettingsConfig{
		Visibility:                             "private",
		PublicJobs:                             gogitlab.Ptr(false),
		RequestAccessEnabled:                   gogitlab.Ptr(false),
		ForkingAccessLevel:                     "private",
		CIForwardDeploymentEnabled:             gogitlab.Ptr(true),
		CIPushRepositoryForJobTokenAllowed:     gogitlab.Ptr(false),
		CIPipelineVariablesMinimumOverrideRole: "maintainer",
		CIRestrictPipelineCancellationRole:     "maintainer",
	}}}
	_, _ = srv.State().UpdateProject(101, func(p *gogitlab.Project) {
		p.Visibility = gogitlab.PublicVisibility
		p.PublicJobs = true
		p.RequestAccessEnabled = true
		p.ForkingAccessLevel = gogitlab.EnabledAccessControl
		p.CIPushRepositoryForJobTokenAllowed = true
		p.CIPipelineVariablesMinimumOverrideRole = "developer"
	})

	plan, err := rec.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"visibility", "public_jobs", "request_access_enabled", "forking_access_level",
		"ci_forward_deployment_enabled", "ci_push_repository_for_job_token_allowed",
		"ci_pipeline_variables_minimum_override_role", "ci_restrict_pipeline_cancellation_role",
	}, planFields(plan))

	_, err = rec.Apply(ctx, client, proj, cfg)
	require.NoError(t, err)
	live, _ := srv.State().GetProject(101)
	assert.Equal(t, gogitlab.PrivateVisibility, live.Visibility)
	assert.False(t, live.PublicJobs)
	assert.Equal(t, gogitlab.PrivateAccessControl, live.ForkingAccessLevel)
	assert.True(t, live.CIForwardDeploymentEnabled)
	assert.Equal(t, "maintainer", live.CIPipelineVariablesMinimumOverrideRole)

	again, err := rec.Plan(ctx, client, live, cfg)
	require.NoError(t, err)
	assert.False(t, again.HasChanges, "%v", planFields(again))
}

func jobTokenPolicy(j *config.JobTokenScopeConfig) *config.PolicyConfig {
	return &config.PolicyConfig{Policies: config.PoliciesConfig{JobTokenScope: j}}
}

func TestJobTokenScope_LimitAllowlistAndPrune(t *testing.T) {
	ctx := context.Background()
	srv, client, proj := seededSecurityServer(t)
	// Existing allowlist: 103 (unmanaged) and group 20 (unmanaged).
	srv.State().AllowJobTokenProject(101, 103)
	srv.State().AllowJobTokenGroup(101, 20)

	rec := governance.NewJobTokenScopeReconciler()
	cfg := jobTokenPolicy(&config.JobTokenScopeConfig{
		LimitAccess:     gogitlab.Ptr(true),
		AllowedProjects: &[]string{"platform/core/cloud-infra", "101"}, // 101 is the project itself
		AllowedGroups:   &[]string{"10"},
		Prune:           gogitlab.Ptr(true),
	})

	plan, err := rec.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	require.True(t, plan.HasChanges)
	byAction := map[string][]any{}
	for _, f := range plan.Diffs[0].Fields {
		v := f.NewValue
		if f.Action == governance.ActionDelete {
			v = f.OldValue
		}
		byAction[f.Field+":"+string(f.Action)] = append(byAction[f.Field+":"+string(f.Action)], v)
	}
	assert.Equal(t, map[string][]any{
		"limit_access:" + string(governance.ActionUpdate):     {true},
		"allowed_projects:" + string(governance.ActionCreate): {"platform/core/cloud-infra"},
		"allowed_projects:" + string(governance.ActionDelete): {"security/security-tools"},
		"allowed_groups:" + string(governance.ActionCreate):   {"platform"},
		"allowed_groups:" + string(governance.ActionDelete):   {"security"},
	}, byAction)
	assert.False(t, srv.State().JobTokenInboundEnabled(101), "plan changes nothing")

	res, err := rec.Apply(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.StatusSuccess, res.Status)
	assert.True(t, srv.State().JobTokenInboundEnabled(101))
	projects, groups := srv.State().JobTokenAllowlist(101)
	assert.Equal(t, []int64{101, 102}, projects, "the project itself is kept")
	assert.Equal(t, []int64{10}, groups)

	again, err := rec.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.False(t, again.HasChanges, "%v", planFields(again))
}

func TestJobTokenScope_WithoutPruneOnlyAdds(t *testing.T) {
	ctx := context.Background()
	srv, client, proj := seededSecurityServer(t)
	srv.State().AllowJobTokenProject(101, 103)

	rec := governance.NewJobTokenScopeReconciler()
	_, err := rec.Apply(ctx, client, proj, jobTokenPolicy(&config.JobTokenScopeConfig{AllowedProjects: &[]string{"platform/core/cloud-infra"}}))
	require.NoError(t, err)
	projects, _ := srv.State().JobTokenAllowlist(101)
	assert.Equal(t, []int64{101, 102, 103}, projects)
	assert.False(t, srv.State().JobTokenInboundEnabled(101), "limit_access unset is left alone")
}

func TestJobTokenScope_UnknownReference(t *testing.T) {
	_, client, proj := seededSecurityServer(t)
	rec := governance.NewJobTokenScopeReconciler()
	_, err := rec.Plan(context.Background(), client, proj, jobTokenPolicy(&config.JobTokenScopeConfig{AllowedProjects: &[]string{"no/such/project"}}))
	require.ErrorContains(t, err, `cannot resolve project "no/such/project"`)
}

func TestJobTokenScope_NotConfiguredAndGroups(t *testing.T) {
	ctx := context.Background()
	_, client, proj := seededSecurityServer(t)
	rec := governance.NewJobTokenScopeReconciler()
	plan, err := rec.Plan(ctx, client, proj, &config.PolicyConfig{})
	require.NoError(t, err)
	assert.Equal(t, governance.ActionNoop, plan.Action)

	gplan, err := rec.PlanGroup(ctx, client, &gogitlab.Group{ID: 10, FullPath: "platform"}, jobTokenPolicy(&config.JobTokenScopeConfig{LimitAccess: gogitlab.Ptr(true)}))
	require.NoError(t, err)
	assert.Equal(t, governance.ActionSkipped, gplan.Action)
}

func TestSecretPushProtection(t *testing.T) {
	ctx := context.Background()
	srv, client, proj := seededSecurityServer(t)
	rec := governance.NewSecretPushProtectionReconciler()
	cfg := &config.PolicyConfig{Policies: config.PoliciesConfig{SecretPushProtection: &config.SecretPushProtectionConfig{Enabled: gogitlab.Ptr(true)}}}

	plan, err := rec.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"secret_push_protection_enabled"}, planFields(plan))
	assert.False(t, srv.State().SecretPushProtection(101))

	_, err = rec.Apply(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.True(t, srv.State().SecretPushProtection(101))

	again, err := rec.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.False(t, again.HasChanges)

	srv.Faults().AddRule(mockserver.FaultRule{Method: "GET", PathPrefix: "/api/v4/projects/101/security_settings", StatusCode: 403, FailCount: 1, Message: "403 Forbidden"})
	_, err = rec.Plan(ctx, client, proj, cfg)
	require.ErrorContains(t, err, "failed to get security settings for project 101")
}
