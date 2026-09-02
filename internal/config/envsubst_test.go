package config_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func TestExpandEnvWithLookup(t *testing.T) {
	mockEnv := map[string]string{
		"EXISTING_VAR": "prod_token",
		"EMPTY_VAR":    "",
		"BASE_URL":     "https://gitlab.example.com",
		"PORT":         "8080",
		"TIER":         "enterprise",
		"REGION":       "us-east-1",
		"INNER_VAL":    "secret_pw",
		"OUTER_VAL":    "token_${INNER_VAL}",
		"LOOP_X":       "${LOOP_Y}",
		"LOOP_Y":       "${LOOP_X}",
	}

	lookup := func(k string) (string, bool) {
		val, ok := mockEnv[k]
		return val, ok
	}

	tests := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{
			name:     "Simple variable expansion",
			input:    "token: ${EXISTING_VAR}",
			expected: "token: prod_token",
		},
		{
			name:     "Unset variable with default",
			input:    "url: ${UNSET_VAR:-https://default.gitlab.com}",
			expected: "url: https://default.gitlab.com",
		},
		{
			name:     "Empty variable with default (POSIX :- semantics)",
			input:    "token: ${EMPTY_VAR:-fallback_token}",
			expected: "token: fallback_token",
		},
		{
			name:     "Set variable ignores default",
			input:    "token: ${EXISTING_VAR:-fallback_token}",
			expected: "token: prod_token",
		},
		{
			name:     "Nested variable default resolved to set env var",
			input:    "url: ${UNSET_1:-${BASE_URL:-http://localhost}}",
			expected: "url: https://gitlab.example.com",
		},
		{
			name:     "Deeply nested fallback to literal",
			input:    "port: ${UNSET_1:-${UNSET_2:-3000}}",
			expected: "port: 3000",
		},
		{
			name:     "Escaped $$ tokens",
			input:    "raw_token: $${EXISTING_VAR} and $$100",
			expected: "raw_token: ${EXISTING_VAR} and $100",
		},
		{
			name:     "Backslash escaped \\$ token",
			input:    "raw_token: \\${EXISTING_VAR}",
			expected: "raw_token: ${EXISTING_VAR}",
		},
		{
			name:     "Regex ending in dollar sign preserved",
			input:    "branch_regex: ^(main|release/.*)$",
			expected: "branch_regex: ^(main|release/.*)$",
		},
		{
			name:     "Multiple tokens on one line",
			input:    "${BASE_URL}:${PORT}/tier/${TIER}",
			expected: "https://gitlab.example.com:8080/tier/enterprise",
		},
		{
			name:     "Unset variable without default expands to empty string",
			input:    "optional: '${NON_EXISTENT_VAR}'",
			expected: "optional: ''",
		},
		{
			name:     "Multiline string with mixed tokens",
			input:    "version: v1\nsettings:\n  concurrency: ${CONCURRENCY:-10}\n  log_level: ${LOG_LEVEL:-info}\n",
			expected: "version: v1\nsettings:\n  concurrency: 10\n  log_level: info\n",
		},
		{
			name:     "Recursive environment variable expansion",
			input:    "auth: ${OUTER_VAL}",
			expected: "auth: token_secret_pw",
		},
		{
			name:        "Circular variable reference returns error",
			input:       "val: ${LOOP_X}",
			expectError: true,
		},
		{
			name:        "Unterminated ${ syntax error",
			input:       "bad: ${UNCLOSED_VAR",
			expectError: true,
		},
		{
			name:        "Invalid variable identifier with digits first",
			input:       "bad: ${123_INVALID}",
			expectError: true,
		},
		{
			name:        "Empty variable identifier",
			input:       "bad: ${}",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ExpandEnvWithLookup(tt.input, lookup)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestExpandEnv_OSLookup(t *testing.T) {
	t.Setenv("TEST_FLEET_GOV_VAR", "injected_secret_value")

	got, err := config.ExpandEnv("val: ${TEST_FLEET_GOV_VAR}")
	require.NoError(t, err)
	assert.Equal(t, "val: injected_secret_value", got)
}

func TestExpandEnv_RecursionLimit(t *testing.T) {
	// Recursive expansion chain exceeding limit
	input := "${A:-${B:-${C:-${D:-${E:-${F:-${G:-${H:-${I:-${J:-${K:-${L:-${M:-${N:-${O:-${P:-${Q:-${R:-${S:-${T:-${U:-${V:-${W:-${X:-${Y:-${Z:-${AA:-${AB:-${AC:-${AD:-${AE:-${AF:-${AG:-deep}}}}}}}}}}}}}}}}}}}}}}}}}}}}}}}}}"
	_, err := config.ExpandEnvWithLookup(input, os.LookupEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum recursion depth")
}
