package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

const trustedURL = "https://gitlab.example.com/api/v4"

func untrustedEnv() config.EnvLookupFunc {
	env := map[string]string{
		"GITLAB_TOKEN":          "glpat-operator-secret",
		"AWS_SECRET_ACCESS_KEY": "aws-operator-secret",
		"GITLAB_BASE_URL":       trustedURL,
		"TEAM_NAME":             "platform",
	}
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func untrustedOpts(mock *MockS3Client, allowed ...string) config.LoadOptions {
	return config.LoadOptions{
		LoaderOptions: []config.LoaderOption{config.WithEnvLookup(untrustedEnv()), config.WithS3Client(mock)},
		Untrusted: &config.UntrustedPolicy{
			AllowedEnv:           append([]string{"GITLAB_BASE_URL"}, allowed...),
			TrustedGitLabBaseURL: trustedURL,
		},
	}
}

func TestUntrusted_EnvExpansionIsAllowListed(t *testing.T) {
	ctx := context.Background()
	doc := []byte(`
version: "v1"
settings:
  gitlab:
    base_url: "${GITLAB_BASE_URL}"
    token: "${GITLAB_TOKEN}"
policies:
  variables:
    - key: LEAK
      value: "x${AWS_SECRET_ACCESS_KEY}${GITLAB_TOKEN}"
    - key: TEAM
      value: "${TEAM_NAME:-none}"
`)

	cfg, _, err := config.LoadInline(ctx, doc, "inline:test", untrustedOpts(&MockS3Client{}))
	require.NoError(t, err)
	assert.Equal(t, trustedURL, cfg.Settings.GitLab.BaseURL, "allow-listed variable expands")
	assert.Empty(t, cfg.Settings.GitLab.Token, "secret must not be copied into the config; the client falls back to GITLAB_TOKEN itself")
	assert.Equal(t, "x", cfg.Policies.Variables[0].Value, "secrets must not expand into policy values")
	assert.Equal(t, "none", cfg.Policies.Variables[1].Value, "non-allow-listed variables resolve as unset")

	cfg, _, err = config.LoadInline(ctx, doc, "inline:test", untrustedOpts(&MockS3Client{}, "TEAM_NAME"))
	require.NoError(t, err)
	assert.Equal(t, "platform", cfg.Policies.Variables[1].Value, "operator can allow-list extra variables")

	// The same document from a trusted source expands everything, as before.
	trusted := config.LoadOptions{LoaderOptions: []config.LoaderOption{config.WithEnvLookup(untrustedEnv())}}
	cfg, _, err = config.LoadInline(ctx, doc, "inline:test", trusted)
	require.NoError(t, err)
	assert.Equal(t, "glpat-operator-secret", cfg.Settings.GitLab.Token)
}

func TestUntrusted_GitLabBaseURL(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		baseURL string
		token   string
		ok      bool
	}{
		{"", "", true},
		{"https://gitlab.com/api/v4", "", true}, // the default: the client then uses GITLAB_BASE_URL
		{trustedURL, "", true},
		{trustedURL + "/", "", true},
		{"https://attacker.example/api/v4", "", false},                // would receive the operator's GITLAB_TOKEN
		{"http://gitlab.example.com/api/v4", "", false},               // downgrade to plain HTTP
		{"https://attacker.example/api/v4", "${GITLAB_TOKEN}", false}, // secret does not expand, so still the operator's token
		{"https://other.example/api/v4", "glpat-callers-own", true},   // the caller's own token, sent where they choose
	} {
		doc := []byte("version: \"v1\"\nsettings:\n  gitlab:\n    base_url: \"" + tc.baseURL + "\"\n    token: \"" + tc.token + "\"\n")
		_, _, err := config.LoadInline(ctx, doc, "inline:test", untrustedOpts(&MockS3Client{}))
		if tc.ok {
			assert.NoError(t, err, "base_url %q token %q", tc.baseURL, tc.token)
		} else {
			assert.ErrorContains(t, err, "settings.gitlab.base_url", "base_url %q token %q", tc.baseURL, tc.token)
		}
	}
}

func TestUntrusted_NoLocalFileReferences(t *testing.T) {
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(local, []byte("operator-only\n"), 0o600))

	for name, doc := range map[string]string{
		"license.file":     "version: \"v1\"\nsettings:\n  license:\n    file: \"" + local + "\"\n",
		"license.crl_file": "version: \"v1\"\nsettings:\n  license:\n    crl_file: \"" + local + "\"\n",
		"license.crl_url":  "version: \"v1\"\nsettings:\n  license:\n    crl_url: \"https://attacker.example/crl\"\n",
		"content_file":     "version: \"v1\"\npolicies:\n  repository_files:\n    - path: LEAK.txt\n      content_file: \"" + local + "\"\n",
		"content_file -":   "version: \"v1\"\npolicies:\n  repository_files:\n    - path: LEAK.txt\n      content_file: \"-\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, _, err := config.LoadInline(ctx, []byte(doc), "inline:test", untrustedOpts(&MockS3Client{}))
			require.ErrorContains(t, err, "untrusted configuration rejected")
			assert.Nil(t, cfg)
		})
	}
}

func TestUntrusted_S3ContentFileIsInlined(t *testing.T) {
	ctx := context.Background()
	mock := &MockS3Client{Objects: map[string][]byte{"templates/CODEOWNERS": []byte("* @platform\n")}}
	doc := []byte("version: \"v1\"\npolicies:\n  repository_files:\n    - path: CODEOWNERS\n      content_file: \"s3://templates/CODEOWNERS\"\n")

	cfg, _, err := config.LoadInline(ctx, doc, "inline:test", untrustedOpts(mock))
	require.NoError(t, err)
	require.Len(t, cfg.Policies.RepositoryFiles, 1)
	assert.Equal(t, "* @platform\n", cfg.Policies.RepositoryFiles[0].Content)
	assert.Empty(t, cfg.Policies.RepositoryFiles[0].ContentFile, "the reference is resolved up front and cleared")

	missing := []byte("version: \"v1\"\npolicies:\n  repository_files:\n    - path: CODEOWNERS\n      content_file: \"s3://templates/missing\"\n")
	_, _, err = config.LoadInline(ctx, missing, "inline:test", untrustedOpts(mock))
	require.ErrorContains(t, err, "repository_files[0].content_file")
}

func TestSanitizeUntrusted_ParsedConfig(t *testing.T) {
	cfg := &config.PolicyConfig{}
	cfg.Settings.GitLab.BaseURL = "https://attacker.example/api/v4"
	cfg.Policies.RepositoryFiles = []config.RepositoryFileConfig{{Path: "LEAK", ContentFile: "/proc/self/environ"}}

	opts := untrustedOpts(&MockS3Client{})
	err := config.SanitizeUntrusted(context.Background(), cfg, opts.Untrusted, opts)
	require.ErrorContains(t, err, "settings.gitlab.base_url")
	require.ErrorContains(t, err, "repository_files[0].content_file must be an s3:// URI")

	assert.NoError(t, config.SanitizeUntrusted(context.Background(), &config.PolicyConfig{}, nil, opts), "nil policy is a no-op")
}
