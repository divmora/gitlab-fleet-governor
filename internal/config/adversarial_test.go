package config_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// MockS3Client for adversarial testing
type mockS3Client struct {
	getObjectFunc func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

func (m *mockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getObjectFunc != nil {
		return m.getObjectFunc(ctx, params, optFns...)
	}
	return nil, errors.New("unimplemented mock GetObject")
}

// ----------------------------------------------------------------------------
// 1. ExpandEnv Adversarial Tests
// ----------------------------------------------------------------------------

func TestAdversarial_ExpandEnv_DeepRecursionAndCircular(t *testing.T) {
	envMap := map[string]string{
		"LOOP_A":     "${LOOP_B}",
		"LOOP_B":     "${LOOP_A}",
		"SELF":       "${SELF}",
		"INDIRECT_1": "${INDIRECT_2}",
		"INDIRECT_2": "resolved_token",
	}
	lookup := func(k string) (string, bool) {
		v, ok := envMap[k]
		return v, ok
	}

	t.Run("Circular reference between two variables", func(t *testing.T) {
		_, err := config.ExpandEnvWithLookup("${LOOP_A}", lookup)
		assert.Error(t, err, "Circular reference must produce maximum recursion depth error")
		assert.Contains(t, err.Error(), "maximum recursion depth (32) exceeded")
	})

	t.Run("Self-referential variable expansion produces error", func(t *testing.T) {
		_, err := config.ExpandEnvWithLookup("${SELF}", lookup)
		assert.Error(t, err, "Self-reference must produce maximum recursion depth error")
		assert.Contains(t, err.Error(), "maximum recursion depth (32) exceeded")
	})

	t.Run("Multi-hop indirect variable expansion resolves correctly", func(t *testing.T) {
		res, err := config.ExpandEnvWithLookup("token: ${INDIRECT_1}", lookup)
		require.NoError(t, err)
		assert.Equal(t, "token: resolved_token", res)
	})

	t.Run("Deeply nested default values exceeding maxEnvSubstDepth (33 levels)", func(t *testing.T) {
		// Build ${UNSET_1:-${UNSET_2:-...${UNSET_35:-fallback}...}}
		var b strings.Builder
		depth := 35
		for i := 1; i <= depth; i++ {
			b.WriteString(fmt.Sprintf("${UNSET_%d:-", i))
		}
		b.WriteString("final_fallback")
		for i := 1; i <= depth; i++ {
			b.WriteString("}")
		}

		_, err := config.ExpandEnvWithLookup(b.String(), func(k string) (string, bool) { return "", false })
		assert.Error(t, err, "Exceeding 32 levels of nested defaults must return error")
		assert.Contains(t, err.Error(), "maximum recursion depth (32) exceeded")
	})

	t.Run("Nested default values exactly at 32 levels", func(t *testing.T) {
		var b strings.Builder
		depth := 32
		for i := 1; i <= depth; i++ {
			b.WriteString(fmt.Sprintf("${UNSET_%d:-", i))
		}
		b.WriteString("valid_value")
		for i := 1; i <= depth; i++ {
			b.WriteString("}")
		}

		res, err := config.ExpandEnvWithLookup(b.String(), func(k string) (string, bool) { return "", false })
		assert.NoError(t, err)
		assert.Equal(t, "valid_value", res)
	})
}

func TestAdversarial_ExpandEnv_UnclosedBracesAndMalformedTokens(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		errContains string
	}{
		{
			name:        "Unclosed opening brace at start",
			input:       "${VAR_NAME",
			errContains: "unterminated '${'",
		},
		{
			name:        "Unclosed opening brace with default",
			input:       "${VAR_NAME:-default_value",
			errContains: "unterminated '${'",
		},
		{
			name:        "Nested unclosed brace",
			input:       "${VAR:-${NESTED_UNCLOSED",
			errContains: "unterminated '${'",
		},
		{
			name:        "Empty token ${}",
			input:       "${}",
			errContains: "invalid environment variable name ''",
		},
		{
			name:        "Whitespace-only token ${   }",
			input:       "${   }",
			errContains: "invalid environment variable name ''",
		},
		{
			name:        "Invalid character in variable name (dash)",
			input:       "${VAR-NAME}",
			errContains: "invalid environment variable name 'VAR-NAME'",
		},
		{
			name:        "Invalid character in variable name (digit first)",
			input:       "${1VAR}",
			errContains: "invalid environment variable name '1VAR'",
		},
		{
			name:        "Invalid character in variable name (dot)",
			input:       "${VAR.NAME}",
			errContains: "invalid environment variable name 'VAR.NAME'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.ExpandEnvWithLookup(tc.input, func(k string) (string, bool) { return "", false })
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestAdversarial_ExpandEnv_EscapingAndRegexAnchors(t *testing.T) {
	lookup := func(k string) (string, bool) {
		if k == "BRANCH" {
			return "main", true
		}
		return "", false
	}

	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Regex ending anchor preserved",
			input:    "^feature/.*$",
			expected: "^feature/.*$",
		},
		{
			name:     "Escaped dollar in regex with branch substitution",
			input:    `^${BRANCH}\$` + "$",
			expected: `^main$$`,
		},
		{
			name:     "Double dollar $$ -> $",
			input:    "cost is $$100 and branch is ${BRANCH}",
			expected: "cost is $100 and branch is main",
		},
		{
			name:     "Backslash dollar \\$ -> $",
			input:    `cost is \$100 and branch is ${BRANCH}`,
			expected: "cost is $100 and branch is main",
		},
		{
			name:     "Complex regex with multiple anchors and groups",
			input:    `^(?:release/|v)[0-9]+\.[0-9]+(?:\.[0-9]+)?$`,
			expected: `^(?:release/|v)[0-9]+\.[0-9]+(?:\.[0-9]+)?$`,
		},
		{
			name:     "Default value containing colons and slashes (URL format)",
			input:    `${GITLAB_URL:-https://gitlab.example.com/api/v4}`,
			expected: "https://gitlab.example.com/api/v4",
		},
		{
			name:     "Default value with unicode characters",
			input:    `${GREETING:-こんにちは 🚀 治理引擎}`,
			expected: "こんにちは 🚀 治理引擎",
		},
		{
			name:     "Default value with escaped dollars",
			input:    `${PATTERN:-\$literal_dollar_\$\$}`,
			expected: "$literal_dollar_$$",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := config.ExpandEnvWithLookup(tc.input, lookup)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, res)
		})
	}
}

