package config

import (
	"fmt"
)

// ============================================================================
// 1. Root Configuration Model
// ============================================================================

// PolicyConfig represents the root declarative configuration schema for
// gitlab-fleet-governor, supporting interchangeable YAML and JSON serialization.
type PolicyConfig struct {
	// Version denotes the schema version of the configuration file (e.g. "v1", "1").
	Version string `yaml:"version" json:"version"`

	// Settings defines global runtime execution and client parameters.
	Settings SettingsConfig `yaml:"settings,omitempty" json:"settings,omitempty"`

	// Targets defines group and project targeting selector rules.
	Targets TargetSelectors `yaml:"targets,omitempty" json:"targets,omitempty"`

	// Policies encapsulates all governance policy rulesets.
	Policies PoliciesConfig `yaml:"policies,omitempty" json:"policies,omitempty"`
}

// SetDefaults applies default settings and parameters across the configuration.
func (p *PolicyConfig) SetDefaults() {
	p.Settings.SetDefaults()
}

// ============================================================================
// 2. Global Settings Model
// ============================================================================

// SettingsConfig encapsulates execution flags, logging, reporting, and GitLab client settings.
type SettingsConfig struct {
	// DryRun enables non-destructive simulation mode (default: true).
	DryRun *bool `yaml:"dry_run,omitempty" json:"dry_run,omitempty"`

	// Concurrency specifies the number of parallel worker goroutines (default: 10).
	Concurrency int `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`

	// LogLevel defines the log filtering level: "debug", "info", "warn", "error" (default: "info").
	LogLevel string `yaml:"log_level,omitempty" json:"log_level,omitempty"`

	// LogFormat defines log rendering format: "text" (human/colored) or "json" (structured) (default: "text").
	LogFormat string `yaml:"log_format,omitempty" json:"log_format,omitempty"`

	// ReportFormat defines summary report format: "table", "json", "csv", "markdown" (default: "table").
	ReportFormat string `yaml:"report_format,omitempty" json:"report_format,omitempty"`

	// OutputFilePath optionally specifies a file destination for the summary report.
	OutputFilePath string `yaml:"output_file_path,omitempty" json:"output_file_path,omitempty"`

	// GitLab contains API connectivity and transport resilience parameters.
	GitLab GitLabSettingsConfig `yaml:"gitlab,omitempty" json:"gitlab,omitempty"`
}

// SetDefaults applies built-in default values to SettingsConfig.
func (s *SettingsConfig) SetDefaults() {
	if s.DryRun == nil {
		defaultDryRun := true
		s.DryRun = &defaultDryRun
	}
	if s.Concurrency <= 0 {
		s.Concurrency = 10
	}
	if s.LogLevel == "" {
		s.LogLevel = "info"
	}
	if s.LogFormat == "" {
		s.LogFormat = "text"
	}
	if s.ReportFormat == "" {
		s.ReportFormat = "table"
	}
	s.GitLab.SetDefaults()
}

// GitLabSettingsConfig encapsulates GitLab API credentials and resilience parameters.
type GitLabSettingsConfig struct {
	// BaseURL is the GitLab REST API v4 endpoint (default: "https://gitlab.com/api/v4").
	BaseURL string `yaml:"base_url,omitempty" json:"base_url,omitempty"`

	// Token is the GitLab authentication token (Private token, OAuth Bearer, or CI Job token).
	Token string `yaml:"token,omitempty" json:"token,omitempty"`

	// TokenType specifies the auth method: "private_token" (default), "oauth", "job_token".
	TokenType string `yaml:"token_type,omitempty" json:"token_type,omitempty"`

	// TimeoutSeconds specifies HTTP request timeout in seconds (default: 30).
	TimeoutSeconds int `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`

	// RateLimitRPS specifies client-side proactive token bucket request rate limit (default: 30.0).
	RateLimitRPS float64 `yaml:"rate_limit_rps,omitempty" json:"rate_limit_rps,omitempty"`

	// RateLimitBurst specifies client-side proactive token bucket burst limit (default: 50).
	RateLimitBurst int `yaml:"rate_limit_burst,omitempty" json:"rate_limit_burst,omitempty"`

	// MaxRetries specifies maximum retry attempts on transient 429 and 5xx errors (default: 3).
	MaxRetries int `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`

	// RetryBaseDelayMs specifies base exponential backoff delay in milliseconds (default: 500).
	RetryBaseDelayMs int `yaml:"retry_base_delay_ms,omitempty" json:"retry_base_delay_ms,omitempty"`

	// RetryMaxDelayMs specifies maximum cap for backoff delay in milliseconds (default: 30000).
	RetryMaxDelayMs int `yaml:"retry_max_delay_ms,omitempty" json:"retry_max_delay_ms,omitempty"`
}

