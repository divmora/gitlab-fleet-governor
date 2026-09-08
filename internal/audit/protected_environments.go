package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// ProtectedEnvironmentsAuditor audits deployment access controls and approvals.
type ProtectedEnvironmentsAuditor struct{}

// NewProtectedEnvironmentsAuditor instantiates a new protected environments auditor.
func NewProtectedEnvironmentsAuditor() *ProtectedEnvironmentsAuditor {
	return &ProtectedEnvironmentsAuditor{}
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

		deploySummary, directUsers, directGroups := summarizeEnvAccessLevels(env.DeployAccessLevels)

		var violations []string
		severity := SeverityPass

		// 1. Unconstrained production deployment (Zero required approvals on production environment)
		if isProd && env.RequiredApprovalCount == 0 {
			violations = append(violations, "Production environment requires zero deployment approvals (required_approval_count = 0)")
			severity = SeverityCritical
		}

		// 2. Direct user deploy access grants
		if len(directUsers) > 0 {
			violations = append(violations, fmt.Sprintf("Direct user deployment access granted to: %s", strings.Join(directUsers, ", ")))
			if severity != SeverityCritical {
				severity = SeverityHigh
			}
		}

		// 3. Permissive role access on production (e.g. Developer or Reporter can deploy directly without approvals)
		if isProd && env.RequiredApprovalCount == 0 {
			for _, acc := range env.DeployAccessLevels {
				if acc != nil && acc.AccessLevel <= gitlab.DeveloperPermissions && acc.AccessLevel > 0 {
					violations = append(violations, fmt.Sprintf("Unrestricted deploy permissions: role %s can deploy directly to production", AccessLevelToName(int(acc.AccessLevel))))
					severity = SeverityCritical
					break
				}
			}
		}

		details := "Protected environment satisfies deployment governance standards"
		remediation := "No action required"
		if len(violations) > 0 {
			details = strings.Join(violations, "; ")
			remediation = "Require at least 1 or 2 deployment approvals, remove direct user deploy access, and restrict deployment roles to Maintainers or authorized deployment groups"
		}

		findings = append(findings, ProtectedEnvironmentFinding{
			ProjectID:                 project.ID,
			ProjectName:               project.Name,
			ProjectPath:               project.PathWithNamespace,
			ProjectWebURL:             webURL,
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

func summarizeEnvAccessLevels(levels []*gitlab.EnvironmentAccessDescription) (summary string, directUsers []string, directGroups []string) {
	if len(levels) == 0 {
		return "No deployment access configured", nil, nil
	}

	parts := make([]string, 0, len(levels))
	for _, l := range levels {
		if l == nil {
			continue
		}
		if l.UserID > 0 {
			uStr := fmt.Sprintf("User ID %d", l.UserID)
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
