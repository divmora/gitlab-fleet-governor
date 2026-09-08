package audit

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Severity classifies the risk impact of an audit violation.
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
	SeverityInfo     Severity = "INFO"
	SeverityPass     Severity = "PASS"
)

// ModuleName identifies an audit module.
type ModuleName string

const (
	ModuleUserAccess            ModuleName = "user_access"
	ModuleProtectedBranches     ModuleName = "protected_branches"
	ModuleProtectedEnvironments ModuleName = "protected_environments"
)

// AllModuleNames returns the canonical list of supported audit modules.
func AllModuleNames() []string {
	return []string{
		string(ModuleUserAccess),
		string(ModuleProtectedBranches),
		string(ModuleProtectedEnvironments),
	}
}

// UserInfo encapsulates metadata for an individual identity discovered across the fleet.
type UserInfo struct {
	ID            int      `json:"id"`
	Username      string   `json:"username"`
	Name          string   `json:"name"`
	Email         string   `json:"email,omitempty"`
	State         string   `json:"state,omitempty"`
	WebURL        string   `json:"web_url,omitempty"`
	IsBot         bool     `json:"is_bot"`
	AccountType   string   `json:"account_type"` // "Human" or "Service Account / Bot"
	ProjectsCount int      `json:"projects_count"`
	ProjectPaths  []string `json:"project_paths,omitempty"`
}

// UserAccessFinding captures access-level and expiration audit observations for a member.
type UserAccessFinding struct {
	ProjectID      int      `json:"project_id"`
	ProjectName    string   `json:"project_name"`
	ProjectPath    string   `json:"project_path"`
	ProjectWebURL  string   `json:"project_web_url"`
	ProjectStatus  string   `json:"project_status"` // "Active", "Archived", "Inactive (X days)"
	UserID         int      `json:"user_id"`
	Username       string   `json:"username"`
	Name           string   `json:"name"`
	Email          string   `json:"email"`
	AccessLevel    int      `json:"access_level"`
	AccessRoleName string   `json:"access_role_name"`
	IsDirect       bool     `json:"is_direct"`
	MembershipType string   `json:"membership_type"` // "Direct (Project)" or "Inherited (Group)"
	IsBot          bool     `json:"is_bot"`
	AccountType    string   `json:"account_type"` // "Human" or "Service Account / Bot"
	ExpiresAt      string   `json:"expires_at,omitempty"`
	HasExpiration  bool     `json:"has_expiration"`
	Severity       Severity `json:"severity"`
	ViolationType  string   `json:"violation_type"`
	Details        string   `json:"details"`
	Remediation    string   `json:"remediation"`
}

// ProtectedBranchFinding captures compliance status for a project branch protection.
type ProtectedBranchFinding struct {
	ProjectID                 int      `json:"project_id"`
	ProjectName               string   `json:"project_name"`
	ProjectPath               string   `json:"project_path"`
	ProjectWebURL             string   `json:"project_web_url"`
	ProjectStatus             string   `json:"project_status"` // "Active", "Archived", "Inactive (X days)"
	BranchName                string   `json:"branch_name"`
	IsDefaultBranch           bool     `json:"is_default_branch"`
	IsProtected               bool     `json:"is_protected"`
	AllowForcePush            bool     `json:"allow_force_push"`
	CodeOwnerApprovalRequired bool     `json:"code_owner_approval_required"`
	PushAccessLevelsSummary   string   `json:"push_access_levels_summary"`
	MergeAccessLevelsSummary  string   `json:"merge_access_levels_summary"`
	DirectUserPushGrants      []string `json:"direct_user_push_grants,omitempty"`
	DirectGroupPushGrants     []string `json:"direct_group_push_grants,omitempty"`
	Severity                  Severity `json:"severity"`
	Violations                []string `json:"violations,omitempty"`
	Details                   string   `json:"details"`
	Remediation               string   `json:"remediation"`
}