func TestAdversarial_ExpandEnv_HighPayloadStress(t *testing.T) {
	// Generate a 1MB payload with 10,000 variable tokens
	var sb strings.Builder
	for i := 0; i < 10000; i++ {
		sb.WriteString(fmt.Sprintf("key_%d: ${VAR_%d:-val_%d}\n", i, i, i))
	}
	raw := sb.String()

	res, err := config.ExpandEnvWithLookup(raw, func(k string) (string, bool) {
		if k == "VAR_500" {
			return "OVERRIDDEN_500", true
		}
		return "", false
	})
	require.NoError(t, err)
	assert.Contains(t, res, "key_0: val_0")
	assert.Contains(t, res, "key_500: OVERRIDDEN_500")
	assert.Contains(t, res, "key_9999: val_9999")
}

// ----------------------------------------------------------------------------
// 2. Loader.LoadRaw Adversarial Tests
// ----------------------------------------------------------------------------

func TestAdversarial_Loader_PrecedenceOrder(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	fileA := filepath.Join(tmpDir, "fileA.yaml")
	fileB := filepath.Join(tmpDir, "fileB.yaml")
	require.NoError(t, os.WriteFile(fileA, []byte("version: from_file_a"), 0600))
	require.NoError(t, os.WriteFile(fileB, []byte("version: from_file_b"), 0600))

	// Precedence: CLI path > CONFIG_CONTENT > CONFIG_SOURCE > default candidates
	envMap := map[string]string{
		"CONFIG_CONTENT": "version: from_config_content",
		"CONFIG_YAML":    "version: from_config_yaml",
		"CONFIG_JSON":    `{"version": "from_config_json"}`,
		"CONFIG_SOURCE":  fileA,
		"CONFIG_FILE":    fileB,
	}
	lookup := func(k string) (string, bool) {
		v, ok := envMap[k]
		return v, ok
	}

	t.Run("Explicit path overrides CONFIG_CONTENT and CONFIG_SOURCE", func(t *testing.T) {
		loader := config.NewLoader(config.WithEnvLookup(lookup))
		data, desc, err := loader.LoadRaw(ctx, fileB)
		require.NoError(t, err)
		assert.Equal(t, fileB, desc)
		assert.Equal(t, "version: from_file_b", string(data))
	})

	t.Run("CONFIG_CONTENT takes precedence over CONFIG_SOURCE when no path provided", func(t *testing.T) {
		loader := config.NewLoader(config.WithEnvLookup(lookup))
		data, desc, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "env:CONFIG_CONTENT", desc)
		assert.Equal(t, "version: from_config_content", string(data))
	})

	t.Run("CONFIG_SOURCE takes precedence over default files when inline content is empty", func(t *testing.T) {
		customLookup := func(k string) (string, bool) {
			if k == "CONFIG_SOURCE" {
				return fileA, true
			}
			return "", false
		}
		loader := config.NewLoader(config.WithEnvLookup(customLookup))
		data, desc, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("env:CONFIG_SOURCE(%s)", fileA), desc)
		assert.Equal(t, "version: from_file_a", string(data))
	})
}

