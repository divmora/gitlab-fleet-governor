package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// TestAdversarial_InvalidRegexPatterns verifies that all regex fields across the entire configuration
// schema properly reject invalid, uncompilable regular expressions.
func TestAdversarial_InvalidRegexPatterns(t *testing.T) {
	invalidRegexes := []string{
		"[a-z(",
		"*main",
		"(?P<invalid",
		"[0-9++",
		"\\",
		"(?<=unsupported_lookbehind)",
	}

	for _, badRegex := range invalidRegexes {
		badRegex := badRegex

		t.Run("ProjectSelector Include Regex: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						ProjectNameRegexInclude: badRegex,
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			valErrs, ok := err.(config.ValidationErrors)
			require.True(t, ok)
			assert.Contains(t, err.Error(), "targets.project_selector.project_name_regex_include")
			assert.Contains(t, err.Error(), "invalid regular expression")
			assert.Len(t, valErrs.Errors(), 1)
		})

		t.Run("ProjectSelector Exclude Regex: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						ProjectNameRegexExclude: badRegex,
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "targets.project_selector.project_name_regex_exclude")
		})

		t.Run("PushRules Author Email Regex: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PushRules: &config.PushRulesConfig{
						AuthorEmailRegex: badRegex,
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "policies.push_rules.author_email_regex")
		})

		t.Run("PushRules Branch Name Regex: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PushRules: &config.PushRulesConfig{
						BranchNameRegex: badRegex,
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "policies.push_rules.branch_name_regex")
		})

		t.Run("PushRules Commit Message Regex: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PushRules: &config.PushRulesConfig{
						CommitMessageRegex: badRegex,
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "policies.push_rules.commit_message_regex")
		})

		t.Run("PushRules Commit Message Negative Regex: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PushRules: &config.PushRulesConfig{
						CommitMessageNegativeRegex: badRegex,
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "policies.push_rules.commit_message_negative_regex")
		})

		t.Run("PushRules File Name Regex: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					PushRules: &config.PushRulesConfig{
						FileNameRegex: badRegex,
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "policies.push_rules.file_name_regex")
		})

		t.Run("Container Expiration Name Regex: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
							NameRegex: badRegex,
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "policies.project_settings.container_expiration_policy.name_regex")
		})

		t.Run("Container Expiration Name Regex Delete: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
							NameRegexDelete: badRegex,
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "policies.project_settings.container_expiration_policy.name_regex_delete")
		})

		t.Run("Container Expiration Name Regex Keep: "+badRegex, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
							NameRegexKeep: badRegex,
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "policies.project_settings.container_expiration_policy.name_regex_keep")
		})
	}
}

