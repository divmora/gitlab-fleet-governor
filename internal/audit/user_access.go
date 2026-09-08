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

// UserAccessAuditor audits project member permissions and expiration dates.
type UserAccessAuditor struct {
	registry *UserRegistry
}

// NewUserAccessAuditor instantiates a new user access audit module.
func NewUserAccessAuditor(registry ...*UserRegistry) *UserAccessAuditor {
	var reg *UserRegistry
	if len(registry) > 0 {
		reg = registry[0]
	}
	return &UserAccessAuditor{registry: reg}
}

// SetUserRegistry binds a UserRegistry to the auditor.
func (a *UserAccessAuditor) SetUserRegistry(r *UserRegistry) {
	a.registry = r
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

	// Determine project active/archived/inactive status
	var lastAct *time.Time
	if project.Raw != nil {
		lastAct = project.Raw.LastActivityAt
	}
	projState, isArchived, isInactive := EvaluateProjectState(project.Archived, lastAct)

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
		webURL = fmt.Sprintf("%s/%s", strings.TrimSuffix(strings.TrimSuffix(client.BaseURL(), "/api/v4"), "/"), project.PathWithNamespace)
	}

	findings := make([]UserAccessFinding, 0, len(allMembers))

	for _, m := range allMembers {
		if m == nil {
			continue
		}

		// Register in fleet-wide user directory
		if a.registry != nil {
			a.registry.Register(m.ID, m.Username, m.Name, m.Email, m.State, m.WebURL, project.PathWithNamespace)
		}

		_, isDirect := directMap[m.ID]
		membershipType := "Inherited (Group)"
		if isDirect {
			membershipType = "Direct (Project)"
		}

		isBot := IsBotOrServiceAccount(m.Username, m.Name)
		accountType := "Human"
		if isBot {
			accountType = "Service Account / Bot"
		}

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
			ProjectStatus:  projState,
			UserID:         m.ID,
			Username:       m.Username,
			Name:           m.Name,
			Email:          m.Email,
			AccessLevel:    level,
			AccessRoleName: roleName,
			IsDirect:       isDirect,
			MembershipType: membershipType,
			IsBot:          isBot,
			AccountType:    accountType,
			ExpiresAt:      expStr,
			HasExpiration:  hasExpiration,
			Severity:       SeverityPass,
			ViolationType:  "COMPLIANT",
			Details:        "Member has compliant access configuration",
			Remediation:    "No action required",
		}

		// Security Risk Evaluation: Non-owner members with indefinite access
		if level < int(gitlab.OwnerPermissions) && !hasExpiration {
			var baseSev Severity
			if level >= int(gitlab.MaintainerPermissions) {
				baseSev = SeverityCritical
				finding.ViolationType = "INDEFINITE_MAINTAINER_ACCESS"
				finding.Details = fmt.Sprintf("High-privilege Maintainer %s (%s) has indefinite access with no expiration date configured", m.Username, roleName)
				if isBot {
					finding.Remediation = "Rotate bot token periodically and set explicit expiration on Project/Group Access Tokens (max 365 days)"
				} else {
					finding.Remediation = "Set mandatory expiration date (expires_at <= 90 days) via Project Members API (PUT /projects/:id/members/:user_id) or GitLab UI"
				}
			} else {
				baseSev = SeverityHigh
				finding.ViolationType = "INDEFINITE_NON_OWNER_ACCESS"
				finding.Details = fmt.Sprintf("Non-owner member %s with %s permissions has indefinite access with no expiration date configured", m.Username, roleName)
				if isBot {
					finding.Remediation = "Set defined expiration on Project/Group Access Tokens"
				} else {
					finding.Remediation = "Enforce access expiration date according to enterprise access control policy"
				}
			}

			// De-prioritize archived or inactive projects
			finding.Severity = AdjustSeverityForProject(baseSev, isArchived, isInactive)
			if isArchived {
				finding.Details = fmt.Sprintf("[ARCHIVED PROJECT] %s", finding.Details)
				finding.Remediation = "Repository is archived; confirm access revocation or maintain read-only state"
			} else if isInactive {
				finding.Details = fmt.Sprintf("[%s] %s", projState, finding.Details)
			}
		}

		findings = append(findings, finding)
	}

	return findings, nil
}