// SetDefaults applies built-in default values to GitLabSettingsConfig.
func (g *GitLabSettingsConfig) SetDefaults() {
	if g.BaseURL == "" {
		g.BaseURL = "https://gitlab.com/api/v4"
	}
	if g.TokenType == "" {
		g.TokenType = "private_token"
	}
	if g.TimeoutSeconds <= 0 {
		g.TimeoutSeconds = 30
	}
	if g.RateLimitRPS <= 0 {
		g.RateLimitRPS = 30.0
	}
	if g.RateLimitBurst <= 0 {
		g.RateLimitBurst = 50
	}
	if g.MaxRetries <= 0 {
		g.MaxRetries = 3
	}
	if g.RetryBaseDelayMs <= 0 {
		g.RetryBaseDelayMs = 500
	}
	if g.RetryMaxDelayMs <= 0 {
		g.RetryMaxDelayMs = 30000
	}
}

// ============================================================================
// 3. Target Selectors Model
// ============================================================================

// TargetSelectors configures fleet-wide group and project discovery criteria.
type TargetSelectors struct {
	// GroupSelector specifies group-level discovery and filtering rules.
	GroupSelector *GroupSelector `yaml:"group_selector,omitempty" json:"group_selector,omitempty"`

	// ProjectSelector specifies project-level matching and exclusion rules.
	ProjectSelector *ProjectSelector `yaml:"project_selector,omitempty" json:"project_selector,omitempty"`
}

// GroupSelector defines group hierarchy inclusion, exclusion, and recursive traversal.
type GroupSelector struct {
	// GroupIDsInclude matches explicit numeric group IDs.
	GroupIDsInclude []int `yaml:"group_ids_include,omitempty" json:"group_ids_include,omitempty"`

	// GroupIDsExclude excludes explicit numeric group IDs.
	GroupIDsExclude []int `yaml:"group_ids_exclude,omitempty" json:"group_ids_exclude,omitempty"`

	// GroupPathsInclude matches group full paths (e.g. "enterprise/platform").
	GroupPathsInclude []string `yaml:"group_paths_include,omitempty" json:"group_paths_include,omitempty"`

	// GroupPathsExclude excludes group full paths.
	GroupPathsExclude []string `yaml:"group_paths_exclude,omitempty" json:"group_paths_exclude,omitempty"`

	// Recursive enables BFS traversal of all subgroups with cycle detection (default: true).
	Recursive *bool `yaml:"recursive,omitempty" json:"recursive,omitempty"`
}

// ProjectSelector defines multi-criteria project filtering pipeline.
type ProjectSelector struct {
	// NamespacesInclude matches projects whose namespace path matches any of these prefixes.
	NamespacesInclude []string `yaml:"namespaces_include,omitempty" json:"namespaces_include,omitempty"`

	// NamespacesExclude excludes projects belonging to these namespaces.
	NamespacesExclude []string `yaml:"namespaces_exclude,omitempty" json:"namespaces_exclude,omitempty"`

	// ProjectNameRegexInclude filters projects matching this regular expression.
	ProjectNameRegexInclude string `yaml:"project_name_regex_include,omitempty" json:"project_name_regex_include,omitempty"`

	// ProjectNameRegexExclude excludes projects matching this regular expression.
	ProjectNameRegexExclude string `yaml:"project_name_regex_exclude,omitempty" json:"project_name_regex_exclude,omitempty"`

	// TopicsInclude requires project to possess at least one of these topics/tags.
	TopicsInclude []string `yaml:"topics_include,omitempty" json:"topics_include,omitempty"`

	// TopicsExclude excludes projects possessing any of these topics/tags.
	TopicsExclude []string `yaml:"topics_exclude,omitempty" json:"topics_exclude,omitempty"`

	// Visibility filters project visibility: "public", "internal", "private", or "any" / "" (default: any).
	Visibility string `yaml:"visibility,omitempty" json:"visibility,omitempty"`

	// Archived filters archived status: true (archived only), false (active only), nil (any).
	Archived *bool `yaml:"archived,omitempty" json:"archived,omitempty"`

	// IDRange filters numeric project IDs within [Min, Max].
	IDRange *IDRange `yaml:"id_range,omitempty" json:"id_range,omitempty"`
}

