package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	varKeyRegex    = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	maskedValRegex = regexp.MustCompile(`^[a-zA-Z0-9_+=/@:~.-]+$`)
)

// ValidationError represents an individual configuration validation error.
type ValidationError struct {
	Field   string      `json:"field"`
	Message string      `json:"message"`
	Value   interface{} `json:"value,omitempty"`
}

// ValidationErrors is a slice of ValidationError implementing error.
type ValidationErrors []ValidationError

func (v ValidationErrors) Error() string {
	if len(v) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("configuration validation failed with %d error(s):\n", len(v)))
	for i, err := range v {
		if err.Value != nil {
			sb.WriteString(fmt.Sprintf("  [%d] %s: %s (got: %v)\n", i+1, err.Field, err.Message, err.Value))
		} else {
			sb.WriteString(fmt.Sprintf("  [%d] %s: %s\n", i+1, err.Field, err.Message))
		}
	}
	return sb.String()
}

// Errors returns the underlying slice of ValidationError.
func (v ValidationErrors) Errors() []ValidationError {
	return v
}

// Validate performs comprehensive semantic validation on PolicyConfig.
func Validate(cfg *PolicyConfig) error {
	if cfg == nil {
		return ValidationErrors{
			{Field: "config", Message: "policy config cannot be nil"},
		}
	}

	var errs ValidationErrors

	validateSettings(&cfg.Settings, "settings", &errs)
	validateTargets(&cfg.Targets, "targets", &errs)
	validatePolicies(&cfg.Policies, "policies", &errs)

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Validate invokes Validate on the receiver PolicyConfig.
func (p *PolicyConfig) Validate() error {
	return Validate(p)
}

func validateSettings(s *SettingsConfig, prefix string, errs *ValidationErrors) {
	if s == nil {
		return
	}

	if s.Concurrency < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".concurrency",
			Message: fmt.Sprintf("concurrency must be at least 1 (got %d)", s.Concurrency),
			Value:   s.Concurrency,
		})
	}

	if s.LogLevel != "" {
		switch strings.ToLower(s.LogLevel) {
		case "debug", "info", "warn", "error":
		default:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".log_level",
				Message: fmt.Sprintf("invalid log_level '%s' (must be debug, info, warn, or error)", s.LogLevel),
				Value:   s.LogLevel,
			})
		}
	}

	if s.LogFormat != "" {
		switch strings.ToLower(s.LogFormat) {
		case "text", "json":
		default:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".log_format",
				Message: fmt.Sprintf("invalid log_format '%s' (must be text or json)", s.LogFormat),
				Value:   s.LogFormat,
			})
		}
	}

	if s.ReportFormat != "" {
		switch strings.ToLower(s.ReportFormat) {
		case "table", "json", "csv", "markdown":
		default:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".report_format",
				Message: fmt.Sprintf("invalid report_format '%s' (must be table, json, csv, or markdown)", s.ReportFormat),
				Value:   s.ReportFormat,
			})
		}
	}

	validateGitLabSettings(&s.GitLab, prefix+".gitlab", errs)
}

func validateGitLabSettings(g *GitLabSettingsConfig, prefix string, errs *ValidationErrors) {
	if g == nil {
		return
	}

	if g.TokenType != "" {
		switch strings.ToLower(g.TokenType) {
		case "private_token", "oauth", "job_token":
		default:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".token_type",
				Message: fmt.Sprintf("invalid token_type '%s' (must be private_token, oauth, or job_token)", g.TokenType),
				Value:   g.TokenType,
			})
		}
	}

	if g.TimeoutSeconds < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".timeout_seconds",
			Message: "timeout_seconds must be non-negative",
			Value:   g.TimeoutSeconds,
		})
	}

	if g.RateLimitRPS < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".rate_limit_rps",
			Message: "rate_limit_rps must be non-negative",
			Value:   g.RateLimitRPS,
		})
	}

	if g.RateLimitBurst < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".rate_limit_burst",
			Message: "rate_limit_burst must be non-negative",
			Value:   g.RateLimitBurst,
		})
	}

	if g.MaxRetries < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".max_retries",
			Message: "max_retries must be non-negative",
			Value:   g.MaxRetries,
		})
	}

	if g.RetryBaseDelayMs < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".retry_base_delay_ms",
			Message: "retry_base_delay_ms must be non-negative",
			Value:   g.RetryBaseDelayMs,
		})
	}

	if g.RetryMaxDelayMs < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".retry_max_delay_ms",
			Message: "retry_max_delay_ms must be non-negative",
			Value:   g.RetryMaxDelayMs,
		})
	}

	if g.RetryBaseDelayMs > 0 && g.RetryMaxDelayMs > 0 && g.RetryBaseDelayMs > g.RetryMaxDelayMs {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".retry_base_delay_ms",
			Message: fmt.Sprintf("retry_base_delay_ms (%d) cannot be greater than retry_max_delay_ms (%d)", g.RetryBaseDelayMs, g.RetryMaxDelayMs),
		})
	}
}