// ProtectedEnvironmentFinding captures compliance status for an environment deployment target.
type ProtectedEnvironmentFinding struct {
	ProjectID                 int      `json:"project_id"`
	ProjectName               string   `json:"project_name"`
	ProjectPath               string   `json:"project_path"`
	ProjectWebURL             string   `json:"project_web_url"`
	ProjectStatus             string   `json:"project_status"` // "Active", "Archived", "Inactive (X days)"
	EnvironmentName           string   `json:"environment_name"`
	IsProduction              bool     `json:"is_production"`
	RequiredApprovalCount     int      `json:"required_approval_count"`
	DeployAccessLevelsSummary string   `json:"deploy_access_levels_summary"`
	DirectUserDeployGrants    []string `json:"direct_user_deploy_grants,omitempty"`
	DirectGroupDeployGrants   []string `json:"direct_group_deploy_grants,omitempty"`
	Severity                  Severity `json:"severity"`
	Violations                []string `json:"violations,omitempty"`
	Details                   string   `json:"details"`
	Remediation               string   `json:"remediation"`
}

// SummaryMetrics captures aggregate statistical breakdown of audit findings across the fleet.
type SummaryMetrics struct {
	TotalProjectsScanned      int              `json:"total_projects_scanned"`
	ActiveProjectsCount       int              `json:"active_projects_count"`
	ArchivedProjectsCount     int              `json:"archived_projects_count"`
	CompliantProjectsCount    int              `json:"compliant_projects_count"`
	NonCompliantProjectsCount int              `json:"non_compliant_projects_count"`
	TotalViolations           int              `json:"total_violations"`
	CriticalSeverityCount     int              `json:"critical_severity_count"`
	HighSeverityCount         int              `json:"high_severity_count"`
	MediumSeverityCount       int              `json:"medium_severity_count"`
	LowSeverityCount          int              `json:"low_severity_count"`
	HumanUserViolations       int              `json:"human_user_violations"`
	BotUserViolations         int              `json:"bot_user_violations"`
	UserAccessViolations      int              `json:"user_access_violations"`
	ProtectedBranchViolations int              `json:"protected_branch_violations"`
	ProtectedEnvViolations    int              `json:"protected_env_violations"`
	SeverityBreakdown         map[Severity]int `json:"severity_breakdown"`
	ModuleViolations          map[string]int   `json:"module_violations"`
}

// AuditReport is the canonical composite audit output model.
type AuditReport struct {
	Title                   string                        `json:"title"`
	GeneratedAt             time.Time                     `json:"generated_at"`
	Duration                time.Duration                 `json:"duration"`
	DurationString          string                        `json:"duration_human"`
	ActiveModules           []string                      `json:"active_modules"`
	Summary                 SummaryMetrics                `json:"summary"`
	UserAccessFindings      []UserAccessFinding           `json:"user_access_findings,omitempty"`
	BotAccessFindings       []UserAccessFinding           `json:"bot_access_findings,omitempty"`
	ProtectedBranchFindings []ProtectedBranchFinding      `json:"protected_branch_findings,omitempty"`
	ProtectedEnvFindings    []ProtectedEnvironmentFinding `json:"protected_env_findings,omitempty"`
	UserDirectory           []*UserInfo                   `json:"user_directory,omitempty"`
}

