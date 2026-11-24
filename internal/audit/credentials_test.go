package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func byTitle(findings []audit.DeployKeyFinding) map[string]audit.DeployKeyFinding {
	out := map[string]audit.DeployKeyFinding{}
	for _, f := range findings {
		out[f.KeyTitle] = f
	}
	return out
}

func TestDeployKeysAuditor(t *testing.T) {
	ctx := context.Background()
	server, client, proj := setupMockGitLab(t)
	day := 24 * time.Hour
	old, recent, past := time.Now().Add(-400*day), time.Now().Add(-10*day), time.Now().Add(-day)
	server.State().AddDeployKey(proj.ID, gogitlab.ProjectDeployKey{Title: "reader", FingerprintSHA256: "SHA256:reader", CreatedAt: &recent})
	server.State().AddDeployKey(proj.ID, gogitlab.ProjectDeployKey{Title: "pusher", FingerprintSHA256: "SHA256:pusher", CanPush: true, CreatedAt: &recent})
	server.State().AddDeployKey(proj.ID, gogitlab.ProjectDeployKey{Title: "ancient", FingerprintSHA256: "SHA256:ancient", CreatedAt: &old})
	server.State().AddDeployKey(proj.ID, gogitlab.ProjectDeployKey{Title: "expired", FingerprintSHA256: "SHA256:expired", CreatedAt: &recent, ExpiresAt: &past})

	// Baseline, no policy: write access and expiry are reported; age uses the 365-day default.
	findings, err := audit.NewDeployKeysAuditor().AuditProject(ctx, client, proj, nil)
	require.NoError(t, err)
	got := byTitle(findings)
	require.Len(t, got, 4)
	assert.Equal(t, audit.SeverityPass, got["reader"].Severity)
	assert.Equal(t, audit.SeverityMedium, got["pusher"].Severity)
	assert.Equal(t, audit.SeverityLow, got["ancient"].Severity, "older than 365 days")
	assert.GreaterOrEqual(t, got["ancient"].AgeDays, 399)
	assert.Equal(t, audit.SeverityMedium, got["expired"].Severity)
	assert.Equal(t, proj.Raw.WebURL, got["reader"].ProjectWebURL)

	// With a policy: listed write keys are fine, unlisted keys are reported, max age applies.
	cfg := &config.PolicyConfig{Policies: config.PoliciesConfig{DeployKeys: &config.DeployKeysConfig{
		Keys:       []config.DeployKeyConfig{{Title: "pusher", CanPush: gogitlab.Ptr(true)}, {Title: "reader"}},
		MaxAgeDays: gogitlab.Ptr(4),
	}}}
	findings, err = audit.NewDeployKeysAuditor().AuditProject(ctx, client, proj, cfg)
	require.NoError(t, err)
	got = byTitle(findings)
	assert.Equal(t, audit.SeverityMedium, got["pusher"].Severity, "10 days old, more than twice the 4-day limit")
	assert.NotContains(t, got["pusher"].Details, "write")
	assert.Contains(t, got["ancient"].Details, "not in the policy")
}

func TestDeployTokensAuditor(t *testing.T) {
	ctx := context.Background()
	server, client, proj := setupMockGitLab(t)
	day := 24 * time.Hour
	past, soon, far := time.Now().Add(-day), time.Now().Add(30*day), time.Now().Add(500*day)
	server.State().AddDeployToken("project", proj.ID, gogitlab.DeployToken{Name: "good", ExpiresAt: &soon, Scopes: []string{"read_repository"}})
	server.State().AddDeployToken("project", proj.ID, gogitlab.DeployToken{Name: "forever", Scopes: []string{"read_repository"}})
	server.State().AddDeployToken("project", proj.ID, gogitlab.DeployToken{Name: "publisher", ExpiresAt: &soon, Scopes: []string{"read_registry", "write_registry"}})
	server.State().AddDeployToken("project", proj.ID, gogitlab.DeployToken{Name: "stale", ExpiresAt: &past, Scopes: []string{"read_repository"}})
	server.State().AddDeployToken("project", proj.ID, gogitlab.DeployToken{Name: "long", ExpiresAt: &far, Scopes: []string{"read_repository"}})
	server.State().AddDeployToken("project", proj.ID, gogitlab.DeployToken{Name: "gone", Revoked: true})

	sev := func(findings []audit.DeployTokenFinding) map[string]audit.Severity {
		out := map[string]audit.Severity{}
		for _, f := range findings {
			out[f.TokenName] = f.Severity
			assert.Equal(t, "project", f.OwnerType)
			assert.Equal(t, proj.PathWithNamespace, f.OwnerPath)
		}
		return out
	}

	findings, err := audit.NewDeployTokensAuditor().AuditProject(ctx, client, proj, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]audit.Severity{
		"good": audit.SeverityPass, "forever": audit.SeverityMedium, "publisher": audit.SeverityMedium,
		"stale": audit.SeverityMedium, "long": audit.SeverityPass,
	}, sev(findings), "revoked tokens are ignored")

	cfg := &config.PolicyConfig{Policies: config.PoliciesConfig{DeployTokens: &config.DeployTokensConfig{
		AllowedScopes:     []string{"read_repository"},
		MaxExpirationDays: gogitlab.Ptr(365),
	}}}
	findings, err = audit.NewDeployTokensAuditor().AuditProject(ctx, client, proj, cfg)
	require.NoError(t, err)
	got := sev(findings)
	assert.Equal(t, audit.SeverityHigh, got["publisher"], "scopes outside the policy")
	assert.Equal(t, audit.SeverityLow, got["long"], "expires beyond max_expiration_days")
}

