package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func TestValidate_ValidConfigs(t *testing.T) {
	t.Run("Valid Complete Configuration", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Version: "v1",
			Settings: config.SettingsConfig{
				DryRun:       boolPtr(true),
				Concurrency:  10,
				LogLevel:     "info",
				LogFormat:    "text",
				ReportFormat: "table",
				GitLab: config.GitLabSettingsConfig{
					BaseURL:          "https://gitlab.com/api/v4",
					TokenType:        "private_token",
					TimeoutSeconds:   30,
					RateLimitRPS:     30.0,
					RateLimitBurst:   50,
					MaxRetries:       3,
					RetryBaseDelayMs: 500,
					RetryMaxDelayMs:  30000,
				},
			},
			Targets: config.TargetSelectors{
				GroupSelector: &config.GroupSelector{
					GroupIDsInclude:   []int{101, 102},
					GroupPathsInclude: []string{"engineering/backend"},
					Recursive:         boolPtr(true),
				},
				ProjectSelector: &config.ProjectSelector{
					NamespacesInclude:       []string{"engineering"},
					ProjectNameRegexInclude: "^(api|service)-.*",
					Visibility:              "private",
					IDRange: &config.IDRange{
						Min: 1,
						Max: 5000,
					},
				},
			},
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					BranchNameRegex:    "^(main|release/.*)$",
					CommitMessageRegex: "^(feat|fix|chore): .+",
					AuthorEmailRegex:   ".+@company\\.com$",
					FileNameRegex:      "\\.(exe|dll)$",
					MaxFileSize:        intPtr(25),
					PreventSecrets:     boolPtr(true),
				},
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "main",
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 0},
						},
						AllowedToMerge: []config.BranchAccessDescription{
							{AccessLevel: 40},
						},
						AllowedToUnprotect: []config.BranchAccessDescription{
							{AccessLevel: 60},
						},
						AllowForcePush:            boolPtr(false),
						CodeOwnerApprovalRequired: boolPtr(true),
					},
				},
				ProjectSettings: &config.ProjectSettingsConfig{
					SquashOption: "always",
					MergeMethod:  "rebase_merge",
					OnlyAllowMergeIfPipelineSucceeds: boolPtr(true),
					ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
						Cadence:   "7d",
						OlderThan: "30d",
						KeepN:     intPtr(10),
						NameRegex: ".*-release",
					},
				},
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: 30,
				},
				Variables: []config.VariableConfig{
					{
						Key:              "DATABASE_PASSWORD",
						Value:            "Sup3rS3cr3tP@ss_123",
						Masked:           boolPtr(true),
						Protected:        boolPtr(true),
						EnvironmentScope: "production",
					},
				},
				ApprovalRules: &config.ApprovalRulesConfig{
					Rules: []config.ApprovalRuleConfig{
						{
							Name:              "Security Review",
							ApprovalsRequired: 2,
							UserUsernames:     []string{"sec-lead", "sec-auditor"},
						},
					},
				},
				Runners: &config.RunnersConfig{
					Runners: []config.RunnerConfig{
						{
							AccessLevel:    "ref_protected",
							MaximumTimeout: intPtr(3600),
						},
					},
				},
				Compliance: &config.ComplianceConfig{
					FrameworkName: "SOC2",
				},
				Webhooks: []config.WebhookConfig{
					{
						URL:         "https://audit.corp/webhook",
						PushEvents:  boolPtr(true),
						SecretToken: "secret123",
					},
				},
				Members: &config.MembersConfig{
					MinAccessLevel:    intPtr(20),
					MaxAccessLevel:    intPtr(40),
					MaxExpirationDays: intPtr(90),
					AllowedMembers: []config.MemberRuleConfig{
						{
							Username:    "lead-dev",
							AccessLevel: 40,
							ExpiresAt:   "2026-12-31",
						},
					},
				},
			},
		}

		err := config.Validate(cfg)
		require.NoError(t, err)
	})

	t.Run("Valid Minimal Configuration", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Version: "v1",
		}
		err := cfg.Validate()
		require.NoError(t, err)
	})
}

