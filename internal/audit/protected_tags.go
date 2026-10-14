package audit

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// ProtectedTagsAuditor audits project protected tag configurations and release protections.
type ProtectedTagsAuditor struct {
	registry *UserRegistry
}

// NewProtectedTagsAuditor instantiates a new protected tags auditor.
func NewProtectedTagsAuditor(registry ...*UserRegistry) *ProtectedTagsAuditor {
	var reg *UserRegistry
	if len(registry) > 0 {
		reg = registry[0]
	}
	return &ProtectedTagsAuditor{registry: reg}
}

// SetUserRegistry binds a UserRegistry to the auditor.
func (a *ProtectedTagsAuditor) SetUserRegistry(r *UserRegistry) {
	a.registry = r
}

// Name returns the module identifier.
func (a *ProtectedTagsAuditor) Name() string {
	return string(ModuleProtectedTags)
}

// AuditProject inspects protected tag rules across a project.
func (a *ProtectedTagsAuditor) AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]ProtectedTagFinding, error) {
	if client == nil || project == nil {
		return nil, nil
	}

	// Project active/archived/inactive/pending deletion state
	var lastAct *time.Time
	isMarked := project.MarkedForDeletion || project.MarkedForDeletionAt != nil || (project.Raw != nil && project.Raw.MarkedForDeletionAt != nil)
	if project.Raw != nil {
		lastAct = project.Raw.LastActivityAt
	}
	projState, isArchived, isInactive := EvaluateProjectLifecycle(project.Archived, isMarked, lastAct)

	// 1. Fetch all protected tags
	var allTags []*gitlab.ProtectedTag
	if client.ProtectedTags() != nil {
		page := 1
		for {
			opts := &gitlab.ListProtectedTagsOptions{
				Page:    page,
				PerPage: 100,
			}
			tags, resp, err := client.ProtectedTags().ListProtectedTags(project.ID, opts, gitlab.WithContext(ctx))
			if err != nil {
				return nil, fmt.Errorf("failed to list protected tags for project %d: %w", project.ID, err)
			}
			allTags = append(allTags, tags...)
			if resp == nil || resp.NextPage == 0 {
				break
			}
			page = resp.NextPage
		}
	}

	webURL := ""
	if project.Raw != nil {
		webURL = project.Raw.WebURL
	}
	if webURL == "" {
		webURL = fmt.Sprintf("%s/%s", strings.TrimSuffix(strings.TrimSuffix(client.BaseURL(), "/api/v4"), "/"), project.PathWithNamespace)
	}

	findings := make([]ProtectedTagFinding, 0)
	hasReleaseTagProtection := false

	for _, t := range allTags {
		if t == nil {
			continue
		}

		if matchesReleasePattern(t.Name) {
			hasReleaseTagProtection = true
		}

		createSummary, directUsers, directGroups := summarizeTagAccessLevels(ctx, t.CreateAccessLevels, a.registry)

		var violations []string
		var remediations []string
		severity := SeverityPass

		// 1. Broad Wildcard Catch-all Protection Check ('*')
		if t.Name == "*" {
			violations = append(violations, "Broad wildcard protection '*' covers all tags and may obscure specific release governance")
			remediations = append(remediations, "Replace catch-all '*' with targeted release patterns (e.g. 'v*', 'release-*')")
			severity = SeverityLow
		}

		// 2. Direct User Create Grants Check
		if len(directUsers) > 0 {
			violations = append(violations, fmt.Sprintf("Direct user tag creation access granted to: %s", strings.Join(directUsers, ", ")))
			remediations = append(remediations, "Remove individual user tag creation grants; enforce role access tiers")
			if severity != SeverityCritical {
				severity = SeverityHigh
			}
		}

		// 3. Permissive Role Tag Creation (Developer access or lower)
		for _, access := range t.CreateAccessLevels {
			if access != nil && access.AccessLevel == gitlab.DeveloperPermissions && access.UserID == 0 && access.GroupID == 0 {
				violations = append(violations, "Permissive tag creation: all Developers permitted to create protected tags")
				remediations = append(remediations, "Restrict tag creation to Maintainers (40) or No Access (0)")
				if severity != SeverityCritical && severity != SeverityHigh {
					severity = SeverityMedium
				}
			}
		}

		details := "Tag protection satisfies release governance baselines"
		remediation := "No action required"
		if len(violations) > 0 {
			details = strings.Join(violations, "; ")
			remediation = strings.Join(remediations, "; ")
		}

		// De-prioritize archived, inactive, or pending deletion projects
		severity = AdjustSeverityForProject(severity, isArchived, isInactive)
		if isMarked {
			details = fmt.Sprintf("[PENDING DELETION] %s", details)
			remediation = "Repository is scheduled for deletion; verify impending purge"
		} else if isArchived {
			details = fmt.Sprintf("[ARCHIVED PROJECT] %s", details)
			remediation = "Repository is archived; confirm tag protection baselines or maintain read-only state"
		} else if isInactive {
			details = fmt.Sprintf("[%s] %s", projState, details)
		}

		findings = append(findings, ProtectedTagFinding{
			ProjectID:                 project.ID,
			ProjectName:               project.Name,
			ProjectPath:               project.PathWithNamespace,
			ProjectWebURL:             webURL,
			ProjectStatus:             projState,
			TagNamePattern:            t.Name,
			IsProtected:               true,
			CreateAccessLevelsSummary: createSummary,
			DirectUserCreateGrants:    directUsers,
			DirectGroupCreateGrants:   directGroups,
			Severity:                  severity,
			Violations:                violations,
			Details:                   details,
			Remediation:               remediation,
		})
	}

	// Flag if the project has zero protected tags configured
	if len(allTags) == 0 {
		defSev := AdjustSeverityForProject(SeverityCritical, isArchived, isInactive)
		details := "Project has zero protected tags configured; releases and tags can be created or deleted arbitrarily"
		remediation := "Configure protected tag patterns (e.g. 'v*', 'release-*') with create_access_level=40 (Maintainer)"
		if isMarked {
			details = fmt.Sprintf("[PENDING DELETION] %s", details)
			remediation = "Repository is scheduled for deletion; verify impending purge"
		} else if isArchived {
			details = fmt.Sprintf("[ARCHIVED PROJECT] %s", details)
			remediation = "Repository is archived; verify tag rules"
		} else if isInactive {
			details = fmt.Sprintf("[%s] %s", projState, details)
		}

		findings = append(findings, ProtectedTagFinding{
			ProjectID:                 project.ID,
			ProjectName:               project.Name,
			ProjectPath:               project.PathWithNamespace,
			ProjectWebURL:             webURL,
			ProjectStatus:             projState,
			TagNamePattern:            "(none)",
			IsProtected:               false,
			CreateAccessLevelsSummary: "Unprotected",
			Severity:                  defSev,
			Violations:                []string{"Project has zero protected tags configured"},
			Details:                   details,
			Remediation:               remediation,
		})
	} else if !hasReleaseTagProtection {
		// Project has protected tags (e.g. test-*), but release tags (v*, release-*) are not covered
		defSev := AdjustSeverityForProject(SeverityHigh, isArchived, isInactive)
		details := "Standard release tag patterns ('v*', 'release-*') are not covered by any protected tag pattern"
		remediation := "Add protected tag patterns for 'v*' and 'release-*' with create_access_level=40"
		if isMarked {
			details = fmt.Sprintf("[PENDING DELETION] %s", details)
			remediation = "Repository is scheduled for deletion; verify impending purge"
		} else if isArchived {
			details = fmt.Sprintf("[ARCHIVED PROJECT] %s", details)
			remediation = "Repository is archived; verify tag rules"
		} else if isInactive {
			details = fmt.Sprintf("[%s] %s", projState, details)
		}

		findings = append(findings, ProtectedTagFinding{
			ProjectID:                 project.ID,
			ProjectName:               project.Name,
			ProjectPath:               project.PathWithNamespace,
			ProjectWebURL:             webURL,
			ProjectStatus:             projState,
			TagNamePattern:            "v*",
			IsProtected:               false,
			CreateAccessLevelsSummary: "Unprotected",
			Severity:                  defSev,
			Violations:                []string{"Release tags are unprotected"},
			Details:                   details,
			Remediation:               remediation,
		})
	}

	return findings, nil
}

func summarizeTagAccessLevels(ctx context.Context, descs []*gitlab.TagAccessDescription, reg *UserRegistry) (summary string, directUsers []string, directGroups []string) {
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
		} else if d.AccessLevelDescription != "" {
			parts = append(parts, d.AccessLevelDescription)
		} else {
			parts = append(parts, AccessLevelToName(int(d.AccessLevel)))
		}
	}

	return strings.Join(parts, ", "), directUsers, directGroups
}

// matchesReleasePattern tests if a protected tag pattern covers typical production releases (e.g. v*, release-*, etc.).
func matchesReleasePattern(pattern string) bool {
	p := strings.TrimSpace(pattern)
	if p == "*" || p == "v*" || p == "release-*" || p == "*-release" || p == "production-*" || p == "prod-*" {
		return true
	}
	// Test against sample version tags
	samples := []string{"v1.0.0", "v2.0.0", "release-1.0.0", "1.0.0"}
	for _, s := range samples {
		if matched, err := path.Match(p, s); err == nil && matched {
			return true
		}
	}
	return false
}