func TestGroupSettingsAndGroupTokensInAudit(t *testing.T) {
	server, client, _ := setupMockGitLab(t)
	server.State().AddGroup(&gogitlab.Group{ID: 50, Name: "Open", Path: "open", FullPath: "open", Visibility: gogitlab.PublicVisibility, ProjectCreationLevel: gogitlab.DeveloperProjectCreation})
	server.State().AddGroup(&gogitlab.Group{ID: 51, Name: "Locked", Path: "locked", FullPath: "locked", Visibility: gogitlab.PrivateVisibility, RequireTwoFactorAuth: true, ProjectCreationLevel: gogitlab.MaintainerProjectCreation})
	server.State().AddDeployToken("group", 50, gogitlab.DeployToken{Name: "group-forever", Scopes: []string{"read_registry"}})

	policy := &config.PolicyConfig{Policies: config.PoliciesConfig{GroupSettings: &config.GroupSettingsConfig{SubGroupCreationLevel: "owner"}}}
	auditor, err := audit.NewAuditor(client,
		audit.WithAuditorTargets(config.TargetSelectors{GroupSelector: &config.GroupSelector{GroupPathsInclude: []string{"open", "locked"}, Recursive: gogitlab.Ptr(false)}}),
		audit.WithAuditorPolicy(policy),
		audit.WithAuditorModules([]string{"group_settings", "deploy_tokens"}),
	)
	require.NoError(t, err)
	report, err := auditor.Execute(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, report.Summary.TotalProjectsScanned, "no projects, yet groups are still audited")
	assert.Equal(t, 2, report.Summary.TotalGroupsScanned)
	require.Len(t, report.GroupSettingFindings, 2)
	open, locked := report.GroupSettingFindings[1], report.GroupSettingFindings[0]
	require.Equal(t, "open", open.GroupPath)
	assert.Equal(t, audit.SeverityHigh, open.Severity)
	assert.Contains(t, open.Details, "Two-factor authentication is not required")
	assert.Contains(t, open.Details, "Group is public")
	assert.Contains(t, open.Details, "subgroup_creation_level", "policy drift from the group_settings reconciler")
	assert.Equal(t, audit.SeverityMedium, locked.Severity, "only the policy drift")

	require.Len(t, report.DeployTokenFindings, 1)
	assert.Equal(t, "group", report.DeployTokenFindings[0].OwnerType)
	assert.Equal(t, "open", report.DeployTokenFindings[0].OwnerPath)

	assert.Equal(t, 2, report.Summary.GroupSettingViolations)
	assert.Equal(t, 1, report.Summary.DeployTokenViolations)
	assert.Equal(t, 0, report.Summary.NonCompliantProjectsCount, "group findings don't count as non-compliant projects")
	assert.Equal(t, 2, report.Summary.ModuleViolations["group_settings"])
}

func TestMergeReports_IncludesCredentialAndGroupFindings(t *testing.T) {
	part := func(name string) *audit.AuditReport {
		r := &audit.AuditReport{
			DeployKeyFindings:    []audit.DeployKeyFinding{{Instance: name, ProjectID: 1, KeyTitle: "k", Severity: audit.SeverityMedium}},
			DeployTokenFindings:  []audit.DeployTokenFinding{{Instance: name, OwnerType: "group", OwnerID: 1, TokenName: "t", Severity: audit.SeverityHigh}},
			GroupSettingFindings: []audit.GroupSettingFinding{{Instance: name, GroupID: 1, Severity: audit.SeverityPass}},
			Summary:              audit.SummaryMetrics{TotalGroupsScanned: 1},
		}
		r.ComputeSummary()
		return r
	}
	merged := audit.MergeReports([]audit.InstanceResult{{Name: "a", Report: part("a")}, {Name: "b", Report: part("b")}}, time.Now())
	assert.Len(t, merged.DeployKeyFindings, 2)
	assert.Len(t, merged.DeployTokenFindings, 2)
	assert.Len(t, merged.GroupSettingFindings, 2)
	assert.Equal(t, 2, merged.Summary.TotalGroupsScanned)
	assert.Equal(t, 2, merged.Summary.DeployKeyViolations)
	assert.Equal(t, 2, merged.Summary.NonCompliantProjectsCount, "project 1 on a and on b")
}