// ComputeSummary recalculates summary metrics based on findings.
func (r *AuditReport) ComputeSummary() {
	r.Summary.SeverityBreakdown = make(map[Severity]int)
	r.Summary.ModuleViolations = make(map[string]int)

	r.Summary.TotalViolations = 0
	r.Summary.CriticalSeverityCount = 0
	r.Summary.HighSeverityCount = 0
	r.Summary.MediumSeverityCount = 0
	r.Summary.LowSeverityCount = 0
	r.Summary.HumanUserViolations = 0
	r.Summary.BotUserViolations = 0
	r.Summary.UserAccessViolations = 0
	r.Summary.ProtectedBranchViolations = 0
	r.Summary.ProtectedEnvViolations = 0

	nonCompliantProjects := make(map[int]struct{})

	// Process Human User Access findings
	for _, f := range r.UserAccessFindings {
		if f.Severity != SeverityPass && f.Severity != SeverityInfo {
			r.Summary.TotalViolations++
			r.Summary.UserAccessViolations++
			r.Summary.HumanUserViolations++
			r.Summary.SeverityBreakdown[f.Severity]++
			nonCompliantProjects[f.ProjectID] = struct{}{}
			switch f.Severity {
			case SeverityCritical:
				r.Summary.CriticalSeverityCount++
			case SeverityHigh:
				r.Summary.HighSeverityCount++
			case SeverityMedium:
				r.Summary.MediumSeverityCount++
			case SeverityLow:
				r.Summary.LowSeverityCount++
			}
		}
	}

	// Process Bot / Service Account Access findings
	for _, f := range r.BotAccessFindings {
		if f.Severity != SeverityPass && f.Severity != SeverityInfo {
			r.Summary.TotalViolations++
			r.Summary.UserAccessViolations++
			r.Summary.BotUserViolations++
			r.Summary.SeverityBreakdown[f.Severity]++
			nonCompliantProjects[f.ProjectID] = struct{}{}
			switch f.Severity {
			case SeverityCritical:
				r.Summary.CriticalSeverityCount++
			case SeverityHigh:
				r.Summary.HighSeverityCount++
			case SeverityMedium:
				r.Summary.MediumSeverityCount++
			case SeverityLow:
				r.Summary.LowSeverityCount++
			}
		}
	}

	// Process Protected Branches findings
	for _, f := range r.ProtectedBranchFindings {
		if f.Severity != SeverityPass && f.Severity != SeverityInfo {
			r.Summary.TotalViolations++
			r.Summary.ProtectedBranchViolations++
			r.Summary.SeverityBreakdown[f.Severity]++
			nonCompliantProjects[f.ProjectID] = struct{}{}
			switch f.Severity {
			case SeverityCritical:
				r.Summary.CriticalSeverityCount++
			case SeverityHigh:
				r.Summary.HighSeverityCount++
			case SeverityMedium:
				r.Summary.MediumSeverityCount++
			case SeverityLow:
				r.Summary.LowSeverityCount++
			}
		}
	}

	// Process Protected Environments findings
	for _, f := range r.ProtectedEnvFindings {
		if f.Severity != SeverityPass && f.Severity != SeverityInfo {
			r.Summary.TotalViolations++
			r.Summary.ProtectedEnvViolations++
			r.Summary.SeverityBreakdown[f.Severity]++
			nonCompliantProjects[f.ProjectID] = struct{}{}
			switch f.Severity {
			case SeverityCritical:
				r.Summary.CriticalSeverityCount++
			case SeverityHigh:
				r.Summary.HighSeverityCount++
			case SeverityMedium:
				r.Summary.MediumSeverityCount++
			case SeverityLow:
				r.Summary.LowSeverityCount++
			}
		}
	}

	r.Summary.ModuleViolations[string(ModuleUserAccess)] = r.Summary.UserAccessViolations
	r.Summary.ModuleViolations[string(ModuleProtectedBranches)] = r.Summary.ProtectedBranchViolations
	r.Summary.ModuleViolations[string(ModuleProtectedEnvironments)] = r.Summary.ProtectedEnvViolations

	r.Summary.NonCompliantProjectsCount = len(nonCompliantProjects)
	compliant := r.Summary.TotalProjectsScanned - r.Summary.NonCompliantProjectsCount
	if compliant < 0 {
		compliant = 0
	}
	r.Summary.CompliantProjectsCount = compliant
}

// AccessLevelToName returns human-readable GitLab access role name.
func AccessLevelToName(level int) string {
	switch level {
	case 10:
		return "Guest (10)"
	case 15:
		return "Planner (15)"
	case 20:
		return "Reporter (20)"
	case 30:
		return "Developer (30)"
	case 40:
		return "Maintainer (40)"
	case 50:
		return "Owner (50)"
	case 60:
		return "Admin (60)"
	default:
		return fmt.Sprintf("Level %d", level)
	}
}

