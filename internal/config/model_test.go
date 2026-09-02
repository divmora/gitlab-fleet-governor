package config_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func boolPtr(b bool) *bool {
	return &b
}

func intPtr(i int) *int {
	return &i
}

func TestModel_Interchangeability(t *testing.T) {
	yamlData := []byte(`
version: "v1"
settings:
  dry_run: false
  concurrency: 15
  log_level: "debug"
  log_format: "json"
  report_format: "markdown"
  output_file_path: "/tmp/report.md"
  gitlab:
    base_url: "https://gitlab.example.com/api/v4"
    token: "glpat-token123"
    token_type: "private_token"
    timeout_seconds: 45
    rate_limit_rps: 50.0
    rate_limit_burst: 100
    max_retries: 5
    retry_base_delay_ms: 300
    retry_max_delay_ms: 15000
targets:
  group_selector:
    group_ids_include: [10, 20]
    group_ids_exclude: [30]
    group_paths_include: ["group/subgroup"]
    group_paths_exclude: ["group/archived"]
    recursive: true
  project_selector:
    namespaces_include: ["group/subgroup"]
    namespaces_exclude: ["group/subgroup/legacy"]
    project_name_regex_include: "^(service|api)-.*"
    project_name_regex_exclude: ".*-deprecated$"
    topics_include: ["production", "tier1"]
    topics_exclude: ["sandbox"]
    visibility: "private"
    archived: false
    id_range:
      min: 100
      max: 5000
policies:
  push_rules:
    author_email_regex: ".+@company\\.com$"
    branch_name_regex: "^(main|release/.*)$"
    commit_message_regex: "^(feat|fix|chore): .+"
    commit_message_negative_regex: "^WIP:"
    file_name_regex: "\\.(exe|dll)$"
    max_file_size: 50
    commit_committer_check: true
    member_check: true
    prevent_secrets: true
    deny_delete_tag: true
    reject_unsigned_commits: true
    reject_non_dco_commits: true
  protected_branches:
    - name: "main"
      allowed_to_push:
        - access_level: 40
      allowed_to_merge:
        - access_level: 30
      allowed_to_unprotect:
        - access_level: 60
      allow_force_push: false
      code_owner_approval_required: true
  approval_rules:
    settings:
      allow_author_approval: false
      allow_committer_approval: false
      allow_overrides_to_approver_list_per_merge_request: false
      retain_approvals_on_push: true
      selective_code_owner_removals: true
      require_password_to_approve: true
    rules:
      - name: "Security Approval"
        approvals_required: 2
        user_usernames: ["sec-lead", "sec-auditor"]
        user_ids: [1001, 1002]
        group_paths: ["sec-team"]
        group_ids: [501]
        protected_branch_names: ["main"]
        protected_branch_ids: [10]
        rule_type: "regular"
    prune: true
  project_settings:
    default_branch: "main"
    squash_option: "always"
    merge_method: "rebase_merge"
    only_allow_merge_if_pipeline_succeeds: true
    allow_merge_on_skipped_pipeline: false
    only_allow_merge_if_all_discussions_are_resolved: true
    remove_source_branch_after_merge: true
    keep_latest_artifact: true
    printing_merge_request_link_enabled: true
    auto_cancel_pending_pipelines: "enabled"
    auto_devops_enabled: false
    container_expiration_policy:
      cadence: "7d"
      enabled: true
      keep_n: 10
      older_than: "30d"
      name_regex: ".*-release"
      name_regex_delete: ".*-temp"
      name_regex_keep: "^v[0-9]+"
  pipeline_retention:
    retention_days: 90
  variables:
    - key: "DATABASE_URL"
      value: "postgres://prod:pass@db:5432/main"
      variable_type: "env_var"
      protected: true
      masked: true
      raw: false
      environment_scope: "production"
      description: "Prod Database connection string"
  runners:
    shared_runners_enabled: false
    group_runners_enabled: true
    runners:
      - id: 101
        description: "Dedicated Build Runner"
        paused: false
        locked: true
        tag_list: ["linux", "docker"]
        run_untagged: false
        access_level: "ref_protected"
        maximum_timeout: 3600
  compliance:
    framework_name: "SOC2"
    framework_id: 12
    prune: true
  webhooks:
    - url: "https://events.example.com/gitlab"
      push_events: true
      merge_requests_events: true
      tag_push_events: false
      issues_events: false
      pipeline_events: true
      job_events: false
      releases_events: true
      enable_ssl_verification: true
      secret_token: "webhook-secret-token"
      push_events_branch_filter: "main"
  members:
    min_access_level: 20
    max_access_level: 40
    enforce_expires_at: true
    max_expiration_days: 365
    remove_inherited_maintainers: true
    allowed_members:
      - username: "lead-dev"
        access_level: 40
        expires_at: "2026-12-31"
    denied_members: ["former-employee"]
`)

	var yamlCfg config.PolicyConfig
	err := yaml.Unmarshal(yamlData, &yamlCfg)
	require.NoError(t, err)

	// Serialize struct to JSON
	jsonData, err := json.Marshal(yamlCfg)
	require.NoError(t, err)

	// Deserialize JSON into new struct
	var jsonCfg config.PolicyConfig
	err = json.Unmarshal(jsonData, &jsonCfg)
	require.NoError(t, err)

	// Verify equality
	assert.Equal(t, yamlCfg, jsonCfg)
}