// TestAdversarial_BranchProtectionAccessLevels verifies that only valid GitLab branch protection
// access levels (0, 30, 40, 60) are accepted, and all others (including member-only levels 10, 20) are rejected.
func TestAdversarial_BranchProtectionAccessLevels(t *testing.T) {
	invalidLevels := []int{-1, 10, 20, 25, 50, 70, 100}

	for _, level := range invalidLevels {
		level := level

		t.Run(fmt.Sprintf("Invalid AllowedToPush Level %d", level), func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProtectedBranches: []config.ProtectedBranchRuleConfig{
						{
							Name: "main",
							AllowedToPush: []config.BranchAccessDescription{
								{AccessLevel: level},
							},
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("invalid access level %d (must be 0, 30, 40, or 60)", level))
		})

		t.Run(fmt.Sprintf("Invalid AllowedToMerge Level %d", level), func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProtectedBranches: []config.ProtectedBranchRuleConfig{
						{
							Name: "main",
							AllowedToMerge: []config.BranchAccessDescription{
								{AccessLevel: level},
							},
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("invalid access level %d (must be 0, 30, 40, or 60)", level))
		})

		t.Run(fmt.Sprintf("Invalid AllowedToUnprotect Level %d", level), func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProtectedBranches: []config.ProtectedBranchRuleConfig{
						{
							Name: "main",
							AllowedToUnprotect: []config.BranchAccessDescription{
								{AccessLevel: level},
							},
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("invalid access level %d (must be 0, 30, 40, or 60)", level))
		})
	}

	validLevels := []int{0, 30, 40, 60}
	for _, level := range validLevels {
		level := level
		t.Run(fmt.Sprintf("Valid Level %d", level), func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProtectedBranches: []config.ProtectedBranchRuleConfig{
						{
							Name: "main",
							AllowedToPush: []config.BranchAccessDescription{
								{AccessLevel: level},
							},
							AllowedToMerge: []config.BranchAccessDescription{
								{AccessLevel: level},
							},
							AllowedToUnprotect: []config.BranchAccessDescription{
								{AccessLevel: level},
							},
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.NoError(t, err)
		})
	}
}

// TestAdversarial_EnumTyposAndInvalidValues verifies that all enum fields reject typos, misspellings,
// and unauthorized values.
func TestAdversarial_EnumTyposAndInvalidValues(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *config.PolicyConfig
		expectedField string
		expectedError string
	}{
		{
			name: "SquashOption Typo: 'Always' instead of 'always'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						SquashOption: "Always",
					},
				},
			},
			expectedField: "policies.project_settings.squash_option",
			expectedError: "invalid squash_option 'Always'",
		},
		{
			name: "SquashOption Invalid: 'sometimes'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						SquashOption: "sometimes",
					},
				},
			},
			expectedField: "policies.project_settings.squash_option",
			expectedError: "invalid squash_option 'sometimes'",
		},
		{
			name: "MergeMethod Typo: 'fast-forward' instead of 'ff'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						MergeMethod: "fast-forward",
					},
				},
			},
			expectedField: "policies.project_settings.merge_method",
			expectedError: "invalid merge_method 'fast-forward'",
		},
		{
			name: "MergeMethod Invalid: 'squash_merge'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						MergeMethod: "squash_merge",
					},
				},
			},
			expectedField: "policies.project_settings.merge_method",
			expectedError: "invalid merge_method 'squash_merge'",
		},
		{
			name: "Visibility Invalid: 'hidden'",
			cfg: &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						Visibility: "hidden",
					},
				},
			},
			expectedField: "targets.project_selector.visibility",
			expectedError: "invalid visibility 'hidden'",
		},
		{
			name: "Visibility Typo: 'restricted'",
			cfg: &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						Visibility: "restricted",
					},
				},
			},
			expectedField: "targets.project_selector.visibility",
			expectedError: "invalid visibility 'restricted'",
		},
		{
			name: "LogLevel Invalid: 'trace'",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					LogLevel: "trace",
				},
			},
			expectedField: "settings.log_level",
			expectedError: "invalid log_level 'trace'",
		},
		{
			name: "LogFormat Invalid: 'yaml'",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					LogFormat: "yaml",
				},
			},
			expectedField: "settings.log_format",
			expectedError: "invalid log_format 'yaml'",
		},
		{
			name: "ReportFormat Invalid: 'html'",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					ReportFormat: "html",
				},
			},
			expectedField: "settings.report_format",
			expectedError: "invalid report_format 'html'",
		},
		{
			name: "GitLab TokenType Invalid: 'bearer'",
			cfg: &config.PolicyConfig{
				Settings: config.SettingsConfig{
					GitLab: config.GitLabSettingsConfig{
						TokenType: "bearer",
					},
				},
			},
			expectedField: "settings.gitlab.token_type",
			expectedError: "invalid token_type 'bearer'",
		},
		{
			name: "VariableType Invalid: 'secret'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Variables: []config.VariableConfig{
						{
							Key:          "DB_PASS",
							Value:        "Sup3rS3cr3tVal",
							VariableType: "secret",
						},
					},
				},
			},
			expectedField: "policies.variables[0].variable_type",
			expectedError: "invalid variable_type 'secret'",
		},
		{
			name: "Runner AccessLevel Invalid: 'admin'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Runners: &config.RunnersConfig{
						Runners: []config.RunnerConfig{
							{
								AccessLevel: "admin",
							},
						},
					},
				},
			},
			expectedField: "policies.runners.runners[0].access_level",
			expectedError: "invalid access_level 'admin'",
		},
		{
			name: "ApprovalRule RuleType Invalid: 'mandatory'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ApprovalRules: &config.ApprovalRulesConfig{
						Rules: []config.ApprovalRuleConfig{
							{
								Name:              "Rule1",
								ApprovalsRequired: 1,
								RuleType:          "mandatory",
								UserUsernames:     []string{"alice"},
							},
						},
					},
				},
			},
			expectedField: "policies.approval_rules.rules[0].rule_type",
			expectedError: "invalid rule_type 'mandatory'",
		},
		{
			name: "AutoCancelPendingPipelines Invalid: 'true'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						AutoCancelPendingPipelines: "true",
					},
				},
			},
			expectedField: "policies.project_settings.auto_cancel_pending_pipelines",
			expectedError: "invalid auto_cancel_pending_pipelines 'true'",
		},
		{
			name: "ContainerExpiration Cadence Invalid: '2d'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
							Cadence: "2d",
						},
					},
				},
			},
			expectedField: "policies.project_settings.container_expiration_policy.cadence",
			expectedError: "invalid cadence '2d'",
		},
		{
			name: "ContainerExpiration OlderThan Invalid: '1d'",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
							OlderThan: "1d",
						},
					},
				},
			},
			expectedField: "policies.project_settings.container_expiration_policy.older_than",
			expectedError: "invalid older_than '1d'",
		},
		{
			name: "ContainerExpiration KeepN Invalid: 3",
			cfg: &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					ProjectSettings: &config.ProjectSettingsConfig{
						ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
							KeepN: intPtr(3),
						},
					},
				},
			},
			expectedField: "policies.project_settings.container_expiration_policy.keep_n",
			expectedError: "invalid keep_n 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := config.Validate(tt.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedField)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