func validateTargets(t *TargetSelectors, prefix string, errs *ValidationErrors) {
	if t == nil {
		return
	}

	if t.GroupSelector != nil {
		validateGroupSelector(t.GroupSelector, prefix+".group_selector", errs)
	}

	if t.ProjectSelector != nil {
		validateProjectSelector(t.ProjectSelector, prefix+".project_selector", errs)
	}
}

func validateGroupSelector(g *GroupSelector, prefix string, errs *ValidationErrors) {
	for i, id := range g.GroupIDsInclude {
		if id <= 0 {
			*errs = append(*errs, ValidationError{
				Field:   fmt.Sprintf("%s.group_ids_include[%d]", prefix, i),
				Message: fmt.Sprintf("group ID must be positive (got %d)", id),
				Value:   id,
			})
		}
	}
	for i, id := range g.GroupIDsExclude {
		if id <= 0 {
			*errs = append(*errs, ValidationError{
				Field:   fmt.Sprintf("%s.group_ids_exclude[%d]", prefix, i),
				Message: fmt.Sprintf("group ID must be positive (got %d)", id),
				Value:   id,
			})
		}
	}
	for i, path := range g.GroupPathsInclude {
		if strings.TrimSpace(path) == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
			*errs = append(*errs, ValidationError{
				Field:   fmt.Sprintf("%s.group_paths_include[%d]", prefix, i),
				Message: fmt.Sprintf("invalid group path '%s' (must be non-empty without leading/trailing slashes)", path),
				Value:   path,
			})
		}
	}
	for i, path := range g.GroupPathsExclude {
		if strings.TrimSpace(path) == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
			*errs = append(*errs, ValidationError{
				Field:   fmt.Sprintf("%s.group_paths_exclude[%d]", prefix, i),
				Message: fmt.Sprintf("invalid group path '%s' (must be non-empty without leading/trailing slashes)", path),
				Value:   path,
			})
		}
	}
}

func validateProjectSelector(p *ProjectSelector, prefix string, errs *ValidationErrors) {
	if p.ProjectNameRegexInclude != "" {
		if _, err := regexp.Compile(p.ProjectNameRegexInclude); err != nil {
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".project_name_regex_include",
				Message: fmt.Sprintf("invalid regular expression: %v", err),
				Value:   p.ProjectNameRegexInclude,
			})
		}
	}

	if p.ProjectNameRegexExclude != "" {
		if _, err := regexp.Compile(p.ProjectNameRegexExclude); err != nil {
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".project_name_regex_exclude",
				Message: fmt.Sprintf("invalid regular expression: %v", err),
				Value:   p.ProjectNameRegexExclude,
			})
		}
	}

	if p.Visibility != "" {
		switch strings.ToLower(p.Visibility) {
		case "public", "internal", "private", "any":
		default:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".visibility",
				Message: fmt.Sprintf("invalid visibility '%s' (must be public, internal, private, or any)", p.Visibility),
				Value:   p.Visibility,
			})
		}
	}

	if p.IDRange != nil {
		if p.IDRange.Min < 0 {
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".id_range.min",
				Message: fmt.Sprintf("min must be non-negative (got %d)", p.IDRange.Min),
				Value:   p.IDRange.Min,
			})
		}
		if p.IDRange.Max < 0 {
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".id_range.max",
				Message: fmt.Sprintf("max must be non-negative (got %d)", p.IDRange.Max),
				Value:   p.IDRange.Max,
			})
		}
		if p.IDRange.Min > 0 && p.IDRange.Max > 0 && p.IDRange.Min > p.IDRange.Max {
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".id_range",
				Message: fmt.Sprintf("min (%d) cannot be greater than max (%d)", p.IDRange.Min, p.IDRange.Max),
			})
		}
	}
}