func TestModel_PointerSemantics(t *testing.T) {
	// 1. Explicit false and zero values
	explicitData := []byte(`
version: "v1"
settings:
  dry_run: false
policies:
  push_rules:
    prevent_secrets: false
    max_file_size: 0
`)
	var explicitCfg config.PolicyConfig
	err := yaml.Unmarshal(explicitData, &explicitCfg)
	require.NoError(t, err)

	require.NotNil(t, explicitCfg.Settings.DryRun)
	assert.False(t, *explicitCfg.Settings.DryRun)

	require.NotNil(t, explicitCfg.Policies.PushRules)
	require.NotNil(t, explicitCfg.Policies.PushRules.PreventSecrets)
	assert.False(t, *explicitCfg.Policies.PushRules.PreventSecrets)
	require.NotNil(t, explicitCfg.Policies.PushRules.MaxFileSize)
	assert.Equal(t, 0, *explicitCfg.Policies.PushRules.MaxFileSize)

	// 2. Omitted values remain nil
	omittedData := []byte(`
version: "v1"
`)
	var omittedCfg config.PolicyConfig
	err = yaml.Unmarshal(omittedData, &omittedCfg)
	require.NoError(t, err)

	assert.Nil(t, omittedCfg.Settings.DryRun)
	assert.Nil(t, omittedCfg.Policies.PushRules)
}

func TestModel_HelperMethods(t *testing.T) {
	// 1. PipelineRetention Seconds()
	retention := config.PipelineRetentionConfig{RetentionDays: 30}
	assert.Equal(t, 30*86400, retention.Seconds())

	// 2. Variable CompositeKey()
	v1 := config.VariableConfig{Key: "AWS_REGION", EnvironmentScope: "production"}
	assert.Equal(t, "AWS_REGION::production", v1.CompositeKey())

	v2 := config.VariableConfig{Key: "API_KEY"}
	assert.Equal(t, "API_KEY::*", v2.CompositeKey())

	// 3. SetDefaults
	var cfg config.PolicyConfig
	cfg.SetDefaults()

	require.NotNil(t, cfg.Settings.DryRun)
	assert.True(t, *cfg.Settings.DryRun)
	assert.Equal(t, 10, cfg.Settings.Concurrency)
	assert.Equal(t, "info", cfg.Settings.LogLevel)
	assert.Equal(t, "text", cfg.Settings.LogFormat)
	assert.Equal(t, "table", cfg.Settings.ReportFormat)
	assert.Equal(t, "https://gitlab.com/api/v4", cfg.Settings.GitLab.BaseURL)
	assert.Equal(t, "private_token", cfg.Settings.GitLab.TokenType)
	assert.Equal(t, 30, cfg.Settings.GitLab.TimeoutSeconds)
	assert.Equal(t, 30.0, cfg.Settings.GitLab.RateLimitRPS)
	assert.Equal(t, 50, cfg.Settings.GitLab.RateLimitBurst)
	assert.Equal(t, 3, cfg.Settings.GitLab.MaxRetries)
	assert.Equal(t, 500, cfg.Settings.GitLab.RetryBaseDelayMs)
	assert.Equal(t, 30000, cfg.Settings.GitLab.RetryMaxDelayMs)
}