// TestAdversarial_RetentionDaysAndNumericBoundaries tests negative and edge numeric constraints.
func TestAdversarial_RetentionDaysAndNumericBoundaries(t *testing.T) {
	t.Run("Negative Pipeline Retention Days -1", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: -1,
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "policies.pipeline_retention.retention_days: retention_days must be non-negative (got -1)")
	})

	t.Run("Negative Pipeline Retention Days -365", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: -365,
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "policies.pipeline_retention.retention_days: retention_days must be non-negative (got -365)")
	})

	t.Run("Zero Pipeline Retention Days (Valid)", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: 0,
				},
			},
		}
		err := config.Validate(cfg)
		require.NoError(t, err)
		assert.Equal(t, 0, cfg.Policies.PipelineRetention.Seconds())
	})

	t.Run("Positive Pipeline Retention Days (Valid 30 days = 2592000s)", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: 30,
				},
			},
		}
		err := config.Validate(cfg)
		require.NoError(t, err)
		assert.Equal(t, 2592000, cfg.Policies.PipelineRetention.Seconds())
	})

	t.Run("Negative Concurrency", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Settings: config.SettingsConfig{
				Concurrency: -5,
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "settings.concurrency: concurrency must be at least 1 (got -5)")
	})

	t.Run("Negative GitLab Timeout", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Settings: config.SettingsConfig{
				GitLab: config.GitLabSettingsConfig{
					TimeoutSeconds: -1,
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "settings.gitlab.timeout_seconds: timeout_seconds must be non-negative")
	})

	t.Run("Negative GitLab MaxRetries", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Settings: config.SettingsConfig{
				GitLab: config.GitLabSettingsConfig{
					MaxRetries: -1,
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "settings.gitlab.max_retries: max_retries must be non-negative")
	})

	t.Run("Negative Push Rule Max File Size", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					MaxFileSize: intPtr(-1),
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "policies.push_rules.max_file_size: max_file_size must be non-negative")
	})

	t.Run("Negative Runner Maximum Timeout", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Runners: &config.RunnersConfig{
					Runners: []config.RunnerConfig{
						{
							MaximumTimeout: intPtr(-10),
						},
					},
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "policies.runners.runners[0].maximum_timeout: maximum_timeout must be non-negative")
	})

	t.Run("Zero or Negative Member Max Expiration Days", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Members: &config.MembersConfig{
					MaxExpirationDays: intPtr(0),
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "policies.members.max_expiration_days: max_expiration_days must be positive (got 0)")
	})
}