func validatePolicies(p *PoliciesConfig, prefix string, errs *ValidationErrors) {
	if p == nil {
		return
	}

	if p.PushRules != nil {
		validatePushRules(p.PushRules, prefix+".push_rules", errs)
	}

	for i := range p.ProtectedBranches {
		validateProtectedBranch(&p.ProtectedBranches[i], fmt.Sprintf("%s.protected_branches[%d]", prefix, i), errs)
	}

	if p.ApprovalRules != nil {
		validateApprovalRules(p.ApprovalRules, prefix+".approval_rules", errs)
	}

	if p.ProjectSettings != nil {
		validateProjectSettings(p.ProjectSettings, prefix+".project_settings", errs)
	}

	if p.PipelineRetention != nil {
		validatePipelineRetention(p.PipelineRetention, prefix+".pipeline_retention", errs)
	}

	validateVariables(p.Variables, prefix+".variables", errs)

	if p.Runners != nil {
		validateRunners(p.Runners, prefix+".runners", errs)
	}

	if p.Compliance != nil {
		validateCompliance(p.Compliance, prefix+".compliance", errs)
	}

	for i := range p.Webhooks {
		validateWebhook(&p.Webhooks[i], fmt.Sprintf("%s.webhooks[%d]", prefix, i), errs)
	}

	if p.Members != nil {
		validateMembers(p.Members, prefix+".members", errs)
	}
}

func validatePushRules(r *PushRulesConfig, prefix string, errs *ValidationErrors) {
	checkRegex(r.AuthorEmailRegex, prefix+".author_email_regex", errs)
	checkRegex(r.BranchNameRegex, prefix+".branch_name_regex", errs)
	checkRegex(r.CommitMessageRegex, prefix+".commit_message_regex", errs)
	checkRegex(r.CommitMessageNegativeRegex, prefix+".commit_message_negative_regex", errs)
	checkRegex(r.FileNameRegex, prefix+".file_name_regex", errs)

	if r.MaxFileSize != nil && *r.MaxFileSize < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".max_file_size",
			Message: "max_file_size must be non-negative",
			Value:   *r.MaxFileSize,
		})
	}
}

func validateProtectedBranch(b *ProtectedBranchRuleConfig, prefix string, errs *ValidationErrors) {
	if strings.TrimSpace(b.Name) == "" {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".name",
			Message: "protected branch name cannot be empty",
		})
	}

	validateBranchAccessList(b.AllowedToPush, prefix+".allowed_to_push", errs)
	validateBranchAccessList(b.AllowedToMerge, prefix+".allowed_to_merge", errs)
	validateBranchAccessList(b.AllowedToUnprotect, prefix+".allowed_to_unprotect", errs)
}

func validateBranchAccessList(list []BranchAccessDescription, prefix string, errs *ValidationErrors) {
	for i, access := range list {
		if access.AccessLevel != 0 && access.AccessLevel != 30 && access.AccessLevel != 40 && access.AccessLevel != 60 {
			*errs = append(*errs, ValidationError{
				Field:   fmt.Sprintf("%s[%d].access_level", prefix, i),
				Message: fmt.Sprintf("invalid access level %d (must be 0, 30, 40, or 60)", access.AccessLevel),
				Value:   access.AccessLevel,
			})
		}
		if access.UserID < 0 {
			*errs = append(*errs, ValidationError{
				Field:   fmt.Sprintf("%s[%d].user_id", prefix, i),
				Message: "user_id must be non-negative",
				Value:   access.UserID,
			})
		}
		if access.GroupID < 0 {
			*errs = append(*errs, ValidationError{
				Field:   fmt.Sprintf("%s[%d].group_id", prefix, i),
				Message: "group_id must be non-negative",
				Value:   access.GroupID,
			})
		}
		if access.DeployKeyID < 0 {
			*errs = append(*errs, ValidationError{
				Field:   fmt.Sprintf("%s[%d].deploy_key_id", prefix, i),
				Message: "deploy_key_id must be non-negative",
				Value:   access.DeployKeyID,
			})
		}
	}
}