func TestAdversarial_Loader_EmptySources(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	emptyFile := filepath.Join(tmpDir, "empty.yaml")
	require.NoError(t, os.WriteFile(emptyFile, []byte("   \n\t  \n"), 0600))

	t.Run("Empty file produces error", func(t *testing.T) {
		loader := config.NewLoader()
		_, _, err := loader.LoadRaw(ctx, emptyFile)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty configuration content")
	})

	t.Run("Empty stdin produces error", func(t *testing.T) {
		loader := config.NewLoader(config.WithStdin(strings.NewReader("   \n")))
		_, _, err := loader.LoadRaw(ctx, "-")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty configuration content")
	})

	t.Run("Empty CONFIG_CONTENT falls through to subsequent sources", func(t *testing.T) {
		lookup := func(k string) (string, bool) {
			if k == "CONFIG_CONTENT" {
				return "   ", true
			}
			if k == "CONFIG_YAML" {
				return "version: from_yaml", true
			}
			return "", false
		}
		loader := config.NewLoader(config.WithEnvLookup(lookup))
		data, desc, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "env:CONFIG_YAML", desc)
		assert.Equal(t, "version: from_yaml", string(data))
	})
}

func TestAdversarial_Loader_S3URIs(t *testing.T) {
	cases := []struct {
		name        string
		uri         string
		expectError bool
		errContains string
	}{
		{
			name:        "Valid S3 URI",
			uri:         "s3://my-bucket/path/to/config.yaml",
			expectError: false,
		},
		{
			name:        "Missing s3 prefix",
			uri:         "https://s3.amazonaws.com/my-bucket/config.yaml",
			expectError: true,
			errContains: "must begin with 's3://'",
		},
		{
			name:        "Empty bucket",
			uri:         "s3:///key.yaml",
			expectError: true,
			errContains: "expected format s3://<bucket>/<key>",
		},
		{
			name:        "Empty key",
			uri:         "s3://my-bucket/",
			expectError: true,
			errContains: "expected format s3://<bucket>/<key>",
		},
		{
			name:        "Missing slash after bucket",
			uri:         "s3://my-bucket",
			expectError: true,
			errContains: "expected format s3://<bucket>/<key>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := config.ParseS3URI(tc.uri)
			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, parsed.Bucket)
				assert.NotEmpty(t, parsed.Key)
			}
		})
	}
}