// IDRange defines a closed interval [Min, Max] for numeric entity IDs.
type IDRange struct {
	Min int `yaml:"min,omitempty" json:"min,omitempty"`
	Max int `yaml:"max,omitempty" json:"max,omitempty"`
}

// IDRangeSelector is an alias for IDRange.
type IDRangeSelector = IDRange

// ============================================================================
// 4. Governance Policies Container
// ============================================================================

// PoliciesConfig contains all policy declarations to be enforced on targeted groups/projects.
type PoliciesConfig struct {
	// PushRules configures project & group push rules.
	PushRules *PushRulesConfig `yaml:"push_rules,omitempty" json:"push_rules,omitempty"`

	// ProtectedBranches configures branch protections on projects.
	ProtectedBranches []ProtectedBranchRuleConfig `yaml:"protected_branches,omitempty" json:"protected_branches,omitempty"`

	// ApprovalRules configures merge request approval settings and named approver rules.
	ApprovalRules *ApprovalRulesConfig `yaml:"approval_rules,omitempty" json:"approval_rules,omitempty"`

	// ProjectSettings configures arbitrary project workflow and repository settings.
	ProjectSettings *ProjectSettingsConfig `yaml:"project_settings,omitempty" json:"project_settings,omitempty"`

	// PipelineRetention configures automatic deletion of pipelines.
	PipelineRetention *PipelineRetentionConfig `yaml:"pipeline_retention,omitempty" json:"pipeline_retention,omitempty"`

	// Variables configures CI/CD variables and secrets.
	Variables []VariableConfig `yaml:"variables,omitempty" json:"variables,omitempty"`

	// Runners configures CI/CD runner fleet settings.
	Runners *RunnersConfig `yaml:"runners,omitempty" json:"runners,omitempty"`

	// Compliance configures project compliance framework label associations.
	Compliance *ComplianceConfig `yaml:"compliance,omitempty" json:"compliance,omitempty"`

	// Webhooks configures project and group webhook integrations.
	Webhooks []WebhookConfig `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`

	// Members configures direct member permissions and expiration enforcement.
	Members *MembersConfig `yaml:"members,omitempty" json:"members,omitempty"`
}

// ============================================================================
// 5. Push Rules Configuration
// ============================================================================

// PushRulesConfig defines project and group push rules.
type PushRulesConfig struct {
	// AuthorEmailRegex enforces author email matching regex pattern.
	AuthorEmailRegex string `yaml:"author_email_regex,omitempty" json:"author_email_regex,omitempty"`

	// BranchNameRegex enforces branch names matching regex pattern.
	BranchNameRegex string `yaml:"branch_name_regex,omitempty" json:"branch_name_regex,omitempty"`

	// CommitMessageRegex enforces commit messages matching regex pattern.
	CommitMessageRegex string `yaml:"commit_message_regex,omitempty" json:"commit_message_regex,omitempty"`

	// CommitMessageNegativeRegex rejects commit messages matching negative regex pattern.
	CommitMessageNegativeRegex string `yaml:"commit_message_negative_regex,omitempty" json:"commit_message_negative_regex,omitempty"`

	// FileNameRegex rejects committed filenames matching regex pattern (e.g. secrets/certs).
	FileNameRegex string `yaml:"file_name_regex,omitempty" json:"file_name_regex,omitempty"`

	// MaxFileSize specifies maximum file size in megabytes (MB) allowed in a commit.
	MaxFileSize *int `yaml:"max_file_size,omitempty" json:"max_file_size,omitempty"`

	// CommitCommitterCheck ensures committer is a recognized GitLab user.
	CommitCommitterCheck *bool `yaml:"commit_committer_check,omitempty" json:"commit_committer_check,omitempty"`

	// MemberCheck ensures committer is a member of the project/group.
	MemberCheck *bool `yaml:"member_check,omitempty" json:"member_check,omitempty"`

	// PreventSecrets scans commits and blocks pushes containing known secrets.
	PreventSecrets *bool `yaml:"prevent_secrets,omitempty" json:"prevent_secrets,omitempty"`

	// DenyDeleteTag prevents deleting Git tags.
	DenyDeleteTag *bool `yaml:"deny_delete_tag,omitempty" json:"deny_delete_tag,omitempty"`

	// RejectUnsignedCommits rejects commits that are not GPG/SSH/X.509 signed.
	RejectUnsignedCommits *bool `yaml:"reject_unsigned_commits,omitempty" json:"reject_unsigned_commits,omitempty"`

	// RejectNonDCOCommits rejects commits without Developer Certificate of Origin (DCO) sign-off.
	RejectNonDCOCommits *bool `yaml:"reject_non_dco_commits,omitempty" json:"reject_non_dco_commits,omitempty"`
}