func validateApprovalRules(a *ApprovalRulesConfig, prefix string, errs *ValidationErrors) {
	for i, rule := range a.Rules {
		rulePrefix := fmt.Sprintf("%s.rules[%d]", prefix, i)
		if strings.TrimSpace(rule.Name) == "" {
			*errs = append(*errs, ValidationError{
				Field:   rulePrefix + ".name",
				Message: "approval rule name cannot be empty",
			})
		}
		if rule.ApprovalsRequired < 1 {
			*errs = append(*errs, ValidationError{
				Field:   rulePrefix + ".approvals_required",
				Message: fmt.Sprintf("approvals_required must be at least 1 (got %d)", rule.ApprovalsRequired),
				Value:   rule.ApprovalsRequired,
			})
		}
		if rule.RuleType != "" {
			switch rule.RuleType {
			case "regular", "code_owner", "any_approver", "report_approver":
			default:
				*errs = append(*errs, ValidationError{
					Field:   rulePrefix + ".rule_type",
					Message: fmt.Sprintf("invalid rule_type '%s' (must be regular, code_owner, any_approver, or report_approver)", rule.RuleType),
					Value:   rule.RuleType,
				})
			}
		}
		if rule.RuleType != "any_approver" {
			hasApprovers := len(rule.UserUsernames) > 0 || len(rule.UserIDs) > 0 || len(rule.GroupPaths) > 0 || len(rule.GroupIDs) > 0
			if !hasApprovers {
				*errs = append(*errs, ValidationError{
					Field:   rulePrefix,
					Message: fmt.Sprintf("approval rule '%s' requires at least one user or group approver", rule.Name),
				})
			}
		}
	}
}

func validateProjectSettings(p *ProjectSettingsConfig, prefix string, errs *ValidationErrors) {
	if p.SquashOption != "" {
		switch p.SquashOption {
		case "never", "always", "default_on", "default_off":
		default:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".squash_option",
				Message: fmt.Sprintf("invalid squash_option '%s' (must be never, always, default_on, or default_off)", p.SquashOption),
				Value:   p.SquashOption,
			})
		}
	}

	if p.MergeMethod != "" {
		switch p.MergeMethod {
		case "merge", "rebase_merge", "ff":
		default:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".merge_method",
				Message: fmt.Sprintf("invalid merge_method '%s' (must be merge, rebase_merge, or ff)", p.MergeMethod),
				Value:   p.MergeMethod,
			})
		}
	}

	if p.AutoCancelPendingPipelines != "" {
		switch p.AutoCancelPendingPipelines {
		case "enabled", "disabled":
		default:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".auto_cancel_pending_pipelines",
				Message: fmt.Sprintf("invalid auto_cancel_pending_pipelines '%s' (must be enabled or disabled)", p.AutoCancelPendingPipelines),
				Value:   p.AutoCancelPendingPipelines,
			})
		}
	}

	if p.ContainerExpirationPolicy != nil {
		cPrefix := prefix + ".container_expiration_policy"
		cep := p.ContainerExpirationPolicy

		if cep.Cadence != "" {
			switch cep.Cadence {
			case "1d", "7d", "14d", "30d", "60d", "90d":
			default:
				*errs = append(*errs, ValidationError{
					Field:   cPrefix + ".cadence",
					Message: fmt.Sprintf("invalid cadence '%s' (must be 1d, 7d, 14d, 30d, 60d, or 90d)", cep.Cadence),
					Value:   cep.Cadence,
				})
			}
		}

		if cep.OlderThan != "" {
			switch cep.OlderThan {
			case "7d", "14d", "30d", "60d", "90d":
			default:
				*errs = append(*errs, ValidationError{
					Field:   cPrefix + ".older_than",
					Message: fmt.Sprintf("invalid older_than '%s' (must be 7d, 14d, 30d, 60d, or 90d)", cep.OlderThan),
					Value:   cep.OlderThan,
				})
			}
		}

		if cep.KeepN != nil {
			switch *cep.KeepN {
			case 0, 1, 5, 10, 25, 50, 100:
			default:
				*errs = append(*errs, ValidationError{
					Field:   cPrefix + ".keep_n",
					Message: fmt.Sprintf("invalid keep_n %d (must be 0, 1, 5, 10, 25, 50, or 100)", *cep.KeepN),
					Value:   *cep.KeepN,
				})
			}
		}

		checkRegex(cep.NameRegex, cPrefix+".name_regex", errs)
		checkRegex(cep.NameRegexDelete, cPrefix+".name_regex_delete", errs)
		checkRegex(cep.NameRegexKeep, cPrefix+".name_regex_keep", errs)
	}
}