func TestAdversarial_Loader_ConcurrentS3Access(t *testing.T) {
	ctx := context.Background()
	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader([]byte("version: from_s3\nsettings:\n  concurrency: 10"))),
			}, nil
		},
	}

	loader := config.NewLoader(config.WithS3Client(mockS3))

	// Concurrently invoke LoadRaw on S3
	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			data, desc, err := loader.LoadRaw(ctx, "s3://fleet-gov-bucket/prod.yaml")
			assert.NoError(t, err)
			assert.Equal(t, "s3://fleet-gov-bucket/prod.yaml", desc)
			assert.Contains(t, string(data), "version: from_s3")
		}()
	}

	wg.Wait()
}

// ----------------------------------------------------------------------------
// 3. UnmarshalStrict Adversarial Tests
// ----------------------------------------------------------------------------

func TestAdversarial_UnmarshalStrict_MalformedPaylod(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		errContains string
	}{
		{
			name:        "Invalid YAML indentation syntax",
			payload:     "version: 1\nsettings:\n  concurrency: 10\n dry_run: false",
			errContains: "configuration syntax or schema error",
		},
		{
			name:        "Unclosed quotation mark",
			payload:     "version: \"unclosed string\nsettings:\n  concurrency: 10",
			errContains: "configuration syntax or schema error",
		},
		{
			name:        "Type mismatch: string where integer expected",
			payload:     "version: \"1\"\nsettings:\n  concurrency: \"ten\"",
			errContains: "configuration syntax or schema error",
		},
		{
			name:        "Type mismatch: array where map expected",
			payload:     "version: \"1\"\nsettings:\n  - concurrency: 10",
			errContains: "configuration syntax or schema error",
		},
		{
			name:        "Multiple YAML documents (--- separator)",
			payload:     "version: \"1\"\n---\nversion: \"2\"",
			errContains: "unexpected extra data in configuration",
		},
		{
			name:        "Trailing JSON payload after valid JSON document",
			payload:     `{"version": "1"} {"version": "2"}`,
			errContains: "unexpected extra data in configuration",
		},
		{
			name:        "Empty whitespace only",
			payload:     "    \n\t  \n  ",
			errContains: "empty configuration content",
		},
		{
			name:        "Comment only YAML document",
			payload:     "# Just a comment\n# Another comment\n",
			errContains: "empty configuration content",
		},
		{
			name:        "Unknown nested field in protected branches",
			payload:     "version: \"1\"\npolicies:\n  protected_branches:\n    - name: main\n      unrecognized_field: true",
			errContains: "not found",
		},
		{
			name:        "Unknown field in MR approval rules",
			payload:     "version: \"1\"\npolicies:\n  approval_rules:\n    rules:\n      - name: Security\n        non_existent_key: 123",
			errContains: "not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg config.PolicyConfig
			err := config.UnmarshalStrict([]byte(tc.payload), &cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestAdversarial_UnmarshalStrict_YAMLAnchorsAndAliases(t *testing.T) {
	// Verify that valid YAML anchors & aliases parse cleanly with KnownFields
	payload := `
version: "1"
settings:
  concurrency: 15
policies:
  push_rules: &default_push_rules
    author_email_regex: "@example\\.com$"
    branch_name_regex: "^(main|feature/.*)$"
    prevent_secrets: true
`
	var cfg config.PolicyConfig
	err := config.UnmarshalStrict([]byte(payload), &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Policies.PushRules)
	assert.Equal(t, "@example\\.com$", cfg.Policies.PushRules.AuthorEmailRegex)
	assert.True(t, *cfg.Policies.PushRules.PreventSecrets)
}

// ----------------------------------------------------------------------------
// 4. Validator Semantic Adversarial Tests
// ----------------------------------------------------------------------------

func TestAdversarial_Validator_EdgeCases(t *testing.T) {
	t.Run("Multiple aggregated validation errors", func(t *testing.T) {
		min := 100
		max := 10
		maxFileSize := -50
		retentionDays := -1
		cfg := &config.PolicyConfig{
			Settings: config.SettingsConfig{
				Concurrency:  -5,
				LogLevel:     "super_verbose", // invalid
				LogFormat:    "xml",           // invalid
				ReportFormat: "pdf",           // invalid
				GitLab: config.GitLabSettingsConfig{
					TokenType:        "bearer_unknown", // invalid
					TimeoutSeconds:   -10,
					RateLimitRPS:     -1.0,
					RateLimitBurst:   -5,
					MaxRetries:       -2,
					RetryBaseDelayMs: 5000,
					RetryMaxDelayMs:  1000, // base > max
				},
			},
			Targets: config.TargetSelectors{
				GroupSelector: &config.GroupSelector{
					GroupIDsInclude:   []int{-1, 0},
					GroupPathsInclude: []string{"/leading/slash", "trailing/slash/"},
				},
				ProjectSelector: &config.ProjectSelector{
					ProjectNameRegexInclude: "[unclosed_regex",
					Visibility:              "super_secret",
					IDRange: &config.IDRange{
						Min: min,
						Max: max, // min > max
					},
				},
			},
			Policies: config.PoliciesConfig{
				PushRules: &config.PushRulesConfig{
					AuthorEmailRegex: "(?P<bad_syntax",
					MaxFileSize:      &maxFileSize,
				},
				ProtectedBranches: []config.ProtectedBranchRuleConfig{
					{
						Name: "", // empty name
						AllowedToPush: []config.BranchAccessDescription{
							{AccessLevel: 99, UserID: -1},
						},
					},
				},
				ApprovalRules: &config.ApprovalRulesConfig{
					Rules: []config.ApprovalRuleConfig{
						{
							Name:              "",
							ApprovalsRequired: 0,
							RuleType:          "invalid_type",
						},
					},
				},
				ProjectSettings: &config.ProjectSettingsConfig{
					SquashOption: "sometimes",
					MergeMethod:  "cherry_pick",
				},
				PipelineRetention: &config.PipelineRetentionConfig{
					RetentionDays: retentionDays,
				},
				Variables: []config.VariableConfig{
					{
						Key:   "INVALID-KEY-WITH-DASHES",
						Value: "short",
						Masked: func() *bool {
							b := true
							return &b
						}(),
					},
					{
						Key:   "VALID_KEY",
						Value: "invalid spaces in value",
						Masked: func() *bool {
							b := true
							return &b
						}(),
					},
				},
				Webhooks: []config.WebhookConfig{
					{
						URL: "ftp://gitlab.example.com/webhook",
					},
					{
						URL: "not a url",
					},
				},
			},
		}

		err := cfg.Validate()
		require.Error(t, err)
		vErrs, ok := err.(config.ValidationErrors)
		require.True(t, ok, "Error must be of type ValidationErrors")
		assert.GreaterOrEqual(t, len(vErrs), 25, "Expected at least 25 aggregated validation errors")

		errStr := err.Error()
		assert.Contains(t, errStr, "concurrency must be at least 1")
		assert.Contains(t, errStr, "invalid log_level")
		assert.Contains(t, errStr, "invalid log_format")
		assert.Contains(t, errStr, "invalid report_format")
		assert.Contains(t, errStr, "invalid token_type")
		assert.Contains(t, errStr, "retry_base_delay_ms (5000) cannot be greater than retry_max_delay_ms (1000)")
		assert.Contains(t, errStr, "group ID must be positive")
		assert.Contains(t, errStr, "invalid group path")
		assert.Contains(t, errStr, "invalid regular expression")
		assert.Contains(t, errStr, "min (100) cannot be greater than max (10)")
		assert.Contains(t, errStr, "protected branch name cannot be empty")
		assert.Contains(t, errStr, "invalid access level 99")
		assert.Contains(t, errStr, "approval rule name cannot be empty")
		assert.Contains(t, errStr, "approvals_required must be at least 1")
		assert.Contains(t, errStr, "invalid squash_option")
		assert.Contains(t, errStr, "retention_days must be non-negative")
		assert.Contains(t, errStr, "invalid variable key")
		assert.Contains(t, errStr, "masked CI/CD variable value must be at least 8 characters long")
		assert.Contains(t, errStr, "masked CI/CD variable value contains invalid characters")
		assert.Contains(t, errStr, "invalid webhook URL")
	})
}
