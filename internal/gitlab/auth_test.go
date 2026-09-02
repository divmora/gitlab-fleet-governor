package gitlab_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

func mockEnv(vars map[string]string) gitlab.EnvLookupFunc {
	return func(key string) (string, bool) {
		val, ok := vars[key]
		return val, ok
	}
}

func TestResolveAuth_TokenPrecedence(t *testing.T) {
	tests := []struct {
		name              string
		cfg               *config.GitLabSettingsConfig
		env               map[string]string
		expectedToken     string
		expectedType      gitlab.TokenType
		expectedSourceTok string
	}{
		{
			name: "config token takes highest precedence",
			cfg: &config.GitLabSettingsConfig{
				Token: "cfg-token-123",
			},
			env: map[string]string{
				"PRIVATE_TOKEN": "priv-env-token",
				"GITLAB_TOKEN":  "gl-env-token",
			},
			expectedToken:     "cfg-token-123",
			expectedType:      gitlab.TokenTypePrivate,
			expectedSourceTok: "config",
		},
		{
			name: "PRIVATE_TOKEN env takes precedence over GITLAB_TOKEN",
			cfg:  &config.GitLabSettingsConfig{},
			env: map[string]string{
				"PRIVATE_TOKEN": "priv-env-token",
				"GITLAB_TOKEN":  "gl-env-token",
				"OAUTH_TOKEN":   "oauth-env-token",
			},
			expectedToken:     "priv-env-token",
			expectedType:      gitlab.TokenTypePrivate,
			expectedSourceTok: "PRIVATE_TOKEN",
		},
		{
			name: "GITLAB_TOKEN env takes precedence over OAUTH_TOKEN",
			cfg:  &config.GitLabSettingsConfig{},
			env: map[string]string{
				"GITLAB_TOKEN": "gl-env-token",
				"OAUTH_TOKEN":  "oauth-env-token",
			},
			expectedToken:     "gl-env-token",
			expectedType:      gitlab.TokenTypePrivate,
			expectedSourceTok: "GITLAB_TOKEN",
		},
		{
			name: "OAUTH_TOKEN env sets oauth type",
			cfg:  &config.GitLabSettingsConfig{},
			env: map[string]string{
				"OAUTH_TOKEN":  "oauth-token-val",
				"CI_JOB_TOKEN": "job-token-val",
			},
			expectedToken:     "oauth-token-val",
			expectedType:      gitlab.TokenTypeOAuth,
			expectedSourceTok: "OAUTH_TOKEN",
		},
		{
			name: "GITLAB_OAUTH_TOKEN env sets oauth type",
			cfg:  &config.GitLabSettingsConfig{},
			env: map[string]string{
				"GITLAB_OAUTH_TOKEN": "gl-oauth-token-val",
			},
			expectedToken:     "gl-oauth-token-val",
			expectedType:      gitlab.TokenTypeOAuth,
			expectedSourceTok: "GITLAB_OAUTH_TOKEN",
		},
		{
			name: "CI_JOB_TOKEN env sets job_token type",
			cfg:  &config.GitLabSettingsConfig{},
			env: map[string]string{
				"CI_JOB_TOKEN": "ci-job-12345",
			},
			expectedToken:     "ci-job-12345",
			expectedType:      gitlab.TokenTypeJob,
			expectedSourceTok: "CI_JOB_TOKEN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := gitlab.ResolveAuth(tc.cfg, mockEnv(tc.env))
			require.NoError(t, err)
			assert.Equal(t, tc.expectedToken, res.Token)
			assert.Equal(t, tc.expectedType, res.TokenType)
			assert.Equal(t, tc.expectedSourceTok, res.SourceToken)
		})
	}
}

func TestResolveAuth_BaseURLPrecedence(t *testing.T) {
	tests := []struct {
		name              string
		cfg               *config.GitLabSettingsConfig
		env               map[string]string
		expectedURL       string
		expectedSourceURL string
	}{
		{
			name: "custom config base_url takes precedence",
			cfg: &config.GitLabSettingsConfig{
				Token:   "tok",
				BaseURL: "https://gitlab.mycorp.com/api/v4/",
			},
			env: map[string]string{
				"GITLAB_BASE_URL": "https://env-gl.example.com/api/v4",
			},
			expectedURL:       "https://gitlab.mycorp.com/api/v4",
			expectedSourceURL: "config",
		},
		{
			name: "GITLAB_BASE_URL env takes precedence over CI_API_V4_URL",
			cfg: &config.GitLabSettingsConfig{
				Token: "tok",
			},
			env: map[string]string{
				"GITLAB_BASE_URL": "https://env-gl.example.com/api/v4",
				"CI_API_V4_URL":   "https://ci-gl.example.com/api/v4",
			},
			expectedURL:       "https://env-gl.example.com/api/v4",
			expectedSourceURL: "GITLAB_BASE_URL",
		},
		{
			name: "CI_API_V4_URL env used if GITLAB_BASE_URL not set",
			cfg: &config.GitLabSettingsConfig{
				Token: "tok",
			},
			env: map[string]string{
				"CI_API_V4_URL": "https://ci-gl.example.com/api/v4",
			},
			expectedURL:       "https://ci-gl.example.com/api/v4",
			expectedSourceURL: "CI_API_V4_URL",
		},
		{
			name: "default URL used if nothing specified",
			cfg: &config.GitLabSettingsConfig{
				Token: "tok",
			},
			env:               map[string]string{},
			expectedURL:       "https://gitlab.com/api/v4",
			expectedSourceURL: "default",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := gitlab.ResolveAuth(tc.cfg, mockEnv(tc.env))
			require.NoError(t, err)
			assert.Equal(t, tc.expectedURL, res.BaseURL)
			assert.Equal(t, tc.expectedSourceURL, res.SourceBaseURL)
		})
	}
}

func TestResolveAuth_Errors(t *testing.T) {
	t.Run("missing token returns error", func(t *testing.T) {
		cfg := &config.GitLabSettingsConfig{}
		_, err := gitlab.ResolveAuth(cfg, mockEnv(nil))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no GitLab authentication token found")
	})

	t.Run("invalid base URL scheme returns error", func(t *testing.T) {
		cfg := &config.GitLabSettingsConfig{
			Token:   "valid-token",
			BaseURL: "ftp://invalid-url.com/api/v4",
		}
		_, err := gitlab.ResolveAuth(cfg, mockEnv(nil))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid GitLab base URL")
	})

	t.Run("unsupported token type returns error", func(t *testing.T) {
		cfg := &config.GitLabSettingsConfig{
			Token:     "valid-token",
			TokenType: "invalid_type",
		}
		_, err := gitlab.ResolveAuth(cfg, mockEnv(nil))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported token type")
	})
}