func validatePipelineRetention(p *PipelineRetentionConfig, prefix string, errs *ValidationErrors) {
	if p.RetentionDays < 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".retention_days",
			Message: fmt.Sprintf("retention_days must be non-negative (got %d)", p.RetentionDays),
			Value:   p.RetentionDays,
		})
	}
}

func validateVariables(vars []VariableConfig, prefix string, errs *ValidationErrors) {
	seenKeys := make(map[string]bool)

	for i, v := range vars {
		varPrefix := fmt.Sprintf("%s[%d]", prefix, i)

		if strings.TrimSpace(v.Key) == "" {
			*errs = append(*errs, ValidationError{
				Field:   varPrefix + ".key",
				Message: "variable key cannot be empty",
			})
		} else if !varKeyRegex.MatchString(v.Key) {
			*errs = append(*errs, ValidationError{
				Field:   varPrefix + ".key",
				Message: fmt.Sprintf("invalid variable key '%s' (must match '^[a-zA-Z0-9_]+$')", v.Key),
				Value:   v.Key,
			})
		}

		if v.VariableType != "" {
			switch v.VariableType {
			case "env_var", "file":
			default:
				*errs = append(*errs, ValidationError{
					Field:   varPrefix + ".variable_type",
					Message: fmt.Sprintf("invalid variable_type '%s' (must be env_var or file)", v.VariableType),
					Value:   v.VariableType,
				})
			}
		}

		if v.Masked != nil && *v.Masked {
			if len(v.Value) < 8 {
				*errs = append(*errs, ValidationError{
					Field:   varPrefix + ".value",
					Message: fmt.Sprintf("masked CI/CD variable value must be at least 8 characters long (got %d characters)", len(v.Value)),
					Value:   "[REDACTED]",
				})
			} else if !maskedValRegex.MatchString(v.Value) {
				*errs = append(*errs, ValidationError{
					Field:   varPrefix + ".value",
					Message: "masked CI/CD variable value contains invalid characters; must match '^[a-zA-Z0-9_+=/@:~.-]+$' without spaces or newlines",
					Value:   "[REDACTED]",
				})
			}
		}

		compKey := v.CompositeKey()
		if seenKeys[compKey] {
			scope := v.EnvironmentScope
			if scope == "" {
				scope = "*"
			}
			*errs = append(*errs, ValidationError{
				Field:   prefix,
				Message: fmt.Sprintf("duplicate variable key '%s' for environment scope '%s'", v.Key, scope),
			})
		} else {
			seenKeys[compKey] = true
		}
	}
}

func validateRunners(r *RunnersConfig, prefix string, errs *ValidationErrors) {
	for i, runner := range r.Runners {
		rPrefix := fmt.Sprintf("%s.runners[%d]", prefix, i)
		if runner.AccessLevel != "" {
			switch runner.AccessLevel {
			case "ref_protected", "not_protected":
			default:
				*errs = append(*errs, ValidationError{
					Field:   rPrefix + ".access_level",
					Message: fmt.Sprintf("invalid access_level '%s' (must be ref_protected or not_protected)", runner.AccessLevel),
					Value:   runner.AccessLevel,
				})
			}
		}
		if runner.MaximumTimeout != nil && *runner.MaximumTimeout < 0 {
			*errs = append(*errs, ValidationError{
				Field:   rPrefix + ".maximum_timeout",
				Message: "maximum_timeout must be non-negative",
				Value:   *runner.MaximumTimeout,
			})
		}
	}
}

