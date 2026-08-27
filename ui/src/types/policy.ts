export interface GitLabSettings {
  base_url?: string;
  token?: string;
  rate_limit_rps?: number;
  rate_limit_burst?: number;
  max_retries?: number;
  retry_base_delay_ms?: number;
}

export interface SettingsConfig {
  dry_run?: boolean;
  concurrency?: number;
  log_level?: 'debug' | 'info' | 'warn' | 'error';
  log_format?: 'text' | 'json';
  report_format?: 'table' | 'summary' | 'json' | 'csv' | 'markdown';
  gitlab?: GitLabSettings;
}

export interface GroupSelector {
  group_ids_include?: number[];
  group_ids_exclude?: number[];
  group_paths_include?: string[];
  group_paths_exclude?: string[];
  recursive?: boolean;
}

export interface ProjectSelector {
  namespaces_include?: string[];
  namespaces_exclude?: string[];
  project_name_regex_include?: string;
  project_name_regex_exclude?: string;
  visibility?: 'public' | 'internal' | 'private' | 'all';
  archived?: boolean;
  id_range?: {
    min?: number;
    max?: number;
  };
}

export interface TargetSelectors {
  group_selector?: GroupSelector;
  project_selector?: ProjectSelector;
}

export interface PushRulesConfig {
  author_email_regex?: string;
  branch_name_regex?: string;
  commit_message_regex?: string;
  file_name_regex?: string;
  max_file_size?: number;
  prevent_secrets?: boolean;
  reject_unsigned_commits?: boolean;
  commit_committer_check?: boolean;
  member_check?: boolean;
  deny_delete_tag?: boolean;
}

export interface AccessRule {
  access_level: number;
}

export interface ProtectedBranchConfig {
  name: string;
  allowed_to_push?: AccessRule[];
  allowed_to_merge?: AccessRule[];
  allowed_to_unprotect?: AccessRule[];
  allow_force_push?: boolean;
  code_owner_approval_required?: boolean;
}

export interface ApprovalRuleItem {
  name: string;
  approvals_required: number;
  user_usernames?: string[];
  protected_branch_names?: string[];
}

export interface ApprovalSettingsConfig {
  allow_author_approval?: boolean;
  allow_committer_approval?: boolean;
  allow_overrides_to_approver_list_per_merge_request?: boolean;
  retain_approvals_on_push?: boolean;
}

export interface ApprovalRulesConfig {
  settings?: ApprovalSettingsConfig;
  rules?: ApprovalRuleItem[];
}

export interface ProjectSettingsConfig {
  default_branch?: string;
  squash_option?: 'always' | 'never' | 'default_on' | 'default_off';
  merge_method?: 'merge' | 'rebase_merge' | 'ff';
  only_allow_merge_if_pipeline_succeeds?: boolean;
  only_allow_merge_if_all_discussions_are_resolved?: boolean;
  keep_latest_artifact?: boolean;
  printing_merge_request_link_enabled?: boolean;
}

export interface PipelineRetentionConfig {
  retention_days: number;
}

export interface VariableItem {
  key: string;
  value: string;
  variable_type?: 'env_var' | 'file';
  protected?: boolean;
  masked?: boolean;
  raw?: boolean;
  environment_scope?: string;
}

export interface RunnerItem {
  id: number;
  paused?: boolean;
  locked?: boolean;
  tag_list?: string[];
  access_level?: 'not_protected' | 'ref_protected';
}

export interface ComplianceConfig {
  framework_name: string;
  prune?: boolean;
}

export interface WebhookItem {
  url: string;
  push_events?: boolean;
  merge_requests_events?: boolean;
  enable_ssl_verification?: boolean;
  secret_token?: string;
}

export interface MemberItem {
  username: string;
  access_level: number;
}

export interface MembersConfig {
  max_access_level?: number;
  enforce_expires_at?: boolean;
  max_expiration_days?: number;
  denied_members?: string[];
  allowed_members?: MemberItem[];
}

export interface PolicyModules {
  push_rules?: PushRulesConfig;
  protected_branches?: ProtectedBranchConfig[];
  approval_rules?: ApprovalRulesConfig;
  project_settings?: ProjectSettingsConfig;
  pipeline_retention?: PipelineRetentionConfig;
  variables?: VariableItem[];
  runners?: RunnerItem[];
  compliance?: ComplianceConfig;
  webhooks?: WebhookItem[];
  members?: MembersConfig;
}

export interface PolicyConfig {
  version: string;
  settings: SettingsConfig;
  targets: TargetSelectors;
  policies: PolicyModules;
}