// TestAdversarial_MaskedVariablesRules tests all masking rules including length, character sets,
// whitespace, newlines, disallowed symbols, and secret redaction in error messages.
func TestAdversarial_MaskedVariablesRules(t *testing.T) {
	shortValues := []string{
		"",
		"a",
		"12",
		"abc",
		"1234",
		"12345",
		"123456",
		"1234567",
	}

	for _, shortVal := range shortValues {
		shortVal := shortVal
		t.Run(fmt.Sprintf("Masked value length %d (< 8)", len(shortVal)), func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Variables: []config.VariableConfig{
						{
							Key:    "SECRET_VAR",
							Value:  shortVal,
							Masked: boolPtr(true),
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("masked CI/CD variable value must be at least 8 characters long (got %d characters)", len(shortVal)))
			if len(shortVal) >= 3 {
				assert.NotContains(t, err.Error(), shortVal)
			}
			assert.Contains(t, err.Error(), "[REDACTED]")
		})
	}

	invalidCharValues := []struct {
		name  string
		value string
	}{
		{name: "With spaces", value: "secret token 123"},
		{name: "Leading space", value: " secrettoken123"},
		{name: "Trailing space", value: "secrettoken123 "},
		{name: "With newline", value: "secrettoken\n123"},
		{name: "With carriage return", value: "secrettoken\r123"},
		{name: "With tab", value: "secrettoken\t123"},
		{name: "With exclamation", value: "secrettoken123!"},
		{name: "With dollar", value: "secrettoken123$"},
		{name: "With hash", value: "secrettoken123#"},
		{name: "With percent", value: "secrettoken123%"},
		{name: "With ampersand", value: "secrettoken123&"},
		{name: "With asterisk", value: "secrettoken123*"},
		{name: "With parentheses", value: "secrettoken123()"},
	}

	for _, tt := range invalidCharValues {
		tt := tt
		t.Run("Disallowed character: "+tt.name, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Variables: []config.VariableConfig{
						{
							Key:    "API_SECRET",
							Value:  tt.value,
							Masked: boolPtr(true),
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "masked CI/CD variable value contains invalid characters; must match '^[a-zA-Z0-9_+=/@:~.-]+$' without spaces or newlines")
			// Secret must be redacted
			if len(tt.value) > 3 {
				assert.NotContains(t, err.Error(), tt.value)
			}
			assert.Contains(t, err.Error(), "[REDACTED]")
		})
	}

	validMaskedValues := []string{
		"Sup3rS3cr3t",
		"aB1_+=/@:~.-",
		"dGhpc19pc19hX2Jhc2U2NF9zZWNyZXQ=",
		"AKIAIOSFODNN7EXAMPLE",
		"dummy-valid-secret-token",
		"https://vault.corp:8200/v1/secret",
	}

	for _, validVal := range validMaskedValues {
		validVal := validVal
		t.Run("Valid masked value: "+validVal, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Policies: config.PoliciesConfig{
					Variables: []config.VariableConfig{
						{
							Key:    "VALID_SECRET",
							Value:  validVal,
							Masked: boolPtr(true),
						},
					},
				},
			}
			err := config.Validate(cfg)
			require.NoError(t, err)
		})
	}

	t.Run("Unmasked variable with spaces and special chars is valid", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{
						Key:    "UNMASKED_TEXT",
						Value:  "This is a normal unmasked string with spaces, !@#$%^&*()",
						Masked: boolPtr(false),
					},
				},
			},
		}
		err := config.Validate(cfg)
		require.NoError(t, err)
	})
}

