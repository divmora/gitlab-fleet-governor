package audit

import (
	"fmt"
	"path/filepath"
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
	ModulePipelineRetention     ModuleName = "pipeline_retention"
)

// AllModuleNames returns the canonical list of supported audit modules.
func AllModuleNames() []string {
	return []string{
		string(ModuleUserAccess),
		string(ModuleProtectedBranches),
		string(ModuleProtectedEnvironments),
		string(ModulePipelineRetention),
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

// PipelineRetentionFinding captures pipeline retention configurations and stale unpruned pipeline audit observations.
type PipelineRetentionFinding struct {
	ProjectID              int      `json:"project_id"`
	ProjectName            string   `json:"project_name"`
	ProjectPath            string   `json:"project_path"`
	ProjectWebURL          string   `json:"project_web_url"`
	ProjectStatus          string   `json:"project_status"` // "Active", "Archived", "Inactive (X days)"
	RetentionSeconds       int      `json:"retention_seconds"`
	RetentionDays          int      `json:"retention_days"`
	HasRetentionConfigured bool     `json:"has_retention_configured"`
	OldestPipelineID       int      `json:"oldest_pipeline_id,omitempty"`
	OldestPipelineRef      string   `json:"oldest_pipeline_ref,omitempty"`
	OldestPipelineStatus   string   `json:"oldest_pipeline_status,omitempty"`
	OldestPipelineCreated  string   `json:"oldest_pipeline_created,omitempty"`
	OldestPipelineAgeDays  int      `json:"oldest_pipeline_age_days,omitempty"`
	StalePipelinesCount    int      `json:"stale_pipelines_count"`
	Severity               Severity `json:"severity"`
	ViolationType          string   `json:"violation_type"`
	Details                string   `json:"details"`
	Remediation            string   `json:"remediation"`
}

// SummaryMetrics captures aggregate statistical breakdown of audit findings across the fleet.
type SummaryMetrics struct {
	TotalProjectsScanned        int              `json:"total_projects_scanned"`
	ActiveProjectsCount         int              `json:"active_projects_count"`
	ArchivedProjectsCount       int              `json:"archived_projects_count"`
	CompliantProjectsCount      int              `json:"compliant_projects_count"`
	NonCompliantProjectsCount   int              `json:"non_compliant_projects_count"`
	TotalViolations             int              `json:"total_violations"`
	CriticalSeverityCount       int              `json:"critical_severity_count"`
	HighSeverityCount           int              `json:"high_severity_count"`
	MediumSeverityCount         int              `json:"medium_severity_count"`
	LowSeverityCount            int              `json:"low_severity_count"`
	HumanUserViolations         int              `json:"human_user_violations"`
	BotUserViolations           int              `json:"bot_user_violations"`
	UserAccessViolations        int              `json:"user_access_violations"`
	ProtectedBranchViolations   int              `json:"protected_branch_violations"`
	ProtectedEnvViolations      int              `json:"protected_env_violations"`
	PipelineRetentionViolations int              `json:"pipeline_retention_violations"`
	AuditedBy                   string           `json:"audited_by,omitempty"`
	SeverityBreakdown           map[Severity]int `json:"severity_breakdown"`
	ModuleViolations            map[string]int   `json:"module_violations"`
}

// LicenseAttestation captures legal and commercial licensing compliance metadata
// under Business Source License 1.1 (BSL 1.1) and Apache 2.0 terms, providing a legally binding,
// non-repudiable attestation record for enterprise SOC 2 / ISO 27001 compliance audits.
type LicenseAttestation struct {
	Status               string `json:"status"`
	LicenseModel         string `json:"license_model"`
	Tier                 string `json:"tier"`
	LicensedTo           string `json:"licensed_to"`
	LicenseID            string `json:"license_id,omitempty"`
	MaxProjects          int    `json:"max_projects"`
	DiscoveredProjects   int    `json:"discovered_projects"`
	ChangeDate           string `json:"change_date,omitempty"`
	AttestationStatement string `json:"attestation_statement"`
}

// AuditReport is the canonical composite audit output model.
type AuditReport struct {
	Title                     string                        `json:"title"`
	GeneratedAt               time.Time                     `json:"generated_at"`
	Duration                  time.Duration                 `json:"duration"`
	DurationString            string                        `json:"duration_human"`
	AuthenticatedUser         *UserInfo                     `json:"authenticated_user,omitempty"`
	ActiveModules             []string                      `json:"active_modules"`
	Summary                   SummaryMetrics                `json:"summary"`
	LicenseAttestation        *LicenseAttestation           `json:"license_attestation,omitempty"`
	UserAccessFindings        []UserAccessFinding           `json:"user_access_findings,omitempty"`
	BotAccessFindings         []UserAccessFinding           `json:"bot_access_findings,omitempty"`
	ProtectedBranchFindings   []ProtectedBranchFinding      `json:"protected_branch_findings,omitempty"`
	ProtectedEnvFindings      []ProtectedEnvironmentFinding `json:"protected_env_findings,omitempty"`
	PipelineRetentionFindings []PipelineRetentionFinding    `json:"pipeline_retention_findings,omitempty"`
	UserDirectory             []*UserInfo                   `json:"user_directory,omitempty"`
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
	r.Summary.PipelineRetentionViolations = 0

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

	// Process Pipeline Retention findings
	for _, f := range r.PipelineRetentionFindings {
		if f.Severity != SeverityPass && f.Severity != SeverityInfo {
			r.Summary.TotalViolations++
			r.Summary.PipelineRetentionViolations++
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
	r.Summary.ModuleViolations[string(ModulePipelineRetention)] = r.Summary.PipelineRetentionViolations

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

// InitiatorDescription returns a descriptive identity string for the user who initiated the audit.
func (r *AuditReport) InitiatorDescription() string {
	if r.AuthenticatedUser != nil {
		desc := fmt.Sprintf("@%s", r.AuthenticatedUser.Username)
		if r.AuthenticatedUser.Name != "" && r.AuthenticatedUser.Name != r.AuthenticatedUser.Username {
			desc += fmt.Sprintf(" (%s)", r.AuthenticatedUser.Name)
		}
		if r.AuthenticatedUser.Email != "" {
			desc += fmt.Sprintf(" <%s>", r.AuthenticatedUser.Email)
		}
		if r.AuthenticatedUser.ID > 0 {
			desc += fmt.Sprintf(" [ID: %d]", r.AuthenticatedUser.ID)
		}
		return desc
	}
	if r.Summary.AuditedBy != "" {
		return r.Summary.AuditedBy
	}
	return "System / Anonymous"
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

	sort.Slice(r.PipelineRetentionFindings, func(i, j int) bool {
		if r.PipelineRetentionFindings[i].ProjectPath != r.PipelineRetentionFindings[j].ProjectPath {
			return r.PipelineRetentionFindings[i].ProjectPath < r.PipelineRetentionFindings[j].ProjectPath
		}
		return r.PipelineRetentionFindings[i].OldestPipelineID < r.PipelineRetentionFindings[j].OldestPipelineID
	})

	sort.Slice(r.UserDirectory, func(i, j int) bool {
		if r.UserDirectory[i].Username == r.UserDirectory[j].Username {
			return r.UserDirectory[i].ID < r.UserDirectory[j].ID
		}
		return strings.ToLower(r.UserDirectory[i].Username) < strings.ToLower(r.UserDirectory[j].Username)
	})
}

// BotClassifier classifies users as bots or service accounts based on explicit list, custom patterns, and GitLab standards.
type BotClassifier struct {
	explicitAccounts map[string]bool // lowercased usernames, emails, or numeric IDs
	patterns         []string        // wildcard or substring patterns
}

// NewBotClassifier creates a new BotClassifier.
func NewBotClassifier(serviceAccounts []string, patterns []string) *BotClassifier {
	bc := &BotClassifier{
		explicitAccounts: make(map[string]bool),
		patterns:         patterns,
	}
	for _, a := range serviceAccounts {
		trimmed := strings.ToLower(strings.TrimSpace(a))
		if trimmed != "" {
			bc.explicitAccounts[trimmed] = true
		}
	}
	return bc
}

// IsBot checks whether a user is a bot or service account using explicit accounts, custom patterns, and standard GitLab formats.
func (c *BotClassifier) IsBot(userID int, username, name, email string) bool {
	u := strings.ToLower(strings.TrimSpace(username))
	n := strings.ToLower(strings.TrimSpace(name))
	e := strings.ToLower(strings.TrimSpace(email))
	idStr := fmt.Sprintf("%d", userID)

	// 1. Explicit service accounts match by username, email, or numeric user ID
	if c != nil && len(c.explicitAccounts) > 0 {
		if (u != "" && c.explicitAccounts[u]) || (e != "" && c.explicitAccounts[e]) || (userID > 0 && c.explicitAccounts[idStr]) {
			return true
		}
	}

	// 2. Custom patterns match against username, display name, or email
	if c != nil && len(c.patterns) > 0 {
		for _, p := range c.patterns {
			pLower := strings.ToLower(strings.TrimSpace(p))
			if pLower == "" {
				continue
			}
			if matched, _ := filepath.Match(pLower, u); matched {
				return true
			}
			if matched, _ := filepath.Match(pLower, e); matched {
				return true
			}
			if strings.Contains(u, pLower) || strings.Contains(n, pLower) || strings.Contains(e, pLower) {
				return true
			}
		}
	}

	// 3. Universal GitLab Conventions (Zero hardcoded company-specific strings)
	// GitLab-managed Project & Group Access Tokens (project_*_bot_*, group_*_bot_*)
	if strings.HasPrefix(u, "project_") || strings.HasPrefix(u, "group_") {
		return true
	}
	// GitLab Enterprise Service Accounts (e.g., service_account_<hash>, service-account-*)
	if strings.HasPrefix(u, "service_account") || strings.HasPrefix(u, "service-account") {
		return true
	}
	// GitLab Enterprise Service Account emails (e.g., service_account_<hash>@noreply.<domain>)
	if strings.HasPrefix(e, "service_account") || strings.HasPrefix(e, "service-account") {
		return true
	}
	if strings.Contains(e, "service_account") || strings.Contains(e, "serviceaccount") {
		return true
	}
	if strings.Contains(e, "@noreply.") && (strings.Contains(e, "service_account") || strings.Contains(e, "bot") || strings.Contains(e, "project_") || strings.Contains(e, "group_")) {
		return true
	}
	// Common bot suffixes (e.g. dependabot, renovate-bot)
	if strings.HasSuffix(u, "_bot") || strings.HasSuffix(u, "-bot") {
		return true
	}
	// GitLab official built-in platform bots
	if u == "gitlab-bot" || u == "support-bot" || u == "alert-bot" || u == "security-bot" {
		return true
	}
	// Standard service account / bot indicators in username, display name, or email
	if strings.Contains(u, "bot") || strings.Contains(u, "service_account") || strings.Contains(u, "serviceaccount") {
		return true
	}
	if strings.Contains(n, " bot") || strings.Contains(n, "token") || strings.Contains(n, "service account") {
		return true
	}
	if strings.HasPrefix(e, "bot@") || strings.HasPrefix(e, "service@") || strings.HasPrefix(e, "automation@") || strings.HasPrefix(e, "ops-bot@") {
		return true
	}

	return false
}

// IsBotOrServiceAccount identifies machine, automation, bot, and token accounts.
// Maintained for simple checks and backwards compatibility.
func IsBotOrServiceAccount(username, name string) bool {
	var bc *BotClassifier
	return bc.IsBot(0, username, name, "")
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
