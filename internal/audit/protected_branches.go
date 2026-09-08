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

// ProtectedBranchesAuditor audits project branch protection configurations.
type ProtectedBranchesAuditor struct {
	registry *UserRegistry
}

// NewProtectedBranchesAuditor instantiates a new protected branches auditor.
func NewProtectedBranchesAuditor(registry ...*UserRegistry) *ProtectedBranchesAuditor {
	var reg *UserRegistry
	if len(registry) > 0 {
		reg = registry[0]
	}
	return &ProtectedBranchesAuditor{registry: reg}
}

// SetUserRegistry binds a UserRegistry to the auditor.
func (a *ProtectedBranchesAuditor) SetUserRegistry(r *UserRegistry) {
	a.registry = r
}

// Name returns the module identifier.
func (a *ProtectedBranchesAuditor) Name() string {
	return string(ModuleProtectedBranches)
}

// AuditProject inspects branch protection rules across a project.
func (a *ProtectedBranchesAuditor) AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]ProtectedBranchFinding, error) {
	if client == nil || project == nil {
		return nil, nil
	}

	// Project active/archived/inactive state
	var lastAct *time.Time
	if project.Raw != nil {
		lastAct = project.Raw.LastActivityAt
	}
	projState, isArchived, isInactive := EvaluateProjectState(project.Archived, lastAct)

	// 1. Fetch all protected branches
	var allBranches []*gitlab.ProtectedBranch
	page := 1
	for {
		opts := &gitlab.ListProtectedBranchesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		}
		branches, resp, err := client.ProtectedBranches().ListProtectedBranches(project.ID, opts, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list protected branches for project %d: %w", project.ID, err)
		}
		allBranches = append(allBranches, branches...)
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

	defaultBranch := project.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	findings := make([]ProtectedBranchFinding, 0)
	defaultBranchFound := false

	for _, b := range allBranches {
		if b == nil {
			continue
		}

		isDefault := b.Name == defaultBranch || b.Name == "master" || b.Name == "main"
		if isDefault {
			defaultBranchFound = true
		}

		pushSummary, directUsers, directGroups := summarizeBranchAccessLevels(ctx, b.PushAccessLevels, a.registry)
		mergeSummary, _, _ := summarizeBranchAccessLevels(ctx, b.MergeAccessLevels, a.registry)

		var violations []string
		var remediations []string
		severity := SeverityPass

		// 1. Force Push Enabled Check
		if b.AllowForcePush {
			violations = append(violations, "Force push is enabled (allow_force_push = true)")
			remediations = append(remediations, "Set allow_force_push: false")
			severity = SeverityCritical
		}

		// 2. Direct User Push Grants Check
		if len(directUsers) > 0 {
			violations = append(violations, fmt.Sprintf("Direct user push access granted to: %s", strings.Join(directUsers, ", ")))
			remediations = append(remediations, "Remove individual user push grants; use role access tiers")
			if severity != SeverityCritical {
				severity = SeverityHigh
			}
		}

		// 3. Absent Code Owner Approval Requirements Check
		if !b.CodeOwnerApprovalRequired {
			violations = append(violations, "Code Owner approval is disabled (code_owner_approval_required = false)")
			remediations = append(remediations, "Enable code_owner_approval_required: true")
			if severity != SeverityCritical {
				severity = SeverityHigh
			}
		}

		// 4. Unrestricted Push Permissions Check
		for _, access := range b.PushAccessLevels {
			if access != nil && access.AccessLevel == gitlab.DeveloperPermissions && access.UserID == 0 && access.GroupID == 0 {
				violations = append(violations, "Unrestricted push access: all Developers permitted to push directly to protected branch")
				remediations = append(remediations, "Restrict push access to Maintainers or Merge Requests only")
				if severity != SeverityCritical && severity != SeverityHigh {
					severity = SeverityMedium
				}
			}
		}

		details := "Branch protection meets all security baselines"
		remediation := "No action required"
		if len(violations) > 0 {
			details = strings.Join(violations, "; ")
			remediation = strings.Join(remediations, "; ")
		}

		// De-prioritize archived or inactive projects
		severity = AdjustSeverityForProject(severity, isArchived, isInactive)
		if isArchived {
			details = fmt.Sprintf("[ARCHIVED PROJECT] %s", details)
			remediation = "Repository is archived; confirm branch protection baselines or maintain read-only state"
		} else if isInactive {
			details = fmt.Sprintf("[%s] %s", projState, details)
		}

		findings = append(findings, ProtectedBranchFinding{
			ProjectID:                 project.ID,
			ProjectName:               project.Name,
			ProjectPath:               project.PathWithNamespace,
			ProjectWebURL:             webURL,
			ProjectStatus:             projState,
			BranchName:                b.Name,
			IsDefaultBranch:           isDefault,
			IsProtected:               true,
			AllowForcePush:            b.AllowForcePush,
			CodeOwnerApprovalRequired: b.CodeOwnerApprovalRequired,
			PushAccessLevelsSummary:   pushSummary,
			MergeAccessLevelsSummary:  mergeSummary,
			DirectUserPushGrants:      directUsers,
			DirectGroupPushGrants:     directGroups,
			Severity:                  severity,
			Violations:                violations,
			Details:                   details,
			Remediation:               remediation,
		})
	}

	// Flag if the project's default branch has no branch protection configured
	if !defaultBranchFound {
		defSev := AdjustSeverityForProject(SeverityCritical, isArchived, isInactive)
		details := fmt.Sprintf("Default branch '%s' has zero branch protection rules configured", defaultBranch)
		remediation := fmt.Sprintf("Protect default branch '%s': configure push_access_level=40, merge_access_level=40, code_owner_approval_required=true", defaultBranch)
		if isArchived {
			details = fmt.Sprintf("[ARCHIVED PROJECT] %s", details)
			remediation = "Repository is archived; verify branch rules"
		} else if isInactive {
			details = fmt.Sprintf("[%s] %s", projState, details)
		}

		findings = append(findings, ProtectedBranchFinding{
			ProjectID:       project.ID,
			ProjectName:     project.Name,
			ProjectPath:     project.PathWithNamespace,
			ProjectWebURL:   webURL,
			ProjectStatus:   projState,
			BranchName:      defaultBranch,
			IsDefaultBranch: true,
			IsProtected:     false,
			Severity:        defSev,
			Violations:      []string{"Default branch is completely unprotected"},
			Details:         details,
			Remediation:     remediation,
		})
	}

	return findings, nil
}

func summarizeBranchAccessLevels(ctx context.Context, descs []*gitlab.BranchAccessDescription, reg *UserRegistry) (summary string, directUsers []string, directGroups []string) {
	if len(descs) == 0 {
		return "No access configured", nil, nil
	}

	parts := make([]string, 0, len(descs))
	for _, d := range descs {
		if d == nil {
			continue
		}
		if d.UserID > 0 {
			var uStr string
			if reg != nil {
				uStr = reg.FormatUser(ctx, d.UserID)
			} else {
				uStr = fmt.Sprintf("User ID %d", d.UserID)
			}
			directUsers = append(directUsers, uStr)
			parts = append(parts, uStr)
		} else if d.GroupID > 0 {
			gStr := fmt.Sprintf("Group ID %d", d.GroupID)
			directGroups = append(directGroups, gStr)
			parts = append(parts, gStr)
		} else if d.DeployKeyID > 0 {
			parts = append(parts, fmt.Sprintf("DeployKey %d", d.DeployKeyID))
		} else {
			parts = append(parts, AccessLevelToName(int(d.AccessLevel)))
		}
	}

	return strings.Join(parts, ", "), directUsers, directGroups
}