// TestAdversarial_DuplicateScopedVariables tests composite key uniqueness (key, environment_scope).
func TestAdversarial_DuplicateScopedVariables(t *testing.T) {
	t.Run("Duplicate in same explicit scope", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{Key: "DB_HOST", Value: "host-prod-1.internal", EnvironmentScope: "production"},
					{Key: "DB_HOST", Value: "host-prod-2.internal", EnvironmentScope: "production"},
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate variable key 'DB_HOST' for environment scope 'production'")
	})

	t.Run("Duplicate between empty scope and wildcard scope '*'", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{Key: "GLOBAL_API", Value: "val1", EnvironmentScope: ""},
					{Key: "GLOBAL_API", Value: "val2", EnvironmentScope: "*"},
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate variable key 'GLOBAL_API' for environment scope '*'")
	})

	t.Run("Duplicate with both wildcard scopes '*'", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{Key: "SERVICE_PORT", Value: "8080", EnvironmentScope: "*"},
					{Key: "SERVICE_PORT", Value: "9090", EnvironmentScope: "*"},
				},
			},
		}
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate variable key 'SERVICE_PORT' for environment scope '*'")
	})

	t.Run("Same key in different scopes is valid", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{Key: "DATABASE_URL", Value: "postgres://dev:5432/db", EnvironmentScope: "development"},
					{Key: "DATABASE_URL", Value: "postgres://staging:5432/db", EnvironmentScope: "staging"},
					{Key: "DATABASE_URL", Value: "postgres://prod:5432/db", EnvironmentScope: "production"},
					{Key: "DATABASE_URL", Value: "postgres://default:5432/db", EnvironmentScope: "*"},
				},
			},
		}
		err := config.Validate(cfg)
		require.NoError(t, err)
	})

	t.Run("Different keys in same scope is valid", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{Key: "DATABASE_HOST", Value: "db.corp", EnvironmentScope: "production"},
					{Key: "DATABASE_USER", Value: "admin", EnvironmentScope: "production"},
					{Key: "DATABASE_PORT", Value: "5432", EnvironmentScope: "production"},
				},
			},
		}
		err := config.Validate(cfg)
		require.NoError(t, err)
	})
}