// SortFindings orders findings deterministically for report reproducibility.
func (r *AuditReport) SortFindings() {
	sort.Slice(r.UserAccessFindings, func(i, j int) bool {
		if r.UserAccessFindings[i].ProjectPath != r.UserAccessFindings[j].ProjectPath {
			return r.UserAccessFindings[i].ProjectPath < r.UserAccessFindings[j].ProjectPath
		}
		return strings.ToLower(r.UserAccessFindings[i].Username) < strings.ToLower(r.UserAccessFindings[j].Username)
	})

	sort.Slice(r.BotAccessFindings, func(i, j int) bool {
		if r.BotAccessFindings[i].ProjectPath != r.BotAccessFindings[j].ProjectPath {
			return r.BotAccessFindings[i].ProjectPath < r.BotAccessFindings[j].ProjectPath
		}
		return strings.ToLower(r.BotAccessFindings[i].Username) < strings.ToLower(r.BotAccessFindings[j].Username)
	})

	sort.Slice(r.ProtectedBranchFindings, func(i, j int) bool {
		if r.ProtectedBranchFindings[i].ProjectPath != r.ProtectedBranchFindings[j].ProjectPath {
			return r.ProtectedBranchFindings[i].ProjectPath < r.ProtectedBranchFindings[j].ProjectPath
		}
		return r.ProtectedBranchFindings[i].BranchName < r.ProtectedBranchFindings[j].BranchName
	})

	sort.Slice(r.ProtectedEnvFindings, func(i, j int) bool {
		if r.ProtectedEnvFindings[i].ProjectPath != r.ProtectedEnvFindings[j].ProjectPath {
			return r.ProtectedEnvFindings[i].ProjectPath < r.ProtectedEnvFindings[j].ProjectPath
		}
		return r.ProtectedEnvFindings[i].EnvironmentName < r.ProtectedEnvFindings[j].EnvironmentName
	})

	sort.Slice(r.UserDirectory, func(i, j int) bool {
		if r.UserDirectory[i].Username == r.UserDirectory[j].Username {
			return r.UserDirectory[i].ID < r.UserDirectory[j].ID
		}
		return strings.ToLower(r.UserDirectory[i].Username) < strings.ToLower(r.UserDirectory[j].Username)
	})
}

// IsBotOrServiceAccount identifies machine, automation, bot, and token accounts.
func IsBotOrServiceAccount(username, name string) bool {
	u := strings.ToLower(strings.TrimSpace(username))
	n := strings.ToLower(strings.TrimSpace(name))

	if strings.HasPrefix(u, "project_") || strings.HasPrefix(u, "group_") {
		return true
	}
	if strings.HasSuffix(u, "_bot") || strings.HasSuffix(u, "-bot") || u == "gitlab-bot" {
		return true
	}
	if strings.Contains(u, "bot") || strings.Contains(u, "service") || strings.Contains(u, "token") {
		return true
	}
	if strings.Contains(n, "bot") || strings.Contains(n, "token") || strings.Contains(n, "service account") {
		return true
	}
	if u == "sonarqube-ce" || u == "pixelvide-operator" || u == "support-bot" || u == "automation" {
		return true
	}
	return false
}

// EvaluateProjectState checks whether a project is actively maintained, archived, or stale.
func EvaluateProjectState(archived bool, lastActivityAt *time.Time) (state string, isArchived bool, isInactive bool) {
	if archived {
		return "Archived", true, false
	}
	if lastActivityAt != nil && !lastActivityAt.IsZero() {
		days := int(time.Since(*lastActivityAt).Hours() / 24)
		if days > 180 {
			return fmt.Sprintf("Inactive (%dd)", days), false, true
		}
	}
	return "Active", false, false
}

// AdjustSeverityForProject de-prioritizes audit severity for archived or inactive repositories.
func AdjustSeverityForProject(base Severity, isArchived, isInactive bool) Severity {
	if !isArchived && !isInactive {
		return base
	}
	if isArchived {
		switch base {
		case SeverityCritical:
			return SeverityMedium
		case SeverityHigh:
			return SeverityLow
		case SeverityMedium:
			return SeverityLow
		default:
			return base
		}
	}
	if isInactive {
		switch base {
		case SeverityCritical:
			return SeverityHigh
		case SeverityHigh:
			return SeverityMedium
		default:
			return base
		}
	}
	return base
}
