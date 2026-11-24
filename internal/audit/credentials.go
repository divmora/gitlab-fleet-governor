package audit

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
)

// DefaultDeployKeyMaxAgeDays is the key age reported for rotation when the
// policy sets no deploy_keys.max_age_days.
const DefaultDeployKeyMaxAgeDays = 365

// writeTokenScopes are deploy token scopes that grant write access.
var writeTokenScopes = map[string]bool{
	"write_registry":         true,
	"write_package_registry": true,
	"write_virtual_registry": true,
}

// projectContext returns the project's web URL and lifecycle state.
func projectContext(client gl.GitLabClient, project *discovery.TargetProject) (webURL, state string, archived, inactive bool) {
	var lastAct *time.Time
	if project.Raw != nil {
		lastAct = project.Raw.LastActivityAt
		webURL = project.Raw.WebURL
	}
	state, archived, inactive = EvaluateProjectLifecycle(project.Archived, project.IsPendingDeletion(), lastAct)
	if webURL == "" {
		webURL = fmt.Sprintf("%s/%s", strings.TrimSuffix(strings.TrimSuffix(client.BaseURL(), "/api/v4"), "/"), project.PathWithNamespace)
	}
	return webURL, state, archived, inactive
}

func groupWebURL(client gl.GitLabClient, g *discovery.TargetGroup) string {
	if g.Raw != nil && g.Raw.WebURL != "" {
		return g.Raw.WebURL
	}
	return fmt.Sprintf("%s/groups/%s", strings.TrimSuffix(strings.TrimSuffix(client.BaseURL(), "/api/v4"), "/"), g.FullPath)
}