// TestAdversarial_FullErrorAggregation tests that Validate collects and aggregates ALL errors
// across all configuration sections rather than failing early.
func TestAdversarial_FullErrorAggregation(t *testing.T) {
	cfg := &config.PolicyConfig{
		Settings: config.SettingsConfig{
			Concurrency:  -1,        // Error 1
			LogLevel:     "invalid", // Error 2
			LogFormat:    "invalid", // Error 3
			ReportFormat: "invalid", // Error 4
			GitLab: config.GitLabSettingsConfig{
				TokenType:        "invalid", // Error 5
				TimeoutSeconds:   -5,        // Error 6
				RetryBaseDelayMs: 5000,
				RetryMaxDelayMs:  1000, // Error 7 (base > max)
			},
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude:   []int{-10},               // Error 8
				GroupPathsInclude: []string{"/bad/prefix/"}, // Error 9
			},
			ProjectSelector: &config.ProjectSelector{
				ProjectNameRegexInclude: "[invalid-regex(", // Error 10
				Visibility:              "top_secret",      // Error 11
				IDRange: &config.IDRange{
					Min: 100,
					Max: 50, // Error 12 (min > max)
				},
			},
		},
		Policies: config.PoliciesConfig{
			PushRules: &config.PushRulesConfig{
				BranchNameRegex: "*bad-branch-regex", // Error 13
				MaxFileSize:     intPtr(-50),         // Error 14
			},
			ProtectedBranches: []config.ProtectedBranchRuleConfig{
				{
					Name: "", // Error 15
					AllowedToPush: []config.BranchAccessDescription{
						{AccessLevel: 99}, // Error 16
					},
				},
			},
			ApprovalRules: &config.ApprovalRulesConfig{
				Rules: []config.ApprovalRuleConfig{
					{
						Name:              "",  // Error 17
						ApprovalsRequired: 0,   // Error 18
						RuleType:          "x", // Error 19
					},
				},
			},
			ProjectSettings: &config.ProjectSettingsConfig{
				SquashOption: "bad_squash", // Error 20
				MergeMethod:  "bad_merge",  // Error 21
				ContainerExpirationPolicy: &config.ContainerExpirationPolicyConfig{
					Cadence:   "bad_cadence", // Error 22
					OlderThan: "bad_age",     // Error 23
					KeepN:     intPtr(999),   // Error 24
					NameRegex: "(?P<bad",     // Error 25
				},
			},
			PipelineRetention: &config.PipelineRetentionConfig{
				RetentionDays: -100, // Error 26
			},
			Variables: []config.VariableConfig{
				{
					Key:    "INVALID-KEY!", // Error 27
					Value:  "short",
					Masked: boolPtr(true), // Error 28
				},
				{
					Key:              "DUP_KEY",
					Value:            "Sup3rS3cr3t123",
					EnvironmentScope: "prod",
				},
				{
					Key:              "DUP_KEY",
					Value:            "Sup3rS3cr3t456",
					EnvironmentScope: "prod", // Error 29
				},
			},
			Runners: &config.RunnersConfig{
				Runners: []config.RunnerConfig{
					{
						AccessLevel:    "invalid_runner_level", // Error 30
						MaximumTimeout: intPtr(-1),             // Error 31
					},
				},
			},
			Compliance: &config.ComplianceConfig{
				// Error 32: both FrameworkName and FrameworkID missing
			},
			Webhooks: []config.WebhookConfig{
				{
					URL: "ftp://bad-scheme.com", // Error 33
				},
			},
			Members: &config.MembersConfig{
				MinAccessLevel:    intPtr(40),
				MaxAccessLevel:    intPtr(20), // Error 34 (min > max)
				MaxExpirationDays: intPtr(-1), // Error 35
				AllowedMembers: []config.MemberRuleConfig{
					{
						Username:    "",           // Error 36
						AccessLevel: 75,           // Error 37
						ExpiresAt:   "invalid-dt", // Error 38
					},
				},
			},
		},
	}

	err := config.Validate(cfg)
	require.Error(t, err)

	valErrs, ok := err.(config.ValidationErrors)
	require.True(t, ok, "Error must be of type config.ValidationErrors")

	t.Logf("Total aggregated validation errors captured: %d", len(valErrs.Errors()))
	assert.GreaterOrEqual(t, len(valErrs.Errors()), 30, "Must aggregate 30+ distinct errors across all config subtrees")

	errStr := err.Error()
	assert.True(t, strings.HasPrefix(errStr, fmt.Sprintf("configuration validation failed with %d error(s):", len(valErrs.Errors()))))

	// Verify specific errors from each subtree are present in the aggregated output
	assert.Contains(t, errStr, "settings.concurrency")
	assert.Contains(t, errStr, "settings.log_level")
	assert.Contains(t, errStr, "settings.gitlab.token_type")
	assert.Contains(t, errStr, "targets.group_selector.group_ids_include[0]")
	assert.Contains(t, errStr, "targets.project_selector.project_name_regex_include")
	assert.Contains(t, errStr, "policies.push_rules.branch_name_regex")
	assert.Contains(t, errStr, "policies.protected_branches[0].name")
	assert.Contains(t, errStr, "policies.approval_rules.rules[0].approvals_required")
	assert.Contains(t, errStr, "policies.project_settings.squash_option")
	assert.Contains(t, errStr, "policies.pipeline_retention.retention_days")
	assert.Contains(t, errStr, "policies.variables[0].key")
	assert.Contains(t, errStr, "duplicate variable key 'DUP_KEY'")
	assert.Contains(t, errStr, "policies.runners.runners[0].access_level")
	assert.Contains(t, errStr, "policies.compliance")
	assert.Contains(t, errStr, "policies.webhooks[0].url")
	assert.Contains(t, errStr, "min_access_level (40) cannot be greater than max_access_level (20)")
}