// ============================================================================
// 6. Protected Branches Configuration
// ============================================================================

// ProtectedBranchRuleConfig defines branch protection parameters for a branch name or pattern.
type ProtectedBranchRuleConfig struct {
	// Name is the branch name or wildcard pattern (e.g. "main", "release/*", "*"). Required.
	Name string `yaml:"name" json:"name"`

	// AllowedToPush defines permissions allowed to push to the branch.
	AllowedToPush []BranchAccessDescription `yaml:"allowed_to_push,omitempty" json:"allowed_to_push,omitempty"`

	// AllowedToMerge defines permissions allowed to merge to the branch.
	AllowedToMerge []BranchAccessDescription `yaml:"allowed_to_merge,omitempty" json:"allowed_to_merge,omitempty"`

	// AllowedToUnprotect defines permissions allowed to unprotect the branch.
	AllowedToUnprotect []BranchAccessDescription `yaml:"allowed_to_unprotect,omitempty" json:"allowed_to_unprotect,omitempty"`

	// AllowForcePush allows force pushes to the branch (default: false).
	AllowForcePush *bool `yaml:"allow_force_push,omitempty" json:"allow_force_push,omitempty"`

	// CodeOwnerApprovalRequired enforces code owner approval on MRs targeting this branch.
	CodeOwnerApprovalRequired *bool `yaml:"code_owner_approval_required,omitempty" json:"code_owner_approval_required,omitempty"`
}

// BranchAccessDescription specifies role level, user, group, or deploy key permissions.
type BranchAccessDescription struct {
	// AccessLevel is the GitLab numeric access level: 0 (No Access), 30 (Developer), 40 (Maintainer), 60 (Admin).
	AccessLevel int `yaml:"access_level,omitempty" json:"access_level,omitempty"`

	// UserID specifies explicit user ID granted access.
	UserID int `yaml:"user_id,omitempty" json:"user_id,omitempty"`

	// GroupID specifies explicit group ID granted access.
	GroupID int `yaml:"group_id,omitempty" json:"group_id,omitempty"`

	// Username specifies username (dynamically resolved to UserID).
	Username string `yaml:"username,omitempty" json:"username,omitempty"`

	// GroupPath specifies group path (dynamically resolved to GroupID).
	GroupPath string `yaml:"group_path,omitempty" json:"group_path,omitempty"`

	// DeployKeyID specifies deploy key ID granted push access.
	DeployKeyID int `yaml:"deploy_key_id,omitempty" json:"deploy_key_id,omitempty"`
}

// ============================================================================
// 7. Merge Request Approval Rules Configuration
// ============================================================================

// ApprovalRulesConfig encapsulates project-level MR approval settings and named approval rules.
type ApprovalRulesConfig struct {
	// ApprovalsBeforeMerge specifies required approvals before merge.
	ApprovalsBeforeMerge *int `yaml:"approvals_before_merge,omitempty" json:"approvals_before_merge,omitempty"`

	// ResetApprovalsOnPush resets approvals when commits are pushed.
	ResetApprovalsOnPush *bool `yaml:"reset_approvals_on_push,omitempty" json:"reset_approvals_on_push,omitempty"`

	// Settings defines general project merge request approval behaviors.
	Settings *ApprovalSettingsConfig `yaml:"settings,omitempty" json:"settings,omitempty"`

	// Rules defines a list of named approval rules to enforce.
	Rules []ApprovalRuleConfig `yaml:"rules,omitempty" json:"rules,omitempty"`

	// Prune deletes unmanaged named approval rules if set to true.
	Prune *bool `yaml:"prune,omitempty" json:"prune,omitempty"`
}