func TestValidate_SemanticErrors(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.PolicyConfig
		expectedErr string
	}{
		{
			name:        "Nil config",
			cfg:         nil,
			expectedErr: "policy config cannot be nil",
		},
		{
			name: "Negative concurrency",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					Concurrency: -1,
				},
			},
			expectedErr: "settings.concurrency",
		},
		{
			name: "Invalid log level",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					LogLevel: "verbose",
				},
			},
			expectedErr: "settings.log_level",
		},
		{
			name: "Invalid log format",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					LogFormat: "xml",
				},
			},
			expectedErr: "settings.log_format",
		},
		{
			name: "Invalid report format",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					ReportFormat: "html",
				},
			},
			expectedErr: "settings.report_format",
		},
		{
			name: "Invalid GitLab token type",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					GitLab: config.GitLabSettingsConfig{
						TokenType: "basic_auth",
					},
				},
			},
			expectedErr: "settings.gitlab.token_type",
		},
		{
			name: "GitLab retry base delay greater than max delay",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					GitLab: config.GitLabSettingsConfig{
						RetryBaseDelayMs: 5000,
						RetryMaxDelayMs:  1000,
					},
				},
			},
			expectedErr: "retry_base_delay_ms (5000) cannot be greater than retry_max_delay_ms (1000)",
		},
		{
			name: "Invalid group selector negative ID",
			cfg: &config.PolicyConfig{
				Targets: config.TargetSelectors{
					GroupSelector: &config.GroupSelector{
						GroupIDsInclude: []int{-5},
					},
				},
			},
			expectedErr: "group_ids_include[0]",
		},
		{
			name: "Invalid group selector path with leading slash",
			cfg: &config.PolicyConfig{
				Targets: config.TargetSelectors{
					GroupSelector: &config.GroupSelector{
						GroupPathsInclude: []string{"/invalid/group/path"},
					},
				},
			},
			expectedErr: "group_paths_include[0]",
		},
		{
			name: "Invalid project selector regex include",
			cfg: &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						ProjectNameRegexInclude: "[a-z(",
					},
				},
			},
			expectedErr: "project_selector.project_name_regex_include",
		},
		{
			name: "Invalid project selector visibility",
			cfg: &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						Visibility: "secret",
					},
				},
			},
			expectedErr: "project_selector.visibility",
		},
		{
			name: "Invalid IDRange min > max",
			cfg: &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						IDRange: &config.IDRange{
							Min: 500,
							Max: 100,
						},
					},
				},
			},
			expectedErr: "min (500) cannot be greater than max (100)",
		},
		{
			name: "Invalid push rule branch regex",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PushRules: &config.PushRulesConfig{
						BranchNameRegex: "*main",
					},
				},
			},
			expectedErr: "push_rules.branch_name_regex",
		},
		{
			name: "Negative max file size",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PushRules: &config.PushRulesConfig{
						MaxFileSize: intPtr(-10),
					},
				},
			},
			expectedErr: "push_rules.max_file_size",
		},
		{
			name: "Empty protected branch name",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProtectedBranches: []config.ProtectedBranchRuleConfig{
						{Name: ""},
					},
				},
			},
			expectedErr: "protected_branches[0].name",
		},
		{
			name: "Invalid protected branch access level (25)",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProtectedBranches: []config.ProtectedBranchRuleConfig{
						{
							Name: "main",
							AllowedToPush: []config.BranchAccessDescription{
								{AccessLevel: 25},
							},
						},
					},
				},
			},
			expectedErr: "protected_branches[0].allowed_to_push[0].access_level",
		},
		{
			name: "Approval rule missing approvers",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ApprovalRules: &config.ApprovalRulesConfig{
						Rules: []config.ApprovalRuleConfig{
							{
								Name:              "Rule without approvers",
								ApprovalsRequired: 1,
							},
						},
					},
				},
			},
			expectedErr: "requires at least one user or group approver",
		},
		{
			name: "Approval rule with 0 approvals required",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ApprovalRules: &config.ApprovalRulesConfig{
						Rules: []config.ApprovalRuleConfig{
							{
								Name:              "Zero Approvals",
								ApprovalsRequired: 0,
								UserUsernames:     []string{"alice"},
							},
						},
					},
				},
			},
			expectedErr: "approvals_required",
		},
		{
			name: "Invalid squash option",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						SquashOption: "sometimes",
					},
				},
			},
			expectedErr: "project_settings.squash_option",
		},
		{
			name: "Invalid merge method",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						MergeMethod: "git_squash",
					},
				},
			},
			expectedErr: "project_settings.merge_method",
		},
		{
			name: "Invalid container expiration keep_n",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
							KeepN: intPtr(3),
						},
					},
				},
			},
			expectedErr: "container_expiration_policy.keep_n",
		},
		{
			name: "Negative pipeline retention days",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PipelineRetention: &config.PipelineRetentionConfig{
						RetentionDays: -10,
					},
				},
			},
			expectedErr: "pipeline_retention.retention_days",
		},
		{
			name: "Invalid variable key with dashes",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Variables: []config.VariableConfig{
						{
							Key:   "MY-SECRET-KEY",
							Value: "secret_val_1234",
						},
					},
				},
			},
			expectedErr: "variables[0].key",
		},
		{
			name: "Masked variable value too short (< 8 chars)",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Variables: []config.VariableConfig{
						{
							Key:    "SECRET",
							Value:  "short",
							Masked: boolPtr(true),
						},
					},
				},
			},
			expectedErr: "must be at least 8 characters long",
		},
		{
			name: "Masked variable with invalid spaces",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Variables: []config.VariableConfig{
						{
							Key:    "SECRET",
							Value:  "value with spaces 123",
							Masked: boolPtr(true),
						},
					},
				},
			},
			expectedErr: "contains invalid characters",
		},
		{
			name: "Duplicate variable key and environment scope",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Variables: []config.VariableConfig{
						{
							Key:              "API_KEY",
							Value:            "val12345678",
							EnvironmentScope: "prod",
						},
						{
							Key:              "API_KEY",
							Value:            "val87654321",
							EnvironmentScope: "prod",
						},
					},
				},
			},
			expectedErr: "duplicate variable key 'API_KEY' for environment scope 'prod'",
		},
		{
			name: "Invalid runner access level",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Runners: &config.RunnersConfig{
						Runners: []config.RunnerConfig{
							{
								AccessLevel: "semi_protected",
							},
						},
					},
				},
			},
			expectedErr: "runners.runners[0].access_level",
		},
		{
			name: "Compliance missing framework name and ID",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Compliance: &config.ComplianceConfig{},
				},
			},
			expectedErr: "compliance configuration requires either framework_name or framework_id",
		},
		{
			name: "Invalid webhook URL format",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Webhooks: []config.WebhookConfig{
						{
							URL: "ftp://invalid-server.com/hook",
						},
					},
				},
			},
			expectedErr: "webhooks[0].url",
		},
		{
			name: "Invalid member access level (99)",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Members: &config.MembersConfig{
						AllowedMembers: []config.MemberRuleConfig{
							{
								Username:    "bob",
								AccessLevel: 99,
							},
						},
					},
				},
			},
			expectedErr: "members.allowed_members[0].access_level",
		},
		{
			name: "Member min_access_level greater than max_access_level",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Members: &config.MembersConfig{
						MinAccessLevel: intPtr(40),
						MaxAccessLevel: intPtr(20),
					},
				},
			},
			expectedErr: "min_access_level (40) cannot be greater than max_access_level (20)",
		},
		{
			name: "Invalid member expires_at date format",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Members: &config.MembersConfig{
						AllowedMembers: []config.MemberRuleConfig{
							{
								Username:    "alice",
								AccessLevel: 30,
								ExpiresAt:   "31-12-2026",
							},
						},
					},
				},
			},
			expectedErr: "invalid expires_at format '31-12-2026'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := config.Validate(tt.cfg)
			require.Error(t, err)

			valErrs, ok := err.(config.ValidationErrors)
			require.True(t, ok)
			assert.NotEmpty(t, valErrs.Errors())
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}
