package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// UserAccessAuditor audits project member permissions and expiration dates.
type UserAccessAuditor struct{}

// NewUserAccessAuditor instantiates a new user access audit module.
func NewUserAccessAuditor() *UserAccessAuditor {
	return &UserAccessAuditor{}
}

// Name returns the module identifier.
func (a *UserAccessAuditor) Name() string {
	return string(ModuleUserAccess)
}

// AuditProject audits all direct and inherited members of a project.
func (a *UserAccessAuditor) AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]UserAccessFinding, error) {
	if client == nil || project == nil {
		return nil, nil
	}

	// 1. Fetch direct project members to distinguish direct vs inherited
	directMembers, _, err := client.Members().ListProjectMembers(project.ID, &gitlab.ListProjectMembersOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100},
	}, gitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list direct project members for %d: %w", project.ID, err)
	}

	directMap := make(map[int]struct{})
	for _, m := range directMembers {
		if m != nil {
			directMap[m.ID] = struct{}{}
		}
	}

	// 2. Fetch all members (both direct and inherited from parent groups)
	var allMembers []*gitlab.ProjectMember
	page := 1
	for {
		opts := &gitlab.ListProjectMembersOptions{
			ListOptions: gitlab.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		}
		members, resp, err := client.Members().ListAllProjectMembers(project.ID, opts, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list all project members for %d: %w", project.ID, err)
		}
		allMembers = append(allMembers, members...)
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
		webURL = fmt.Sprintf("%s/%s", strings.TrimRight(client.BaseURL(), "/api/v4"), project.PathWithNamespace)
	}

	findings := make([]UserAccessFinding, 0, len(allMembers))

	for _, m := range allMembers {
		if m == nil {
			continue
		}

		_, isDirect := directMap[m.ID]
		level := int(m.AccessLevel)
		roleName := AccessLevelToName(level)

		hasExpiration := m.ExpiresAt != nil && m.ExpiresAt.String() != ""
		expStr := ""
		if hasExpiration {
			expStr = m.ExpiresAt.String()
		}

		finding := UserAccessFinding{
			ProjectID:      project.ID,
			ProjectName:    project.Name,
			ProjectPath:    project.PathWithNamespace,
			ProjectWebURL:  webURL,
			UserID:         m.ID,
			Username:       m.Username,
			Name:           m.Name,
			Email:          m.Email,
			AccessLevel:    level,
			AccessRoleName: roleName,
			IsDirect:       isDirect,
			ExpiresAt:      expStr,
			HasExpiration:  hasExpiration,
			Severity:       SeverityPass,
			ViolationType:  "COMPLIANT",
			Details:        "Member has compliant access configuration",
			Remediation:    "No action required",
		}

		// Security Risk Evaluation: Non-owner members with indefinite access
		if level < int(gitlab.OwnerPermissions) && !hasExpiration {
			if level >= int(gitlab.MaintainerPermissions) {
				finding.Severity = SeverityCritical
				finding.ViolationType = "INDEFINITE_MAINTAINER_ACCESS"
				finding.Details = fmt.Sprintf("High-privilege Maintainer %s (%s) has indefinite access with no expiration date configured", m.Username, roleName)
				finding.Remediation = "Set a mandatory expiration date (e.g. 90-180 days) for non-owner member access"
			} else {
				finding.Severity = SeverityHigh
				finding.ViolationType = "INDEFINITE_NON_OWNER_ACCESS"
				finding.Details = fmt.Sprintf("Non-owner member %s with %s permissions has indefinite access with no expiration date configured", m.Username, roleName)
				finding.Remediation = "Enforce access expiration date according to enterprise access control policy"
			}
		}

		findings = append(findings, finding)
	}

	return findings, nil
}
