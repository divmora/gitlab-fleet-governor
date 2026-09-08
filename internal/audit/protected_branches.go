package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// ProtectedBranchesAuditor audits project branch protection configurations.
type ProtectedBranchesAuditor struct{}

// NewProtectedBranchesAuditor instantiates a new protected branches auditor.
func NewProtectedBranchesAuditor() *ProtectedBranchesAuditor {
	return &ProtectedBranchesAuditor{}
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
		webURL = fmt.Sprintf("%s/%s", strings.TrimRight(client.BaseURL(), "/api/v4"), project.PathWithNamespace)
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

		pushSummary, directUsers, directGroups := summarizeBranchAccessLevels(b.PushAccessLevels)
		mergeSummary, _, _ := summarizeBranchAccessLevels(b.MergeAccessLevels)

		var violations []string
		severity := SeverityPass

		// 1. Force Push Enabled Check
		if b.AllowForcePush {
			violations = append(violations, "Force push is enabled (allow_force_push = true)")
			severity = SeverityCritical
		}

		// 2. Direct User Push Grants Check
		if len(directUsers) > 0 {
			violations = append(violations, fmt.Sprintf("Direct user push access granted to: %s", strings.Join(directUsers, ", ")))
			if severity != SeverityCritical {
				severity = SeverityHigh
			}
		}

		// 3. Absent Code Owner Approval Requirements Check
		if !b.CodeOwnerApprovalRequired {
			violations = append(violations, "Code Owner approval is disabled (code_owner_approval_required = false)")
			if severity != SeverityCritical {
				severity = SeverityHigh
			}
		}

		// 4. Unrestricted Push Permissions Check (e.g. Developers allowed to push directly)
		for _, access := range b.PushAccessLevels {
			if access != nil && access.AccessLevel == gitlab.DeveloperPermissions && access.UserID == 0 && access.GroupID == 0 {
				violations = append(violations, "Unrestricted push access: all Developers permitted to push directly to protected branch")
				if severity != SeverityCritical && severity != SeverityHigh {
					severity = SeverityMedium
				}
			}
		}

		details := "Branch protection meets all security baselines"
		remediation := "No action required"
		if len(violations) > 0 {
			details = strings.Join(violations, "; ")
			remediation = "Disable force push, restrict push access to Maintainers or Merge Requests, require Code Owner approvals, and remove direct user push grants"
		}

		findings = append(findings, ProtectedBranchFinding{
			ProjectID:                 project.ID,
			ProjectName:               project.Name,
			ProjectPath:               project.PathWithNamespace,
			ProjectWebURL:             webURL,
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

	// Flag unprotected default branch if missing from protected branches
	if !defaultBranchFound && len(allBranches) == 0 {
		findings = append(findings, ProtectedBranchFinding{
			ProjectID:                 project.ID,
			ProjectName:               project.Name,
			ProjectPath:               project.PathWithNamespace,
			ProjectWebURL:             webURL,
			BranchName:                defaultBranch,
			IsDefaultBranch:           true,
			IsProtected:               false,
			AllowForcePush:            true,
			CodeOwnerApprovalRequired: false,
			PushAccessLevelsSummary:   "Unprotected (open push)",
			MergeAccessLevelsSummary:  "Unprotected (open merge)",
			Severity:                  SeverityCritical,
			Violations:                []string{"Default branch is completely unprotected"},
			Details:                   fmt.Sprintf("Default branch '%s' has zero branch protection rules configured", defaultBranch),
			Remediation:               "Protect the default branch with Maintainer push restrictions and mandatory Code Owner approvals",
		})
	}

	return findings, nil
}

func summarizeBranchAccessLevels(descs []*gitlab.BranchAccessDescription) (summary string, directUsers []string, directGroups []string) {
	if len(descs) == 0 {
		return "No access configured", nil, nil
	}

	parts := make([]string, 0, len(descs))
	for _, d := range descs {
		if d == nil {
			continue
		}
		if d.UserID > 0 {
			uStr := fmt.Sprintf("User ID %d", d.UserID)
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
