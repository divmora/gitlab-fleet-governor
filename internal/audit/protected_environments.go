package audit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// ProtectedEnvironmentsAuditor audits deployment access controls and approvals.
type ProtectedEnvironmentsAuditor struct {
	registry *UserRegistry
}

// NewProtectedEnvironmentsAuditor instantiates a new protected environments auditor.
func NewProtectedEnvironmentsAuditor(registry ...*UserRegistry) *ProtectedEnvironmentsAuditor {
	var reg *UserRegistry
	if len(registry) > 0 {
		reg = registry[0]
	}
	return &ProtectedEnvironmentsAuditor{registry: reg}
}

// SetUserRegistry binds a UserRegistry to the auditor.
func (a *ProtectedEnvironmentsAuditor) SetUserRegistry(r *UserRegistry) {
	a.registry = r
}

// Name returns the module identifier.
func (a *ProtectedEnvironmentsAuditor) Name() string {
	return string(ModuleProtectedEnvironments)
}

// AuditProject audits protected environments for the targeted project.
func (a *ProtectedEnvironmentsAuditor) AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]ProtectedEnvironmentFinding, error) {
	if client == nil || project == nil {
		return nil, nil
	}

	// Project active/archived/inactive state
	var lastAct *time.Time
	if project.Raw != nil {
		lastAct = project.Raw.LastActivityAt
	}
	projState, isArchived, isInactive := EvaluateProjectState(project.Archived, lastAct)

	var allEnvs []*gitlab.ProtectedEnvironment
	page := 1
	for {
		opts := &gitlab.ListProtectedEnvironmentsOptions{
			Page:    page,
			PerPage: 100,
		}
		envs, resp, err := client.ProtectedEnvironments().ListProtectedEnvironments(project.ID, opts, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list protected environments for project %d: %w", project.ID, err)
		}
		allEnvs = append(allEnvs, envs...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	webURL := ""
	if project.Raw != nil {
		webURL = project.Raw.WebURL
	}
	if webURL == "" {
		webURL = fmt.Sprintf("%s/%s", strings.TrimSuffix(strings.TrimSuffix(client.BaseURL(), "/api/v4"), "/"), project.PathWithNamespace)
	}

	findings := make([]ProtectedEnvironmentFinding, 0, len(allEnvs))

	for _, env := range allEnvs {
		if env == nil {
			continue
		}

		nameLower := strings.ToLower(env.Name)
		isProd := strings.Contains(nameLower, "prod") ||
			strings.Contains(nameLower, "live") ||
			strings.Contains(nameLower, "production")

		deploySummary, directUsers, directGroups := summarizeEnvAccessLevels(ctx, env.DeployAccessLevels, a.registry)

		var violations []string
		var remediations []string
		severity := SeverityPass

		// 1. Unconstrained production deployment (Zero required approvals on production environment)
		if isProd && env.RequiredApprovalCount == 0 {
			violations = append(violations, "Production environment requires zero deployment approvals (required_approval_count = 0)")
			remediations = append(remediations, fmt.Sprintf("Configure required_approval_count >= 1 for production environment '%s'", env.Name))
			severity = SeverityCritical
		}

		// 2. Direct user deploy access grants
		if len(directUsers) > 0 {
			violations = append(violations, fmt.Sprintf("Direct user deployment access granted to: %s", strings.Join(directUsers, ", ")))
			remediations = append(remediations, "Remove individual user deploy access; restrict deployment to role-based group tiers")
			if severity != SeverityCritical {
				severity = SeverityHigh
			}
		}

		// 3. Permissive role access on production
		if isProd && env.RequiredApprovalCount == 0 {
			for _, acc := range env.DeployAccessLevels {
				if acc != nil && acc.AccessLevel <= gitlab.DeveloperPermissions && acc.AccessLevel > 0 {
					violations = append(violations, fmt.Sprintf("Unrestricted deploy permissions: role %s can deploy directly to production", AccessLevelToName(int(acc.AccessLevel))))
					remediations = append(remediations, "Restrict deploy roles on production to Maintainers or approved CI deployment service accounts")
					severity = SeverityCritical
					break
				}
			}
		}

		details := "Protected environment satisfies deployment governance standards"
		remediation := "No action required"
		if len(violations) > 0 {
			details = strings.Join(violations, "; ")
			remediation = strings.Join(remediations, "; ")
		}

		// De-prioritize archived or inactive projects
		severity = AdjustSeverityForProject(severity, isArchived, isInactive)
		if isArchived {
			details = fmt.Sprintf("[ARCHIVED PROJECT] %s", details)
			remediation = "Repository is archived; verify environment deployment policies"
		} else if isInactive {
			details = fmt.Sprintf("[%s] %s", projState, details)
		}

		findings = append(findings, ProtectedEnvironmentFinding{
			ProjectID:                 project.ID,
			ProjectName:               project.Name,
			ProjectPath:               project.PathWithNamespace,
			ProjectWebURL:             webURL,
			ProjectStatus:             projState,
			EnvironmentName:           env.Name,
			IsProduction:              isProd,
			RequiredApprovalCount:     env.RequiredApprovalCount,
			DeployAccessLevelsSummary: deploySummary,
			DirectUserDeployGrants:    directUsers,
			DirectGroupDeployGrants:   directGroups,
			Severity:                  severity,
			Violations:                violations,
			Details:                   details,
			Remediation:               remediation,
		})
	}

	return findings, nil
}

func summarizeEnvAccessLevels(ctx context.Context, levels []*gitlab.EnvironmentAccessDescription, reg *UserRegistry) (summary string, directUsers []string, directGroups []string) {
	if len(levels) == 0 {
		return "No deployment access configured", nil, nil
	}

	parts := make([]string, 0, len(levels))
	for _, l := range levels {
		if l == nil {
			continue
		}
		if l.UserID > 0 {
			var uStr string
			if reg != nil {
				uStr = reg.FormatUser(ctx, l.UserID)
			} else {
				uStr = fmt.Sprintf("User ID %d", l.UserID)
			}
			directUsers = append(directUsers, uStr)
			parts = append(parts, uStr)
		} else if l.GroupID > 0 {
			gStr := fmt.Sprintf("Group ID %d", l.GroupID)
			directGroups = append(directGroups, gStr)
			parts = append(parts, gStr)
		} else if l.AccessLevelDescription != "" {
			parts = append(parts, l.AccessLevelDescription)
		} else {
			parts = append(parts, AccessLevelToName(int(l.AccessLevel)))
		}
	}

	return strings.Join(parts, ", "), directUsers, directGroups
}