// ApprovalSettingsConfig specifies general project merge request approval settings.
type ApprovalSettingsConfig struct {
	// ApprovalsBeforeMerge specifies required approvals before merge.
	ApprovalsBeforeMerge *int `yaml:"approvals_before_merge,omitempty" json:"approvals_before_merge,omitempty"`

	// ResetApprovalsOnPush resets approvals when commits are pushed.
	ResetApprovalsOnPush *bool `yaml:"reset_approvals_on_push,omitempty" json:"reset_approvals_on_push,omitempty"`

	// AllowAuthorApproval allows merge request authors to approve their own MRs.
	AllowAuthorApproval *bool `yaml:"allow_author_approval,omitempty" json:"allow_author_approval,omitempty"`

	// AllowCommitterApproval allows committers to approve MRs containing their commits.
	AllowCommitterApproval *bool `yaml:"allow_committer_approval,omitempty" json:"allow_committer_approval,omitempty"`

	// AllowOverridesToApproverListPerMergeRequest allows authors/approvers to modify the approvers list.
	AllowOverridesToApproverListPerMergeRequest *bool `yaml:"allow_overrides_to_approver_list_per_merge_request,omitempty" json:"allow_overrides_to_approver_list_per_merge_request,omitempty"`

	// RetainApprovalsOnPush retains approvals when new commits are pushed to the source branch.
	RetainApprovalsOnPush *bool `yaml:"retain_approvals_on_push,omitempty" json:"retain_approvals_on_push,omitempty"`

	// SelectiveCodeOwnerRemovals clears only approvals of touched code owners on push.
	SelectiveCodeOwnerRemovals *bool `yaml:"selective_code_owner_removals,omitempty" json:"selective_code_owner_removals,omitempty"`

	// RequirePasswordToApprove requires users to provide password/2FA to approve.
	RequirePasswordToApprove *bool `yaml:"require_password_to_approve,omitempty" json:"require_password_to_approve,omitempty"`
}

// ApprovalRuleConfig defines a single named merge request approval rule.
type ApprovalRuleConfig struct {
	// Name is the unique identifier for the approval rule (e.g. "Security Review"). Required.
	Name string `yaml:"name" json:"name"`

	// ApprovalsRequired specifies minimum number of approvals required (>= 1).
	ApprovalsRequired int `yaml:"approvals_required" json:"approvals_required"`

	// UserUsernames specifies approver usernames (dynamically resolved to UserIDs).
	UserUsernames []string `yaml:"user_usernames,omitempty" json:"user_usernames,omitempty"`

	// UserIDs specifies explicit approver user IDs.
	UserIDs []int `yaml:"user_ids,omitempty" json:"user_ids,omitempty"`

	// GroupPaths specifies approver group paths (dynamically resolved to GroupIDs).
	GroupPaths []string `yaml:"group_paths,omitempty" json:"group_paths,omitempty"`

	// GroupIDs specifies explicit approver group IDs.
	GroupIDs []int `yaml:"group_ids,omitempty" json:"group_ids,omitempty"`

	// ProtectedBranchNames specifies target branch names/wildcards for this rule.
	ProtectedBranchNames []string `yaml:"protected_branch_names,omitempty" json:"protected_branch_names,omitempty"`

	// ProtectedBranchIDs specifies explicit target protected branch IDs.
	ProtectedBranchIDs []int `yaml:"protected_branch_ids,omitempty" json:"protected_branch_ids,omitempty"`

	// RuleType specifies the rule type: "regular" (default), "code_owner", "any_approver", "report_approver".
	RuleType string `yaml:"rule_type,omitempty" json:"rule_type,omitempty"`
}

// ============================================================================
// 8. Project Settings Configuration
// ============================================================================