func validateCompliance(c *ComplianceConfig, prefix string, errs *ValidationErrors) {
	if strings.TrimSpace(c.FrameworkName) == "" && c.FrameworkID == nil {
		*errs = append(*errs, ValidationError{
			Field:   prefix,
			Message: "compliance configuration requires either framework_name or framework_id",
		})
	}
}

func validateWebhook(w *WebhookConfig, prefix string, errs *ValidationErrors) {
	if strings.TrimSpace(w.URL) == "" {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".url",
			Message: "webhook url cannot be empty",
		})
		return
	}

	u, err := url.ParseRequestURI(w.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".url",
			Message: fmt.Sprintf("invalid webhook URL '%s' (must be a valid absolute HTTP/HTTPS URL)", w.URL),
			Value:   w.URL,
		})
	}
}

func validateMembers(m *MembersConfig, prefix string, errs *ValidationErrors) {
	if m.MinAccessLevel != nil {
		if !isValidMemberAccessLevel(*m.MinAccessLevel) {
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".min_access_level",
				Message: fmt.Sprintf("invalid min_access_level %d (must be 10, 20, 30, 40, or 50)", *m.MinAccessLevel),
				Value:   *m.MinAccessLevel,
			})
		}
	}

	if m.MaxAccessLevel != nil {
		if !isValidMemberAccessLevel(*m.MaxAccessLevel) {
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".max_access_level",
				Message: fmt.Sprintf("invalid max_access_level %d (must be 10, 20, 30, 40, or 50)", *m.MaxAccessLevel),
				Value:   *m.MaxAccessLevel,
			})
		}
	}

	if m.MinAccessLevel != nil && m.MaxAccessLevel != nil && *m.MinAccessLevel > *m.MaxAccessLevel {
		*errs = append(*errs, ValidationError{
			Field:   prefix,
			Message: fmt.Sprintf("min_access_level (%d) cannot be greater than max_access_level (%d)", *m.MinAccessLevel, *m.MaxAccessLevel),
		})
	}

	if m.MaxExpirationDays != nil && *m.MaxExpirationDays <= 0 {
		*errs = append(*errs, ValidationError{
			Field:   prefix + ".max_expiration_days",
			Message: fmt.Sprintf("max_expiration_days must be positive (got %d)", *m.MaxExpirationDays),
			Value:   *m.MaxExpirationDays,
		})
	}

	for i, member := range m.AllowedMembers {
		mPrefix := fmt.Sprintf("%s.allowed_members[%d]", prefix, i)
		if strings.TrimSpace(member.Username) == "" {
			*errs = append(*errs, ValidationError{
				Field:   mPrefix + ".username",
				Message: "member username cannot be empty",
			})
		}
		if !isValidMemberAccessLevel(member.AccessLevel) {
			*errs = append(*errs, ValidationError{
				Field:   mPrefix + ".access_level",
				Message: fmt.Sprintf("invalid access level %d (must be 10, 20, 30, 40, or 50)", member.AccessLevel),
				Value:   member.AccessLevel,
			})
		}
		if member.ExpiresAt != "" {
			if _, err := time.Parse("2006-01-02", member.ExpiresAt); err != nil {
				*errs = append(*errs, ValidationError{
					Field:   mPrefix + ".expires_at",
					Message: fmt.Sprintf("invalid expires_at format '%s' (expected YYYY-MM-DD)", member.ExpiresAt),
					Value:   member.ExpiresAt,
				})
			}
		}
	}
}

func isValidMemberAccessLevel(level int) bool {
	return level == 10 || level == 20 || level == 30 || level == 40 || level == 50
}

func checkRegex(pattern, field string, errs *ValidationErrors) {
	if pattern == "" {
		return
	}
	if _, err := regexp.Compile(pattern); err != nil {
		*errs = append(*errs, ValidationError{
			Field:   field,
			Message: fmt.Sprintf("invalid regular expression: %v", err),
			Value:   pattern,
		})
	}
}