// worst returns the more severe of a and b.
func worst(a, b Severity) Severity {
	rank := map[Severity]int{SeverityPass: 0, SeverityInfo: 1, SeverityLow: 2, SeverityMedium: 3, SeverityHigh: 4, SeverityCritical: 5}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// ----------------------------------------------------------------------------
// Deploy keys
// ----------------------------------------------------------------------------

// DeployKeysAuditor audits the deploy keys enabled on each project.
type DeployKeysAuditor struct {
	now func() time.Time
}

// NewDeployKeysAuditor instantiates a new deploy keys auditor.
func NewDeployKeysAuditor() *DeployKeysAuditor { return &DeployKeysAuditor{now: time.Now} }

// Name returns the module identifier.
func (a *DeployKeysAuditor) Name() string { return string(ModuleDeployKeys) }

// AuditProject reports write access, age, expiry and (when the policy lists
// keys) unmanaged keys for every deploy key on the project.
func (a *DeployKeysAuditor) AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject, cfg *config.PolicyConfig) ([]DeployKeyFinding, error) {
	if client == nil || project == nil || client.DeployKeys() == nil {
		return nil, nil
	}
	var keys []*gitlab.ProjectDeployKey
	page := int64(1)
	for {
		batch, resp, err := client.DeployKeys().ListProjectDeployKeys(project.ID, &gitlab.ListProjectDeployKeysOptions{ListOptions: gitlab.ListOptions{Page: page, PerPage: 100}}, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list deploy keys for project %d: %w", project.ID, err)
		}
		keys = append(keys, batch...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	webURL, state, archived, inactive := projectContext(client, project)
	var policy *config.DeployKeysConfig
	if cfg != nil {
		policy = cfg.Policies.DeployKeys
	}
	maxAge := DefaultDeployKeyMaxAgeDays
	if policy != nil && policy.MaxAgeDays != nil {
		maxAge = *policy.MaxAgeDays
	}
	now := a.now()

	findings := make([]DeployKeyFinding, 0, len(keys))
	for _, k := range keys {
		if k == nil {
			continue
		}
		managed := managedDeployKey(policy, k)
		severity := SeverityPass
		var violations, fixes []string

		if k.CanPush && (managed == nil || managed.CanPush == nil || !*managed.CanPush) {
			severity = worst(severity, SeverityMedium)
			violations = append(violations, "Deploy key has write (push) access")
			fixes = append(fixes, "Make the key read-only unless it must push; list it with can_push: true in the policy if it must")
		}
		age := 0
		created := ""
		if k.CreatedAt != nil {
			age = int(now.Sub(*k.CreatedAt).Hours() / 24)
			created = k.CreatedAt.Format(time.DateOnly)
			if age > maxAge {
				sev := SeverityLow
				if age > 2*maxAge {
					sev = SeverityMedium
				}
				severity = worst(severity, sev)
				violations = append(violations, fmt.Sprintf("Deploy key is %d days old (limit %d)", age, maxAge))
				fixes = append(fixes, "Rotate the key: add a new key, update its users, then remove the old one")
			}
		}
		expires := ""
		if k.ExpiresAt != nil {
			expires = k.ExpiresAt.Format(time.DateOnly)
			if !k.ExpiresAt.After(now) {
				severity = worst(severity, SeverityMedium)
				violations = append(violations, "Deploy key has expired but is still enabled")
				fixes = append(fixes, "Remove the expired key from the project")
			}
		}
		if policy != nil && len(policy.Keys) > 0 && managed == nil {
			severity = worst(severity, SeverityLow)
			violations = append(violations, "Deploy key is not in the policy")
			fixes = append(fixes, "Add the key to deploy_keys.keys or remove it from the project")
		}

		severity = AdjustSeverityForProject(severity, archived, inactive)
		details := "Deploy key is compliant"
		if len(violations) > 0 {
			details = strings.Join(violations, "; ")
		}
		findings = append(findings, DeployKeyFinding{
			ProjectID: project.ID, ProjectName: project.Name, ProjectPath: project.PathWithNamespace,
			ProjectWebURL: webURL, ProjectStatus: state,
			KeyTitle: k.Title, Fingerprint: k.FingerprintSHA256, CanPush: k.CanPush,
			CreatedAt: created, AgeDays: age, ExpiresAt: expires,
			Severity: severity, Violations: violations, Details: details, Remediation: strings.Join(fixes, "; "),
		})
	}
	return findings, nil
}

// managedDeployKey returns the policy entry for k, matched like the
// deploy_keys reconciler: by fingerprint when known, otherwise by title.
func managedDeployKey(policy *config.DeployKeysConfig, k *gitlab.ProjectDeployKey) *config.DeployKeyConfig {
	if policy == nil {
		return nil
	}
	for i := range policy.Keys {
		want := &policy.Keys[i]
		fp := strings.TrimRight(strings.TrimSpace(want.Fingerprint), "=")
		if fp == "" && want.Key != "" {
			fp, _ = config.SSHKeyFingerprint(want.Key)
		}
		if fp != "" {
			if strings.TrimRight(k.FingerprintSHA256, "=") == fp {
				return want
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k.Title), strings.TrimSpace(want.Title)) {
			return want
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Deploy tokens
// ----------------------------------------------------------------------------

// DeployTokensAuditor audits project and group deploy tokens.
type DeployTokensAuditor struct {
	now func() time.Time
}

// NewDeployTokensAuditor instantiates a new deploy tokens auditor.
func NewDeployTokensAuditor() *DeployTokensAuditor { return &DeployTokensAuditor{now: time.Now} }

// Name returns the module identifier.
func (a *DeployTokensAuditor) Name() string { return string(ModuleDeployTokens) }

// AuditProject audits the project's deploy tokens.
func (a *DeployTokensAuditor) AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject, cfg *config.PolicyConfig) ([]DeployTokenFinding, error) {
	if client == nil || project == nil || client.DeployTokens() == nil {
		return nil, nil
	}
	tokens, err := collectTokens(func(page int64) ([]*gitlab.DeployToken, *gitlab.Response, error) {
		return client.DeployTokens().ListProjectDeployTokens(project.ID, &gitlab.ListProjectDeployTokensOptions{ListOptions: gitlab.ListOptions{Page: page, PerPage: 100}}, gitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list deploy tokens for project %d: %w", project.ID, err)
	}
	webURL, state, archived, inactive := projectContext(client, project)
	findings := a.evaluate(tokens, cfg)
	for i := range findings {
		findings[i].OwnerType, findings[i].OwnerID, findings[i].OwnerPath = "project", project.ID, project.PathWithNamespace
		findings[i].OwnerWebURL, findings[i].ProjectStatus = webURL, state
		findings[i].Severity = AdjustSeverityForProject(findings[i].Severity, archived, inactive)
	}
	return findings, nil
}

// AuditGroup audits the group's deploy tokens.
func (a *DeployTokensAuditor) AuditGroup(ctx context.Context, client gl.GitLabClient, group *discovery.TargetGroup, cfg *config.PolicyConfig) ([]DeployTokenFinding, error) {
	if client == nil || group == nil || client.DeployTokens() == nil {
		return nil, nil
	}
	tokens, err := collectTokens(func(page int64) ([]*gitlab.DeployToken, *gitlab.Response, error) {
		return client.DeployTokens().ListGroupDeployTokens(group.ID, &gitlab.ListGroupDeployTokensOptions{ListOptions: gitlab.ListOptions{Page: page, PerPage: 100}}, gitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list deploy tokens for group %d: %w", group.ID, err)
	}
	findings := a.evaluate(tokens, cfg)
	for i := range findings {
		findings[i].OwnerType, findings[i].OwnerID, findings[i].OwnerPath = "group", group.ID, group.FullPath
		findings[i].OwnerWebURL = groupWebURL(client, group)
	}
	return findings, nil
}

func collectTokens(list func(page int64) ([]*gitlab.DeployToken, *gitlab.Response, error)) ([]*gitlab.DeployToken, error) {
	var out []*gitlab.DeployToken
	page := int64(1)
	for {
		batch, resp, err := list(page)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if resp == nil || resp.NextPage == 0 {
			return out, nil
		}
		page = resp.NextPage
	}
}

// evaluate applies the baseline rules and the policy's deploy_tokens rules.
// Without a policy: expired tokens, tokens without expiry and write scopes
// are reported. With a policy, its allowed scopes and expiry limits apply.
func (a *DeployTokensAuditor) evaluate(tokens []*gitlab.DeployToken, cfg *config.PolicyConfig) []DeployTokenFinding {
	var policy *config.DeployTokensConfig
	if cfg != nil {
		policy = cfg.Policies.DeployTokens
	}
	allowed := map[string]bool{}
	if policy != nil {
		for _, s := range policy.AllowedScopes {
			allowed[s] = true
		}
	}
	now := a.now()

	findings := make([]DeployTokenFinding, 0, len(tokens))
	for _, t := range tokens {
		if t == nil || t.Revoked {
			continue
		}
		severity := SeverityPass
		var violations, fixes []string
		expired := t.Expired || (t.ExpiresAt != nil && !t.ExpiresAt.After(now))
		expires := ""
		if t.ExpiresAt != nil {
			expires = t.ExpiresAt.Format(time.DateOnly)
		}

		if expired {
			severity = worst(severity, SeverityMedium)
			violations = append(violations, "Deploy token has expired but has not been revoked")
			fixes = append(fixes, "Revoke the token (or set deploy_tokens.revoke_expired)")
		} else {
			if t.ExpiresAt == nil {
				severity = worst(severity, SeverityMedium)
				violations = append(violations, "Deploy token never expires")
				fixes = append(fixes, "Replace it with a token that has an expiry date")
			} else if policy != nil && policy.MaxExpirationDays != nil && t.ExpiresAt.After(now.AddDate(0, 0, *policy.MaxExpirationDays)) {
				severity = worst(severity, SeverityLow)
				violations = append(violations, fmt.Sprintf("Deploy token expires %s, more than %d days from now", expires, *policy.MaxExpirationDays))
				fixes = append(fixes, "Replace it with a shorter-lived token")
			}
		}

		scopes := append([]string(nil), t.Scopes...)
		sort.Strings(scopes)
		var notAllowed, write []string
		for _, s := range scopes {
			switch {
			case len(allowed) > 0 && !allowed[s]:
				notAllowed = append(notAllowed, s)
			case len(allowed) == 0 && writeTokenScopes[s]:
				write = append(write, s)
			}
		}
		if len(notAllowed) > 0 {
			severity = worst(severity, SeverityHigh)
			violations = append(violations, "Scopes not allowed by policy: "+strings.Join(notAllowed, ", "))
			fixes = append(fixes, "Replace the token with one limited to deploy_tokens.allowed_scopes")
		}
		if len(write) > 0 {
			severity = worst(severity, SeverityMedium)
			violations = append(violations, "Deploy token has write scopes: "+strings.Join(write, ", "))
			fixes = append(fixes, "Use read-only scopes unless the token must publish; set deploy_tokens.allowed_scopes to make this explicit")
		}

		details := "Deploy token is compliant"
		if len(violations) > 0 {
			details = strings.Join(violations, "; ")
		}
		findings = append(findings, DeployTokenFinding{
			TokenName: t.Name, Username: t.Username, Scopes: scopes, ExpiresAt: expires, Expired: expired,
			Severity: severity, Violations: violations, Details: details, Remediation: strings.Join(fixes, "; "),
		})
	}
	return findings
}

// ----------------------------------------------------------------------------
// Group settings
// ----------------------------------------------------------------------------

// GroupSettingsAuditor audits group security settings against a baseline
// and, when the policy has group_settings, against the policy.
type GroupSettingsAuditor struct{}

// NewGroupSettingsAuditor instantiates a new group settings auditor.
func NewGroupSettingsAuditor() *GroupSettingsAuditor { return &GroupSettingsAuditor{} }

// Name returns the module identifier.
func (a *GroupSettingsAuditor) Name() string { return string(ModuleGroupSettings) }

// AuditGroup reports weak security settings and drift from the policy.
func (a *GroupSettingsAuditor) AuditGroup(ctx context.Context, client gl.GitLabClient, group *discovery.TargetGroup, cfg *config.PolicyConfig) (*GroupSettingFinding, error) {
	if client == nil || group == nil {
		return nil, nil
	}
	live, _, err := client.Groups().GetGroup(group.ID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get group %d: %w", group.ID, err)
	}
	if live == nil {
		return nil, nil
	}

	severity := SeverityPass
	var violations, fixes []string
	add := func(sev Severity, violation, fix string) {
		severity = worst(severity, sev)
		violations = append(violations, violation)
		fixes = append(fixes, fix)
	}

	// Baseline checks, which apply with or without a policy.
	if !live.RequireTwoFactorAuth {
		add(SeverityHigh, "Two-factor authentication is not required for members", "Set require_two_factor_authentication: true")
	}
	if live.Visibility == gitlab.PublicVisibility {
		add(SeverityMedium, "Group is public", "Make the group private or internal unless it is meant to be public")
	}
	if live.ProjectCreationLevel == gitlab.DeveloperProjectCreation {
		add(SeverityLow, "Developers can create projects", "Set project_creation_level to maintainer or owner")
	}
	if bp := live.DefaultBranchProtectionDefaults; bp != nil && bp.AllowForcePush {
		add(SeverityMedium, "Default branch protection allows force push", "Set default_branch_protection.allow_force_push: false")
	}

	// Drift from the policy, computed by the group_settings reconciler so
	// audit and enforcement agree.
	if cfg != nil && cfg.Policies.GroupSettings != nil {
		plan, err := governance.NewGroupSettingsReconciler().PlanGroup(ctx, client, live, cfg)
		if err != nil {
			return nil, err
		}
		for _, d := range plan.Diffs {
			for _, f := range d.Fields {
				add(SeverityMedium, fmt.Sprintf("%s is %v, policy requires %v", f.Field, f.OldValue, f.NewValue), "Run governance apply to enforce group_settings")
			}
		}
	}

	details := "Group settings are compliant"
	if len(violations) > 0 {
		details = strings.Join(violations, "; ")
	}
	return &GroupSettingFinding{
		GroupID: group.ID, GroupPath: group.FullPath, GroupWebURL: groupWebURL(client, group),
		Visibility: string(live.Visibility), RequireTwoFactorAuth: live.RequireTwoFactorAuth,
		ProjectCreationLevel: string(live.ProjectCreationLevel), SubGroupCreationLevel: string(live.SubGroupCreationLevel),
		Severity: severity, Violations: violations, Details: details, Remediation: strings.Join(fixes, "; "),
	}, nil
}