// ProjectSettingsConfig configures project workflow, merge, and artifact settings.
type ProjectSettingsConfig struct {
	// DefaultBranch specifies the repository default branch name (e.g. "main").
	DefaultBranch string `yaml:"default_branch,omitempty" json:"default_branch,omitempty"`

	// SquashOption specifies MR squash option: "never", "always", "default_on", "default_off".
	SquashOption string `yaml:"squash_option,omitempty" json:"squash_option,omitempty"`

	// MergeMethod specifies MR merge strategy: "merge", "rebase_merge", "ff".
	MergeMethod string `yaml:"merge_method,omitempty" json:"merge_method,omitempty"`

	// OnlyAllowMergeIfPipelineSucceeds blocks merge unless pipeline passes.
	OnlyAllowMergeIfPipelineSucceeds *bool `yaml:"only_allow_merge_if_pipeline_succeeds,omitempty" json:"only_allow_merge_if_pipeline_succeeds,omitempty"`

	// AllowMergeOnSkippedPipeline allows merge even if pipeline is skipped.
	AllowMergeOnSkippedPipeline *bool `yaml:"allow_merge_on_skipped_pipeline,omitempty" json:"allow_merge_on_skipped_pipeline,omitempty"`

	// OnlyAllowMergeIfAllDiscussionsAreResolved blocks merge until discussions are resolved.
	OnlyAllowMergeIfAllDiscussionsAreResolved *bool `yaml:"only_allow_merge_if_all_discussions_are_resolved,omitempty" json:"only_allow_merge_if_all_discussions_are_resolved,omitempty"`

	// RemoveSourceBranchAfterMerge defaults MR option to delete source branch after merge.
	RemoveSourceBranchAfterMerge *bool `yaml:"remove_source_branch_after_merge,omitempty" json:"remove_source_branch_after_merge,omitempty"`

	// KeepLatestArtifact keeps the latest artifact for each ref regardless of expiry.
	KeepLatestArtifact *bool `yaml:"keep_latest_artifact,omitempty" json:"keep_latest_artifact,omitempty"`

	// PrintingMergeRequestLinkEnabled prints MR creation URL upon Git push over CLI.
	PrintingMergeRequestLinkEnabled *bool `yaml:"printing_merge_request_link_enabled,omitempty" json:"printing_merge_request_link_enabled,omitempty"`

	// AutoCancelPendingPipelines automatically cancels outdated pending/running pipelines ("enabled" | "disabled").
	AutoCancelPendingPipelines string `yaml:"auto_cancel_pending_pipelines,omitempty" json:"auto_cancel_pending_pipelines,omitempty"`

	// AutoDevopsEnabled enables GitLab Auto DevOps.
	AutoDevopsEnabled *bool `yaml:"auto_devops_enabled,omitempty" json:"auto_devops_enabled,omitempty"`

	// ContainerExpirationPolicy configures container registry cleanup rules.
	ContainerExpirationPolicy *ContainerExpirationPolicyConfig `yaml:"container_expiration_policy,omitempty" json:"container_expiration_policy,omitempty"`
}

// ContainerExpirationPolicyConfig defines project container registry expiration policies.
type ContainerExpirationPolicyConfig struct {
	// Cadence specifies policy run interval: "1d", "7d", "14d", "30d", "60d", "90d".
	Cadence string `yaml:"cadence,omitempty" json:"cadence,omitempty"`

	// Enabled enables automatic container expiration.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// KeepN specifies number of container tags to retain per image.
	KeepN *int `yaml:"keep_n,omitempty" json:"keep_n,omitempty"`

	// OlderThan specifies retention duration: "7d", "14d", "30d", "60d", "90d".
	OlderThan string `yaml:"older_than,omitempty" json:"older_than,omitempty"`

	// NameRegex matches tag names for expiration policy.
	NameRegex string `yaml:"name_regex,omitempty" json:"name_regex,omitempty"`

	// NameRegexDelete matches tag names to delete.
	NameRegexDelete string `yaml:"name_regex_delete,omitempty" json:"name_regex_delete,omitempty"`

	// NameRegexKeep matches tag names to preserve.
	NameRegexKeep string `yaml:"name_regex_keep,omitempty" json:"name_regex_keep,omitempty"`
}

// ============================================================================
// 9. Pipeline Retention Configuration
// ============================================================================

// PipelineRetentionConfig defines high-level pipeline retention policy in days.
type PipelineRetentionConfig struct {
	// RetentionDays specifies the number of days to retain pipeline history.
	// Maps directly to GitLab API's ci_delete_pipelines_in_seconds = RetentionDays * 86400.
	RetentionDays int `yaml:"retention_days" json:"retention_days"`
}

// Seconds converts RetentionDays to seconds for GitLab's ci_delete_pipelines_in_seconds.
func (p *PipelineRetentionConfig) Seconds() int {
	return p.RetentionDays * 86400
}

// ============================================================================
// 10. CI/CD Variables Configuration
// ============================================================================

// VariableConfig defines a project or group CI/CD variable.
type VariableConfig struct {
	// Key is the environment variable name (e.g. "AWS_REGION"). Required.
	Key string `yaml:"key" json:"key"`

	// Value is the variable value. Supports ${ENV_VAR} substitution. Required.
	Value string `yaml:"value" json:"value"`

	// VariableType is "env_var" (default) or "file".
	VariableType string `yaml:"variable_type,omitempty" json:"variable_type,omitempty"`

	// Protected restricts variable availability to protected branches/tags.
	Protected *bool `yaml:"protected,omitempty" json:"protected,omitempty"`

	// Masked masks variable in job logs (must meet GitLab masking criteria).
	Masked *bool `yaml:"masked,omitempty" json:"masked,omitempty"`

	// Raw disables environment variable expansion inside GitLab CI runner.
	Raw *bool `yaml:"raw,omitempty" json:"raw,omitempty"`

	// EnvironmentScope defines target environment scope (default: "*").
	EnvironmentScope string `yaml:"environment_scope,omitempty" json:"environment_scope,omitempty"`

	// Description provides human-readable context for the variable.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// CompositeKey returns the unique composite identifier (Key + EnvironmentScope).
func (v *VariableConfig) CompositeKey() string {
	scope := v.EnvironmentScope
	if scope == "" {
		scope = "*"
	}
	return fmt.Sprintf("%s::%s", v.Key, scope)
}

// ============================================================================
// 11. Runners Configuration
// ============================================================================

// RunnersConfig configures project runner fleet settings and assignments.
type RunnersConfig struct {
	// SharedRunnersEnabled enables shared instance runners for the project.
	SharedRunnersEnabled *bool `yaml:"shared_runners_enabled,omitempty" json:"shared_runners_enabled,omitempty"`

	// GroupRunnersEnabled enables inherited group runners for the project.
	GroupRunnersEnabled *bool `yaml:"group_runners_enabled,omitempty" json:"group_runners_enabled,omitempty"`

	// Runners defines individual runner attribute assertions.
	Runners []RunnerConfig `yaml:"runners,omitempty" json:"runners,omitempty"`
}

// RunnerConfig defines configuration assertions for a specific runner.
type RunnerConfig struct {
	// ID is the numeric runner ID.
	ID int `yaml:"id,omitempty" json:"id,omitempty"`

	// Description is the runner name/description.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Paused pauses the runner from accepting new jobs.
	Paused *bool `yaml:"paused,omitempty" json:"paused,omitempty"`

	// Locked locks runner to the assigned project.
	Locked *bool `yaml:"locked,omitempty" json:"locked,omitempty"`

	// TagList specifies mandatory runner tags.
	TagList []string `yaml:"tag_list,omitempty" json:"tag_list,omitempty"`

	// RunUntagged allows runner to pick up jobs without tags.
	RunUntagged *bool `yaml:"run_untagged,omitempty" json:"run_untagged,omitempty"`

	// AccessLevel defines runner access level: "ref_protected" or "not_protected".
	AccessLevel string `yaml:"access_level,omitempty" json:"access_level,omitempty"`

	// MaximumTimeout specifies maximum job execution timeout in seconds.
	MaximumTimeout *int `yaml:"maximum_timeout,omitempty" json:"maximum_timeout,omitempty"`
}

// ============================================================================
// 12. Compliance Framework Configuration
// ============================================================================

// ComplianceConfig configures compliance framework label enforcement.
type ComplianceConfig struct {
	// FrameworkName is the human-readable compliance framework name (e.g. "SOC2", "PCI-DSS").
	FrameworkName string `yaml:"framework_name,omitempty" json:"framework_name,omitempty"`

	// FrameworkID is the explicit compliance framework ID (resolved from FrameworkName if omitted).
	FrameworkID *int `yaml:"framework_id,omitempty" json:"framework_id,omitempty"`

	// Prune removes compliance framework labels from non-compliant projects.
	Prune *bool `yaml:"prune,omitempty" json:"prune,omitempty"`
}

// ============================================================================
// 13. Webhooks Configuration
// ============================================================================

// WebhookConfig defines a project or group webhook integration endpoint.
type WebhookConfig struct {
	// URL is the destination webhook endpoint URL. Required.
	URL string `yaml:"url" json:"url"`

	// PushEvents triggers webhook on Git push events.
	PushEvents *bool `yaml:"push_events,omitempty" json:"push_events,omitempty"`

	// MergeRequestsEvents triggers webhook on MR events.
	MergeRequestsEvents *bool `yaml:"merge_requests_events,omitempty" json:"merge_requests_events,omitempty"`

	// TagPushEvents triggers webhook on tag creation/deletion.
	TagPushEvents *bool `yaml:"tag_push_events,omitempty" json:"tag_push_events,omitempty"`

	// IssuesEvents triggers webhook on issue events.
	IssuesEvents *bool `yaml:"issues_events,omitempty" json:"issues_events,omitempty"`

	// PipelineEvents triggers webhook on pipeline status changes.
	PipelineEvents *bool `yaml:"pipeline_events,omitempty" json:"pipeline_events,omitempty"`

	// JobEvents triggers webhook on CI job status changes.
	JobEvents *bool `yaml:"job_events,omitempty" json:"job_events,omitempty"`

	// ReleasesEvents triggers webhook on release events.
	ReleasesEvents *bool `yaml:"releases_events,omitempty" json:"releases_events,omitempty"`

	// EnableSSLVerification enables SSL certificate validation (default: true).
	EnableSSLVerification *bool `yaml:"enable_ssl_verification,omitempty" json:"enable_ssl_verification,omitempty"`

	// SecretToken specifies HMAC secret verification token.
	SecretToken string `yaml:"secret_token,omitempty" json:"secret_token,omitempty"`

	// PushEventsBranchFilter restricts push webhook triggers to matching branch patterns.
	PushEventsBranchFilter string `yaml:"push_events_branch_filter,omitempty" json:"push_events_branch_filter,omitempty"`
}

// ============================================================================
// 14. Members & Access Governance Configuration
// ============================================================================

// MembersConfig defines member access policy and expiration audit rules.
type MembersConfig struct {
	// MinAccessLevel asserts minimum allowed role level (10=Guest, 20=Reporter, 30=Dev, 40=Maintainer, 50=Owner).
	MinAccessLevel *int `yaml:"min_access_level,omitempty" json:"min_access_level,omitempty"`

	// MaxAccessLevel asserts maximum allowed role level (flags over-privileged users).
	MaxAccessLevel *int `yaml:"max_access_level,omitempty" json:"max_access_level,omitempty"`

	// EnforceExpiresAt requires all direct project members to have an expiration date configured.
	EnforceExpiresAt *bool `yaml:"enforce_expires_at,omitempty" json:"enforce_expires_at,omitempty"`

	// MaxExpirationDays limits member expiration date to at most N days from today.
	MaxExpirationDays *int `yaml:"max_expiration_days,omitempty" json:"max_expiration_days,omitempty"`

	// RemoveInheritedMaintainers removes redundant direct maintainers when inherited maintainer exists.
	RemoveInheritedMaintainers *bool `yaml:"remove_inherited_maintainers,omitempty" json:"remove_inherited_maintainers,omitempty"`

	// AllowedMembers defines explicitly allowed users and their mandatory access levels.
	AllowedMembers []MemberRuleConfig `yaml:"allowed_members,omitempty" json:"allowed_members,omitempty"`

	// DeniedMembers defines list of usernames that must NOT have direct or inherited membership.
	DeniedMembers []string `yaml:"denied_members,omitempty" json:"denied_members,omitempty"`
}

// MemberRuleConfig specifies role and expiration assertions for a single member.
type MemberRuleConfig struct {
	// Username is the GitLab user handle. Required.
	Username string `yaml:"username" json:"username"`

	// AccessLevel is the required numeric access level (10, 20, 30, 40, 50).
	AccessLevel int `yaml:"access_level" json:"access_level"`

	// ExpiresAt is the optional expiration date formatted as YYYY-MM-DD.
	ExpiresAt string `yaml:"expires_at,omitempty" json:"expires_at,omitempty"`
}
